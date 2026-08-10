package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"testing"
	"time"

	originlog "github.com/duanhf2012/origin/v3/log"
)

var _ originlog.Handler = (*jsonHandler)(nil)

// TestJSONHandlerWritesCompleteRecord 固定示例承诺的 Record、Field 到 JSON Lines 映射。
func TestJSONHandlerWritesCompleteRecord(t *testing.T) {
	var output bytes.Buffer
	handler := &jsonHandler{output: &output, minimum: originlog.InfoLevel}
	if handler.Enabled(originlog.DebugLevel) {
		t.Fatal("debug must be below the configured minimum")
	}
	if !handler.Enabled(originlog.InfoLevel) {
		t.Fatal("info must be enabled at the configured minimum")
	}
	record := originlog.Record{
		Time:    time.Date(2026, 8, 8, 12, 0, 0, 0, time.FixedZone("CST", 8*60*60)),
		Level:   originlog.InfoLevel,
		Message: "player loaded",
		Caller:  originlog.Caller{File: "player/service.go", Line: 42},
		Stack:   "stack trace",
	}
	if err := handler.Write(record, []originlog.Field{
		originlog.String("name", "alice"),
		originlog.Int64("player_id", 10001),
		originlog.Bool("online", true),
		originlog.Int("int", -1),
		originlog.Int32("int32", -2),
		originlog.Uint("uint", 1),
		originlog.Uint32("uint32", 2),
		originlog.Uint64("uint64", 3),
		originlog.Float32("float32", 1.25),
		originlog.Float64("float64", 2.5),
		originlog.Duration("duration", 1500*time.Millisecond),
		originlog.Time("event_time", record.Time),
		originlog.Bytes("bytes", []byte{0xff, 0x00}),
		originlog.Err(errors.New("disk full")),
		originlog.Any("position", map[string]int{"x": 10, "y": 20}),
	}); err != nil {
		t.Fatalf("Write() error = %v", err)
	}
	var document map[string]any
	if err := json.Unmarshal(output.Bytes(), &document); err != nil {
		t.Fatalf("output is not JSON Lines: %q, error = %v", output.String(), err)
	}
	if document["time"] != "2026-08-08T04:00:00Z" ||
		document["level"] != "info" ||
		document["caller"] != "player/service.go:42" ||
		document["message"] != "player loaded" || document["stack"] != "stack trace" {
		t.Fatalf("record JSON = %#v", document)
	}
	fields := document["fields"].(map[string]any)
	for key, want := range map[string]any{
		"name": "alice", "player_id": float64(10001), "online": true,
		"int": float64(-1), "int32": float64(-2),
		"uint": float64(1), "uint32": float64(2), "uint64": float64(3),
		"float32": float64(1.25), "float64": float64(2.5),
		"duration": "1.5s", "event_time": "2026-08-08T04:00:00Z",
		"bytes": "/wA=", "error": "disk full",
	} {
		if fields[key] != want {
			t.Errorf("field %q = %#v, want %#v", key, fields[key], want)
		}
	}
	position := fields["position"].(map[string]any)
	if position["x"] != float64(10) || position["y"] != float64(20) {
		t.Fatalf("Any field JSON = %#v", position)
	}
	if fieldValue(originlog.Field{}) != nil {
		t.Fatal("invalid Field must map to nil")
	}
	if err := handler.Sync(); err != nil {
		t.Fatalf("Sync() error = %v", err)
	}
	if err := handler.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
}

func TestNewJSONHandlerUsesConfiguredMinimum(t *testing.T) {
	t.Parallel()

	config := originlog.DefaultConfig()
	config.Console.Level = originlog.WarnLevel
	raw, err := newJSONHandler(config)
	if err != nil {
		t.Fatalf("newJSONHandler() error = %v", err)
	}
	handler := raw.(*jsonHandler)
	if handler.Enabled(originlog.InfoLevel) || !handler.Enabled(originlog.WarnLevel) {
		t.Fatal("configured minimum was not applied")
	}
}
