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

	// 建立全部主要 Field 构造器样本并锁定 Kind。
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
	// 第一阶段统一检查 Key 对应的类型标签。
	for _, test := range tests {
		if test.field.Kind() != test.kind {
			t.Errorf("%s kind = %v, want %v", test.field.Key(), test.field.Kind(), test.kind)
		}
	}
	// 第二阶段分别检查各底层存储槽的读取结果。
	if !originlog.Bool("value", true).BoolValue() || originlog.Bool("value", false).BoolValue() {
		t.Error("BoolValue() did not preserve true/false")
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

	// Bytes 构造后修改源切片，字段必须仍保留调用点快照。
	source := []byte("before")
	bytesField := originlog.Bytes("data", source)
	copy(source, "after!")
	if got := bytesField.BytesValue(); !bytes.Equal(got, []byte("before")) {
		t.Fatalf("Bytes snapshot = %q, want before", got)
	}

	// Any 构造后修改源 Map，再反解 JSON 验证异步安全快照。
	value := map[string]int{"score": 10}
	anyField := originlog.Any("player", value)
	value["score"] = 20

	// 解码字段保存的 JSON，并断言仍是修改前值。
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

	// Channel 无法 JSON 编码，用于触发快照失败兜底。
	field := originlog.Any("invalid", make(chan int))
	// 兜底本身必须是合法 JSON 并包含可诊断错误文本。
	var snapshot map[string]string
	if err := json.Unmarshal(field.BytesValue(), &snapshot); err != nil {
		t.Fatalf("decode fallback snapshot: %v", err)
	}
	if snapshot["snapshot_error"] == "" {
		t.Fatalf("snapshot_error is empty")
	}
}
