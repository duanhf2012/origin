package command

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// Runner 持有单个命令运行环境及其自定义命令注册表。
//
// Runner 不支持并发 Register 或 Run。首次 Run 后注册表被冻结，但同一 Runner 可以在
// 没有并发的前提下顺序执行多次命令，便于测试和嵌入式工具复用。
type Runner struct {
	programName string
	stdin       io.Reader
	stdout      io.Writer
	stderr      io.Writer
	start       StartHandler
	commands    map[string]Command
	runStarted  bool
}

// New 校验依赖并创建一个没有包级副作用的 Runner。
func New(options Options) (*Runner, error) {
	// Start 是唯一必需回调；即使当前只执行 help，也保持所有 Runner 的构造契约一致。
	if options.Start == nil {
		return nil, invalidArgumentf("start handler is required")
	}

	// 缺省程序名来自当前可执行文件，只用于帮助外观，不参与进程身份判定。
	programName := strings.TrimSpace(options.ProgramName)
	if programName == "" {
		programName = filepath.Base(os.Args[0])
		if programName == "" || programName == "." {
			programName = "origin"
		}
	}

	// 各输入输出按实例补齐默认值，避免后续命令路径反复判断 nil。
	stdin := options.Stdin
	if stdin == nil {
		stdin = os.Stdin
	}
	stdout := options.Stdout
	if stdout == nil {
		stdout = os.Stdout
	}
	stderr := options.Stderr
	if stderr == nil {
		stderr = os.Stderr
	}

	return &Runner{
		programName: programName,
		stdin:       stdin,
		stdout:      stdout,
		stderr:      stderr,
		start:       options.Start,
		commands:    make(map[string]Command),
	}, nil
}

// Register 在首次 Run 前注册一个实例级自定义离线命令。
func (runner *Runner) Register(command Command) error {
	// 运行开始后冻结注册表，保证帮助排序和命令查找在执行期间不会变化。
	if runner.runStarted {
		return invalidArgumentf("cannot register command %q after runner has started", command.Name)
	}

	// 名称与 AppName 复用严格 kebab-case 校验，形成单一清晰命名规则。
	if err := validateKebabName(command.Name, "command name"); err != nil {
		return err
	}
	if isBuiltInCommand(command.Name) {
		return invalidArgumentf("command name %q is reserved", command.Name)
	}
	if _, exists := runner.commands[command.Name]; exists {
		return invalidArgumentf("command %q is already registered", command.Name)
	}
	if strings.TrimSpace(command.Summary) == "" {
		return invalidArgumentf("command %q summary is required", command.Name)
	}
	if strings.TrimSpace(command.Usage) == "" {
		return invalidArgumentf("command %q usage is required", command.Name)
	}
	if command.Run == nil {
		return invalidArgumentf("command %q run callback is required", command.Name)
	}

	// Command 仅包含字符串和函数值，按值保存即可与调用方后续变量赋值隔离。
	runner.commands[command.Name] = command
	return nil
}

// Run 解析并同步执行一个主命令，返回稳定退出码和可由最外层打印一次的错误。
func (runner *Runner) Run(
	ctx context.Context,
	args []string,
) (code ExitCode, err error) {
	// 最外层恢复用于覆盖解析、帮助生成等框架代码中的意外 panic；用户回调还有独立边界。
	defer func() {
		if value := recover(); value != nil {
			code = ExitFailure
			err = panicError("command runner", value)
		}
	}()

	// nil Context 会使平台信号和派生取消逻辑 panic，因此在任何资源创建前拒绝。
	if ctx == nil {
		return ExitUsage, invalidArgumentf("context is required")
	}
	runner.runStarted = true

	// 无参数时先输出简短总帮助，再返回稳定用法错误。
	if len(args) == 0 {
		if writeErr := runner.writeGeneralHelp(); writeErr != nil {
			return ExitFailure, writeErr
		}
		return ExitUsage, invalidArgumentf("command is required")
	}

	name := args[0]
	commandArgs := args[1:]
	switch name {
	case "help":
		return runner.runHelp(commandArgs)
	case "version":
		return runner.runVersion(commandArgs)
	case "start":
		return runner.runStart(ctx, commandArgs)
	case "stop":
		return runner.runStop(ctx, commandArgs)
	default:
		custom, exists := runner.commands[name]
		if !exists {
			return ExitUsage, invalidArgumentf("unknown command %q", args[0])
		}
		return runner.runCustom(ctx, custom, commandArgs)
	}
}

// runCustom 在隔离的 Context 和 panic 边界中执行一次性离线命令。
func (runner *Runner) runCustom(
	ctx context.Context,
	command Command,
	args []string,
) (ExitCode, error) {
	// 复制参数，避免回调修改调用方传入的底层数组并影响后续诊断或测试。
	commandArgs := append([]string(nil), args...)
	commandContext := Context{
		Context: ctx,
		Stdin:   runner.stdin,
		Stdout:  runner.stdout,
		Stderr:  runner.stderr,
	}

	err := callSafely("custom command "+command.Name, func() error {
		return command.Run(commandContext, commandArgs)
	})
	if err != nil {
		return ExitFailure, err
	}
	return ExitSuccess, nil
}

// isBuiltInCommand 报告名称是否属于不能被自定义命令覆盖的内置命令。
func isBuiltInCommand(name string) bool {
	switch name {
	case "start", "stop", "help", "version":
		return true
	default:
		return false
	}
}

// joinExecutionErrors 保留主操作错误，并把资源清理错误附加到同一错误链。
func joinExecutionErrors(primary error, cleanup ...error) error {
	// errors.Join 会忽略 nil；把主错误放在最前面可以让诊断文本先展示业务失败。
	all := make([]error, 0, 1+len(cleanup))
	if primary != nil {
		all = append(all, primary)
	}
	for _, cleanupErr := range cleanup {
		if cleanupErr != nil {
			all = append(all, cleanupErr)
		}
	}
	return errors.Join(all...)
}
