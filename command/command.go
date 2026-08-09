// Package command 提供 Origin 可执行程序的命令解析和本地进程控制能力。
//
// Runner 是实例级对象，不使用 flag.CommandLine、包级注册表或隐式 init 注册。同一进程
// 可以创建多个互不影响的 Runner，最终 main 自行决定是否把 ExitCode 交给 os.Exit。
package command

import (
	"context"
	"io"
)

// ExitCode 是命令运行结果对应的稳定进程退出码。
type ExitCode int

const (
	// ExitSuccess 表示命令成功、帮助或版本已经输出，或者停止目标本来就未运行。
	ExitSuccess ExitCode = iota
	// ExitFailure 表示 Start Handler、自定义命令或可恢复 panic 执行失败。
	ExitFailure
	// ExitUsage 表示命令、参数或配置目录无效。
	ExitUsage
	// ExitProcessControl 表示重复启动或本地进程控制失败。
	ExitProcessControl
	// ExitControlTimeout 表示在线控制命令超过调用方指定的总体等待时间。
	ExitControlTimeout
)

// StartRequest 是 command 包校验并交给 Start Handler 的启动参数快照。
type StartRequest struct {
	// AppName 是当前 Application 的本地进程标识。
	AppName string
	// ConfigDir 是已经清理并绝对化的配置目录。
	ConfigDir string
	// PIDDir 是已经清理并绝对化的 PID 控制目录。
	PIDDir string
	// NodeIDs 按命令行声明顺序保存要启动的 Node；空切片表示由 Application 使用配置顺序。
	NodeIDs []string
	// Controls 由当前 start 持有的本地控制邮箱提供；nil 表示没有在线控制入口。
	Controls <-chan ControlRequest
	// AdminAddress 非空时要求 Application 在 Node 启动前监听通用管理 HTTP 地址。
	AdminAddress string
	// PprofAddress 非空时要求 Application 在 Node 启动前监听 Go pprof 地址。
	PprofAddress string
}

// StartHandler 同步运行上层 Application，直到其全部资源已经停止并可以释放 PID 锁。
type StartHandler func(ctx context.Context, request StartRequest) error

// Options 定义一个 Runner 实例的不可变依赖。
type Options struct {
	// ProgramName 是帮助中的程序名；为空时使用当前可执行文件名。
	ProgramName string
	// Stdin 是自定义命令读取输入的来源；为空时使用 os.Stdin。
	Stdin io.Reader
	// Stdout 接收帮助、版本和正常状态；为空时使用 os.Stdout。
	Stdout io.Writer
	// Stderr 交给自定义命令输出参数说明；为空时使用 os.Stderr。
	Stderr io.Writer
	// Start 是 start 命令唯一调用的上层生命周期入口，不能为空。
	Start StartHandler
}

// Context 是自定义离线命令收到的取消信号和实例级输入输出。
type Context struct {
	// Context 传递调用方取消和截止时间。
	context.Context
	// Stdin 是当前 Runner 注入的标准输入。
	Stdin io.Reader
	// Stdout 是当前 Runner 注入的正常输出。
	Stdout io.Writer
	// Stderr 是当前 Runner 注入的错误与参数说明输出。
	Stderr io.Writer
}

// Command 描述一个不获得 PID 锁、不创建 Application 的一次性离线命令。
type Command struct {
	// Name 是严格小写 kebab-case 命令名。
	Name string
	// Summary 是总帮助中显示的单行说明。
	Summary string
	// Usage 是子命令帮助中显示的完整用法。
	Usage string
	// Run 执行命令名之后的原始参数。
	Run func(ctx Context, args []string) error
}
