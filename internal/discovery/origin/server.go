package origin

import (
	"container/heap"
	"context"
	"crypto/rand"
	"encoding/binary"
	"sync"
	"time"

	publicprovider "github.com/duanhf2012/origin/v3/discovery/provider"
	"github.com/duanhf2012/origin/v3/errs"
	"github.com/duanhf2012/origin/v3/internal/bufferpool"
	originlog "github.com/duanhf2012/origin/v3/log"
	"github.com/duanhf2012/origin/v3/rpc"
	"github.com/duanhf2012/origin/v3/service"
)

const (
	maxControlConnections = 16384
	actorQueueCommands    = 32768
	clientSendMessages    = 64
	maxExpiriesPerTurn    = 1024
)

// Service 是框架保留的 DiscoveryService 实例。
//
// 普通生命周期方法保持无业务副作用；Node 在基础设施阶段显式 Prepare Listener，并在
// 当前 Provider 关闭后显式 Close。
type Service struct {
	service.Service

	config Config
	pool   *bufferpool.Pool
	logger originlog.Logger

	mu       sync.Mutex
	cancel   context.CancelFunc
	commands chan serverCommand
	done     chan struct{}
	prepared bool
	closed   bool
}

type commandKind uint8

const (
	commandOpen commandKind = iota + 1
	commandMessage
	commandClose
)

type serverCommand struct {
	kind    commandKind
	conn    rpc.SystemPeer
	payload []byte
}

type serverClient struct {
	conn      rpc.SystemPeer
	nodeID    string
	sessionID uint64
	hello     bool
	published bool
}

type serverRecord struct {
	node       publicprovider.Node
	owner      rpc.SystemPeer
	expiresAt  time.Time
	generation uint64
	wireSize   int
}

type expiryEntry struct {
	nodeID     string
	expiresAt  time.Time
	generation uint64
}

type expiryHeap []expiryEntry

func (entries expiryHeap) Len() int { return len(entries) }
func (entries expiryHeap) Less(left, right int) bool {
	return entries[left].expiresAt.Before(entries[right].expiresAt)
}
func (entries expiryHeap) Swap(left, right int) {
	entries[left], entries[right] = entries[right], entries[left]
}
func (entries *expiryHeap) Push(value any) {
	*entries = append(*entries, value.(expiryEntry))
}
func (entries *expiryHeap) Pop() any {
	old := *entries
	last := old[len(old)-1]
	old[len(old)-1] = expiryEntry{}
	*entries = old[:len(old)-1]
	return last
}

// NewService 创建尚未绑定 Listener 的保留系统 Service。
func NewService(
	config Config,
	pool *bufferpool.Pool,
	logger originlog.Logger,
) *Service {
	return &Service{
		config:   config,
		pool:     pool,
		logger:   logger.WithScope(config.Server.Node, "DiscoveryService"),
		commands: make(chan serverCommand, actorQueueCommands),
		done:     make(chan struct{}),
	}
}

// OnInit 保持普通 Service 生命周期契约；资源在 PrepareDiscovery 创建。
func (*Service) OnInit() error { return nil }

// OnStart 不重复启动 Listener；Node 已在全部业务 OnStart 前完成 Prepare。
func (*Service) OnStart(context.Context) error { return nil }

// OnStop 不提前关闭发现端；Node 会在自己的 Provider 退出后调用 CloseDiscovery。
func (*Service) OnStop(context.Context) error { return nil }

// PrepareDiscovery 启动单 Actor。控制连接由已经启动的 RPC Transport 承载，因而不再
// 创建第二个 TCP Listener 或重复声明端口。
func (service *Service) PrepareDiscovery(ctx context.Context) error {
	if service == nil || ctx == nil {
		return errs.ErrInvalidArgument
	}
	service.mu.Lock()
	if service.prepared {
		service.mu.Unlock()
		return nil
	}
	if service.closed {
		service.mu.Unlock()
		return errs.ErrServiceStopped
	}
	actorCtx, cancel := context.WithCancel(context.Background())
	service.cancel = cancel
	service.prepared = true
	service.mu.Unlock()

	epoch, err := randomNonZero()
	if err != nil {
		cancel()
		service.finishActorWithoutStart()
		return err
	}
	go service.actorLoop(actorCtx, epoch)
	return nil
}

