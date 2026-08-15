// Package tcpnet 提供 Origin 框架内部复用的 TCP 长度帧传输能力。
//
// tcpnet 只负责连接、字节帧、Buffer 所有权、背压和资源生命周期，
// 不包含 NodeID、RPC 方法、服务发现、自动重连或业务 TcpModule 语义。
package tcpnet

import (
	"math"
	"time"

	"github.com/duanhf2012/origin/v3/internal/bufferpool"
	"github.com/duanhf2012/origin/v3/internal/bytebudget"
	originlog "github.com/duanhf2012/origin/v3/log"
)

const (
	// 默认长度字段使用四字节，允许上层按协议需要覆盖单帧上限。
	defaultLengthFieldSize = 4
	// 通用 TCP 默认单帧上限为 4M；RPC Adapter 会按自己的固定上限显式覆盖。
	defaultMaxMessageSize = 4 * 1024 * 1024
	// 通用 TCP 默认预留 4096 个发送槽位；RPC 会显式覆盖为 16384。
	defaultSendQueueFrames = 4096
	// 默认每连接最多保留 64M 等待/正在写出的 Payload 容量。
	defaultSendQueueBytes = 64 * 1024 * 1024
	// 默认同一 Options 产生的全部连接共享 256M 发送总容量。
	defaultSendQueueTotalBytes = 256 * 1024 * 1024
	// 写入一个完整帧最多等待 15 秒。
	defaultWriteTimeout = 15 * time.Second
	// 发送队列持续高水位超过十秒时把对端判定为慢连接。
	defaultSlowClientTimeout = 10 * time.Second
	// 系统 TCP KeepAlive 默认使用 30 秒周期。
	defaultKeepAlive = 30 * time.Second
	// 单个 Listener 默认最多管理 4096 条活动连接。
	defaultMaxConnections = 4096
)

// ByteOrder 是 TCP 长度字段使用的固定端序。
type ByteOrder uint8

const (
	// BigEndian 使用网络字节序，是 TCP 帧的默认值。
	BigEndian ByteOrder = iota + 1
	// LittleEndian 支持使用小端长度字段的游戏客户端协议。
	LittleEndian
)

// FrameOptions 配置长度字段宽度和端序。
type FrameOptions struct {
	// LengthFieldSize 只允许一、二或四字节。
	LengthFieldSize int
	// ByteOrder 对二、四字节生效；一字节没有端序差异。
	ByteOrder ByteOrder
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
	// SendQueueBytes 限制每条连接排队 Payload 的保留容量。
	SendQueueBytes int64
	// SendBudget 限制共享该实例的全部连接排队及正在写出的 Payload 容量。
	SendBudget *bytebudget.Budget
	// ReadTimeout 是读一个完整帧的空闲上限；零表示关闭。
	ReadTimeout time.Duration
	// WriteTimeout 是写一个完整帧的上限，必须大于零。
	WriteTimeout time.Duration
	// SlowClientTimeout 是发送队列连续保持高水位的最长时间，必须大于零。
	SlowClientTimeout time.Duration
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

// WritableHandler 是 Handler 可选实现的发送高低水位通知。
//
// 回调可能由并发 Send 或唯一 WriteLoop 触发，不能直接访问只允许 Service 串行执行的业务状态。
// 上层网络 Module 只在这里投递 Service Task；RPC 等不需要背压事件的 Handler 无需实现。
type WritableHandler interface {
	OnWritableChanged(conn *Conn, writable bool)
}

// DefaultConnectionOptions 返回通用 TCP 场景的完整默认配置。
func DefaultConnectionOptions(pool *bufferpool.Pool) ConnectionOptions {
	// 所有默认值集中在一个构造函数中，调用方修改个别字段后仍能保留其他安全边界。
	return ConnectionOptions{
		Pool:   pool,
		Logger: originlog.NewNop(),
		Frame: FrameOptions{
			LengthFieldSize: defaultLengthFieldSize,
			ByteOrder:       BigEndian,
		},
		MaxMessageSize:    defaultMaxMessageSize,
		SendQueueFrames:   defaultSendQueueFrames,
		SendQueueBytes:    defaultSendQueueBytes,
		SendBudget:        mustNewByteBudget(defaultSendQueueTotalBytes),
		ReadTimeout:       0,
		WriteTimeout:      defaultWriteTimeout,
		SlowClientTimeout: defaultSlowClientTimeout,
		KeepAlive:         defaultKeepAlive,
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
	if options.Frame.ByteOrder != BigEndian && options.Frame.ByteOrder != LittleEndian {
		return invalidConfig("tcpnet: Frame.ByteOrder 只能是 BigEndian 或 LittleEndian")
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
	if options.SendQueueBytes <= 0 ||
		int64(bufferpool.RetainedCapacity(options.MaxMessageSize)) > options.SendQueueBytes {
		return invalidConfig("tcpnet: SendQueueBytes 必须大于零且不能小于 MaxMessageSize")
	}
	if options.SendBudget == nil || options.SendBudget.Snapshot().Limit < options.SendQueueBytes {
		return invalidConfig("tcpnet: SendBudget 不能为空且上限不能小于 SendQueueBytes")
	}

	// 读超时允许零值关闭，写超时必须存在以避免 WriteLoop 永久挂起。
	if options.ReadTimeout < 0 {
		return invalidConfig("tcpnet: ReadTimeout 不能为负数")
	}
	if options.WriteTimeout <= 0 {
		return invalidConfig("tcpnet: WriteTimeout 必须大于零")
	}
	if options.SlowClientTimeout <= 0 {
		return invalidConfig("tcpnet: SlowClientTimeout 必须大于零")
	}
	if options.KeepAlive < 0 {
		return invalidConfig("tcpnet: KeepAlive 不能为负数")
	}
	return nil
}

// mustNewByteBudget 构造只使用编译期正数默认值，失败表示内部常量被破坏。
func mustNewByteBudget(limit int64) *bytebudget.Budget {
	budget, err := bytebudget.New(limit)
	if err != nil {
		panic("tcpnet: 非法默认发送总容量")
	}
	return budget
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
