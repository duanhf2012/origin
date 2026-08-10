package rpc

import (
	"encoding/binary"
	"math"
	"strconv"

	"github.com/duanhf2012/origin/v3/errs"
	"google.golang.org/protobuf/proto"
)

const nilLength = math.MaxUint32

// Sizer 在生成代码中计算单条方法载荷的准确大小。
//
// Sizer 是栈上小值，不需要池化。任一部分超过默认 4M 上限或发生整数溢出后，后续调用
// 保持返回同一个稳定编码错误。
type Sizer struct {
	size int
	err  error
}

// NewSizer 创建一个使用当前固定消息上限的空大小计算器。
func NewSizer() Sizer {
	return Sizer{}
}

// Add 增加 n 个字节，并在提交前检查负数、整数溢出和消息上限。
func (sizer *Sizer) Add(n int) error {
	// 错误状态保持粘滞，使生成代码可以直接逐字段返回而不会覆盖首个原因。
	if sizer == nil {
		return errs.ErrRPCEncodeFailed
	}
	if sizer.err != nil {
		return sizer.err
	}
	if n < 0 || n > DefaultMaxPayloadSize-sizer.size {
		sizer.err = errs.ErrRPCEncodeFailed
		return sizer.err
	}
	sizer.size += n
	return nil
}

// AddString 增加四字节长度和字符串原始字节。
func (sizer *Sizer) AddString(value string) error {
	// 字符串长度不能使用 nil 标记，最大合法内容仍受整条消息 4M 限制。
	if uint64(len(value)) >= uint64(nilLength) {
		return sizer.fail()
	}
	if err := sizer.Add(4); err != nil {
		return err
	}
	return sizer.Add(len(value))
}

// AddBytes 增加可区分 nil 与非 nil 空值的四字节长度和原始内容。
func (sizer *Sizer) AddBytes(value []byte) error {
	if uint64(len(value)) >= uint64(nilLength) {
		return sizer.fail()
	}
	if err := sizer.Add(4); err != nil {
		return err
	}
	if value == nil {
		return nil
	}
	return sizer.Add(len(value))
}

// AddContainer 增加 Slice 或 Map 的四字节 presence/数量，并校验统一元素上限。
func (sizer *Sizer) AddContainer(length int, isNil bool) error {
	if length < 0 || length > MaxContainerElements {
		return sizer.fail()
	}
	if isNil && length != 0 {
		return sizer.fail()
	}
	return sizer.Add(4)
}

// AddCustom 增加自定义 Codec 的四字节长度边界和准确 payload 长度。
//
// 自定义值自身不使用 nil 标记；外层指针的 nil 语义由生成代码使用 WritePresence 独立
// 表示。该入口在任何状态改变前拒绝负数和保留值，再复用 Add 完成整条消息上限检查。
func (sizer *Sizer) AddCustom(size int) error {
	if size < 0 || uint64(size) >= uint64(nilLength) {
		return sizer.fail()
	}
	if err := sizer.Add(4); err != nil {
		return err
	}
	return sizer.Add(size)
}

// AddProto 增加非 nil 顶层 Protobuf 消息的 presence/长度和标准 Protobuf 内容。
//
// nil 判断由生成代码按声明类型静态完成，避免这里使用反射识别有类型 nil。
func (sizer *Sizer) AddProto(message proto.Message) error {
	if message == nil {
		return sizer.fail()
	}
	size := proto.Size(message)
	if size < 0 || uint64(size) >= uint64(nilLength) {
		return sizer.fail()
	}
	if err := sizer.Add(4); err != nil {
		return err
	}
	return sizer.Add(size)
}

// Size 返回最终准确大小；此前任一步失败时返回零值和稳定编码错误。
func (sizer *Sizer) Size() (int, error) {
	if sizer == nil {
		return 0, errs.ErrRPCEncodeFailed
	}
	if sizer.err != nil {
		return 0, sizer.err
	}
	return sizer.size, nil
}

// fail 记录并返回统一编码错误。
func (sizer *Sizer) fail() error {
	if sizer != nil && sizer.err == nil {
		sizer.err = errs.ErrRPCEncodeFailed
	}
	return errs.ErrRPCEncodeFailed
}

