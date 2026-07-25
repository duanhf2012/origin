package log

import (
	"encoding/json"
	"math"
	"time"
)

// FieldKind 表示结构化字段的值类型。
type FieldKind uint8

const (
	InvalidField FieldKind = iota
	StringField
	BoolField
	IntField
	Int32Field
	Int64Field
	UintField
	Uint32Field
	Uint64Field
	Float32Field
	Float64Field
	DurationField
	TimeField
	BytesField
	ErrorField
	AnyField
)

// Field 是一项不可变的结构化日志字段。
type Field struct {
	key      string
	kind     FieldKind
	integer  int64
	unsigned uint64
	text     string
	bytes    []byte
	time     time.Time
}

// String 创建字符串字段。
func String(key, value string) Field {
	return Field{key: key, kind: StringField, text: value}
}

// Bool 创建布尔字段。
func Bool(key string, value bool) Field {
	var encoded uint64
	if value {
		encoded = 1
	}
	return Field{key: key, kind: BoolField, unsigned: encoded}
}

// Int 创建 int 字段。
func Int(key string, value int) Field {
	return Field{key: key, kind: IntField, integer: int64(value)}
}

// Int32 创建 int32 字段。
func Int32(key string, value int32) Field {
	return Field{key: key, kind: Int32Field, integer: int64(value)}
}

// Int64 创建 int64 字段。
func Int64(key string, value int64) Field {
	return Field{key: key, kind: Int64Field, integer: value}
}

// Uint 创建 uint 字段。
func Uint(key string, value uint) Field {
	return Field{key: key, kind: UintField, unsigned: uint64(value)}
}

// Uint32 创建 uint32 字段。
func Uint32(key string, value uint32) Field {
	return Field{key: key, kind: Uint32Field, unsigned: uint64(value)}
}

// Uint64 创建 uint64 字段。
func Uint64(key string, value uint64) Field {
	return Field{key: key, kind: Uint64Field, unsigned: value}
}

// Float32 创建 float32 字段。
func Float32(key string, value float32) Field {
	return Field{key: key, kind: Float32Field, unsigned: uint64(math.Float32bits(value))}
}

// Float64 创建 float64 字段。
func Float64(key string, value float64) Field {
	return Field{key: key, kind: Float64Field, unsigned: math.Float64bits(value)}
}

// Duration 创建时长字段。
func Duration(key string, value time.Duration) Field {
	return Field{key: key, kind: DurationField, integer: int64(value)}
}

// Time 创建时间字段并移除单调时钟部分。
func Time(key string, value time.Time) Field {
	return Field{key: key, kind: TimeField, time: value.Round(0)}
}

// Bytes 在调用点复制字节并创建字段。
func Bytes(key string, value []byte) Field {
	return Field{key: key, kind: BytesField, bytes: append([]byte(nil), value...)}
}

// Err 使用固定字段名 error，并在调用点保存错误文本。
func Err(value error) Field {
	if value == nil {
		return Field{}
	}
	return Field{key: "error", kind: ErrorField, text: value.Error()}
}

// Any 在调用协程把 value 序列化为不可变 JSON 快照。
func Any(key string, value any) Field {
	snapshot, err := json.Marshal(value)
	if err != nil {
		snapshot, _ = json.Marshal(map[string]string{"snapshot_error": err.Error()})
	}
	return Field{key: key, kind: AnyField, bytes: snapshot}
}

// Key 返回字段名。
func (field Field) Key() string {
	return field.key
}

// Kind 返回字段值类型。
func (field Field) Kind() FieldKind {
	return field.kind
}

// StringValue 返回字符串或错误字段值。
func (field Field) StringValue() string {
	return field.text
}

// BoolValue 返回布尔字段值。
func (field Field) BoolValue() bool {
	return field.unsigned != 0
}

// Int64Value 返回有符号整数的统一表示。
func (field Field) Int64Value() int64 {
	return field.integer
}

// Uint64Value 返回无符号整数的统一表示。
func (field Field) Uint64Value() uint64 {
	return field.unsigned
}

// Float32Value 返回 float32 字段值。
func (field Field) Float32Value() float32 {
	return math.Float32frombits(uint32(field.unsigned))
}

// Float64Value 返回 float64 字段值。
func (field Field) Float64Value() float64 {
	return math.Float64frombits(field.unsigned)
}

// DurationValue 返回时长字段值。
func (field Field) DurationValue() time.Duration {
	return time.Duration(field.integer)
}

// TimeValue 返回时间字段值。
func (field Field) TimeValue() time.Time {
	return field.time
}

// BytesValue 返回字段拥有的只读字节。Handler 不得修改或保留该切片。
func (field Field) BytesValue() []byte {
	return field.bytes
}
