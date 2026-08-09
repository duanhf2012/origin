package origin

import (
	"context"
	"sync"
	"time"

	publicprovider "github.com/duanhf2012/origin/v3/discovery/provider"
	"github.com/duanhf2012/origin/v3/errs"
	originlog "github.com/duanhf2012/origin/v3/log"
	"github.com/duanhf2012/origin/v3/rpc"
)

const clientEventCapacity = 256

type clientCommandKind uint8

const (
	clientPublish clientCommandKind = iota + 1
	clientWithdraw
)

type clientCommand struct {
	kind   clientCommandKind
	ctx    context.Context
	node   publicprovider.Node
	result chan error
}

type clientEvent struct {
	peer    rpc.SystemPeer
	payload []byte
}

// clientProvider 是每个 Node 独占的 Origin 控制连接所有者。
type clientProvider struct {
	context publicprovider.Context
	config  Config
	runtime *rpc.Runtime
	target  rpc.SystemTarget
	logger  originlog.Logger

	mu      sync.Mutex
	started bool
	closed  bool
	cancel  context.CancelFunc
	done    chan struct{}

	commands chan clientCommand
	events   chan clientEvent
	// closedPeers 保存不会因消息队列已满而丢失的关闭事实；closeWake 只负责唤醒。
	closedPeers sync.Map
	closeWake   chan struct{}
	startedC    chan error
}

// NewFactory 返回绑定当前 Node RPC Runtime 和静态 Discovery Server 目标的 Factory。
func NewFactory(
	runtime *rpc.Runtime,
	target rpc.SystemTarget,
) publicprovider.Factory {
	return func(context publicprovider.Context) (publicprovider.Provider, error) {
		if runtime == nil || target.NodeID == "" {
			return nil, invalidConfig("Origin Provider RPC Runtime 与 Discovery Server 目标不能为空")
		}
		config, err := DecodeConfig(context.Config)
		if err != nil {
			return nil, err
		}
		return &clientProvider{
			context:   context,
			config:    config,
			runtime:   runtime,
			target:    target,
			logger:    context.Logger,
			commands:  make(chan clientCommand),
			events:    make(chan clientEvent, clientEventCapacity),
			closeWake: make(chan struct{}, 1),
			startedC:  make(chan error, 1),
			done:      make(chan struct{}),
		}, nil
	}
}

func (provider *clientProvider) Start(ctx context.Context) error {
	if provider == nil || ctx == nil {
		return errs.ErrInvalidArgument
	}
	provider.mu.Lock()
	if provider.started || provider.closed {
		provider.mu.Unlock()
		return errs.ErrInvalidArgument
	}
	runCtx, cancel := context.WithCancel(context.Background())
	provider.cancel = cancel
	provider.started = true
	provider.mu.Unlock()

	if err := provider.context.Host.SetTTL(provider.config.TTL); err != nil {
		cancel()
		close(provider.done)
		return err
	}
	provider.context.Host.Report(publicprovider.Report{
		State: publicprovider.StateStarting,
	})
	go provider.ownerLoop(runCtx)

	select {
	case err := <-provider.startedC:
		if err != nil {
			cancel()
			<-provider.done
		}
		return err
	case <-ctx.Done():
		cancel()
		<-provider.done
		return errs.Wrap(errs.CodeOf(ctx.Err()), ctx.Err())
	}
}

func (provider *clientProvider) Publish(
	ctx context.Context,
	node publicprovider.Node,
) error {
	normalized, err := publicprovider.NormalizeNode(node)
	if err != nil {
		return err
	}
	return provider.command(ctx, clientCommand{
		kind:   clientPublish,
		node:   normalized,
		result: make(chan error, 1),
	})
}

func (provider *clientProvider) Withdraw(ctx context.Context) error {
	return provider.command(ctx, clientCommand{
		kind:   clientWithdraw,
		result: make(chan error, 1),
	})
}

