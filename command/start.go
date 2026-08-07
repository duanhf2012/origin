package command

import (
	"context"
	"flag"
	"io"
	"strings"
	"sync"
)

// runStart 解析启动参数、取得运行权并同步执行 Start Handler。
func (runner *Runner) runStart(ctx context.Context, args []string) (ExitCode, error) {
	// 内置命令的 --help 只输出帮助，不校验目录，也不创建 PID 文件。
	if containsHelpFlag(args) {
		text, _ := runner.builtInHelp("start")
		return runner.writeHelpText(text)
	}

	flags := flag.NewFlagSet("start", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	appName := flags.String("app-name", "", "Application 名称")
	configDir := flags.String("config", "./config", "配置目录")
	pidDir := flags.String("pid-dir", "./run", "PID 目录")
	nodeOption := registerStringOption(flags, "node", "", "逗号分隔的 NodeID")
	initialRetired := flags.Bool("retired", false, "以 Retired 状态发布全部选中 Service")
	diagnosticsAddress := flags.String("diagnostics", "", "Diagnostics JSON 监听地址")
	pprofAddress := flags.String("pprof", "", "Go pprof 监听地址")
	if err := flags.Parse(args); err != nil {
		return ExitUsage, invalidArgumentf("parse start arguments: %v", err)
	}
	if err := rejectPositionals(flags); err != nil {
		return ExitUsage, err
	}

	// 所有参数和目录必须在创建 PID 资源前完成校验。
	if err := validateKebabName(*appName, "app name"); err != nil {
		return ExitUsage, err
	}
	nodeIDs, err := parseNodeIDs(nodeOption)
	if err != nil {
		return ExitUsage, err
	}
	absoluteConfigDir, err := resolveExistingDir(*configDir, "config directory")
	if err != nil {
		return ExitUsage, err
	}
	absolutePIDDir, err := resolvePIDDir(*pidDir)
	if err != nil {
		return ExitProcessControl, err
	}

	// PID lease 从获得锁开始一直持有到 Handler 返回和平台控制监听停止。
	lease, err := acquirePIDLease(absolutePIDDir, *appName)
	if err != nil {
		return ExitProcessControl, err
	}
	// 保底 defer 覆盖框架内部意外 panic；正常路径仍在下方显式清理并收集错误。
	defer func() {
		_ = lease.close()
	}()
	runCtx, closeControl, err := startPlatformControl(
		ctx,
		stopFilePath(absolutePIDDir, *appName),
	)
	if err != nil {
		releaseErr := lease.close()
		return ExitProcessControl, joinExecutionErrors(err, wrapLeaseCleanup(lease.path, releaseErr))
	}
	closeControl = onceCleanup(closeControl)
	defer func() {
		_ = closeControl()
	}()

	// 复制 NodeIDs 形成 Handler 独占的参数快照，防止修改解析期切片。
	request := StartRequest{
		AppName:            *appName,
		ConfigDir:          absoluteConfigDir,
		PIDDir:             absolutePIDDir,
		NodeIDs:            append([]string(nil), nodeIDs...),
		InitialRetired:     *initialRetired,
		DiagnosticsAddress: strings.TrimSpace(*diagnosticsAddress),
		PprofAddress:       strings.TrimSpace(*pprofAddress),
	}
	handlerErr := callSafely("start handler", func() error {
		return runner.start(runCtx, request)
	})

	// 固定按控制监听、PID 锁的逆序清理；两个步骤即使失败也都必须执行。
	controlErr := closeControl()
	releaseErr := lease.close()
	cleanupErr := joinExecutionErrors(
		wrapControlCleanup(*appName, controlErr),
		wrapLeaseCleanup(lease.path, releaseErr),
	)
	if handlerErr != nil {
		return ExitFailure, joinExecutionErrors(handlerErr, cleanupErr)
	}
	if cleanupErr != nil {
		return ExitProcessControl, cleanupErr
	}
	return ExitSuccess, nil
}

// containsHelpFlag 报告内置参数中是否显式请求了命令帮助。
func containsHelpFlag(args []string) bool {
	for _, arg := range args {
		if arg == "-h" || arg == "--help" {
			return true
		}
	}
	return false
}

// wrapControlCleanup 给平台监听清理错误附加稳定进程控制语义。
func wrapControlCleanup(appName string, err error) error {
	if err == nil {
		return nil
	}
	return processControlf("close stop control for app %q: %v", appName, err)
}

// wrapLeaseCleanup 给 PID 锁清理错误附加稳定进程控制语义。
func wrapLeaseCleanup(path string, err error) error {
	if err == nil {
		return nil
	}
	return processControlf("release pid lease %q: %v", path, err)
}

// onceCleanup 把平台清理函数包装为可安全重复调用并复用第一次执行结果。
func onceCleanup(cleanup func() error) func() error {
	var once sync.Once
	var result error
	return func() error {
		// defer 保底和正常显式清理都可能到达这里，真实平台资源只释放一次。
		once.Do(func() {
			result = cleanup()
		})
		return result
	}
}
