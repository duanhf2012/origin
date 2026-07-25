package log_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"testing"
	"time"

	originlog "github.com/duanhf2012/origin/v3/log"
)

func TestFieldValues(t *testing.T) {
	t.Parallel()

	now := time.Now()
	tests := []struct {
		field originlog.Field
		kind  originlog.FieldKind
	}{
		{field: originlog.String("string", "value"), kind: originlog.StringField},
		{field: originlog.Bool("bool", true), kind: originlog.BoolField},
		{field: originlog.Int("int", 1), kind: originlog.IntField},
		{field: originlog.Int32("int32", 2), kind: originlog.Int32Field},
		{field: originlog.Int64("int64", 3), kind: originlog.Int64Field},
		{field: originlog.Uint("uint", 4), kind: originlog.UintField},
		{field: originlog.Uint32("uint32", 5), kind: originlog.Uint32Field},
		{field: originlog.Uint64("uint64", 6), kind: originlog.Uint64Field},
		{field: originlog.Float32("float32", 1.5), kind: originlog.Float32Field},
		{field: originlog.Float64("float64", 2.5), kind: originlog.Float64Field},
		{field: originlog.Duration("duration", time.Second), kind: originlog.DurationField},
		{field: originlog.Time("time", now), kind: originlog.TimeField},
		{field: originlog.Err(errors.New("failed")), kind: originlog.ErrorField},
	}
	for _, test := range tests {
		if test.field.Kind() != test.kind {
			t.Errorf("%s kind = %v, want %v", test.field.Key(), test.field.Kind(), test.kind)
		}
	}
	if got := originlog.Int64("value", -3).Int64Value(); got != -3 {
		t.Errorf("Int64Value() = %d", got)
	}
	if got := originlog.Uint64("value", 6).Uint64Value(); got != 6 {
		t.Errorf("Uint64Value() = %d", got)
	}
	if got := originlog.Float32("value", 1.5).Float32Value(); got != 1.5 {
		t.Errorf("Float32Value() = %f", got)
	}
	if got := originlog.Float64("value", 2.5).Float64Value(); got != 2.5 {
		t.Errorf("Float64Value() = %f", got)
	}
	if got := originlog.Duration("value", time.Second).DurationValue(); got != time.Second {
		t.Errorf("DurationValue() = %v", got)
	}
	if !originlog.Time("value", now).TimeValue().Equal(now) {
		t.Errorf("TimeValue() changed the instant")
	}
	if got := originlog.Err(nil).Kind(); got != originlog.InvalidField {
		t.Errorf("Err(nil).Kind() = %v, want InvalidField", got)
	}
}

func TestBytesAndAnySnapshot(t *testing.T) {
	t.Parallel()

	source := []byte("before")
	bytesField := originlog.Bytes("data", source)
	copy(source, "after!")
	if got := bytesField.BytesValue(); !bytes.Equal(got, []byte("before")) {
		t.Fatalf("Bytes snapshot = %q, want before", got)
	}

	value := map[string]int{"score": 10}
	anyField := originlog.Any("player", value)
	value["score"] = 20

	var snapshot map[string]int
	if err := json.Unmarshal(anyField.BytesValue(), &snapshot); err != nil {
		t.Fatalf("decode Any snapshot: %v", err)
	}
	if snapshot["score"] != 10 {
		t.Fatalf("Any snapshot score = %d, want 10", snapshot["score"])
	}
}

func TestAnyMarshalFailureIsSnapshot(t *testing.T) {
	t.Parallel()

	field := originlog.Any("invalid", make(chan int))
	var snapshot map[string]string
	if err := json.Unmarshal(field.BytesValue(), &snapshot); err != nil {
		t.Fatalf("decode fallback snapshot: %v", err)
	}
	if snapshot["snapshot_error"] == "" {
		t.Fatalf("snapshot_error is empty")
	}
}
