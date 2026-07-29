package tcpnet

import (
	"context"
	"encoding/binary"
	"errors"
	"io"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/duanhf2012/origin/v3/errs"
	"github.com/duanhf2012/origin/v3/internal/bufferpool"
)

const testWaitTimeout = 3 * time.Second

// recordingHandler 是连接测试使用的可编排 Handler。
//
// 默认 OnMessage 会复制可见字节并立即释放 Buffer；各测试也可以覆盖任一回调，
// 精确制造返回错误、panic、阻塞收尾或所有权继续转移等路径。
type recordingHandler struct {
	mu     sync.Mutex
	events []string

	opened   chan struct{}
	messages chan []byte
	closed   chan error

	onOpen    func(*Conn)
	onMessage func(*Conn, *bufferpool.Buffer) error
	onClose   func(*Conn, error)
}

// newRecordingHandler 创建带有有界观测通道的默认 Handler。
func newRecordingHandler() *recordingHandler {
	// 通道只用于测试同步，不参与生产实现；容量避免 ReadLoop 因测试尚未读取而阻塞。
	return &recordingHandler{
		opened:   make(chan struct{}, 1),
		messages: make(chan []byte, 16),
		closed:   make(chan error, 1),
	}
}

// OnOpen 记录生命周期顺序并执行可选测试逻辑。
func (handler *recordingHandler) OnOpen(conn *Conn) {
	// 先记录事件，保证即使自定义逻辑 panic，也能验证回调确实到达。
	handler.appendEvent("open")
	if handler.onOpen != nil {
		handler.onOpen(conn)
	}
	select {
	case handler.opened <- struct{}{}:
	default:
	}
}

// OnMessage 默认复制消息并归还 Buffer，也允许测试接管完整所有权路径。
func (handler *recordingHandler) OnMessage(
	conn *Conn,
	packet *bufferpool.Buffer,
) error {
	// 每条消息都记录同一个串行事件标记，具体数据通过 messages 通道验证。
	handler.appendEvent("message")
	if handler.onMessage != nil {
		return handler.onMessage(conn, packet)
	}

	// 默认实现必须在退出前释放网络层已经转移过来的唯一所有权。
	copied := append([]byte(nil), packet.Bytes()...)
	packet.Release()
	handler.messages <- copied
	return nil
}

// OnClose 记录最终原因并执行可选收尾逻辑。
func (handler *recordingHandler) OnClose(conn *Conn, cause error) {
	// OnClose 必须成为同一连接最后一个事件。
	handler.appendEvent("close")
	if handler.onClose != nil {
		handler.onClose(conn, cause)
	}
	select {
	case handler.closed <- cause:
	default:
	}
}

// appendEvent 保护跨测试 goroutine 读取的事件序列。
func (handler *recordingHandler) appendEvent(event string) {
	// Handler 自身可能被多个不同连接并发调用，因此测试替身也遵守线程安全约束。
	handler.mu.Lock()
	handler.events = append(handler.events, event)
	handler.mu.Unlock()
}

// eventSnapshot 返回独立副本，避免断言与回调并发访问底层切片。
func (handler *recordingHandler) eventSnapshot() []string {
	// 复制后立即释放锁，使失败输出不会延长临界区。
	handler.mu.Lock()
	events := append([]string(nil), handler.events...)
	handler.mu.Unlock()
	return events
}

