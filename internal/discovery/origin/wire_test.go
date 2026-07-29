package origin

import (
	"bytes"
	"testing"

	publicprovider "github.com/duanhf2012/origin/v3/discovery/provider"
)

func TestNodeWireRoundTripIsDeterministic(t *testing.T) {
	node := wireTestNode("game-1", 17)
	node.Labels = map[string]string{
		"stage":  "prod",
		"region": "cn-east",
	}
	node.Services = []publicprovider.Service{
		{ServiceName: "PlayerService", State: publicprovider.ServiceStateRunning},
		{ServiceName: "ChatService", State: publicprovider.ServiceStateRetired},
	}
	first, err := encodePublish(node)
	if err != nil {
		t.Fatalf("encodePublish() error = %v", err)
	}
	second, err := encodePublish(node)
	if err != nil {
		t.Fatalf("second encodePublish() error = %v", err)
	}
	if !bytes.Equal(first, second) {
		t.Fatal("相同 Node 没有得到确定性 Wire")
	}
	decoded, err := decodePublish(first[1:])
	if err != nil {
		t.Fatalf("decodePublish() error = %v", err)
	}
	if decoded.Services[0].ServiceName != "ChatService" ||
		decoded.Labels["region"] != "cn-east" ||
		decoded.SessionID != 17 {
		t.Fatalf("decoded = %+v", decoded)
	}
}

func TestFullSnapshotRejectsNonCanonicalOrTrailingData(t *testing.T) {
	nodes := []publicprovider.Node{
		wireTestNode("game-1", 1),
		wireTestNode("game-2", 2),
	}
	payload, err := encodeFull(9, 3, nodes)
	if err != nil {
		t.Fatalf("encodeFull() error = %v", err)
	}
	epoch, revision, decoded, err := decodeFull(payload[1:])
	if err != nil {
		t.Fatalf("decodeFull() error = %v", err)
	}
	if epoch != 9 || revision != 3 || len(decoded) != 2 {
		t.Fatalf("full = (%d, %d, %d)", epoch, revision, len(decoded))
	}
	if _, _, _, err := decodeFull(append(payload[1:], 0)); err == nil {
		t.Fatal("尾随数据没有被拒绝")
	}

	reversed, err := encodeFull(9, 3, []publicprovider.Node{nodes[1], nodes[0]})
	if err != nil {
		t.Fatalf("encode reversed full error = %v", err)
	}
	if _, _, _, err := decodeFull(reversed[1:]); err == nil {
		t.Fatal("非规范 Node 顺序没有被拒绝")
	}
}

func TestWireRejectsInvalidDirectionBody(t *testing.T) {
	if _, _, err := decodeHello([]byte{0, 0}); err == nil {
		t.Fatal("非法 Hello 没有被拒绝")
	}
	if _, err := decodeAck([]byte{0, 1}); err == nil {
		t.Fatal("截断 Ack 没有被拒绝")
	}
	if _, err := decodeError([]byte{0, 0, 0, 0}); err == nil {
		t.Fatal("零错误码没有被拒绝")
	}
}

func FuzzDecodeOriginFrames(f *testing.F) {
	hello := encodeHello("game-1", 1)
	full, _ := encodeFull(1, 0, nil)
	publish, _ := encodePublish(wireTestNode("game-1", 1))
	for _, seed := range [][]byte{
		hello,
		full,
		publish,
		{frameHeartbeat},
		{frameError, 0, 0, 0, 1},
	} {
		f.Add(seed)
	}
	f.Fuzz(func(_ *testing.T, payload []byte) {
		if len(payload) == 0 {
			return
		}
		body := payload[1:]
		switch payload[0] {
		case frameHello:
			_, _, _ = decodeHello(body)
		case framePublish:
			_, _ = decodePublish(body)
		case frameHelloAck:
			_, _, _, _ = decodeHelloAck(body)
		case frameFullSnapshot:
			_, _, _, _ = decodeFull(body)
		case frameUpsertNode:
			_, _, _ = decodeUpsert(body)
		case frameDeleteNode:
			_, _, _, _ = decodeDelete(body)
		case framePublishAck, frameWithdrawAck:
			_, _ = decodeAck(body)
		case frameError:
			_, _ = decodeError(body)
		}
	})
}

func BenchmarkEncodeOriginNode(b *testing.B) {
	node := wireTestNode("game-1", 1)
	node.Labels = map[string]string{
		"region": "cn-east",
		"stage":  "production",
	}
	node.Services = make([]publicprovider.Service, 32)
	for index := range node.Services {
		node.Services[index] = publicprovider.Service{
			ServiceName: "Service" + string(rune('A'+index)),
			State:       publicprovider.ServiceStateRunning,
		}
	}
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		if _, err := encodePublish(node); err != nil {
			b.Fatal(err)
		}
	}
}

func wireTestNode(nodeID string, sessionID uint64) publicprovider.Node {
	return publicprovider.Node{
		NodeID:    nodeID,
		SessionID: sessionID,
		Transport: publicprovider.TransportNATS,
		Services: []publicprovider.Service{{
			ServiceName: "PlayerService",
			State:       publicprovider.ServiceStateRunning,
		}},
	}
}
