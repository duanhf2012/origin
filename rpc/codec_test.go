package rpc

import (
	"errors"
	"math"
	"strconv"
	"testing"

	"github.com/duanhf2012/origin/v3/errs"
	"github.com/duanhf2012/origin/v3/internal/bufferpool"
	"google.golang.org/protobuf/types/known/wrapperspb"
)

// TestCodecPrimitiveRoundTrip 覆盖固定宽度、浮点、字符串、nil/空字节和容器头的完整往返。
func TestCodecPrimitiveRoundTrip(t *testing.T) {
	t.Parallel()

	sizer := NewSizer()
	for _, size := range []int{1, 1, 2, 2, 4, 4, 8, 8, 8, 8, 4, 8, 1} {
		if err := sizer.Add(size); err != nil {
			t.Fatalf("Sizer.Add(%d) error = %v", size, err)
		}
	}
	if err := sizer.AddString("player"); err != nil {
		t.Fatal(err)
	}
	if err := sizer.AddBytes(nil); err != nil {
		t.Fatal(err)
	}
	if err := sizer.AddBytes([]byte{}); err != nil {
		t.Fatal(err)
	}
	if err := sizer.AddBytes([]byte{1, 2, 3}); err != nil {
		t.Fatal(err)
	}
	if err := sizer.AddContainer(2, false); err != nil {
		t.Fatal(err)
	}
	size, err := sizer.Size()
	if err != nil {
		t.Fatal(err)
	}

	data := make([]byte, size)
	writer := NewWriter(data)
	steps := []error{
		writer.WriteBool(true),
		writer.WriteInt8(-8),
		writer.WriteUint16(16),
		writer.WriteInt16(-16),
		writer.WriteUint32(32),
		writer.WriteInt32(-32),
		writer.WriteUint64(64),
		writer.WriteInt64(-64),
		writer.WriteUint(128),
		writer.WriteInt(-128),
		writer.WriteFloat32(1.25),
		writer.WriteFloat64(-2.5),
		writer.WritePresence(true),
		writer.WriteString("player"),
		writer.WriteBytes(nil),
		writer.WriteBytes([]byte{}),
		writer.WriteBytes([]byte{1, 2, 3}),
		writer.WriteContainer(2, false),
	}
	for index, stepErr := range steps {
		if stepErr != nil {
			t.Fatalf("writer step %d error = %v", index, stepErr)
		}
	}
	if err := writer.Done(); err != nil {
		t.Fatalf("Writer.Done() error = %v", err)
	}

	reader := NewRequestReader(data)
	assertEqual(t, reader.ReadBool, true)
	assertEqual(t, reader.ReadInt8, int8(-8))
	assertEqual(t, reader.ReadUint16, uint16(16))
	assertEqual(t, reader.ReadInt16, int16(-16))
	assertEqual(t, reader.ReadUint32, uint32(32))
	assertEqual(t, reader.ReadInt32, int32(-32))
	assertEqual(t, reader.ReadUint64, uint64(64))
	assertEqual(t, reader.ReadInt64, int64(-64))
	assertEqual(t, reader.ReadUint, uint(128))
	assertEqual(t, reader.ReadInt, int(-128))
	assertEqual(t, reader.ReadFloat32, float32(1.25))
	assertEqual(t, reader.ReadFloat64, float64(-2.5))
	assertEqual(t, reader.ReadPresence, true)
	assertEqual(t, reader.ReadString, "player")

	nilBytes, err := reader.ReadBytes()
	if err != nil || nilBytes != nil {
		t.Fatalf("nil bytes = %#v, error = %v", nilBytes, err)
	}
	emptyBytes, err := reader.ReadBytes()
	if err != nil || emptyBytes == nil || len(emptyBytes) != 0 {
		t.Fatalf("empty bytes = %#v, error = %v", emptyBytes, err)
	}
	bytesValue, err := reader.ReadBytes()
	if err != nil || len(bytesValue) != 3 || bytesValue[2] != 3 {
		t.Fatalf("bytes = %#v, error = %v", bytesValue, err)
	}
	count, isNil, err := reader.ReadContainer()
	if err != nil || isNil || count != 2 {
		t.Fatalf("container = (%d, %t, %v)", count, isNil, err)
	}
	if err := reader.Done(); err != nil {
		t.Fatalf("Reader.Done() error = %v", err)
	}
}