func TestConnReceivesEmptyAndNonEmptyFrames(t *testing.T) {
	t.Parallel()

	// 使用跟踪 Pool 和内存管道，直接控制远端线协议字节。
	pool := bufferpool.NewPool(bufferpool.Options{TrackUsage: true})
	options := smallConnectionOptions(pool)
	options.Frame.LengthFieldSize = 1
	handler := newRecordingHandler()
	local, remote := net.Pipe()
	conn := newConn(local, options, handler, nil)
	conn.start()

	// 一次写入连续发送空帧和三字节帧，覆盖粘包情况下的逐帧解析。
	if _, err := remote.Write([]byte{0, 3, 'a', 'b', 'c'}); err != nil {
		t.Fatalf("写入测试帧失败：%v", err)
	}
	assertMessage(t, handler.messages, nil)
	assertMessage(t, handler.messages, []byte("abc"))

	// 远端正常关闭会让本端以 TransportUnavailable 结束。
	if err := remote.Close(); err != nil {
		t.Fatalf("关闭测试远端失败：%v", err)
	}
	err := waitConn(t, conn)
	if !errs.IsCode(err, errs.CodeTransportUnavailable) {
		t.Fatalf("Conn.Wait error=%v", err)
	}

	// 生命周期必须严格有序，两个接收 Buffer 也必须全部归还。
	wantEvents := []string{"open", "message", "message", "close"}
	if got := handler.eventSnapshot(); !equalStrings(got, wantEvents) {
		t.Fatalf("Handler 事件=%v，期望=%v", got, wantEvents)
	}
	assertPoolEmpty(t, pool)
}

func TestConnSendWritesFramesAndOwnsBuffers(t *testing.T) {
	t.Parallel()

	// 本测试从管道远端读取真实帧，验证头部字节序、空 payload 和发送所有权。
	pool := bufferpool.NewPool(bufferpool.Options{TrackUsage: true})
	options := smallConnectionOptions(pool)
	options.Frame.LengthFieldSize = 2
	options.Frame.ByteOrder = LittleEndian
	handler := newRecordingHandler()
	local, remote := net.Pipe()
	conn := newConn(local, options, handler, nil)
	conn.start()

	// Send 成功后测试不能再访问 Buffer，只在远端确认线协议数据。
	empty := pool.Acquire(0)
	if err := conn.Send(empty); err != nil {
		t.Fatalf("发送空 Buffer 失败：%v", err)
	}
	payload := pool.Acquire(4)
	copy(payload.Bytes(), "data")
	if err := conn.Send(payload); err != nil {
		t.Fatalf("发送 payload 失败：%v", err)
	}

	// 读取两个完整帧；二字节小端长度分别应为 0 和 4。
	assertRawFrame(t, remote, LittleEndian, 2, nil)
	assertRawFrame(t, remote, LittleEndian, 2, []byte("data"))

	// 主动关闭必须打断 ReadLoop，并在 Wait 前释放全部发送对象。
	conn.Close()
	if err := waitConn(t, conn); !errs.IsCode(err, errs.CodeTransportClosed) {
		t.Fatalf("主动关闭 error=%v", err)
	}
	_ = remote.Close()
	assertPoolEmpty(t, pool)
}

func TestConnSendRejectsInvalidPayloadWithoutTakingOwnership(t *testing.T) {
	t.Parallel()

	// 不启动连接循环即可验证 Send 的纯准入边界；测试结束显式关闭底层管道。
	pool := bufferpool.NewPool(bufferpool.Options{TrackUsage: true})
	options := smallConnectionOptions(pool)
	options.MaxMessageSize = 4
	local, remote := net.Pipe()
	conn := newConn(local, options, newRecordingHandler(), nil)
	defer local.Close()
	defer remote.Close()

	// nil 和超大 Buffer 均应被拒绝；超大对象仍由调用方持有并负责释放。
	if err := conn.Send(nil); !errs.IsCode(err, errs.CodeInvalidArgument) {
		t.Fatalf("Send(nil) error=%v", err)
	}
	oversize := pool.Acquire(5)
	if err := conn.Send(oversize); !errors.Is(err, errs.ErrTransportMessageTooLarge) {
		t.Fatalf("发送超大消息 error=%v", err)
	}
	if got := len(oversize.Bytes()); got != 5 {
		t.Fatalf("Send 失败后 Buffer 已失效，长度=%d", got)
	}
	oversize.Release()

	// Close 后拒绝的 Buffer 同样必须保持有效。
	conn.Close()
	closedPayload := pool.Acquire(1)
	if err := conn.Send(closedPayload); !errors.Is(err, errs.ErrTransportClosed) {
		t.Fatalf("关闭后 Send error=%v", err)
	}
	closedPayload.Bytes()[0] = 1
	closedPayload.Release()
	assertPoolEmpty(t, pool)
}