// Writer 把生成代码的字段按固定小端格式写入准确大小的最终 Buffer。
type Writer struct {
	data   []byte
	offset int
	err    error
}

// NewWriter 创建一个覆盖 dst 全部长度的栈上写入器。
func NewWriter(dst []byte) Writer {
	return Writer{data: dst}
}

// WriteBool 写入严格的单字节布尔值。
func (writer *Writer) WriteBool(value bool) error {
	if value {
		return writer.WriteUint8(1)
	}
	return writer.WriteUint8(0)
}

// WriteInt8 写入一字节有符号整数的二进制补码。
func (writer *Writer) WriteInt8(value int8) error {
	return writer.WriteUint8(uint8(value))
}

// WriteUint8 写入一字节无符号整数。
func (writer *Writer) WriteUint8(value uint8) error {
	target, err := writer.reserve(1)
	if err != nil {
		return err
	}
	target[0] = value
	return nil
}

// WriteInt16 写入小端二字节有符号整数。
func (writer *Writer) WriteInt16(value int16) error {
	return writer.WriteUint16(uint16(value))
}

// WriteUint16 写入小端二字节无符号整数。
func (writer *Writer) WriteUint16(value uint16) error {
	target, err := writer.reserve(2)
	if err != nil {
		return err
	}
	binary.LittleEndian.PutUint16(target, value)
	return nil
}

// WriteInt32 写入小端四字节有符号整数。
func (writer *Writer) WriteInt32(value int32) error {
	return writer.WriteUint32(uint32(value))
}

// WriteUint32 写入小端四字节无符号整数。
func (writer *Writer) WriteUint32(value uint32) error {
	target, err := writer.reserve(4)
	if err != nil {
		return err
	}
	binary.LittleEndian.PutUint32(target, value)
	return nil
}

// WriteInt64 写入小端八字节有符号整数。
func (writer *Writer) WriteInt64(value int64) error {
	return writer.WriteUint64(uint64(value))
}

// WriteUint64 写入小端八字节无符号整数。
func (writer *Writer) WriteUint64(value uint64) error {
	target, err := writer.reserve(8)
	if err != nil {
		return err
	}
	binary.LittleEndian.PutUint64(target, value)
	return nil
}

// WriteInt 把 Go int 统一编码为八字节线值。
func (writer *Writer) WriteInt(value int) error {
	return writer.WriteInt64(int64(value))
}

// WriteUint 把 Go uint 统一编码为八字节线值。
func (writer *Writer) WriteUint(value uint) error {
	return writer.WriteUint64(uint64(value))
}

// WriteFloat32 写入 IEEE 754 小端四字节值。
func (writer *Writer) WriteFloat32(value float32) error {
	return writer.WriteUint32(math.Float32bits(value))
}

// WriteFloat64 写入 IEEE 754 小端八字节值。
func (writer *Writer) WriteFloat64(value float64) error {
	return writer.WriteUint64(math.Float64bits(value))
}

// WritePresence 写入普通指针的一字节 nil 标记。
func (writer *Writer) WritePresence(present bool) error {
	return writer.WriteBool(present)
}

// WriteNil 写入 string、[]byte、容器或顶层 Protobuf 共用的四字节 nil 标记。
//
// 生成代码只在静态类型允许 nil 的位置调用；普通 string 不使用该入口。
func (writer *Writer) WriteNil() error {
	return writer.WriteUint32(nilLength)
}

// WriteString 写入四字节长度和字符串原始字节，不构造临时 []byte。
func (writer *Writer) WriteString(value string) error {
	if uint64(len(value)) >= uint64(nilLength) {
		return writer.fail()
	}
	if err := writer.WriteUint32(uint32(len(value))); err != nil {
		return err
	}
	target, err := writer.reserve(len(value))
	if err != nil {
		return err
	}
	copy(target, value)
	return nil
}

// WriteBytes 写入可区分 nil 和非 nil 空值的原始字节。
func (writer *Writer) WriteBytes(value []byte) error {
	if value == nil {
		return writer.WriteUint32(nilLength)
	}
	if uint64(len(value)) >= uint64(nilLength) {
		return writer.fail()
	}
	if err := writer.WriteUint32(uint32(len(value))); err != nil {
		return err
	}
	target, err := writer.reserve(len(value))
	if err != nil {
		return err
	}
	copy(target, value)
	return nil
}