// CloseDiscovery 停止 Actor；RPC Runtime 随后统一关闭底层系统连接。
func (service *Service) CloseDiscovery(ctx context.Context) error {
	if service == nil || ctx == nil {
		return errs.ErrInvalidArgument
	}
	service.mu.Lock()
	if service.closed {
		done := service.done
		service.mu.Unlock()
		<-done
		return nil
	}
	service.closed = true
	cancel := service.cancel
	prepared := service.prepared
	service.mu.Unlock()

	if cancel != nil {
		cancel()
	}
	if prepared {
		<-service.done
	} else {
		service.finishActorWithoutStart()
	}
	return nil
}

func (service *Service) finishActorWithoutStart() {
	select {
	case <-service.done:
	default:
		close(service.done)
	}
}

type serverHandler struct {
	service *Service
}

// BindSystemRPC 在 RPC Freeze 前把 DiscoveryService 注册到保留控制平面。它不会进入
// 业务 Service 目录，也不会让项目代码借此获得额外调用入口。
func (service *Service) BindSystemRPC(runtime *rpc.Runtime) error {
	if service == nil || runtime == nil {
		return errs.ErrInvalidArgument
	}
	return runtime.BindSystemHandler(&serverHandler{service: service})
}

func (handler *serverHandler) OnSystemOpen(peer rpc.SystemPeer) {
	handler.service.enqueue(serverCommand{kind: commandOpen, conn: peer})
}

func (handler *serverHandler) OnSystemMessage(
	peer rpc.SystemPeer,
	payload []byte,
) {
	payload = append([]byte(nil), payload...)
	if len(payload) == 0 {
		peer.Close()
		return
	}
	if !handler.service.enqueue(serverCommand{
		kind:    commandMessage,
		conn:    peer,
		payload: payload,
	}) {
		peer.Close()
	}
}

func (handler *serverHandler) OnSystemClose(peer rpc.SystemPeer, _ error) {
	handler.service.enqueue(serverCommand{kind: commandClose, conn: peer})
}

func (service *Service) enqueue(command serverCommand) bool {
	select {
	case service.commands <- command:
		return true
	default:
		if command.conn != nil {
			command.conn.Close()
		}
		return false
	}
}

func (service *Service) actorLoop(ctx context.Context, epoch uint64) {
	defer close(service.done)
	clients := make(map[rpc.SystemPeer]*serverClient)
	records := make(map[string]serverRecord)
	expiries := make(expiryHeap, 0)
	heap.Init(&expiries)
	revision := uint64(0)
	totalServices := 0
	totalBytes := 0
	warmingUntil := time.Now().Add(warmingDuration(service.config.TTL))
	ready := false
	timer := time.NewTimer(time.Until(warmingUntil))
	defer timer.Stop()

	resetTimer := func() {
		next := warmingUntil
		if ready {
			if len(expiries) == 0 {
				if !timer.Stop() {
					select {
					case <-timer.C:
					default:
					}
				}
				return
			}
			next = expiries[0].expiresAt
		}
		delay := time.Until(next)
		if delay < 0 {
			delay = 0
		}
		if !timer.Stop() {
			select {
			case <-timer.C:
			default:
			}
		}
		timer.Reset(delay)
	}

	for {
		select {
		case <-ctx.Done():
			return
		case command := <-service.commands:
			switch command.kind {
			case commandOpen:
				clients[command.conn] = &serverClient{conn: command.conn}
			case commandClose:
				client := clients[command.conn]
				if client != nil && client.published {
					if record, exists := records[client.nodeID]; exists &&
						record.owner == command.conn &&
						record.node.SessionID == client.sessionID {
						delete(records, client.nodeID)
						totalServices -= len(record.node.Services)
						totalBytes -= record.wireSize
						revision++
						if ready {
							service.broadcast(
								clients,
								encodeDelete(revision, client.nodeID, client.sessionID),
							)
						}
					}
				}
				delete(clients, command.conn)
			case commandMessage:
				client := clients[command.conn]
				if client == nil {
					command.conn.Close()
					break
				}
				revision = service.handleMessage(
					client,
					clients,
					records,
					&expiries,
					&totalServices,
					&totalBytes,
					epoch,
					revision,
					ready,
					command.payload,
				)
			}
			resetTimer()
		case <-timer.C:
			now := time.Now()
			if !ready && !now.Before(warmingUntil) {
				ready = true
				for _, client := range clients {
					if !client.hello {
						continue
					}
					service.sendFull(client.conn, epoch, revision, records)
				}
			}
			expired := 0
			for len(expiries) > 0 &&
				!expiries[0].expiresAt.After(now) &&
				expired < maxExpiriesPerTurn {
				entry := heap.Pop(&expiries).(expiryEntry)
				record, exists := records[entry.nodeID]
				if !exists || record.generation != entry.generation ||
					record.expiresAt.After(now) {
					continue
				}
				expired++
				delete(records, entry.nodeID)
				totalServices -= len(record.node.Services)
				totalBytes -= record.wireSize
				revision++
				service.broadcast(
					clients,
					encodeDelete(revision, entry.nodeID, record.node.SessionID),
				)
				if client := clients[record.owner]; client != nil {
					client.published = false
					record.owner.Close()
				}
			}
			resetTimer()
		}
	}
}

