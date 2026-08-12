package kafkamodule

import (
	standardjson "encoding/json"
	"testing"

	"github.com/bytedance/sonic"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/structpb"
)

type benchmarkPlayerEvent struct {
	EventID  string           `json:"event_id"`
	PlayerID int64            `json:"player_id"`
	Level    int64            `json:"level"`
	Items    []int64          `json:"items"`
	Tags     map[string]int64 `json:"tags"`
}

var benchmarkEvent = benchmarkPlayerEvent{EventID: "evt-10001", PlayerID: 9007199254740991, Level: 88, Items: []int64{1001, 1002, 1003, 1004}, Tags: map[string]int64{"server": 12, "zone": 3}}

func BenchmarkJSONSonic(b *testing.B) {
	b.ReportAllocs()
	for index := 0; index < b.N; index++ {
		if _, err := sonic.Marshal(benchmarkEvent); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkJSONStandardLibrary(b *testing.B) {
	b.ReportAllocs()
	for index := 0; index < b.N; index++ {
		if _, err := standardjson.Marshal(benchmarkEvent); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkPB(b *testing.B) {
	message, err := structpb.NewStruct(map[string]any{"event_id": "evt-10001", "player_id": float64(123456), "level": float64(88)})
	if err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	for index := 0; index < b.N; index++ {
		if _, err = proto.Marshal(message); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkDelivery(b *testing.B) {
	b.ReportAllocs()
	for index := 0; index < b.N; index++ {
		delivery := newDelivery()
		delivery.complete(DeliveryResult{Metadata: Metadata{Offset: int64(index)}})
		if _, ok := delivery.Result(); !ok {
			b.Fatal("delivery did not complete")
		}
	}
}
