package kafkamodule

import (
	"errors"
	"testing"

	"google.golang.org/protobuf/types/known/structpb"
	"google.golang.org/protobuf/types/known/wrapperspb"
)

func TestJSONEncodingCreatesStableSnapshot(t *testing.T) {
	value := map[string]any{"player_id": int64(9), "level": int64(12)}
	message, err := encodeJSON(JSONMessage{Topic: "player-events", Value: value})
	if err != nil {
		t.Fatal(err)
	}
	value["level"] = int64(99)
	var decoded map[string]any
	if err = (&Message{Value: message.value}).DecodeJSON(&decoded); err != nil {
		t.Fatal(err)
	}
	if level, ok := decoded["level"].(int64); !ok || level != 12 {
		t.Fatalf("integer precision/type or snapshot lost: %#v", decoded)
	}
}

func TestJSONNilEncodesNull(t *testing.T) {
	message, err := encodeJSON(JSONMessage{Topic: "player-events", Value: nil})
	if err != nil || string(message.value) != "null" {
		t.Fatalf("JSON nil semantics changed: %q, %v", message.value, err)
	}
}

func TestPBEncodingAndDecode(t *testing.T) {
	source := wrapperspb.String("player-9")
	message, err := encodePB(PBMessage{Topic: "player-events", Value: source})
	if err != nil {
		t.Fatal(err)
	}
	source.Value = "mutated"
	decoded := &wrapperspb.StringValue{}
	if err = (&Message{Value: message.value}).DecodePB(decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.Value != "player-9" {
		t.Fatalf("PB snapshot lost: %q", decoded.Value)
	}
}

func TestPBRejectsNilAndTypedNil(t *testing.T) {
	var typedNil *wrapperspb.StringValue
	for _, value := range []PBMessage{{Topic: "events"}, {Topic: "events", Value: typedNil}} {
		if _, err := encodePB(value); !errors.Is(err, ErrInvalidArgument) {
			t.Fatalf("nil PB accepted: %v", err)
		}
	}
	message := &Message{Value: []byte{}}
	if err := message.DecodePB(typedNil); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("typed nil destination accepted: %v", err)
	}
}

func TestDecodeRejectsInvalidReceiversAndPayloads(t *testing.T) {
	var message *Message
	if err := message.DecodeJSON(&map[string]any{}); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("nil message accepted: %v", err)
	}
	if err := (&Message{Value: []byte("{")}).DecodeJSON(&map[string]any{}); err == nil {
		t.Fatal("invalid JSON accepted")
	}
	if err := (&Message{Value: []byte{0xff}}).DecodePB(&structpb.Struct{}); err == nil {
		t.Fatal("invalid PB accepted")
	}
}
