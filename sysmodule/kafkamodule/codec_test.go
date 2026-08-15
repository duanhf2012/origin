package kafkamodule

import (
	"errors"
	"testing"

	"google.golang.org/protobuf/types/known/structpb"
	"google.golang.org/protobuf/types/known/wrapperspb"
)

func TestJSONEncodingCreatesStableSnapshot(t *testing.T) {
	value := map[string]any{"player_id": int64(9), "level": int64(12)}
	key := []byte("player-9")
	header := []byte("v1")
	message, err := encodeJSON(JSONMessage{Topic: "player-events", Key: key, Value: value, Headers: []Header{{Key: "schema", Value: header}}})
	if err != nil {
		t.Fatal(err)
	}
	value["level"] = int64(99)
	key[0], header[0] = 'X', 'X'
	var decoded map[string]any
	if err = (&Message{Value: message.value}).DecodeJSON(&decoded); err != nil {
		t.Fatal(err)
	}
	if level, ok := decoded["level"].(int64); !ok || level != 12 {
		t.Fatalf("integer precision/type or snapshot lost: %#v", decoded)
	}
	if string(message.key) != "player-9" || string(message.headers[0].Value) != "v1" {
		t.Fatalf("key/header snapshot lost: key=%q headers=%+v", message.key, message.headers)
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
	key := []byte("player-9")
	header := []byte("v1")
	message, err := encodePB(PBMessage{Topic: "player-events", Key: key, Value: source, Headers: []Header{{Key: "schema", Value: header}}})
	if err != nil {
		t.Fatal(err)
	}
	source.Value = "mutated"
	key[0], header[0] = 'X', 'X'
	decoded := &wrapperspb.StringValue{}
	if err = (&Message{Value: message.value}).DecodePB(decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.Value != "player-9" {
		t.Fatalf("PB snapshot lost: %q", decoded.Value)
	}
	if string(message.key) != "player-9" || string(message.headers[0].Value) != "v1" {
		t.Fatalf("key/header snapshot lost: key=%q headers=%+v", message.key, message.headers)
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
	if err := (&Message{Value: []byte(`{} {}`)}).DecodeJSON(&map[string]any{}); err == nil {
		t.Fatal("multiple top-level JSON values accepted")
	}
	if err := (&Message{Value: []byte(`{} x`)}).DecodeJSON(&map[string]any{}); err == nil {
		t.Fatal("invalid trailing JSON accepted")
	}
	invalidUTF8 := append([]byte(`{"value":"`), 0xff)
	invalidUTF8 = append(invalidUTF8, []byte(`"}`)...)
	if err := (&Message{Value: invalidUTF8}).DecodeJSON(&map[string]any{}); err == nil {
		t.Fatal("invalid UTF-8 JSON accepted")
	}
	if err := (&Message{Value: []byte(`{"value":9223372036854775808}`)}).DecodeJSON(&map[string]any{}); err == nil {
		t.Fatal("int64 overflow accepted as a lossy float64")
	}
	if err := (&Message{Value: []byte(`{"value":1e400}`)}).DecodeJSON(&map[string]any{}); err == nil {
		t.Fatal("float64 overflow accepted")
	}
	if err := (&Message{Value: []byte{0xff}}).DecodePB(&structpb.Struct{}); err == nil {
		t.Fatal("invalid PB accepted")
	}
}

func TestDecodeJSONNormalizesInterfaceNumbersInStructuredDestination(t *testing.T) {
	type child struct {
		Count any
	}
	type payload struct {
		Value   any
		Items   [2]any
		Child   *child
		Empty   any
		Missing *child
	}
	message := &Message{Value: []byte(`{"Value":3,"Items":[4,5.5],"Child":{"Count":6}}`)}
	var decoded payload
	if err := message.DecodeJSON(&decoded); err != nil {
		t.Fatal(err)
	}
	if value, ok := decoded.Value.(int64); !ok || value != 3 {
		t.Fatalf("Value = %#v (%T)", decoded.Value, decoded.Value)
	}
	if first, ok := decoded.Items[0].(int64); !ok || first != 4 {
		t.Fatalf("Items[0] = %#v (%T)", decoded.Items[0], decoded.Items[0])
	}
	if second, ok := decoded.Items[1].(float64); !ok || second != 5.5 {
		t.Fatalf("Items[1] = %#v (%T)", decoded.Items[1], decoded.Items[1])
	}
	if decoded.Child == nil {
		t.Fatal("Child is nil")
	}
	if count, ok := decoded.Child.Count.(int64); !ok || count != 6 {
		t.Fatalf("Child.Count = %#v (%T)", decoded.Child.Count, decoded.Child.Count)
	}
	if decoded.Empty != nil || decoded.Missing != nil {
		t.Fatalf("zero fields changed: Empty=%#v Missing=%#v", decoded.Empty, decoded.Missing)
	}
}

func TestDecodeJSONNormalizesNestedInterfaceNumbers(t *testing.T) {
	// 样本同时覆盖 Map、Slice、整数和小数，确保递归归一化只改变整数类型。
	message := &Message{Value: []byte(`{"player":{"id":9007199254740991,"scores":[1,2.5,1e3]}}`)}
	var decoded map[string]any
	if err := message.DecodeJSON(&decoded); err != nil {
		t.Fatal(err)
	}
	player, ok := decoded["player"].(map[string]any)
	if !ok {
		t.Fatalf("player type = %T", decoded["player"])
	}
	if id, ok := player["id"].(int64); !ok || id != 9007199254740991 {
		t.Fatalf("id = %#v (%T)", player["id"], player["id"])
	}
	scores, ok := player["scores"].([]any)
	if !ok || len(scores) != 3 {
		t.Fatalf("scores = %#v", player["scores"])
	}
	if first, ok := scores[0].(int64); !ok || first != 1 {
		t.Fatalf("scores[0] = %#v (%T)", scores[0], scores[0])
	}
	if second, ok := scores[1].(float64); !ok || second != 2.5 {
		t.Fatalf("scores[1] = %#v (%T)", scores[1], scores[1])
	}
	if third, ok := scores[2].(float64); !ok || third != 1000 {
		t.Fatalf("scores[2] = %#v (%T)", scores[2], scores[2])
	}
}
