// Package tcpnet 提供 Origin 框架内部复用的 TCP 长度帧传输能力。
//
// tcpnet 只负责连接、字节帧、Buffer 所有权、背压和资源生命周期，
// 不包含 NodeID、RPC 方法、服务发现、自动重连或业务 TcpModule 语义。
package tcpnet

import (
	"math"
	"time"

	"github.com/duanhf2012/origin/v3/internal/bufferpool"
	originlog "github.com/duanhf2012/origin/v3/log"
)

const (
	// 默认长度字段使用四字节，能够直接承载 RPC 的 4M 消息上限。
	defaultLengthFieldSize = 4
	// 默认单帧上限为 4M，与已经确认的 RPC 配置保持一致。
	defaultMaxMessageSize = 4 * 1024 * 1024
	// 通用 TCP 默认预留 4096 个发送槽位；RPC 会显式覆盖为 16384。
	defaultSendQueueFrames = 4096
	// 写入一个完整帧最多等待 15 秒。
	defaultWriteTimeout = 15 * time.Second
	// 系统 TCP KeepAlive 默认使用 30 秒周期。
	defaultKeepAlive = 30 * time.Second
	// 单个 Listener 默认最多管理 4096 条活动连接。
	defaultMaxConnections = 4096
)

// FrameOptions 配置使用网络字节序编码的长度字段宽度。
type FrameOptions struct {
	// LengthFieldSize 只允许一、二或四字节。
	LengthFieldSize int
}

// ConnectionOptions 配置一条 TCP 连接的帧、队列、超时和依赖实例。
type ConnectionOptions struct {
	// Pool 提供收发 payload Buffer，必须由更高层显式持有并注入。
	Pool *bufferpool.Pool
	// Logger 记录连接生命周期和限频后的关键异常；零值等同 Nop Logger。
	Logger originlog.Logger
	// Frame 定义 TCP 长度帧外观。
	Frame FrameOptions
	// MaxMessageSize 同时限制本地发送和远端声明的单帧 payload 长度。
	MaxMessageSize int
	// SendQueueFrames 限制每条连接等待发送的帧数量。
	SendQueueFrames int
	// ReadTimeout 是读一个完整帧的空闲上限；零表示关闭。
	ReadTimeout time.Duration
	// WriteTimeout 是写一个完整帧的上限，必须大于零。
	WriteTimeout time.Duration
	// KeepAlive 是系统 TCP 保活周期；零表示关闭系统保活。
	KeepAlive time.Duration
}

// ListenOptions 配置 Listener 以及其接受的全部 Connection。
type ListenOptions struct {
	// Connection 是每条入站连接使用的共同配置快照。
	Connection ConnectionOptions
	// MaxConnections 是 Listener 同时持有的最大连接数。
	MaxConnections int
}

// Handler 接收一条连接上的有序生命周期和消息事件。
//
// 同一 Conn 的回调严格按照 OnOpen、零到多次 OnMessage、OnClose 顺序串行执行；
// 不同 Conn 可以并发调用同一个 Handler，因此共享状态需要由实现自行同步。
type Handler interface {
	// OnOpen 在 ReadLoop 开始读取第一帧前调用。
	OnOpen(conn *Conn)
	// OnMessage 接管 packet 的唯一所有权。
	//
	// 实现必须在同步处理、转移给另一个明确所有者或任意失败路径中最终 Release；
	// 返回非 nil error 会关闭当前连接。
	//
	// panic 路径下 tcpnet 不会兜底释放 packet。这是唯一所有权模型的必然结果：
	// 实现可能在 panic 前已经把 packet 转交给业务队列，此时 tcpnet 再 Release 会
	// 对另一个所有者持有的活对象二次释放并引发 use-after-free。因此实现必须自行
	// 保证在 panic recover 边界内释放或转移 packet（参见
	// TestOnMessagePanicWithoutReleaseLeaksBuffer 固化的契约）。
	OnMessage(conn *Conn, packet *bufferpool.Buffer) error
	// OnClose 在读写循环停止后恰好调用一次，cause 是首个有效关闭原因。
	OnClose(conn *Conn, cause error)
}