func (service *Service) handleMessage(
	client *serverClient,
	clients map[rpc.SystemPeer]*serverClient,
	records map[string]serverRecord,
	expiries *expiryHeap,
	totalServices *int,
	totalBytes *int,
	epoch uint64,
	revision uint64,
	ready bool,
	payload []byte,
) uint64 {
	frame := payload[0]
	body := payload[1:]
	if !client.hello && frame != frameHello {
		service.sendError(client.conn, errs.CodeTransportProtocol)
		client.conn.Close()
		return revision
	}
	switch frame {
	case frameHello:
		if client.hello {
			service.sendError(client.conn, errs.CodeTransportProtocol)
			client.conn.Close()
			return revision
		}
		nodeID, sessionID, err := decodeHello(body)
		if err != nil || !validKebab(nodeID) {
			service.sendError(client.conn, errs.CodeTransportProtocol)
			client.conn.Close()
			return revision
		}
		client.nodeID = nodeID
		client.sessionID = sessionID
		client.hello = true
		state := syncWarming
		if ready {
			state = syncReady
		}
		service.send(client.conn, encodeHelloAck(epoch, revision, state))
		if ready {
			service.sendFull(client.conn, epoch, revision, records)
		}
	case framePublish:
		node, err := decodePublish(body)
		if err != nil || node.NodeID != client.nodeID ||
			node.SessionID != client.sessionID {
			service.sendError(client.conn, errs.CodeDiscoverySnapshotInvalid)
			return revision
		}
		current, exists := records[node.NodeID]
		if exists && current.node.SessionID != node.SessionID {
			service.sendError(client.conn, errs.CodeDiscoveryDuplicateNode)
			return revision
		}
		if exists && current.owner != client.conn {
			oldOwner := current.owner
			if oldClient := clients[oldOwner]; oldClient != nil {
				oldClient.published = false
			}
			current.owner = client.conn
			records[node.NodeID] = current
			client.published = true
			oldOwner.Close()
		}
		if exists && nodeEqual(current.node, node) {
			current.expiresAt = time.Now().Add(service.config.TTL)
			current.generation++
			records[node.NodeID] = current
			heap.Push(expiries, expiryEntry{
				nodeID: node.NodeID, expiresAt: current.expiresAt,
				generation: current.generation,
			})
			service.send(client.conn, encodeAck(framePublishAck, revision))
			return revision
		}
		current, replacing := records[node.NodeID]
		if !replacing && len(records) >= publicprovider.MaxNodes {
			service.sendError(client.conn, errs.CodeDiscoveryCapacity)
			return revision
		}
		projectedServices := *totalServices + len(node.Services)
		projectedBytes := *totalBytes + len(payload) - 1
		if replacing {
			projectedServices -= len(current.node.Services)
			projectedBytes -= current.wireSize
		}
		if projectedServices > publicprovider.MaxServices ||
			projectedBytes+19 > publicprovider.MaxSnapshotSize {
			service.sendError(client.conn, errs.CodeDiscoveryCapacity)
			return revision
		}
		generation := uint64(1)
		if replacing {
			generation = current.generation + 1
		}
		record := serverRecord{
			node:       node,
			owner:      client.conn,
			expiresAt:  time.Now().Add(service.config.TTL),
			generation: generation,
			wireSize:   len(payload) - 1,
		}
		records[node.NodeID] = record
		*totalServices = projectedServices
		*totalBytes = projectedBytes
		heap.Push(expiries, expiryEntry{
			nodeID: node.NodeID, expiresAt: record.expiresAt,
			generation: record.generation,
		})
		client.published = true
		revision++
		service.send(client.conn, encodeAck(framePublishAck, revision))
		upsert, err := encodeUpsert(revision, node)
		if err != nil {
			service.sendError(client.conn, errs.CodeOf(err))
			return revision
		}
		if ready {
			service.broadcast(clients, upsert)
		}
	case frameWithdraw:
		if len(body) != 0 {
			service.sendError(client.conn, errs.CodeTransportProtocol)
			return revision
		}
		current, exists := records[client.nodeID]
		if exists && current.owner == client.conn &&
			current.node.SessionID == client.sessionID {
			delete(records, client.nodeID)
			*totalServices -= len(current.node.Services)
			*totalBytes -= current.wireSize
			client.published = false
			revision++
			if ready {
				service.broadcast(
					clients,
					encodeDelete(revision, client.nodeID, client.sessionID),
				)
			}
		}
		service.send(client.conn, encodeAck(frameWithdrawAck, revision))
	case frameHeartbeat:
		if len(body) != 0 {
			service.sendError(client.conn, errs.CodeTransportProtocol)
			return revision
		}
		if current, exists := records[client.nodeID]; exists &&
			current.owner == client.conn &&
			current.node.SessionID == client.sessionID {
			current.expiresAt = time.Now().Add(service.config.TTL)
			current.generation++
			records[client.nodeID] = current
			heap.Push(expiries, expiryEntry{
				nodeID: client.nodeID, expiresAt: current.expiresAt,
				generation: current.generation,
			})
		}
		service.send(client.conn, encodeEmpty(frameHeartbeatAck))
	case frameResync:
		if len(body) != 0 || !ready {
			service.sendError(client.conn, errs.CodeTransportProtocol)
			return revision
		}
		service.sendFull(client.conn, epoch, revision, records)
	default:
		service.sendError(client.conn, errs.CodeTransportProtocol)
		client.conn.Close()
	}
	return revision
}

