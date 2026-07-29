package etcd

import (
	"bytes"
	"testing"

	publicprovider "github.com/duanhf2012/origin/v3/discovery/provider"
	"github.com/duanhf2012/origin/v3/errs"
	"google.golang.org/protobuf/encoding/protowire"
)

func TestRecordDeterministicRoundTripAndUnknownFields(t *testing.T) {
	node := recordTestNode("game-1", 42)
	first, err := encodeRecord("cn-east", node)
	if err != nil {
		t.Fatalf("encodeRecord() error = %v", err)
	}
	second, err := encodeRecord("cn-east", node)
	if err != nil || !bytes.Equal(first, second) {
		t.Fatalf("deterministic encode = %v, equal=%v", err, bytes.Equal(first, second))
	}
	withUnknown := protowire.AppendTag(
		append([]byte(nil), first...),
		100,
		protowire.VarintType,
	)
	withUnknown = protowire.AppendVarint(withUnknown, 7)
	network, decoded, err := decodeRecord(withUnknown)
	if err != nil {
		t.Fatalf("decodeRecord() error = %v", err)
	}
	if network != "cn-east" || !nodesEqual(decoded, node) {
		t.Fatalf("decoded = %q %+v", network, decoded)
	}
}

func TestRecordRejectsDuplicateScalarAndNonCanonicalServices(t *testing.T) {
	node := recordTestNode("game-1", 42)
	encoded, err := encodeRecord("cn-east", node)
	if err != nil {
		t.Fatalf("encodeRecord() error = %v", err)
	}
	duplicateSchema := appendVarintField(
		append([]byte(nil), encoded...),
		1,
		recordSchemaV1,
	)
	if _, _, err := decodeRecord(duplicateSchema); !errs.IsCode(
		err,
		errs.CodeDiscoverySnapshotInvalid,
	) {
		t.Fatalf("duplicate schema error = %v", err)
	}

	node.Services = []publicprovider.Service{
		{ServiceName: "ZuluService", State: publicprovider.ServiceStateRunning},
		{ServiceName: "AlphaService", State: publicprovider.ServiceStateRunning},
	}
	// Encoder normalizes caller order; decoder must still reject a manually reversed wire order.
	normalized, err := publicprovider.NormalizeNode(node)
	if err != nil {
		t.Fatalf("NormalizeNode() error = %v", err)
	}
	alpha := appendStringField(nil, 1, normalized.Services[0].ServiceName)
	alpha = appendVarintField(alpha, 2, uint64(normalized.Services[0].State))
	zulu := appendStringField(nil, 1, normalized.Services[1].ServiceName)
	zulu = appendVarintField(zulu, 2, uint64(normalized.Services[1].State))
	base := appendVarintField(nil, 1, recordSchemaV1)
	base = appendStringField(base, 2, node.NodeID)
	base = appendVarintField(base, 3, node.SessionID)
	base = appendStringField(base, 4, "cn-east")
	base = appendVarintField(base, 5, uint64(node.Transport))
	base = appendBytesField(base, 8, zulu)
	base = appendBytesField(base, 8, alpha)
	if _, _, err := decodeRecord(base); !errs.IsCode(
		err,
		errs.CodeDiscoverySnapshotInvalid,
	) {
		t.Fatalf("reversed services error = %v", err)
	}
}

func TestRecordRejectsOversizeWithCapacityCode(t *testing.T) {
	if _, _, err := decodeRecord(
		make([]byte, publicprovider.MaxRecordSize+1),
	); !errs.IsCode(err, errs.CodeDiscoveryCapacity) {
		t.Fatalf("oversize record error = %v", err)
	}
}

func FuzzDecodeEtcdNodeRecord(f *testing.F) {
	seed, _ := encodeRecord("cn-east", recordTestNode("game-1", 42))
	f.Add(seed)
	f.Add([]byte{})
	f.Add([]byte{0xff, 0xff})
	f.Fuzz(func(t *testing.T, data []byte) {
		network, node, err := decodeRecord(data)
		if err != nil {
			return
		}
		reencoded, err := encodeRecord(network, node)
		if err != nil {
			t.Fatalf("accepted record cannot re-encode: %v", err)
		}
		nextNetwork, next, err := decodeRecord(reencoded)
		if err != nil || nextNetwork != network || !nodesEqual(next, node) {
			t.Fatalf("round trip = %q %+v, %v", nextNetwork, next, err)
		}
	})
}

func BenchmarkEncodeEtcdNodeRecord(b *testing.B) {
	node := recordTestNode("game-1", 42)
	b.ReportAllocs()
	for range b.N {
		if _, err := encodeRecord("cn-east", node); err != nil {
			b.Fatal(err)
		}
	}
}

func recordTestNode(nodeID string, sessionID uint64) publicprovider.Node {
	return publicprovider.Node{
		NodeID:    nodeID,
		SessionID: sessionID,
		Labels: map[string]string{
			"region": "cn-east",
			"zone":   "a",
		},
		Transport: publicprovider.TransportTCP,
		Address:   "127.0.0.1:7000",
		Services: []publicprovider.Service{
			{
				ServiceName: "PlayerService",
				State:       publicprovider.ServiceStateRunning,
			},
			{
				ServiceName: "SceneService",
				State:       publicprovider.ServiceStateRetired,
				ContractID:  9,
				ContractFingerprint: [32]byte{
					1, 2, 3,
				},
			},
		},
	}
}
