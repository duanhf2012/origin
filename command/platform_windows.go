//go:build windows

package command

import (
	"context"
	"errors"
	"os"
	"os/signal"
	"time"
)

// windowsStopPollInterval 是已确认的普通控制台停止请求检查周期。
const windowsStopPollInterval = 100 * time.Millisecond

// startPlatformControl 清理陈旧请求并启动 Windows 普通控制台停止文件监听。
func startPlatformControl(
	parent context.Context,
	stopPath string,
) (context.Context, func() error, error) {
	// 只有获得 PID 锁的新 start 才能清理同 AppName 的陈旧请求，避免误删活动进程请求。
	if err := os.Remove(stopPath); err != nil && !os.IsNotExist(err) {
		return nil, nil, processControlf("remove stale stop request %q: %v", stopPath, err)
	}

	// Windows 普通控制台的跨进程 stop 使用文件；当前控制台自身的 Ctrl+C 仍应进入
	// 相同优雅停止路径，因此同时监听标准 os.Interrupt。
	signalCtx, stopSignal := signal.NotifyContext(parent, os.Interrupt)
	runCtx, cancel := context.WithCancel(signalCtx)
	done := make(chan struct{})
	var controlErr error
	go func() {
		defer close(done)

		// 单个低频 ticker 只服务进程控制，不进入 Service Runner 或游戏逻辑热路径。
		ticker := time.NewTicker(windowsStopPollInterval)
		defer ticker.Stop()
		for {
			select {
			case <-runCtx.Done():
				return
			case <-ticker.C:
				_, err := os.Stat(stopPath)
				if os.IsNotExist(err) {
					continue
				}
				if err != nil {
					controlErr = err
					cancel()
					return
				}

				// 先删除请求再取消运行期，避免正常停止留下会影响下一次 start 的文件。
				if err := os.Remove(stopPath); err != nil && !os.IsNotExist(err) {
					controlErr = err
					cancel()
					return
				}
				cancel()
				return
			}
		}
	}()

	closeControl := func() error {
		// 取消并等待唯一控制 goroutine，channel 关闭建立 controlErr 的可见性顺序。
		cancel()
		<-done
		stopSignal()
		return controlErr
	}
	return runCtx, closeControl, nil
}

// requestPlatformStop 原子创建 Windows 普通控制台的空停止请求文件。
func requestPlatformStop(_ int, stopPath string) error {
	// O_EXCL 让并发 stop 只有一个真正创建文件，已存在按幂等请求处理。
	file, err := os.OpenFile(stopPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if os.IsExist(err) {
		// 只有已经存在的普通文件才表示另一个 stop 已发出同一请求。目录、设备或其他
		// 文件类型不能伪装成幂等请求，否则目标永远收不到可删除的空控制文件。
		info, statErr := os.Stat(stopPath)
		if statErr != nil {
			return statErr
		}
		if !info.Mode().IsRegular() {
			return &os.PathError{Op: "create stop request", Path: stopPath, Err: errors.New("path is not a regular file")}
		}
		return nil
	}
	if err != nil {
		return err
	}
	return file.Close()
}

// platformProcessGone 在文件控制方案中没有“发送给已退出 PID”的平台错误。
func platformProcessGone(error) bool {
	return false
}