func TestConnSendOverloadAndCloseReleaseEveryBuffer(t *testing.T) {
	t.Parallel()

	// blockingConn 会在 Writer 开始首个 Write 后停住，使队列水位完全可控。
	pool := bufferpool.NewPool(bufferpool.Options{TrackUsage: true})
	options := smallConnectionOptions(pool)
	options.SendQueueFrames = 2
	raw := newBlockingConn()
	conn := newConn(raw, options, newRecordingHandler(), nil)
	conn.start()

	// 第一帧被 Writer 取走并阻塞，后两帧恰好占满固定队列。
	first := acquireBytes(pool, "1111")
	if err := conn.Send(first); err != nil {
		t.Fatalf("首帧 Send 失败：%v", err)
	}
	select {
	case <-raw.writeStarted:
	case <-time.After(testWaitTimeout):
		t.Fatal("WriteLoop 没有开始写入")
	}
	for _, value := range []string{"2222", "3333"} {
		packet := acquireBytes(pool, value)
		if err := conn.Send(packet); err != nil {
			t.Fatalf("填充队列 Send(%s) 失败：%v", value, err)
		}
	}

	// 第四帧因帧数和字节额度均已耗尽而失败，所有权仍属于测试。
	rejected := acquireBytes(pool, "4444")
	if err := conn.Send(rejected); !errors.Is(err, errs.ErrTransportOverloaded) {
		t.Fatalf("队列满 Send error=%v", err)
	}
	rejected.Bytes()[0] = 'x'
	rejected.Release()

	// Close 同时释放活动写项和两个排队项；重复 Close 不得重复释放。
	conn.Close()
	conn.Close()
	if err := waitConn(t, conn); !errs.IsCode(err, errs.CodeTransportClosed) {
		t.Fatalf("关闭阻塞连接 error=%v", err)
	}
	assertPoolEmpty(t, pool)
}

func TestConnReadTimeoutAndTruncatedPayload(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		write func(net.Conn)
		code  errs.Code
	}{
		{
			name: "read timeout",
			write: func(net.Conn) {
				// 不发送任何字节，让 ReadDeadline 结束头部读取。
			},
			code: errs.CodeDeadlineExceeded,
		},
		{
			name: "truncated header",
			write: func(remote net.Conn) {
				// 四字节长度头只发送两字节，随后关闭远端制造半个帧头。
				_, _ = remote.Write([]byte{0, 0})
				_ = remote.Close()
			},
			code: errs.CodeTransportUnavailable,
		},
		{
			name: "truncated payload",
			write: func(remote net.Conn) {
				// 声明三字节却只发送一个字节，随后关闭远端制造半帧 EOF。
				_, _ = remote.Write([]byte{0, 0, 0, 3, 'x'})
				_ = remote.Close()
			},
			code: errs.CodeTransportUnavailable,
		},
		{
			name: "message too large",
			write: func(remote net.Conn) {
				// 只写入超过上限的长度头，ReadLoop 必须在分配 payload 前拒绝。
				var header [4]byte
				binary.BigEndian.PutUint32(header[:], 17)
				_, _ = remote.Write(header[:])
			},
			code: errs.CodeTransportMessageTooLarge,
		},
	}

	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			// 每个错误路径使用独立 Pool，测试完成时应无任何活跃 Buffer。
			pool := bufferpool.NewPool(bufferpool.Options{TrackUsage: true})
			options := smallConnectionOptions(pool)
			options.ReadTimeout = 40 * time.Millisecond
			local, remote := net.Pipe()
			conn := newConn(local, options, newRecordingHandler(), nil)
			conn.start()
			test.write(remote)

			err := waitConn(t, conn)
			if !errs.IsCode(err, test.code) {
				t.Fatalf("Conn.Wait error=%v，期望 code=%d", err, test.code)
			}
			_ = remote.Close()
			assertPoolEmpty(t, pool)
		})
	}
}

