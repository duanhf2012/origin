package origin

import (
	"container/heap"
	"testing"
	"time"

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

var _ rpc.SystemPeer = (*originServerTestPeer)(nil)
