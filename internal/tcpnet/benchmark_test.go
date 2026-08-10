package tcpnet

import (
	"io"
	"net"
	"testing"
	"time"

	"github.com/duanhf2012/origin/v3/internal/bufferpool"
)

func BenchmarkFrameLength(b *testing.B) {
	// 基准覆盖 RPC 默认四字节大端帧头的编码和解码热路径。
	options := FrameOptions{LengthFieldSize: 4}
	var header [4]byte
	b.ReportAllocs()
	b.ResetTimer()
	for index := 0; index < b.N; index++ {
		encodeFrameLength(&header, index&0x3fffff, options)
		_ = decodeFrameLength(header[:], options)
	}
}

func BenchmarkSendQueue(b *testing.B) {
	// 使用关闭统计的 Pool 测量固定环形队列本身，不把诊断原子计数混入结果。
	pool := bufferpool.NewPool(bufferpool.Options{})
	queue := newTestSendQueue(b, 4096, 64*1024*1024)
	b.ReportAllocs()
	b.ResetTimer()
	for index := 0; index < b.N; index++ {
		packet := pool.Acquire(64)
		item := sendItem{buffer: packet, payloadSize: 64, chargeBytes: int64(packet.Capacity())}
		if err := testEnqueue(queue, item); err != nil {
			b.Fatalf("enqueue 失败：%v", err)
		}
		got, ok := testNext(queue)
		if !ok {
			b.Fatal("next 意外结束")
		}
		queue.releaseItem(&got)
	}
}

func BenchmarkWriteItem(b *testing.B) {
	// 直接测量生产 writeItem 热路径，锁定连接级 scatter/gather 描述符的零分配目标。
	pool := bufferpool.NewPool(bufferpool.Options{})
	options := smallConnectionOptions(pool)
	options.MaxMessageSize = 1024
	conn := newConn(
		discardNetConn{},
		options,
		newRecordingHandler(),
		nil,
	)
	b.ReportAllocs()
	b.ResetTimer()
	for index := 0; index < b.N; index++ {
		packet := pool.Acquire(1024)
		item := sendItem{
			buffer:      packet,
			payloadSize: 1024,
			headerSize:  4,
		}
		encodeFrameLength(&item.header, item.payloadSize, options.Frame)
		if err := conn.writeItem(item); err != nil {
			b.Fatalf("writeItem 失败：%v", err)
		}
		packet.Release()
	}
}

func BenchmarkScatterGatherAndCopy(b *testing.B) {
	// 两个子基准为是否拼接完整帧提供同机可复现数据，不设跨机器绝对阈值。
	var header [4]byte
	payload := make([]byte, 1024)
	encodeFrameLength(
		&header,
		len(payload),
		FrameOptions{LengthFieldSize: 4},
	)

	b.Run("scatter_gather", func(b *testing.B) {
		b.ReportAllocs()
		for index := 0; index < b.N; index++ {
			buffers := net.Buffers{header[:], payload}
			if _, err := buffers.WriteTo(io.Discard); err != nil {
				b.Fatal(err)
			}
		}
	})
	b.Run("copy", func(b *testing.B) {
		b.ReportAllocs()
		for index := 0; index < b.N; index++ {
			frame := make([]byte, 0, len(header)+len(payload))
			frame = append(frame, header[:]...)
			frame = append(frame, payload...)
			if _, err := io.Discard.Write(frame); err != nil {
				b.Fatal(err)
			}
		}
	})
}

// discardNetConn 是接收全部写入但不保存数据的 Benchmark net.Conn。
type discardNetConn struct{}

// Read 在写路径基准中不会调用。
func (discardNetConn) Read([]byte) (int, error) {
	return 0, io.EOF
}

// Write 模拟内核一次完整接受当前分片。
func (discardNetConn) Write(data []byte) (int, error) {
	return len(data), nil
}

// Close 不持有真实资源。
func (discardNetConn) Close() error {
	return nil
}

// LocalAddr 返回固定地址。
func (discardNetConn) LocalAddr() net.Addr {
	return testAddr("local")
}

// RemoteAddr 返回固定地址。
func (discardNetConn) RemoteAddr() net.Addr {
	return testAddr("remote")
}

// SetDeadline 对内存替身无操作。
func (discardNetConn) SetDeadline(time.Time) error {
	return nil
}

// SetReadDeadline 对内存替身无操作。
func (discardNetConn) SetReadDeadline(time.Time) error {
	return nil
}

// SetWriteDeadline 对内存替身无操作。
func (discardNetConn) SetWriteDeadline(time.Time) error {
	return nil
}