func (provider *clientProvider) command(
	ctx context.Context,
	command clientCommand,
) error {
	if provider == nil || ctx == nil {
		return errs.ErrInvalidArgument
	}
	provider.mu.Lock()
	started := provider.started
	closed := provider.closed
	done := provider.done
	provider.mu.Unlock()
	if !started || closed {
		return errs.ErrServiceStopped
	}
	command.ctx = ctx
	select {
	case provider.commands <- command:
	case <-ctx.Done():
		return errs.Wrap(errs.CodeOf(ctx.Err()), ctx.Err())
	case <-done:
		return errs.ErrServiceStopped
	}
	select {
	case err := <-command.result:
		return err
	case <-ctx.Done():
		return errs.Wrap(errs.CodeOf(ctx.Err()), ctx.Err())
	case <-done:
		return errs.ErrServiceStopped
	}
}

func (provider *clientProvider) Close(ctx context.Context) error {
	if provider == nil {
		return nil
	}
	if ctx == nil {
		return errs.ErrInvalidArgument
	}
	provider.mu.Lock()
	if provider.closed {
		done := provider.done
		started := provider.started
		provider.mu.Unlock()
		if started {
			select {
			case <-done:
			case <-ctx.Done():
				return errs.Wrap(errs.CodeOf(ctx.Err()), ctx.Err())
			}
		}
		return nil
	}
	provider.closed = true
	cancel := provider.cancel
	started := provider.started
	provider.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	if started {
		select {
		case <-provider.done:
		case <-ctx.Done():
			return errs.Wrap(errs.CodeOf(ctx.Err()), ctx.Err())
		}
	} else {
		close(provider.done)
	}
	return nil
}