// WriteContainer 写入 Slice 或 Map 的 presence/元素数量。
func (writer *Writer) WriteContainer(length int, isNil bool) error {
	if length < 0 || length > MaxContainerElements || (isNil && length != 0) {
		return writer.fail()
	}
	if isNil {
		return writer.WriteUint32(nilLength)
	}
	return writer.WriteUint32(uint32(length))
}

// ReserveCustom 写入自定义 payload 长度，并返回其准确大小的最终可写区域。
//
// 返回 Slice 只在当前 Writer 生命周期内有效。生成代码必须立即调用具体 Provider 的
// MarshalTo，并验证其返回长度；Provider 不能保存该 Slice。
func (writer *Writer) ReserveCustom(size int) ([]byte, error) {
	if size < 0 || uint64(size) >= uint64(nilLength) {
		return nil, writer.fail()
	}
	if err := writer.WriteUint32(uint32(size)); err != nil {
		return nil, err
	}
	return writer.reserve(size)
}

// WriteProto 直接把非 nil 顶层 Protobuf 消息追加到最终 Buffer。
func (writer *Writer) WriteProto(message proto.Message) error {
	if message == nil {
		return writer.fail()
	}
	size := proto.Size(message)
	if size < 0 || uint64(size) >= uint64(nilLength) {
		return writer.fail()
	}
	if err := writer.WriteUint32(uint32(size)); err != nil {
		return err
	}
	target, err := writer.reserve(size)
	if err != nil {
		return err
	}
	encoded, err := (proto.MarshalOptions{}).MarshalAppend(target[:0], message)
	if err != nil || len(encoded) != size {
		return writer.fail()
	}
	if size > 0 && &encoded[0] != &target[0] {
		// proto.Size 与真正写入不一致时 MarshalAppend 可能扩容；这里禁止隐藏的第二份载荷。
		return writer.fail()
	}
	return nil
}

// Done 验证生成代码恰好覆盖目标 Buffer，没有漏写或越界。
func (writer *Writer) Done() error {
	if writer == nil || writer.err != nil || writer.offset != len(writer.data) {
		return errs.ErrRPCEncodeFailed
	}
	return nil
}

// reserve 取得接下来 size 个可写字节并推进位置。
func (writer *Writer) reserve(size int) ([]byte, error) {
	if writer == nil || writer.err != nil || size < 0 ||
		size > len(writer.data)-writer.offset {
		return nil, writer.fail()
	}
	start := writer.offset
	writer.offset += size
	return writer.data[start:writer.offset], nil
}

// fail 记录并返回粘滞编码错误。
func (writer *Writer) fail() error {
	if writer != nil && writer.err == nil {
		writer.err = errs.ErrRPCEncodeFailed
	}
	return errs.ErrRPCEncodeFailed
}

// Reader 按生成期已知 Schema 从只读方法载荷解码字段。
type Reader struct {
	data       []byte
	offset     int
	failure    error
	failed     bool
	messageMax int
}

// NewRequestReader 创建把非法载荷映射为请求解码错误的 Reader。
func NewRequestReader(data []byte) Reader {
	return newReader(data, errs.ErrRPCRequestDecodeFailed)
}

// NewResponseReader 创建把非法载荷映射为响应解码错误的 Reader。
func NewResponseReader(data []byte) Reader {
	return newReader(data, errs.ErrRPCResponseDecodeFailed)
}

// newReader 在栈上建立固定错误语义，避免每个字段动态包装错误。
func newReader(data []byte, failure error) Reader {
	return Reader{
		data:       data,
		failure:    failure,
		messageMax: DefaultMaxPayloadSize,
	}
}

// ReadBool 读取只允许 0 或 1 的布尔值。
func (reader *Reader) ReadBool() (bool, error) {
	value, err := reader.ReadUint8()
	if err != nil {
		return false, err
	}
	switch value {
	case 0:
		return false, nil
	case 1:
		return true, nil
	default:
		return false, reader.fail()
	}
}

// ReadInt8 读取一字节有符号整数。
func (reader *Reader) ReadInt8() (int8, error) {
	value, err := reader.ReadUint8()
	return int8(value), err
}