func TestConnConcurrentSendAndClose(t *testing.T) {
	t.Parallel()

	// 大量发送者与 Close 竞争，重点交给 -race 验证队列状态和 Buffer 所有权边界。
	pool := bufferpool.NewPool(bufferpool.Options{TrackUsage: true})
	options := smallConnectionOptions(pool)
	options.SendQueueFrames = 32
	raw := newBlockingConn()
	conn := newConn(raw, options, newRecordingHandler(), nil)
	conn.start()

	const (
		workers    = 16
		iterations = 200
	)
	start := make(chan struct{})
	var wait sync.WaitGroup
	wait.Add(workers)
	for worker := 0; worker < workers; worker++ {
		go func(worker int) {
			defer wait.Done()
			<-start
			for iteration := 0; iteration < iterations; iteration++ {
				packet := pool.Acquire(8)
				packet.Bytes()[0] = byte(worker)
				if err := conn.Send(packet); err != nil {
					// 过载或关闭都表示所有权未转移，发送者立即归还。
					packet.Release()
					if !errors.Is(err, errs.ErrTransportOverloaded) &&
						!errors.Is(err, errs.ErrTransportClosed) {
						t.Errorf("并发 Send error=%v", err)
						return
					}
				}
			}
		}(worker)
	}
	close(start)

	// Close 与正在执行的 enqueue 竞争，但只能形成“成功并由队列释放”或“失败自行释放”。
	conn.Close()
	wait.Wait()
	if err := waitConn(t, conn); !errs.IsCode(err, errs.CodeTransportClosed) {
		t.Fatalf("并发关闭终态=%v", err)
	}
	assertPoolEmpty(t, pool)
}

func TestConnWriteTimeoutReleasesActiveBuffer(t *testing.T) {
	t.Parallel()

	// net.Pipe 的远端不读取时 Write 会阻塞，因此可以稳定触发每帧 WriteDeadline。
	pool := bufferpool.NewPool(bufferpool.Options{TrackUsage: true})
	options := smallConnectionOptions(pool)
	options.WriteTimeout = 40 * time.Millisecond
	local, remote := net.Pipe()
	conn := newConn(local, options, newRecordingHandler(), nil)
	conn.start()

	packet := acquireBytes(pool, "blocked")
	if err := conn.Send(packet); err != nil {
		t.Fatalf("Send 失败：%v", err)
	}
	err := waitConn(t, conn)
	if !errs.IsCode(err, errs.CodeDeadlineExceeded) {
		t.Fatalf("WriteTimeout error=%v", err)
	}
	_ = remote.Close()
	assertPoolEmpty(t, pool)
}

func TestWriteLoopPanicReleasesActiveBuffer(t *testing.T) {
	t.Parallel()

	// 故意构造内部长度记账不一致，验证 Writer 最外层恢复和活动 Buffer 回收。
	pool := bufferpool.NewPool(bufferpool.Options{TrackUsage: true})
	options := smallConnectionOptions(pool)
	raw := newBlockingConn()
	conn := newConn(raw, options, newRecordingHandler(), nil)
	packet := acquireBytes(pool, "panic")
	item := sendItem{
		buffer:      packet,
		payloadSize: len(packet.Bytes()) + 1,
		headerSize:  4,
	}
	encodeFrameLength(&item.header, item.payloadSize, options.Frame)
	if err := conn.send.enqueue(item); err != nil {
		t.Fatalf("注入测试队列项失败：%v", err)
	}
	conn.start()

	// writeItem panic 必须转换为 CodeInternal 并关闭阻塞中的 ReadLoop。
	if err := waitConn(t, conn); !errs.IsCode(err, errs.CodeInternal) {
		t.Fatalf("WriteLoop panic 终态=%v", err)
	}
	assertPoolEmpty(t, pool)
}