func (provider *clientProvider) ownerLoop(ctx context.Context) {
	defer close(provider.done)
	var peer rpc.SystemPeer
	defer func() {
		if peer != nil {
			peer.Close()
		}
		provider.context.Host.Report(publicprovider.Report{
			State: publicprovider.StateStopped,
		})
	}()

	records := make(map[string]publicprovider.Node)
	var epoch uint64
	var revision uint64
	var desired *publicprovider.Node
	var confirmed *publicprovider.Node
	var pending *clientCommand
	var connected bool
	var synchronized bool
	var heartbeatAck bool
	// automatic 串行化重连后的期望状态对账；Warming 时必须在首个 FullSnapshot 前重注册。
	var automatic clientCommandKind
	var automaticNode publicprovider.Node
	// confirmedOnConnection 只记录当前控制连接已确认的状态，重连时不能复用旧 Ack。
	var confirmedOnConnection *publicprovider.Node
	var reconnects uint64
	var failures uint32
	var startComplete bool
	var stableSince time.Time
	var nextDial time.Time
	randomState := provider.context.SessionID ^ 0x9e3779b97f4a7c15
	backoff := 100 * time.Millisecond
	heartbeatInterval := provider.config.TTL / 3
	if heartbeatInterval < time.Second {
		heartbeatInterval = time.Second
	}
	heartbeat := time.NewTimer(jitterDelay(heartbeatInterval, 10, &randomState))
	defer heartbeat.Stop()

	reportRecovering := func(code errs.Code) {
		failures++
		provider.context.Host.Report(publicprovider.Report{
			State:               publicprovider.StateRecovering,
			Reconnects:          reconnects,
			ConsecutiveFailures: failures,
			ErrorCode:           code,
		})
	}
	reportReady := func() {
		provider.context.Host.Report(publicprovider.Report{
			State:               publicprovider.StateReady,
			Reconnects:          reconnects,
			ConsecutiveFailures: failures,
		})
	}
	failPending := func(err error) {
		if pending != nil {
			pending.result <- err
			pending = nil
		}
	}
	sameNode := func(left, right *publicprovider.Node) bool {
		if left == nil || right == nil {
			return left == nil && right == nil
		}
		return nodeEqual(*left, *right)
	}
	reconcileDesired := func() error {
		if !connected || peer == nil || automatic != 0 {
			return nil
		}
		if desired != nil {
			if sameNode(desired, confirmedOnConnection) {
				if synchronized {
					reportReady()
					if !startComplete {
						startComplete = true
						provider.finishStart(nil)
					}
				}
				return nil
			}
			payload, err := encodePublish(*desired)
			if err != nil {
				return err
			}
			automatic = clientPublish
			automaticNode = *desired
			if err := provider.send(peer, payload); err != nil {
				automatic = 0
				return err
			}
			return nil
		}
		if confirmedOnConnection != nil {
			automatic = clientWithdraw
			if err := provider.send(peer, encodeEmpty(frameWithdraw)); err != nil {
				automatic = 0
				return err
			}
			return nil
		}
		if synchronized {
			confirmed = nil
			reportReady()
			if !startComplete {
				startComplete = true
				provider.finishStart(nil)
			}
		}
		return nil
	}

	for {
		if peer == nil {
			if wait := time.Until(nextDial); wait > 0 {
				retry := time.NewTimer(wait)
				select {
				case <-retry.C:
				case command := <-provider.commands:
					stopTimer(retry)
					if command.kind == clientPublish {
						copyNode := command.node
						desired = &copyNode
					} else {
						desired = nil
					}
					command.result <- errs.ErrDiscoveryUnavailable
					continue
				case <-ctx.Done():
					stopTimer(retry)
					if !startComplete {
						provider.finishStart(errs.Wrap(errs.CodeOf(ctx.Err()), ctx.Err()))
					}
					return
				}
			}
			dialCtx, cancel := context.WithTimeout(ctx, derivedTimeout(provider.config.TTL))
			next, err := provider.runtime.DialSystem(
				dialCtx,
				provider.target,
				&clientHandler{provider: provider},
			)
			cancel()
			if err != nil {
				if ctx.Err() != nil {
					if !startComplete {
						provider.finishStart(errs.Wrap(errs.CodeOf(ctx.Err()), ctx.Err()))
					}
					return
				}
				reportRecovering(errs.CodeDiscoveryUnavailable)
				nextDial = time.Now().Add(jitterDelay(backoff, 20, &randomState))
				backoff *= 2
				if backoff > 5*time.Second {
					backoff = 5 * time.Second
				}
				continue
			}
			peer = next
			connected = true
			synchronized = false
			automatic = 0
			confirmedOnConnection = nil
			heartbeatAck = true
			nextDial = time.Time{}
			stopTimer(heartbeat)
			heartbeat.Reset(jitterDelay(heartbeatInterval, 10, &randomState))
			if reconnects > 0 || startComplete {
				reconnects++
			}
			if err := provider.send(peer, encodeHello(
				provider.context.NodeID,
				provider.context.SessionID,
			)); err != nil {
				peer.Close()
			}
		}

		select {
		case <-ctx.Done():
			failPending(errs.ErrCanceled)
			return
		case command := <-provider.commands:
			if pending != nil {
				command.result <- errs.ErrDiscoveryUnavailable
				continue
			}
			switch command.kind {
			case clientPublish:
				copyNode := command.node
				desired = &copyNode
				if !connected || !synchronized || automatic != 0 {
					command.result <- errs.ErrDiscoveryUnavailable
					continue
				}
				payload, err := encodePublish(copyNode)
				if err != nil {
					command.result <- err
					continue
				}
				pending = &command
				if err := provider.send(peer, payload); err != nil {
					failPending(err)
					peer.Close()
				}
			case clientWithdraw:
				desired = nil
				if !connected || !synchronized || automatic != 0 {
					command.result <- errs.ErrDiscoveryUnavailable
					continue
				}
				pending = &command
				if err := provider.send(peer, encodeEmpty(frameWithdraw)); err != nil {
					failPending(err)
					peer.Close()
				}
			}
		case <-provider.closeWake:
			currentClosed := false
			provider.closedPeers.Range(func(key, _ any) bool {
				provider.closedPeers.Delete(key)
				if key == peer {
					currentClosed = true
				}
				return true
			})
			if currentClosed {
				peer = nil
				connected = false
				synchronized = false
				automatic = 0
				confirmedOnConnection = nil
				stableSince = time.Time{}
				failPending(errs.ErrDiscoveryUnavailable)
				reportRecovering(errs.CodeDiscoveryUnavailable)
				nextDial = time.Now().Add(jitterDelay(backoff, 20, &randomState))
				backoff *= 2
				if backoff > 5*time.Second {
					backoff = 5 * time.Second
				}
			}
		case event := <-provider.events:
			if event.peer != peer {
				continue
			}
			if len(event.payload) == 0 {
				peer.Close()
				continue
			}
			frame := event.payload[0]
			body := event.payload[1:]
			switch frame {
			case frameHelloAck:
				nextEpoch, _, state, err := decodeHelloAck(body)
				if err != nil {
					peer.Close()
					continue
				}
				if epoch != 0 && epoch != nextEpoch {
					records = make(map[string]publicprovider.Node)
					revision = 0
				}
				epoch = nextEpoch
				if state == syncWarming {
					if err := reconcileDesired(); err != nil {
						peer.Close()
					}
				}
			case frameFullSnapshot:
				nextEpoch, nextRevision, nodes, err := decodeFull(body)
				if err != nil || nextEpoch != epoch {
					peer.Close()
					continue
				}
				nextRecords := make(map[string]publicprovider.Node, len(nodes))
				for _, node := range nodes {
					nextRecords[node.NodeID] = node
				}
				if err := provider.context.Host.ReplaceSnapshot(
					publicprovider.Snapshot{Nodes: nodes},
				); err != nil {
					if !startComplete {
						provider.finishStart(err)
					}
					return
				}
				records = nextRecords
				revision = nextRevision
				synchronized = true
				stableSince = time.Now()
				if err := reconcileDesired(); err != nil {
					peer.Close()
				}
			case frameUpsertNode:
				nextRevision, node, err := decodeUpsert(body)
				if err != nil || !synchronized || nextRevision != revision+1 {
					synchronized = false
					reportRecovering(errs.CodeDiscoveryUnavailable)
					_ = provider.send(peer, encodeEmpty(frameResync))
					continue
				}
				records[node.NodeID] = node
				revision = nextRevision
				if err := provider.replaceRecords(records); err != nil {
					peer.Close()
				}
			case frameDeleteNode:
				nextRevision, nodeID, sessionID, err := decodeDelete(body)
				if err != nil || !synchronized || nextRevision != revision+1 {
					synchronized = false
					reportRecovering(errs.CodeDiscoveryUnavailable)
					_ = provider.send(peer, encodeEmpty(frameResync))
					continue
				}
				if current, exists := records[nodeID]; exists &&
					current.SessionID == sessionID {
					delete(records, nodeID)
				}
				revision = nextRevision
				if err := provider.replaceRecords(records); err != nil {
					peer.Close()
				}
			case framePublishAck:
				_, err := decodeAck(body)
				if err != nil {
					peer.Close()
					continue
				}
				if pending != nil && pending.kind == clientPublish {
					copyNode := pending.node
					confirmed = &copyNode
					confirmedOnConnection = &copyNode
					pending.result <- nil
					pending = nil
				}
				if automatic == clientPublish {
					copyNode := automaticNode
					confirmed = &copyNode
					confirmedOnConnection = &copyNode
					automatic = 0
					if err := reconcileDesired(); err != nil {
						peer.Close()
					}
				}
			case frameWithdrawAck:
				_, err := decodeAck(body)
				if err != nil {
					peer.Close()
					continue
				}
				if pending != nil && pending.kind == clientWithdraw {
					confirmed = nil
					confirmedOnConnection = nil
					pending.result <- nil
					pending = nil
				}
				if automatic == clientWithdraw {
					confirmed = nil
					confirmedOnConnection = nil
					automatic = 0
					if err := reconcileDesired(); err != nil {
						peer.Close()
					}
				}
			case frameHeartbeatAck:
				if len(body) != 0 {
					peer.Close()
					continue
				}
				heartbeatAck = true
				if !stableSince.IsZero() &&
					time.Since(stableSince) >= provider.config.TTL {
					failures = 0
					backoff = 100 * time.Millisecond
					stableSince = time.Time{}
				}
				if synchronized && automatic == 0 &&
					sameNode(desired, confirmedOnConnection) {
					reportReady()
				}
			case frameError:
				code, err := decodeError(body)
				if err != nil {
					peer.Close()
					continue
				}
				mapped := errs.New(code)
				if pending != nil {
					if confirmed == nil {
						desired = nil
					} else {
						copyNode := *confirmed
						desired = &copyNode
					}
				}
				failPending(mapped)
				if automatic != 0 {
					automatic = 0
					peer.Close()
				}
				if code == errs.CodeDiscoveryDuplicateNode ||
					code == errs.CodeDiscoverySnapshotInvalid ||
					code == errs.CodeDiscoveryCapacity {
					if !startComplete {
						provider.finishStart(mapped)
						return
					}
				}
			default:
				peer.Close()
			}
		case <-heartbeat.C:
			heartbeat.Reset(jitterDelay(heartbeatInterval, 10, &randomState))
			if !connected {
				continue
			}
			if !heartbeatAck {
				peer.Close()
				continue
			}
			heartbeatAck = false
			if err := provider.send(peer, encodeEmpty(frameHeartbeat)); err != nil {
				peer.Close()
			}
		}
	}
}

