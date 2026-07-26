package log

import (
	"encoding/json"
	"math"
	"time"
)

// FieldKind 表示结构化字段的值类型。
type FieldKind uint8

const (
	// InvalidField 是零值占位，写入时会被过滤。
	InvalidField FieldKind = iota
	// StringField 保存 UTF-8 字符串。
	StringField
	// BoolField 使用 unsigned 的 0/1 保存布尔值。
	BoolField
	// IntField 保存平台 int 转换后的 int64。
	IntField
	// Int32Field 保存 int32。
	Int32Field
	// Int64Field 保存 int64。
	Int64Field
	// UintField 保存平台 uint 转换后的 uint64。
	UintField
	// Uint32Field 保存 uint32。
	Uint32Field
	// Uint64Field 保存 uint64。
	Uint64Field
	// Float32Field 保存 IEEE 754 位模式。
	Float32Field
	// Float64Field 保存 IEEE 754 位模式。
	Float64Field
	// DurationField 保存 time.Duration 的纳秒整数。
	DurationField
	// TimeField 保存移除单调时钟部分的时间。
	TimeField
	// BytesField 保存调用点复制的字节快照。
	BytesField
	// ErrorField 保存错误在调用点生成的文本。
	ErrorField
	// AnyField 保存调用点生成的 JSON 快照。
	AnyField
)

// Field 是一项不可变的结构化日志字段。
type Field struct {
	// key 和 kind 决定 Handler 应读取哪个存储字段。
	key  string
	kind FieldKind
	// integer 和 unsigned 避免基础数值进入 interface{} 产生装箱。
	integer  int64
	unsigned uint64
	// text 保存字符串或错误文本；bytes 保存字节或 Any JSON。
	text  string
	bytes []byte
	// time 单独保存时间值，避免丢失时区信息。
	time time.Time
}

// String 创建字符串字段。
func String(key, value string) Field {
	// 字符串本身不可变，可以直接保存而不复制底层字节。
	return Field{key: key, kind: StringField, text: value}
}

// Bool 创建布尔字段。
func Bool(key string, value bool) Field {
	// 使用统一数值槽保存布尔值，保持 Field 为紧凑值对象。
	var encoded uint64
	if value {
		encoded = 1
	}
	// encoded 只可能为 0 或 1。
	return Field{key: key, kind: BoolField, unsigned: encoded}
}

// Int 创建 int 字段。
func Int(key string, value int) Field {
	// Go 支持的平台 int 均可无损转换到 int64。
	return Field{key: key, kind: IntField, integer: int64(value)}
}

// Int32 创建 int32 字段。
func Int32(key string, value int32) Field {
	// 统一存入有符号整数槽，Kind 保留原始类型供 Handler 编码。
	return Field{key: key, kind: Int32Field, integer: int64(value)}
}

// Int64 创建 int64 字段。
func Int64(key string, value int64) Field {
	// int64 直接存入有符号整数槽。
	return Field{key: key, kind: Int64Field, integer: value}
}

// Uint 创建 uint 字段。
func Uint(key string, value uint) Field {
	// Go 支持的平台 uint 均可无损转换到 uint64。
	return Field{key: key, kind: UintField, unsigned: uint64(value)}
}

// Uint32 创建 uint32 字段。
func Uint32(key string, value uint32) Field {
	// 统一存入无符号整数槽，Kind 保留原始类型供 Handler 编码。
	return Field{key: key, kind: Uint32Field, unsigned: uint64(value)}
}

// Uint64 创建 uint64 字段。
func Uint64(key string, value uint64) Field {
	// uint64 直接存入无符号整数槽。
	return Field{key: key, kind: Uint64Field, unsigned: value}
}

// Float32 创建 float32 字段。
func Float32(key string, value float32) Field {
	// 保存位模式可避免 Field 增加第二个浮点存储槽。
	return Field{key: key, kind: Float32Field, unsigned: uint64(math.Float32bits(value))}
}

// Float64 创建 float64 字段。
func Float64(key string, value float64) Field {
	// 保存位模式确保 NaN 等特殊值在交给 Handler 前不被改变。
	return Field{key: key, kind: Float64Field, unsigned: math.Float64bits(value)}
}

// Duration 创建时长字段。
func Duration(key string, value time.Duration) Field {
	// time.Duration 的底层是 int64 纳秒，可复用 integer 槽。
	return Field{key: key, kind: DurationField, integer: int64(value)}
}

// Time 创建时间字段并移除单调时钟部分。
func Time(key string, value time.Time) Field {
	// Round(0) 去掉进程内单调时钟数据，只保留可序列化墙上时间。
	return Field{key: key, kind: TimeField, time: value.Round(0)}
}

// Bytes 在调用点复制字节并创建字段。
func Bytes(key string, value []byte) Field {
	// 调用点复制切片，把后续修改与日志协程异步编码完全隔离。
	return Field{key: key, kind: BytesField, bytes: append([]byte(nil), value...)}
}

// Err 使用固定字段名 error，并在调用点保存错误文本。
func Err(value error) Field {
	// nil error 不产生字段，避免输出误导性的空 error。
	if value == nil {
		return Field{}
	}
	// 立即提取文本，日志协程不再持有可能可变的自定义错误对象。
	return Field{key: "error", kind: ErrorField, text: value.Error()}
}

// Any 在调用协程把 value 序列化为不可变 JSON 快照。
func Any(key string, value any) Field {
	// 在业务调用协程完成序列化，避免异步日志读取随后被修改的对象。
	snapshot, err := json.Marshal(value)
	if err != nil {
		// 快照失败仍保留可诊断字段，不让日志 API 向业务返回新错误分支。
		snapshot, _ = json.Marshal(map[string]string{"snapshot_error": err.Error()})
	}
	// snapshot 是新分配字节，所有权直接转移给 Field。
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