func TestConnHandlerFailuresAreIsolated(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		configure func(*recordingHandler)
		trigger   func(net.Conn)
	}{
		{
			name: "OnOpen panic",
			configure: func(handler *recordingHandler) {
				handler.onOpen = func(*Conn) {
					panic("open")
				}
			},
			trigger: func(net.Conn) {},
		},
		{
			name: "OnMessage error",
			configure: func(handler *recordingHandler) {
				handler.onMessage = func(_ *Conn, packet *bufferpool.Buffer) error {
					// Handler 已经取得所有权，返回错误前仍须主动释放。
					packet.Release()
					return errors.New("message failed")
				}
			},
			trigger: func(remote net.Conn) {
				_, _ = remote.Write([]byte{0, 0, 0, 0})
			},
		},
		{
			name: "OnMessage panic",
			configure: func(handler *recordingHandler) {
				handler.onMessage = func(_ *Conn, packet *bufferpool.Buffer) error {
					// panic 前本地保护释放，模拟 M10 Adapter 必须遵守的所有权规则。
					packet.Release()
					panic("message")
				}
			},
			trigger: func(remote net.Conn) {
				_, _ = remote.Write([]byte{0, 0, 0, 0})
			},
		},
		{
			name: "OnClose panic",
			configure: func(handler *recordingHandler) {
				handler.onClose = func(*Conn, error) {
					panic("close")
				}
			},
			trigger: func(remote net.Conn) {
				_ = remote.Close()
			},
		},
	}

	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			// 每个回调故障都必须结束连接并发布 done，不能让 panic 越过 goroutine。
			pool := bufferpool.NewPool(bufferpool.Options{TrackUsage: true})
			options := smallConnectionOptions(pool)
			handler := newRecordingHandler()
			test.configure(handler)
			local, remote := net.Pipe()
			conn := newConn(local, options, handler, nil)
			conn.start()
			test.trigger(remote)

			err := waitConn(t, conn)
			if test.name == "OnClose panic" {
				if !errs.IsCode(err, errs.CodeTransportUnavailable) {
					t.Fatalf("OnClose panic 不应覆盖关闭原因：%v", err)
				}
			} else if !errs.IsCode(err, errs.CodeInternal) {
				t.Fatalf("Handler 故障 error=%v", err)
			}
			_ = remote.Close()
			assertPoolEmpty(t, pool)
		})
	}
}

func TestConnWaitContextAndRepeatedWait(t *testing.T) {
	t.Parallel()

	// 未结束连接上的已取消 Context 只取消本次等待，不改变连接终态。
	pool := bufferpool.NewPool(bufferpool.Options{TrackUsage: true})
	options := smallConnectionOptions(pool)
	local, remote := net.Pipe()
	conn := newConn(local, options, newRecordingHandler(), nil)
	conn.start()

	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if err := conn.Wait(canceled); !errs.IsCode(err, errs.CodeCanceled) {
		t.Fatalf("已取消 Wait error=%v", err)
	}
	if err := conn.Wait(nil); !errs.IsCode(err, errs.CodeInvalidArgument) {
		t.Fatalf("Wait(nil) error=%v", err)
	}

	// 连接关闭后，不同 Context 的重复 Wait 都返回同一个稳定终态。
	conn.Close()
	first := waitConn(t, conn)
	second := waitConn(t, conn)
	if !errors.Is(first, errs.ErrTransportClosed) ||
		!errors.Is(second, errs.ErrTransportClosed) {
		t.Fatalf("重复 Wait 结果=(%v, %v)", first, second)
	}
	_ = remote.Close()
	assertPoolEmpty(t, pool)
}