// jitterDelay 使用 Provider 私有的确定状态生成对称抖动，避免大量 Node 同时重连或心跳。
func jitterDelay(base time.Duration, percent int64, state *uint64) time.Duration {
	if base <= 0 || percent <= 0 || state == nil {
		return base
	}
	value := *state
	value ^= value << 13
	value ^= value >> 7
	value ^= value << 17
	*state = value
	span := int64(base) * percent / 100
	if span <= 0 {
		return base
	}
	offset := int64(value%(uint64(span)*2+1)) - span
	return base + time.Duration(offset)
}

func stopTimer(timer *time.Timer) {
	if timer == nil || timer.Stop() {
		return
	}
	select {
	case <-timer.C:
	default:
	}
}

func (provider *clientProvider) finishStart(err error) {
	select {
	case provider.startedC <- err:
	default:
	}
}

func (provider *clientProvider) replaceRecords(
	records map[string]publicprovider.Node,
) error {
	return provider.context.Host.ReplaceSnapshot(publicprovider.Snapshot{
		Nodes: stableNodes(records),
	})
}

func (provider *clientProvider) send(peer rpc.SystemPeer, payload []byte) error {
	if peer == nil {
		return errs.ErrDiscoveryUnavailable
	}
	return peer.Send(payload)
}

type clientHandler struct {
	provider *clientProvider
}

func (*clientHandler) OnSystemOpen(rpc.SystemPeer) {}

func (handler *clientHandler) OnSystemMessage(
	peer rpc.SystemPeer,
	payload []byte,
) {
	payload = append([]byte(nil), payload...)
	select {
	case handler.provider.events <- clientEvent{
		peer: peer, payload: payload,
	}:
	default:
		peer.Close()
	}
}

func (handler *clientHandler) OnSystemClose(peer rpc.SystemPeer, _ error) {
	handler.provider.closedPeers.Store(peer, struct{}{})
	select {
	case handler.provider.closeWake <- struct{}{}:
	default:
	}
}

var _ publicprovider.Provider = (*clientProvider)(nil)
