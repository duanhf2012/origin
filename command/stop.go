package command

import (
	"context"
	"fmt"
	"time"

	"github.com/duanhf2012/origin/v3/errs"
)

// stopLockPollInterval 只用于低频进程控制，不进入任何游戏逻辑热路径。
const stopLockPollInterval = 25 * time.Millisecond

// runStop 解析停止参数、发送平台通知并等待目标释放 PID 运行权。
func (runner *Runner) runStop(ctx context.Context, args []string) (ExitCode, error) {
	// 帮助路径不能创建目录、打开 PID 文件或向目标发送通知。
	if containsHelpFlag(args) {
		text, _ := runner.builtInHelp("stop")
		return runner.writeHelpText(text)
	}

	target, code, err := runner.parseControlTarget("stop", args)
	if err != nil {
		return code, err
	}
	if !target.exists {
		return runner.writeNotRunning()
	}
	controlCtx, cancel := context.WithTimeout(ctx, target.timeout)
	defer cancel()

	pidPath := pidFilePath(target.pidDir, target.appName)
	running, pid, err := readRunningPID(pidPath)
	if err != nil {
		return ExitProcessControl, err
	}
	if !running {
		return runner.writeNotRunning()
	}

	// 平台通知只表达一次停止意图；目标进程仍自行完成 Handler 内的优雅关闭。
	requestErr := requestPlatformStop(pid, stopFilePath(target.pidDir, target.appName))
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
			target.appName,
			pidPath,
			requestErr,
		)
	}

	timedOut, waitErr := waitForPIDUnlock(controlCtx, pidPath)
	if waitErr != nil {
		if timedOut {
			return ExitControlTimeout, waitErr
		}
		return ExitFailure, waitErr
	}
	return ExitSuccess, nil
}

// waitForPIDUnlock 轮询目标运行权，区分外部 timeout 与调用方 Context 取消。
func waitForPIDUnlock(
	ctx context.Context,
	pidPath string,
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
	for {
		select {
		case <-ctx.Done():
			if ctx.Err() == context.DeadlineExceeded {
				return true, errs.NewMessage(
					errs.CodeDeadlineExceeded,
					fmt.Sprintf("waiting for pid lock %q exceeded command timeout", pidPath),
				)
			}
			return false, errs.Wrap(errs.CodeCanceled, ctx.Err())
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