// TestCodecRejectsInvalidData 覆盖非法 bool、截断、nil string、元素数量、尾部和写入越界。
func TestCodecRejectsInvalidData(t *testing.T) {
	t.Parallel()

	requestCases := [][]byte{
		{2},
		{1, 2, 3},
		{0xff, 0xff, 0xff, 0xff},
	}
	for index, data := range requestCases {
		reader := NewRequestReader(data)
		var err error
		switch index {
		case 0:
			_, err = reader.ReadBool()
		case 1:
			_, err = reader.ReadUint64()
		default:
			_, err = reader.ReadString()
		}
		if !errors.Is(err, errs.ErrRPCRequestDecodeFailed) {
			t.Errorf("case %d error = %v", index, err)
		}
	}

	reader := NewResponseReader([]byte{0, 1})
	if _, err := reader.ReadUint8(); err != nil {
		t.Fatal(err)
	}
	if err := reader.Done(); !errors.Is(err, errs.ErrRPCResponseDecodeFailed) {
		t.Fatalf("response tail error = %v", err)
	}

	writer := NewWriter(make([]byte, 1))
	if err := writer.WriteUint64(1); !errors.Is(err, errs.ErrRPCEncodeFailed) {
		t.Fatalf("writer overflow error = %v", err)
	}

	sizer := NewSizer()
	if err := sizer.AddContainer(MaxContainerElements+1, false); !errors.Is(
		err,
		errs.ErrRPCEncodeFailed,
	) {
		t.Fatalf("container overflow error = %v", err)
	}
}

// TestCodecProtobufRoundTrip 验证顶层 Protobuf 直接写入最终 Buffer，并保留 nil/空边界。
func TestCodecProtobufRoundTrip(t *testing.T) {
	t.Parallel()

	message := wrapperspb.String("origin")
	sizer := NewSizer()
	if err := sizer.AddProto(message); err != nil {
		t.Fatal(err)
	}
	size, err := sizer.Size()
	if err != nil {
		t.Fatal(err)
	}
	data := make([]byte, size)
	writer := NewWriter(data)
	if err := writer.WriteProto(message); err != nil {
		t.Fatal(err)
	}
	if err := writer.Done(); err != nil {
		t.Fatal(err)
	}

	reader := NewResponseReader(data)
	payload, isNil, err := reader.ReadProtoPayload()
	if err != nil || isNil {
		t.Fatalf("ReadProtoPayload() = (%d bytes, %t, %v)", len(payload), isNil, err)
	}
	decoded := &wrapperspb.StringValue{}
	if err := UnmarshalProto(payload, decoded, true); err != nil {
		t.Fatal(err)
	}
	if decoded.Value != message.Value {
		t.Fatalf("decoded value = %q", decoded.Value)
	}
	if err := reader.Done(); err != nil {
		t.Fatal(err)
	}
}

// TestResponseWriterAllocatesOnceAndReleases 验证响应只能取得一个最终 Buffer，失败清理配平。
func TestResponseWriterAllocatesOnceAndReleases(t *testing.T) {
	t.Parallel()

	pool := bufferpool.NewPool(bufferpool.Options{TrackUsage: true})
	writer := newResponseWriter(pool, DefaultMaxPayloadSize, 0)
	data, err := writer.Allocate(32)
	if err != nil || len(data) != 32 {
		t.Fatalf("Allocate() = (%d, %v)", len(data), err)
	}
	if _, err := writer.Allocate(1); !errors.Is(err, errs.ErrRPCEncodeFailed) {
		t.Fatalf("duplicate Allocate() error = %v", err)
	}
	buffer := writer.take()
	if buffer == nil {
		t.Fatal("take() returned nil")
	}
	buffer.Release()
	if stats := pool.Stats(); stats.InUseBuffers != 0 {
		t.Fatalf("released stats = %+v", stats)
	}

	failed := newResponseWriter(pool, DefaultMaxPayloadSize, 0)
	if _, err := failed.Allocate(DefaultMaxPayloadSize + 1); !errors.Is(
		err,
		errs.ErrRPCEncodeFailed,
	) {
		t.Fatalf("oversize Allocate() error = %v", err)
	}
	failed.release()
}

