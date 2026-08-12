package kafkamodule

import (
	"errors"
	"testing"
)

func TestRawTombstoneRequiresKey(t *testing.T) {
	if _, err := encodeRaw(ProducerMessage{Topic: "player-state", Value: nil}); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("missing tombstone key accepted: %v", err)
	}
	message, err := encodeRaw(ProducerMessage{Topic: "player-state", Key: []byte("p-1"), Value: nil})
	if err != nil || message.value != nil {
		t.Fatalf("valid tombstone rejected: %#v, %v", message, err)
	}
}

func TestRawPreservesZeroCopyBuffers(t *testing.T) {
	value := []byte("event")
	message, err := encodeRaw(ProducerMessage{Topic: "player-events", Value: value})
	if err != nil {
		t.Fatal(err)
	}
	value[0] = 'E'
	if string(message.value) != "Event" {
		t.Fatalf("raw input was copied: %q", message.value)
	}
}

func TestMessageValidationRejectsEmptyTopicAndHeaderKey(t *testing.T) {
	if _, err := encodeRaw(ProducerMessage{Value: []byte("x")}); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("empty topic accepted: %v", err)
	}
	if _, err := encodeRaw(ProducerMessage{Topic: "events", Value: []byte("x"), Headers: []Header{{Value: []byte("v")}}}); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("empty header key accepted: %v", err)
	}
}