func TestWriteItemDetectsShortWrite(t *testing.T) {
	t.Parallel()

	// shortWriteConn 每次只接受一部分字节且不返回底层错误，用于覆盖 io.ErrShortWrite。
	pool := bufferpool.NewPool(bufferpool.Options{TrackUsage: true})
	options := smallConnectionOptions(pool)
	raw := &shortWriteConn{}
	conn := newConn(raw, options, newRecordingHandler(), nil)
	packet := acquireBytes(pool, "payload")
	item := sendItem{
		buffer:      packet,
		payloadSize: len(packet.Bytes()),
		headerSize:  4,
	}
	encodeFrameLength(&item.header, item.payloadSize, options.Frame)

	if err := conn.writeItem(item); !errs.IsCode(err, errs.CodeTransportUnavailable) {
		t.Fatalf("短写 error=%v", err)
	}
	packet.Release()
	assertPoolEmpty(t, pool)
}

// smallConnectionOptions 返回单元测试使用的小内存配置，同时保留生产超时语义。
func smallConnectionOptions(pool *bufferpool.Pool) ConnectionOptions {
	// 小上限显著降低并行测试内存，且仍覆盖相同代码路径。
	options := DefaultConnectionOptions(pool)
	options.MaxMessageSize = 16
	options.SendQueueFrames = 8
	options.WriteTimeout = time.Second
	return options
}

// acquireBytes 取得并填充一个由调用方拥有的测试 Buffer。
func acquireBytes(pool *bufferpool.Pool, value string) *bufferpool.Buffer {
	// 填满全部有效区域，避免池中旧字节影响断言。
	packet := pool.Acquire(len(value))
	copy(packet.Bytes(), value)
	return packet
}

// assertMessage 在固定时间内读取并比较一条 Handler 消息。
func assertMessage(t *testing.T, messages <-chan []byte, want []byte) {
	t.Helper()

	// 超时用于把连接循环卡死转化为可定位的测试失败。
	select {
	case got := <-messages:
		if string(got) != string(want) {
			t.Fatalf("消息=%q，期望=%q", got, want)
		}
	case <-time.After(testWaitTimeout):
		t.Fatal("等待 Handler 消息超时")
	}
}

// assertRawFrame 从远端读取一个完整帧并验证长度头和 payload。
func assertRawFrame(
	t *testing.T,
	remote net.Conn,
	order ByteOrder,
	headerSize int,
	want []byte,
) {
	t.Helper()

	// 设置测试级 Deadline，避免生产 Writer 故障时单元测试永久阻塞。
	if err := remote.SetReadDeadline(time.Now().Add(testWaitTimeout)); err != nil {
		t.Fatalf("设置测试读取 Deadline 失败：%v", err)
	}
	header := make([]byte, headerSize)
	if _, err := io.ReadFull(remote, header); err != nil {
		t.Fatalf("读取帧头失败：%v", err)
	}
	options := FrameOptions{LengthFieldSize: headerSize, ByteOrder: order}
	if got := decodeFrameLength(header, options); got != uint64(len(want)) {
		t.Fatalf("帧长=%d，期望=%d", got, len(want))
	}
	payload := make([]byte, len(want))
	if _, err := io.ReadFull(remote, payload); err != nil {
		t.Fatalf("读取 payload 失败：%v", err)
	}
	if string(payload) != string(want) {
		t.Fatalf("payload=%q，期望=%q", payload, want)
	}
}

// waitConn 使用有界 Context 等待连接完整清理。
func waitConn(t *testing.T, conn *Conn) error {
	t.Helper()

	// 每次测试独立创建 Deadline，便于定位资源退出问题。
	ctx, cancel := context.WithTimeout(context.Background(), testWaitTimeout)
	defer cancel()
	return conn.Wait(ctx)
}