// TestReaderChecksElementMinimumBeforeAllocation 验证元素数量与剩余载荷的乘法检查。
func TestReaderChecksElementMinimumBeforeAllocation(t *testing.T) {
	t.Parallel()

	reader := NewRequestReader(make([]byte, 8))
	if err := reader.CheckElements(MaxContainerElements, math.MaxInt); !errors.Is(
		err,
		errs.ErrRPCRequestDecodeFailed,
	) {
		t.Fatalf("CheckElements() error = %v", err)
	}
}

// TestCodecMessageSizeBoundary 锁定包含四字节长度前缀后的准确 4M 上限。
func TestCodecMessageSizeBoundary(t *testing.T) {
	t.Parallel()
	maximumPayload := make([]byte, DefaultMaxPayloadSize-4)
	sizer := NewSizer()
	if err := sizer.AddBytes(maximumPayload); err != nil {
		t.Fatalf("maximum AddBytes() error = %v", err)
	}
	size, err := sizer.Size()
	if err != nil || size != DefaultMaxPayloadSize {
		t.Fatalf("maximum Size() = %d, error = %v", size, err)
	}

	// 再增加一个内容字节就会使“长度前缀 + 内容”超过整条消息硬上限。
	oversize := NewSizer()
	if err := oversize.AddBytes(make([]byte, DefaultMaxPayloadSize-3)); !errors.Is(
		err,
		errs.ErrRPCEncodeFailed,
	) {
		t.Fatalf("oversize AddBytes() error = %v", err)
	}
}

