package kafkamodule

import (
	standardjson "encoding/json"
	"fmt"
	"testing"

	"github.com/bytedance/sonic"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/wrapperspb"
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

func BenchmarkRawPrepare(b *testing.B) {
	message := ProducerMessage{Topic: "player-events", Key: []byte("player-10001"), Value: []byte(`{"event_id":"evt-10001","player_id":9007199254740991,"level":88}`)}
	b.ReportAllocs()
	for index := 0; index < b.N; index++ {
		if _, err := encodeRaw(message); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkJSONPrepare(b *testing.B) {
	message := JSONMessage{Topic: "player-events", Key: []byte("player-10001"), Value: benchmarkEvent}
	b.ReportAllocs()
	for index := 0; index < b.N; index++ {
		if _, err := encodeJSON(message); err != nil {
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
	message := wrapperspb.Int64(9007199254740991)
	b.ReportAllocs()
	for index := 0; index < b.N; index++ {
		if _, err := proto.Marshal(message); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkPBPrepare(b *testing.B) {
	message := PBMessage{Topic: "player-events", Key: []byte("player-10001"), Value: wrapperspb.Int64(9007199254740991)}
	b.ReportAllocs()
	for index := 0; index < b.N; index++ {
		if _, err := encodePB(message); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkRawBatchPrepare(b *testing.B) {
	for _, size := range []int{10, 100, 1000} {
		b.Run(fmt.Sprintf("Messages%d", size), func(b *testing.B) {
			messages := make([]ProducerMessage, size)
			for index := range messages {
				messages[index] = ProducerMessage{Topic: "player-events", Key: []byte("player-10001"), Value: []byte("payload")}
			}
			b.ReportAllocs()
			for iteration := 0; iteration < b.N; iteration++ {
				for _, message := range messages {
					if _, err := encodeRaw(message); err != nil {
						b.Fatal(err)
					}
				}
			}
		})
	}
}

func BenchmarkSubmitQueueBudget(b *testing.B) {
	queue := newSubmitQueue(1, 1024)
	envelope := &producerEnvelope{encoded: &encodedMessage{payloadBytes: 128}, delivery: newDelivery()}
	b.ReportAllocs()
	for index := 0; index < b.N; index++ {
		if err := queue.trySubmit(envelope); err != nil {
			b.Fatal(err)
		}
		<-queue.items
		queue.release(envelope)
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
