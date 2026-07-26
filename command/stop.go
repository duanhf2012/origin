package command

import (
	"context"
	"flag"
	"fmt"
	"io"
	"time"

	"github.com/duanhf2012/origin/v3/errs"
)

const (
	// defaultStopTimeout 是 stop 命令等待目标释放 PID 锁的默认外部时间。
	defaultStopTimeout = 30 * time.Second
	// stopLockPollInterval 只用于低频进程控制，不进入任何游戏逻辑热路径。
	stopLockPollInterval = 25 * time.Millisecond
)

// runStop 解析停止参数、发送平台通知并等待目标释放 PID 运行权。
func (runner *Runner) runStop(ctx context.Context, args []string) (ExitCode, error) {
	// 帮助路径不能创建目录、打开 PID 文件或向目标发送通知。
	if containsHelpFlag(args) {
		text, _ := runner.builtInHelp("stop")
		return runner.writeHelpText(text)
	}

	flags := flag.NewFlagSet("stop", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	appName := flags.String("app-name", "", "Application 名称")
	pidDir := flags.String("pid-dir", "./run", "PID 目录")
	timeoutText := flags.String("timeout", defaultStopTimeout.String(), "等待目标退出的最长时间")
	if err := flags.Parse(args); err != nil {
		return ExitUsage, invalidArgumentf("parse stop arguments: %v", err)
	}
	if err := rejectPositionals(flags); err != nil {
		return ExitUsage, err
	}
	if err := validateKebabName(*appName, "app name"); err != nil {
		return ExitUsage, err
	}

	// timeout 只控制外部 stop 命令等待时间，不传入目标 Application。
	timeout, err := time.ParseDuration(*timeoutText)
	if err != nil || timeout <= 0 {
		return ExitUsage, invalidArgumentf("stop timeout %q must be a positive duration", *timeoutText)
	}
	absolutePIDDir, exists, err := resolvePIDDirForStop(*pidDir)
	if err != nil {
		return ExitProcessControl, err
	}
	if !exists {
		return runner.writeNotRunning()
	}

	pidPath := pidFilePath(absolutePIDDir, *appName)
	running, pid, err := readRunningPID(pidPath)
	if err != nil {
		return ExitProcessControl, err
	}
	if !running {
		return runner.writeNotRunning()
	}

	// 平台通知只表达一次停止意图；目标进程仍自行完成 Handler 内的优雅关闭。
	requestErr := requestPlatformStop(pid, stopFilePath(absolutePIDDir, *appName))
	if requestErr != nil {
		if platformProcessGone(requestErr) {
			// 目标可能在发送瞬间正常退出；只有锁也释放时才把该竞争视为成功。
			stillRunning, inspectErr := isPIDLocked(pidPath)
			if inspectErr != nil {
				return ExitProcessControl, inspectErr
			}
			if !stillRunning {
				return ExitSuccess, nil
			}
		}
		return ExitProcessControl, processControlf(
			"request stop for app %q using pid file %q: %v",
			*appName,
			pidPath,
			requestErr,
		)
	}

	timedOut, waitErr := waitForPIDUnlock(ctx, pidPath, timeout)
	if waitErr != nil {
		if timedOut {
			return ExitStopTimeout, waitErr
		}
		return ExitFailure, waitErr
	}
	return ExitSuccess, nil
}

// waitForPIDUnlock 轮询目标运行权，区分外部 timeout 与调用方 Context 取消。
func waitForPIDUnlock(
	ctx context.Context,
	pidPath string,
	timeout time.Duration,
) (timedOut bool, err error) {
	// 先立即检查一次，覆盖目标在通知送达后快速退出的常见路径。
	locked, err := isPIDLocked(pidPath)
	if err != nil {
		return false, err
	}
	if !locked {
		return false, nil
	}

	ticker := time.NewTicker(stopLockPollInterval)
	defer ticker.Stop()
	timer := time.NewTimer(timeout)
	defer timer.Stop()

	for {
		select {
		case <-ctx.Done():
			// 调用方取消不是 stop 自身 30s timeout，保持普通失败退出码和取消错误码。
			return false, errs.Wrap(errs.CodeCanceled, ctx.Err())
		case <-timer.C:
			return true, errs.NewMessage(
				errs.CodeDeadlineExceeded,
				fmt.Sprintf("waiting for pid lock %q exceeded %s", pidPath, timeout),
			)
		case <-ticker.C:
			locked, inspectErr := isPIDLocked(pidPath)
			if inspectErr != nil {
				return false, inspectErr
			}
			if !locked {
				return false, nil
			}
		}
	}
}

// writeNotRunning 输出 stop 的幂等状态并返回成功。
func (runner *Runner) writeNotRunning() (ExitCode, error) {
	if _, err := fmt.Fprintln(runner.stdout, "not running"); err != nil {
		return ExitFailure, errs.Wrap(errs.CodeInternal, fmt.Errorf("write stop status: %w", err))
	}
	return ExitSuccess, nil
}