func (service *Service) sendFull(
	conn rpc.SystemPeer,
	epoch uint64,
	revision uint64,
	records map[string]serverRecord,
) {
	nodes := make(map[string]publicprovider.Node, len(records))
	for nodeID, record := range records {
		nodes[nodeID] = record.node
	}
	payload, err := encodeFull(epoch, revision, stableNodes(nodes))
	if err != nil {
		service.sendError(conn, errs.CodeOf(err))
		return
	}
	service.send(conn, payload)
}

func (service *Service) broadcast(
	clients map[rpc.SystemPeer]*serverClient,
	payload []byte,
) {
	for _, client := range clients {
		if client.hello {
			service.send(client.conn, payload)
		}
	}
}

func (service *Service) sendError(conn rpc.SystemPeer, code errs.Code) {
	if code == 0 {
		code = errs.CodeInternal
	}
	service.send(conn, encodeError(code))
}

func (service *Service) send(conn rpc.SystemPeer, payload []byte) {
	if err := conn.Send(payload); err != nil {
		conn.Close()
	}
}

func randomNonZero() (uint64, error) {
	var raw [8]byte
	for {
		if _, err := rand.Read(raw[:]); err != nil {
			return 0, err
		}
		value := binary.BigEndian.Uint64(raw[:])
		if value != 0 {
			return value, nil
		}
	}
}

func warmingDuration(ttl time.Duration) time.Duration {
	result := ttl / 3
	if result > 5*time.Second {
		result = 5 * time.Second
	}
	if result < time.Second {
		result = time.Second
	}
	return result
}

func derivedTimeout(ttl time.Duration) time.Duration {
	result := ttl / 3
	if result < time.Second {
		result = time.Second
	}
	if result > 5*time.Second {
		result = 5 * time.Second
	}
	return result
}