// DefaultConnectionOptions 返回通用 TCP 场景的完整默认配置。
func DefaultConnectionOptions(pool *bufferpool.Pool) ConnectionOptions {
	// 所有默认值集中在一个构造函数中，调用方修改个别字段后仍能保留其他安全边界。
	return ConnectionOptions{
		Pool:   pool,
		Logger: originlog.NewNop(),
		Frame: FrameOptions{
			LengthFieldSize: defaultLengthFieldSize,
		},
		MaxMessageSize:  defaultMaxMessageSize,
		SendQueueFrames: defaultSendQueueFrames,
		ReadTimeout:     0,
		WriteTimeout:    defaultWriteTimeout,
		KeepAlive:       defaultKeepAlive,
	}
}

// DefaultListenOptions 返回通用 TCP Listener 的完整默认配置。
func DefaultListenOptions(pool *bufferpool.Pool) ListenOptions {
	// Listener 复用 Connection 的单一默认来源，只额外设置活动连接上限。
	return ListenOptions{
		Connection:     DefaultConnectionOptions(pool),
		MaxConnections: defaultMaxConnections,
	}
}

// validateConnectionOptions 在创建任何 socket 或 goroutine 前验证完整连接配置。
func validateConnectionOptions(options ConnectionOptions) error {
	// Pool 是 Buffer 唯一所有权链的起点，不能在运行中隐式创建或替换。
	if options.Pool == nil {
		return invalidConfig("tcpnet: Pool 不能为空")
	}

	// 长度字段只支持设计确认的一、二、四字节。
	switch options.Frame.LengthFieldSize {
	case 1, 2, 4:
	default:
		return invalidConfig("tcpnet: LengthFieldSize 只能是 1、2 或 4")
	}
	// 最大消息必须为正数，并且能够由所选长度字段和当前平台 int 表达。
	if options.MaxMessageSize <= 0 {
		return invalidConfig("tcpnet: MaxMessageSize 必须大于零")
	}
	if uint64(options.MaxMessageSize) > maxFramePayload(options.Frame.LengthFieldSize) {
		return invalidConfig("tcpnet: MaxMessageSize 超出长度字段可表达范围")
	}

	// 发送队列只按完整消息数量形成一个明确上限。
	if options.SendQueueFrames <= 0 {
		return invalidConfig("tcpnet: SendQueueFrames 必须大于零")
	}

	// 读超时允许零值关闭，写超时必须存在以避免 WriteLoop 永久挂起。
	if options.ReadTimeout < 0 {
		return invalidConfig("tcpnet: ReadTimeout 不能为负数")
	}
	if options.WriteTimeout <= 0 {
		return invalidConfig("tcpnet: WriteTimeout 必须大于零")
	}
	if options.KeepAlive < 0 {
		return invalidConfig("tcpnet: KeepAlive 不能为负数")
	}
	return nil
}

// validateListenOptions 在绑定端口前验证 Listener 自身和连接配置。
func validateListenOptions(options ListenOptions) error {
	// 先验证所有 Connection 字段，避免 Listener 成功后才暴露同一错误。
	if err := validateConnectionOptions(options.Connection); err != nil {
		return err
	}
	// 零或负连接上限会使任何 Accept 都无法成立，按无效配置处理。
	if options.MaxConnections <= 0 {
		return invalidConfig("tcpnet: MaxConnections 必须大于零")
	}
	return nil
}

// maxFramePayload 返回指定长度字段能够表达的最大无符号 payload 长度。
func maxFramePayload(lengthFieldSize int) uint64 {
	// 显式分支避免依赖平台 int 宽度，也防止位移到 64 位边界。
	switch lengthFieldSize {
	case 1:
		return math.MaxUint8
	case 2:
		return math.MaxUint16
	case 4:
		return math.MaxUint32
	default:
		// 该函数只应在配置校验或已经校验的热路径调用。
		return 0
	}
}
