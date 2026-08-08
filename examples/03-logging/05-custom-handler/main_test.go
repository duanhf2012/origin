package main

import (
	"bytes"
	"encoding/json"
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
	}
	if err := handler.Write(record, []originlog.Field{
		originlog.Int64("player_id", 10001),
		originlog.Bool("online", true),
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
		document["message"] != "player loaded" {
		t.Fatalf("record JSON = %#v", document)
	}
	fields := document["fields"].(map[string]any)
	if fields["player_id"] != float64(10001) || fields["online"] != true {
		t.Fatalf("field JSON = %#v", fields)
	}
	position := fields["position"].(map[string]any)
	if position["x"] != float64(10) || position["y"] != float64(20) {
		t.Fatalf("Any field JSON = %#v", position)
	}
}