// assertPoolEmpty 验证测试使用的跟踪 Pool 已经配平所有 Buffer 所有权。
func assertPoolEmpty(t *testing.T, pool *bufferpool.Pool) {
	t.Helper()

	// 网络测试只关心总数量和容量；任一非零都表示至少一条路径泄漏。
	stats := pool.Stats()
	if !stats.Enabled {
		t.Fatal("测试 Pool 未开启使用量统计")
	}
	if stats.InUseBuffers != 0 || stats.InUseCapacityBytes != 0 {
		t.Fatalf("Pool 仍有未释放 Buffer：%+v", stats)
	}
}

// equalStrings 比较两个顺序敏感的事件列表。
func equalStrings(left, right []string) bool {
	// 生命周期顺序不能使用集合比较，因此逐项验证。
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}

// blockingConn 是可确定控制 Write 阻塞与 Close 唤醒的 net.Conn 测试替身。
type blockingConn struct {
	closeOnce    sync.Once
	closed       chan struct{}
	writeStarted chan struct{}
	writeOnce    sync.Once
}

// newBlockingConn 初始化所有同步信号。
func newBlockingConn() *blockingConn {
	return &blockingConn{
		closed:       make(chan struct{}),
		writeStarted: make(chan struct{}),
	}
}

// Read 一直等待 Close，用于保持 ReadLoop 存活。
func (conn *blockingConn) Read([]byte) (int, error) {
	<-conn.closed
	return 0, net.ErrClosed
}

// Write 发布已开始信号后一直等待 Close。
func (conn *blockingConn) Write([]byte) (int, error) {
	conn.writeOnce.Do(func() {
		close(conn.writeStarted)
	})
	<-conn.closed
	return 0, net.ErrClosed
}

// Close 幂等唤醒所有阻塞 I/O。
func (conn *blockingConn) Close() error {
	conn.closeOnce.Do(func() {
		close(conn.closed)
	})
	return nil
}

// LocalAddr 返回稳定测试地址。
func (conn *blockingConn) LocalAddr() net.Addr {
	return testAddr("local")
}

// RemoteAddr 返回稳定测试地址。
func (conn *blockingConn) RemoteAddr() net.Addr {
	return testAddr("remote")
}

// SetDeadline 对本替身无操作，关闭由测试显式控制。
func (conn *blockingConn) SetDeadline(time.Time) error {
	return nil
}

// SetReadDeadline 对本替身无操作。
func (conn *blockingConn) SetReadDeadline(time.Time) error {
	return nil
}

// SetWriteDeadline 对本替身无操作。
func (conn *blockingConn) SetWriteDeadline(time.Time) error {
	return nil
}

// shortWriteConn 只用于直接测试单帧写入的短写处理。
type shortWriteConn struct{}

// Read 不是当前测试路径的一部分。
func (conn *shortWriteConn) Read([]byte) (int, error) {
	return 0, io.EOF
}

// Write 故意只接收一个字节且返回 nil error。
func (conn *shortWriteConn) Write(data []byte) (int, error) {
	if len(data) == 0 {
		return 0, nil
	}
	return 1, nil
}

// Close 不持有真实资源。
func (conn *shortWriteConn) Close() error {
	return nil
}

// LocalAddr 返回稳定测试地址。
func (conn *shortWriteConn) LocalAddr() net.Addr {
	return testAddr("local")
}

// RemoteAddr 返回稳定测试地址。
func (conn *shortWriteConn) RemoteAddr() net.Addr {
	return testAddr("remote")
}

// SetDeadline 对替身无操作。
func (conn *shortWriteConn) SetDeadline(time.Time) error {
	return nil
}

// SetReadDeadline 对替身无操作。
func (conn *shortWriteConn) SetReadDeadline(time.Time) error {
	return nil
}

// SetWriteDeadline 对替身无操作。
func (conn *shortWriteConn) SetWriteDeadline(time.Time) error {
	return nil
}

// testAddr 实现最小 net.Addr。
type testAddr string

// Network 返回测试网络名。
func (address testAddr) Network() string {
	return "test"
}

// String 返回测试地址文本。
func (address testAddr) String() string {
	return string(address)
}