// TestCustomPayloadBoundaryRoundTrip 验证自定义 Codec 共用的长度边界支持零长度和普通
// payload，并严格拒绝保留 nil 标记、截断数据和消息上限溢出。
func TestCustomPayloadBoundaryRoundTrip(t *testing.T) {
	t.Parallel()

	// 先计算两个相邻自定义值，第二个值故意使用零长度，锁定它不是 nil。
	sizer := NewSizer()
	if err := sizer.AddCustom(3); err != nil {
		t.Fatalf("AddCustom(3) error = %v", err)
	}
	if err := sizer.AddCustom(0); err != nil {
		t.Fatalf("AddCustom(0) error = %v", err)
	}
	size, err := sizer.Size()
	if err != nil || size != 11 {
		t.Fatalf("Size() = %d, error = %v", size, err)
	}

	// Writer 直接返回最终 Buffer 中的准确区域，不建立中间 payload。
	data := make([]byte, size)
	writer := NewWriter(data)
	payload, err := writer.ReserveCustom(3)
	if err != nil {
		t.Fatalf("ReserveCustom(3) error = %v", err)
	}
	copy(payload, []byte{1, 2, 3})
	empty, err := writer.ReserveCustom(0)
	if err != nil || empty == nil || len(empty) != 0 {
		t.Fatalf("ReserveCustom(0) = %#v, error = %v", empty, err)
	}
	if err := writer.Done(); err != nil {
		t.Fatalf("Writer.Done() error = %v", err)
	}

	// Reader 返回借用 Slice，并允许调用方在当前解码阶段验证全部内容。
	reader := NewRequestReader(data)
	decoded, err := reader.ReadCustomPayload()
	if err != nil || len(decoded) != 3 || decoded[2] != 3 {
		t.Fatalf("ReadCustomPayload() = %#v, error = %v", decoded, err)
	}
	decodedEmpty, err := reader.ReadCustomPayload()
	if err != nil || decodedEmpty == nil || len(decodedEmpty) != 0 {
		t.Fatalf("empty ReadCustomPayload() = %#v, error = %v", decodedEmpty, err)
	}
	if err := reader.Done(); err != nil {
		t.Fatalf("Reader.Done() error = %v", err)
	}

	// nilLength 是协议保留值，不能被 Size 或 Writer 当成普通自定义 payload。
	invalidSizer := NewSizer()
	if err := invalidSizer.AddCustom(-1); !errors.Is(
		err,
		errs.ErrRPCEncodeFailed,
	) {
		t.Fatalf("negative AddCustom() error = %v", err)
	}
	invalidWriter := NewWriter(make([]byte, 4))
	if _, err := invalidWriter.ReserveCustom(-1); !errors.Is(
		err,
		errs.ErrRPCEncodeFailed,
	) {
		t.Fatalf("negative ReserveCustom() error = %v", err)
	}
	if strconv.IntSize == 64 {
		// 64 位平台可以表达 uint32 nil 保留值，必须显式证明它不会被当成 payload 长度。
		reservedLength := uint64(nilLength)
		reservedSizer := NewSizer()
		if err := reservedSizer.AddCustom(int(reservedLength)); !errors.Is(
			err,
			errs.ErrRPCEncodeFailed,
		) {
			t.Fatalf("reserved AddCustom() error = %v", err)
		}
		reservedWriter := NewWriter(make([]byte, 4))
		if _, err := reservedWriter.ReserveCustom(
			int(reservedLength),
		); !errors.Is(err, errs.ErrRPCEncodeFailed) {
			t.Fatalf("reserved ReserveCustom() error = %v", err)
		}
	}
	shortWriter := NewWriter(make([]byte, 4))
	if _, err := shortWriter.ReserveCustom(1); !errors.Is(
		err,
		errs.ErrRPCEncodeFailed,
	) {
		t.Fatalf("overflow ReserveCustom() error = %v", err)
	}
	headerWriter := NewWriter(make([]byte, 3))
	if _, err := headerWriter.ReserveCustom(0); !errors.Is(
		err,
		errs.ErrRPCEncodeFailed,
	) {
		t.Fatalf("short-header ReserveCustom() error = %v", err)
	}
	fullSizer := NewSizer()
	if err := fullSizer.Add(DefaultMaxPayloadSize); err != nil {
		t.Fatalf("full Sizer.Add() error = %v", err)
	}
	if err := fullSizer.AddCustom(0); !errors.Is(
		err,
		errs.ErrRPCEncodeFailed,
	) {
		t.Fatalf("full AddCustom() error = %v", err)
	}

	// 声明三字节但只携带一字节时必须在进入自定义 Unmarshal 前失败。
	truncated := NewRequestReader([]byte{3, 0, 0, 0, 1})
	if _, err := truncated.ReadCustomPayload(); !errors.Is(
		err,
		errs.ErrRPCRequestDecodeFailed,
	) {
		t.Fatalf("truncated ReadCustomPayload() error = %v", err)
	}
	nilMarked := NewRequestReader([]byte{0xff, 0xff, 0xff, 0xff})
	if _, err := nilMarked.ReadCustomPayload(); !errors.Is(
		err,
		errs.ErrRPCRequestDecodeFailed,
	) {
		t.Fatalf("nil-marked ReadCustomPayload() error = %v", err)
	}

	// Reject 保留 Reader 创建时的请求/响应错误分类。
	response := NewResponseReader(nil)
	if err := response.Reject(); !errors.Is(err, errs.ErrRPCResponseDecodeFailed) {
		t.Fatalf("response Reject() error = %v", err)
	}

	maximum := NewSizer()
	if err := maximum.AddCustom(DefaultMaxPayloadSize - 4); err != nil {
		t.Fatalf("maximum AddCustom() error = %v", err)
	}
	maximumData := make([]byte, DefaultMaxPayloadSize)
	maximumWriter := NewWriter(maximumData)
	maximumPayload, err := maximumWriter.ReserveCustom(
		DefaultMaxPayloadSize - 4,
	)
	if err != nil || len(maximumPayload) != DefaultMaxPayloadSize-4 {
		t.Fatalf(
			"maximum ReserveCustom() length = %d, error = %v",
			len(maximumPayload),
			err,
		)
	}
	if err := maximumWriter.Done(); err != nil {
		t.Fatalf("maximum Writer.Done() error = %v", err)
	}
	maximumReader := NewResponseReader(maximumData)
	maximumDecoded, err := maximumReader.ReadCustomPayload()
	if err != nil || len(maximumDecoded) != DefaultMaxPayloadSize-4 {
		t.Fatalf(
			"maximum ReadCustomPayload() length = %d, error = %v",
			len(maximumDecoded),
			err,
		)
	}
	if err := maximumReader.Done(); err != nil {
		t.Fatalf("maximum Reader.Done() error = %v", err)
	}
	oversize := NewSizer()
	if err := oversize.AddCustom(DefaultMaxPayloadSize - 3); !errors.Is(
		err,
		errs.ErrRPCEncodeFailed,
	) {
		t.Fatalf("oversize AddCustom() error = %v", err)
	}
}

// assertEqual 统一执行返回 `(T, error)` 的基础类型读取断言。
func assertEqual[T comparable](
	t *testing.T,
	read func() (T, error),
	want T,
) {
	t.Helper()
	got, err := read()
	if err != nil {
		t.Fatalf("read error = %v", err)
	}
	if got != want {
		t.Fatalf("read value = %v, want %v", got, want)
	}
}
