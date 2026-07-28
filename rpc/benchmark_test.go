package rpc

import (
	"fmt"
	"testing"

	"github.com/duanhf2012/origin/v3/internal/bufferpool"
)

func BenchmarkTargetConstruction(b *testing.B) {
	b.ReportAllocs()
	for b.Loop() {
		target := ToServiceOnNode("game-1", "PlayerService")
		if !target.valid() {
			b.Fatal("Target unexpectedly invalid")
		}
	}
}

func BenchmarkPrimitiveCodec(b *testing.B) {
	pool := bufferpool.NewPool(bufferpool.Options{})
	b.ReportAllocs()
	b.SetBytes(24)
	for b.Loop() {
		buffer := pool.Acquire(24)
		writer := NewWriter(buffer.Bytes())
		_ = writer.WriteInt64(1001)
		_ = writer.WriteFloat64(3.14)
		_ = writer.WriteString("player")
		_ = writer.Done()

		reader := NewRequestReader(buffer.Bytes())
		_, _ = reader.ReadInt64()
		_, _ = reader.ReadFloat64()
		_, _ = reader.ReadString()
		_ = reader.Done()
		buffer.Release()
	}
}

// BenchmarkBytePayloadCodec 保存小消息、普通消息和接近 4M 上限消息的编解码基线。
//
// ReadBytes 按已确认所有权规则复制业务结果，因此 B/op 会真实包含业务独立 Slice；该
// Benchmark 不是零复制 Transport 测试，不能用来推断 M13 网络帧的复制次数。
func BenchmarkBytePayloadCodec(b *testing.B) {
	cases := []int{
		16,
		1024,
		DefaultMaxMessageSize - 4,
	}
	for _, payloadSize := range cases {
		b.Run(fmt.Sprintf("%dB", payloadSize), func(b *testing.B) {
			// 样本和 Pool 在计时前创建，循环只测量准确大小计算、最终写入、读取复制和归还。
			payload := make([]byte, payloadSize)
			pool := bufferpool.NewPool(bufferpool.Options{})
			b.ReportAllocs()
			b.SetBytes(int64(payloadSize))
			b.ResetTimer()
			for b.Loop() {
				sizer := NewSizer()
				if err := sizer.AddBytes(payload); err != nil {
					b.Fatal(err)
				}
				size, err := sizer.Size()
				if err != nil {
					b.Fatal(err)
				}
				buffer := pool.Acquire(size)
				writer := NewWriter(buffer.Bytes())
				if err := writer.WriteBytes(payload); err != nil {
					buffer.Release()
					b.Fatal(err)
				}
				if err := writer.Done(); err != nil {
					buffer.Release()
					b.Fatal(err)
				}

				reader := NewResponseReader(buffer.Bytes())
				decoded, err := reader.ReadBytes()
				if err != nil {
					buffer.Release()
					b.Fatal(err)
				}
				if err := reader.Done(); err != nil {
					buffer.Release()
					b.Fatal(err)
				}
				if len(decoded) != payloadSize {
					buffer.Release()
					b.Fatalf("decoded size = %d", len(decoded))
				}
				buffer.Release()
			}
		})
	}
}

// benchmarkBlobCodec 模拟遵守 M12 所有权规则的变长自定义 Codec。
//
// MarshalTo 直接写最终 Buffer；Unmarshal 必须复制为业务独立 Slice，因此 Benchmark
// 会真实记录大 payload 的必要业务分配。
type benchmarkBlob []byte

type benchmarkBlobCodec struct{}

func (benchmarkBlobCodec) Size(value *benchmarkBlob) (int, error) {
	return len(*value), nil
}

func (benchmarkBlobCodec) MarshalTo(
	dst []byte,
	value *benchmarkBlob,
) (int, error) {
	return copy(dst, *value), nil
}

func (benchmarkBlobCodec) Unmarshal(
	src []byte,
	value *benchmarkBlob,
) error {
	*value = append((*value)[:0], src...)
	return nil
}

// 编译期断言锁定 Benchmark 与公开 StaticCodec 形状一致。
var _ StaticCodec[benchmarkBlob] = benchmarkBlobCodec{}

// BenchmarkCustomPayloadCodec 保存 16B、1KB 和接近 4M 自定义 payload 的完整边界基线。
func BenchmarkCustomPayloadCodec(b *testing.B) {
	for _, payloadSize := range []int{
		16,
		1024,
		DefaultMaxMessageSize - 4,
	} {
		b.Run(fmt.Sprintf("%dB", payloadSize), func(b *testing.B) {
			source := make(benchmarkBlob, payloadSize)
			pool := bufferpool.NewPool(bufferpool.Options{})
			codec := benchmarkBlobCodec{}
			b.ReportAllocs()
			b.SetBytes(int64(payloadSize))
			b.ResetTimer()
			for b.Loop() {
				// Size 和 MarshalTo 都是具体静态调用，payload 只写入一个最终 Buffer。
				customSize, err := codec.Size(&source)
				if err != nil {
					b.Fatal(err)
				}
				sizer := NewSizer()
				if err := sizer.AddCustom(customSize); err != nil {
					b.Fatal(err)
				}
				total, err := sizer.Size()
				if err != nil {
					b.Fatal(err)
				}
				buffer := pool.Acquire(total)
				writer := NewWriter(buffer.Bytes())
				target, err := writer.ReserveCustom(customSize)
				if err != nil {
					buffer.Release()
					b.Fatal(err)
				}
				written, err := codec.MarshalTo(target, &source)
				if err != nil || written != len(target) {
					buffer.Release()
					b.Fatalf("MarshalTo() = %d, %v", written, err)
				}
				if err := writer.Done(); err != nil {
					buffer.Release()
					b.Fatal(err)
				}

				// Unmarshal 建立业务独立所有权，不把 Buffer 借用扩散到循环之外。
				reader := NewResponseReader(buffer.Bytes())
				payload, err := reader.ReadCustomPayload()
				if err != nil {
					buffer.Release()
					b.Fatal(err)
				}
				var decoded benchmarkBlob
				if err := codec.Unmarshal(payload, &decoded); err != nil {
					buffer.Release()
					b.Fatal(err)
				}
				if err := reader.Done(); err != nil {
					buffer.Release()
					b.Fatal(err)
				}
				if len(decoded) != payloadSize {
					buffer.Release()
					b.Fatalf("decoded size = %d", len(decoded))
				}
				buffer.Release()
			}
		})
	}
}

func BenchmarkAwaitLocalCallBaselineAllocation(b *testing.B) {
	// Await localCall 的完成 Channel 会关闭，不能安全复用。该基线用于判断仅池化
	// 外层对象能否抵消代次、晚到响应和 ABA 状态机的维护成本。
	b.ReportAllocs()
	for b.Loop() {
		call := newAwaitCall()
		call.complete(nil, nil)
		_, _ = call.take()
	}
}

func BenchmarkAsyncLocalCallBaselineAllocation(b *testing.B) {
	// Async 还需要提交和中止门闩；三条 Channel 都是一次性终态。
	b.ReportAllocs()
	for b.Loop() {
		call := newAsyncCall()
		call.commit()
		call.complete(nil, nil)
		_, _ = call.take()
	}
}