// ReadUint8 读取一字节无符号整数。
func (reader *Reader) ReadUint8() (uint8, error) {
	source, err := reader.take(1)
	if err != nil {
		return 0, err
	}
	return source[0], nil
}

// ReadInt16 读取小端二字节有符号整数。
func (reader *Reader) ReadInt16() (int16, error) {
	value, err := reader.ReadUint16()
	return int16(value), err
}

// ReadUint16 读取小端二字节无符号整数。
func (reader *Reader) ReadUint16() (uint16, error) {
	source, err := reader.take(2)
	if err != nil {
		return 0, err
	}
	return binary.LittleEndian.Uint16(source), nil
}

// ReadInt32 读取小端四字节有符号整数。
func (reader *Reader) ReadInt32() (int32, error) {
	value, err := reader.ReadUint32()
	return int32(value), err
}

// ReadUint32 读取小端四字节无符号整数。
func (reader *Reader) ReadUint32() (uint32, error) {
	source, err := reader.take(4)
	if err != nil {
		return 0, err
	}
	return binary.LittleEndian.Uint32(source), nil
}

// ReadInt64 读取小端八字节有符号整数。
func (reader *Reader) ReadInt64() (int64, error) {
	value, err := reader.ReadUint64()
	return int64(value), err
}

// ReadUint64 读取小端八字节无符号整数。
func (reader *Reader) ReadUint64() (uint64, error) {
	source, err := reader.take(8)
	if err != nil {
		return 0, err
	}
	return binary.LittleEndian.Uint64(source), nil
}

// ReadInt 读取八字节线值，并在 32 位目标上拒绝超出本机 int 的数据。
func (reader *Reader) ReadInt() (int, error) {
	value, err := reader.ReadInt64()
	if err != nil {
		return 0, err
	}
	if strconv.IntSize == 32 && (value < math.MinInt32 || value > math.MaxInt32) {
		return 0, reader.fail()
	}
	return int(value), nil
}

// ReadUint 读取八字节线值，并在 32 位目标上拒绝超出本机 uint 的数据。
func (reader *Reader) ReadUint() (uint, error) {
	value, err := reader.ReadUint64()
	if err != nil {
		return 0, err
	}
	if strconv.IntSize == 32 && value > math.MaxUint32 {
		return 0, reader.fail()
	}
	return uint(value), nil
}

// ReadFloat32 读取 IEEE 754 小端四字节值。
func (reader *Reader) ReadFloat32() (float32, error) {
	value, err := reader.ReadUint32()
	return math.Float32frombits(value), err
}

// ReadFloat64 读取 IEEE 754 小端八字节值。
func (reader *Reader) ReadFloat64() (float64, error) {
	value, err := reader.ReadUint64()
	return math.Float64frombits(value), err
}

// ReadPresence 读取普通指针的一字节 presence，并拒绝其他数值。
func (reader *Reader) ReadPresence() (bool, error) {
	return reader.ReadBool()
}

// ReadString 读取长度前缀字符串；string 转换会建立业务独立所有权。
func (reader *Reader) ReadString() (string, error) {
	length, isNil, err := reader.readLength(false)
	if err != nil || isNil {
		return "", reader.fail()
	}
	source, err := reader.take(length)
	if err != nil {
		return "", err
	}
	return string(source), nil
}

// ReadBytes 读取并复制业务可见 []byte，准确保留 nil 与非 nil 空值。
func (reader *Reader) ReadBytes() ([]byte, error) {
	length, isNil, err := reader.readLength(true)
	if err != nil {
		return nil, err
	}
	if isNil {
		return nil, nil
	}
	source, err := reader.take(length)
	if err != nil {
		return nil, err
	}
	if length == 0 {
		return make([]byte, 0), nil
	}
	result := make([]byte, length)
	copy(result, source)
	return result, nil
}

// ReadContainer 读取 Slice 或 Map 的 presence/元素数量。
func (reader *Reader) ReadContainer() (length int, isNil bool, err error) {
	// 容器前缀表示“元素数量”而不是后续字节长度，不能像 string/[]byte 那样直接
	// 与 Remaining 比较。生成代码会在分配前调用 CheckElements，以字段类型的最小
	// 编码尺寸完成第二层校验。
	encoded, err := reader.ReadUint32()
	if err != nil {
		return 0, false, err
	}
	if encoded == nilLength {
		return 0, true, nil
	}
	if encoded > MaxContainerElements {
		return 0, false, reader.fail()
	}
	return int(encoded), false, nil
}

