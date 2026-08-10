package origin

import (
	"container/heap"
	"testing"
	"time"

	"github.com/duanhf2012/origin/v3/errs"
	"github.com/duanhf2012/origin/v3/internal/bufferpool"
	originlog "github.com/duanhf2012/origin/v3/log"
	"github.com/duanhf2012/origin/v3/rpc"
)

type originServerTestPeer struct {
	sent   [][]byte
	closed bool
}

func (peer *originServerTestPeer) Send(payload []byte) error {
	peer.sent = append(peer.sent, append([]byte(nil), payload...))
	return nil
}

func (peer *originServerTestPeer) Close() { peer.closed = true }

func TestServerCloseFactSurvivesFullActorQueue(t *testing.T) {
	peer := &originServerTestPeer{}
	service := &Service{
		commands:  make(chan serverCommand, 1),
		closeWake: make(chan struct{}, 1),
	}
	handler := &serverHandler{service: service}
	handler.OnSystemClose(peer, errs.ErrTransportClosed)
	select {
	case <-service.closeWake:
		t.Fatal("未知 Peer 的关闭事实不应唤醒 Actor")
	default:
	}
	handler.OnSystemOpen(peer)
	handler.OnSystemClose(peer, errs.ErrTransportClosed)

	value, exists := service.peerStates.Load(peer)
	if !exists || !value.(*serverPeerState).closed.Load() {
		t.Fatal("Actor 队列已满时丢失了 Peer 关闭事实")
	}
	select {
	case <-service.closeWake:
	default:
		t.Fatal("Peer 关闭没有唤醒 Actor")
	}
}

// TestOriginServerRebindsSameSession verifies that a one-sided NATS disconnect
// may establish a fresh control peer without waiting for the stale peer's TTL.
func TestOriginServerRebindsSameSession(t *testing.T) {
	service := NewService(
		Config{TTL: 3 * time.Second, Server: ServerConfig{Node: "discovery-1"}},
		bufferpool.NewPool(bufferpool.Options{TrackUsage: true}),
		originlog.NewNop(),
	)
	oldPeer := &originServerTestPeer{}
	newPeer := &originServerTestPeer{}
	clients := map[rpc.SystemPeer]*serverClient{
		oldPeer: {conn: oldPeer},
		newPeer: {conn: newPeer},
	}
	records := make(map[string]serverRecord)
	expiries := make(expiryHeap, 0)
	heap.Init(&expiries)
	totalServices := 0
	totalBytes := 0
	epoch := uint64(1)
	revision := uint64(0)

	revision = service.handleMessage(
		clients[oldPeer], clients, records, &expiries,
		&totalServices, &totalBytes, epoch, revision, true,
		encodeHello("game-1", 11),
	)
	publish, err := encodePublish(wireTestNode("game-1", 11))
	if err != nil {
		t.Fatalf("encodePublish() error = %v", err)
	}
	revision = service.handleMessage(
		clients[oldPeer], clients, records, &expiries,
		&totalServices, &totalBytes, epoch, revision, true, publish,
	)
	if revision != 1 || records["game-1"].owner != oldPeer {
		t.Fatalf("initial record = %+v, revision=%d", records["game-1"], revision)
	}

	revision = service.handleMessage(
		clients[newPeer], clients, records, &expiries,
		&totalServices, &totalBytes, epoch, revision, true,
		encodeHello("game-1", 11),
	)
	revision = service.handleMessage(
		clients[newPeer], clients, records, &expiries,
		&totalServices, &totalBytes, epoch, revision, true, publish,
	)

	if revision != 1 {
		t.Fatalf("same Session rebind revision = %d, want 1", revision)
	}
	if records["game-1"].owner != newPeer || !oldPeer.closed ||
		clients[oldPeer].published || !clients[newPeer].published {
		t.Fatalf(
			"same Session was not rebound: record=%+v old=%+v new=%+v",
			records["game-1"], clients[oldPeer], clients[newPeer],
		)
	}
	if len(newPeer.sent) == 0 || newPeer.sent[len(newPeer.sent)-1][0] != framePublishAck {
		t.Fatalf("same Session rebind response = %v", newPeer.sent)
	}
}

// TestExpiryHeapOrdersAndReleasesEntries 验证发现租约按最早到期时间弹出，并在 Pop 时
// 清空底层最后一个槽位，避免长期 Actor 保留已失效 NodeID。
func TestExpiryHeapOrdersAndReleasesEntries(t *testing.T) {
	now := time.Now()
	expiries := expiryHeap{
		{nodeID: "late", expiresAt: now.Add(time.Second)},
		{nodeID: "early", expiresAt: now.Add(100 * time.Millisecond)},
	}
	heap.Init(&expiries)
	first := heap.Pop(&expiries).(expiryEntry)
	second := heap.Pop(&expiries).(expiryEntry)
	if first.nodeID != "early" || second.nodeID != "late" || len(expiries) != 0 {
		t.Fatalf("expiry order = %q, %q remaining=%d", first.nodeID, second.nodeID, len(expiries))
	}
}

// TestCloseDiscoveryBeforePrepareIsIdempotent 验证构造后尚未启动 Actor 的服务也能完成
// 有界关闭，重复调用不会二次关闭 done Channel。
func TestCloseDiscoveryBeforePrepareIsIdempotent(t *testing.T) {
	service := NewService(
		Config{Server: ServerConfig{Node: "discovery-1"}},
		bufferpool.NewPool(bufferpool.Options{}),
		originlog.NewNop(),
	)
	if err := service.CloseDiscovery(t.Context()); err != nil {
		t.Fatalf("first CloseDiscovery() error = %v", err)
	}
	if err := service.CloseDiscovery(t.Context()); err != nil {
		t.Fatalf("second CloseDiscovery() error = %v", err)
	}
	select {
	case <-service.done:
	default:
		t.Fatal("CloseDiscovery() did not close done")
	}
}

var _ rpc.SystemPeer = (*originServerTestPeer)(nil)
