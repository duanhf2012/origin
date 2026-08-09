package command

import (
	"fmt"
	"runtime"
	"sort"
	"strings"

	"github.com/duanhf2012/origin/v3/buildinfo"
	"github.com/duanhf2012/origin/v3/errs"
)

// runHelp 输出总帮助或一个内置、自定义命令的详细帮助。
func (runner *Runner) runHelp(args []string) (ExitCode, error) {
	// help 最多接受一个命令名，避免静默忽略拼写错误或多余参数。
	if len(args) > 1 {
		return ExitUsage, invalidArgumentf("help accepts at most one command name")
	}
	if len(args) == 0 {
		if err := runner.writeGeneralHelp(); err != nil {
			return ExitFailure, err
		}
		return ExitSuccess, nil
	}

	name := args[0]
	if text, exists := runner.builtInHelp(name); exists {
		return runner.writeHelpText(text)
	}
	if custom, exists := runner.commands[name]; exists {
		text := fmt.Sprintf(
			"Usage:\n  %s\n\n%s\n",
			custom.Usage,
			custom.Summary,
		)
		return runner.writeHelpText(text)
	}
	return ExitUsage, invalidArgumentf("unknown command %q", args[0])
}

// writeGeneralHelp 以稳定顺序输出内置命令和实例已注册的自定义命令。
func (runner *Runner) writeGeneralHelp() error {
	var builder strings.Builder
	fmt.Fprintf(&builder, "Usage:\n  %s <command> [options]\n\n", runner.programName)
	if buildTime := buildinfo.BuildTime(); buildTime != "" {
		fmt.Fprintf(&builder, "Build time: %s\n\n", buildTime)
	}
	builder.WriteString("Commands:\n")
	fmt.Fprintf(&builder, "  %-14s %s\n", "start", "启动 Application")
	fmt.Fprintf(&builder, "  %-14s %s\n", "stop", "请求 Application 优雅停止")
	fmt.Fprintf(&builder, "  %-14s %s\n", "help", "显示命令帮助")
	fmt.Fprintf(&builder, "  %-14s %s\n", "version", "显示构建版本")

	// Map 迭代没有稳定顺序，因此先收集并排序自定义名称。
	names := make([]string, 0, len(runner.commands))
	for name := range runner.commands {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		fmt.Fprintf(&builder, "  %-14s %s\n", name, runner.commands[name].Summary)
	}
	fmt.Fprintf(&builder, "\nRun %q for command details.\n", runner.programName+" help <command>")

	_, err := fmt.Fprint(runner.stdout, builder.String())
	if err != nil {
		return errs.Wrap(errs.CodeInternal, fmt.Errorf("write command help: %w", err))
	}
	return nil
}

// builtInHelp 返回固定内置命令的详细帮助文本。
func (runner *Runner) builtInHelp(name string) (string, bool) {
	switch name {
	case "start":
		return fmt.Sprintf(
			"Usage:\n  %s start --app-name <name> [--config ./config] [--pid-dir ./run] [--node id1,id2] [--diagnostics 127.0.0.1:6061] [--pprof 127.0.0.1:6060]\n",
			runner.programName,
		), true
	case "stop":
		return fmt.Sprintf(
			"Usage:\n  %s stop --app-name <name> [--pid-dir ./run] [--timeout 30s]\n",
			runner.programName,
		), true
	case "help":
		return fmt.Sprintf("Usage:\n  %s help [command]\n", runner.programName), true
	case "version":
		return fmt.Sprintf("Usage:\n  %s version\n", runner.programName), true
	default:
		return "", false
	}
}

// writeHelpText 输出已经构造好的子命令帮助并返回成功退出码。
func (runner *Runner) writeHelpText(text string) (ExitCode, error) {
	if _, err := fmt.Fprint(runner.stdout, text); err != nil {
		return ExitFailure, errs.Wrap(errs.CodeInternal, fmt.Errorf("write command help: %w", err))
	}
	return ExitSuccess, nil
}

// runVersion 输出字段稳定的构建版本信息，不初始化其他运行时资源。
func (runner *Runner) runVersion(args []string) (ExitCode, error) {
	// version 不接受额外参数；详细用法统一通过 help version 获取。
	if len(args) != 0 {
		return ExitUsage, invalidArgumentf("version does not accept arguments")
	}

	text := fmt.Sprintf(
		"version: %s\ncommit: %s\nbuild_time: %s\ngo_version: %s\n",
		unknownIfEmpty(buildinfo.Version()),
		unknownIfEmpty(buildinfo.Commit()),
		unknownIfEmpty(buildinfo.BuildTime()),
		runtime.Version(),
	)
	if _, err := fmt.Fprint(runner.stdout, text); err != nil {
		return ExitFailure, errs.Wrap(errs.CodeInternal, fmt.Errorf("write version: %w", err))
	}
	return ExitSuccess, nil
}

// unknownIfEmpty 保持 version 输出字段稳定，未注入链接值时使用明确占位。
func unknownIfEmpty(value string) string {
	if value == "" {
		return "unknown"
	}
	return value
}