// ReadCustomPayload 读取一个非 nil 自定义值的长度边界和只读 payload。
//
// 长度在返回前已经按剩余数据和单消息上限校验。返回 Slice 只借给当前生成的 Unmarshal
// 调用，业务结果不得继续引用它。
func (reader *Reader) ReadCustomPayload() ([]byte, error) {
	length, isNil, err := reader.readLength(false)
	if err != nil || isNil {
		return nil, reader.fail()
	}
	return reader.take(length)
}

// CheckElements 在分配容器前按元素最小编码大小检查剩余载荷。
func (reader *Reader) CheckElements(count, minimumSize int) error {
	if reader == nil || count < 0 || count > MaxContainerElements || minimumSize < 0 {
		return reader.fail()
	}
	if minimumSize == 0 {
		return nil
	}
	if count > reader.Remaining()/minimumSize {
		return reader.fail()
	}
	return nil
}

// ReadProtoPayload 返回顶层 Protobuf 的只读标准字节和 nil 标记。
//
// 生成代码负责创建准确的具体消息指针并调用 proto.Unmarshal；返回 Slice 只能在请求或
// 响应 Buffer 释放前使用。
func (reader *Reader) ReadProtoPayload() ([]byte, bool, error) {
	length, isNil, err := reader.readLength(true)
	if err != nil || isNil {
		return nil, isNil, err
	}
	source, err := reader.take(length)
	if err != nil {
		return nil, false, err
	}
	return source, false, nil
}

// Remaining 返回尚未消费的载荷字节数。
func (reader *Reader) Remaining() int {
	if reader == nil || reader.offset > len(reader.data) {
		return 0
	}
	return len(reader.data) - reader.offset
}

// Done 验证载荷恰好消费完毕，拒绝多余尾部数据。
func (reader *Reader) Done() error {
	if reader == nil || reader.failed || reader.offset != len(reader.data) {
		return reader.fail()
	}
	return nil
}

// Reject 把自定义 Codec 报告的任意解码失败映射成 Reader 已冻结的请求或响应错误。
//
// 该方法供 origingen 生成代码使用，不保留 Provider 的动态 error，确保本地和后续远端
// RPC 使用相同稳定错误码。
func (reader *Reader) Reject() error {
	return reader.fail()
}

// readLength 读取四字节长度，并根据 nullable 决定是否允许 nil 标记。
func (reader *Reader) readLength(nullable bool) (length int, isNil bool, err error) {
	value, err := reader.ReadUint32()
	if err != nil {
		return 0, false, err
	}
	if value == nilLength {
		if !nullable {
			return 0, false, reader.fail()
		}
		return 0, true, nil
	}
	if uint64(value) > uint64(reader.Remaining()) ||
		uint64(value) > uint64(reader.messageMax) {
		return 0, false, reader.fail()
	}
	return int(value), false, nil
}

// take 返回下一段只读字节，并在任何越界情况下进入失败状态。
func (reader *Reader) take(size int) ([]byte, error) {
	if reader == nil || reader.failed || size < 0 || size > reader.Remaining() {
		return nil, reader.fail()
	}
	start := reader.offset
	reader.offset += size
	return reader.data[start:reader.offset], nil
}

// fail 记录并返回请求或响应 Reader 创建时固定的错误。
func (reader *Reader) fail() error {
	if reader == nil {
		return errs.ErrRPCRequestDecodeFailed
	}
	reader.failed = true
	if reader.failure == nil {
		reader.failure = errs.ErrRPCRequestDecodeFailed
	}
	return reader.failure
}

// UnmarshalProto 把标准 Protobuf 字节解码到生成代码提供的具体消息。
func UnmarshalProto(payload []byte, message proto.Message, response bool) error {
	if message == nil {
		if response {
			return errs.ErrRPCResponseDecodeFailed
		}
		return errs.ErrRPCRequestDecodeFailed
	}
	if err := proto.Unmarshal(payload, message); err != nil {
		if response {
			return errs.ErrRPCResponseDecodeFailed
		}
		return errs.ErrRPCRequestDecodeFailed
	}
	return nil
}
