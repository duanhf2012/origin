package etcd

import (
	"testing"

	publicprovider "github.com/duanhf2012/origin/v3/discovery/provider"
	"github.com/duanhf2012/origin/v3/errs"
	clientv3 "go.etcd.io/etcd/client/v3"
)

func TestReserveRangeRecordRejectsCapacityBeforeAccumulation(t *testing.T) {
	node := recordTestNode("game-1", 1)
	tests := []providerSession{
		{rangeNodes: publicprovider.MaxNodes},
		{rangeServices: publicprovider.MaxServices},
		{rangeBytes: publicprovider.MaxSnapshotSize},
	}
	for index := range tests {
		if err := tests[index].reserveRangeRecord(
			node,
			1,
		); !errs.IsCode(err, errs.CodeDiscoveryCapacity) {
			t.Errorf("case %d reserveRangeRecord() error = %v", index, err)
		}
	}
}

func TestIngestRejectsCompactedWatch(t *testing.T) {
	session := providerSession{
		networks: map[string]*networkMirror{
			"cn-east": {
				records:     make(map[string]publicprovider.Node),
				rangeRev:    1,
				observedRev: 1,
			},
		},
	}
	_, err := session.ingest(watchEnvelope{
		network: "cn-east",
		response: clientv3.WatchResponse{
			Canceled:        true,
			CompactRevision: 2,
		},
	})
	if err == nil {
		t.Fatal("compacted WatchResponse unexpectedly accepted")
	}
}
