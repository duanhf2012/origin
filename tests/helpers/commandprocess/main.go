// commandprocess 是 Command 跨进程集成测试专用辅助程序。
//
// 它只通过公开 command API 持有真实 PID 锁并响应平台停止通知，不作为使用者示例发布。
package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strconv"
	"time"

	"github.com/duanhf2012/origin/v3/command"
)

const (
	envReadyFile    = "ORIGIN_COMMAND_TEST_READY_FILE"
	envMode         = "ORIGIN_COMMAND_TEST_MODE"
	envStopDelay    = "ORIGIN_COMMAND_TEST_STOP_DELAY"
	envControlFile  = "ORIGIN_COMMAND_TEST_CONTROL_FILE"
	envControlDelay = "ORIGIN_COMMAND_TEST_CONTROL_DELAY"
)

// main 是测试辅助程序唯一允许决定 os.Exit 的最终入口。
func main() {
	os.Exit(run())
}

// run 建立 Runner、执行命令并在所有资源释放后返回稳定退出码。
func run() int {
	runner, err := command.New(command.Options{
		ProgramName: "commandprocess",
		Start:       runStart,
	})
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return int(command.ExitFailure)
	}

	code, err := runner.Run(context.Background(), os.Args[1:])
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
	}
	return int(code)
}

// runStart 按环境变量选择立即返回、正常等待取消或故意忽略取消的测试行为。
func runStart(ctx context.Context, startRequest command.StartRequest) error {
	// ready 文件只通知父测试“Handler 已开始且 PID 锁已经持有”，不承载控制语义。
	if readyPath := os.Getenv(envReadyFile); readyPath != "" {
		if err := os.WriteFile(readyPath, []byte(strconv.Itoa(os.Getpid())), 0o600); err != nil {
			return fmt.Errorf("write ready file: %w", err)
		}
	}

	switch os.Getenv(envMode) {
	case "immediate":
		return nil
	case "ignore":
		// 长周期 timer 保持进程可运行，同时故意不读取 ctx.Done。单独等待 nil channel
		// 可能在平台控制 goroutine 退出后触发 Go runtime 的 deadlock 检测并意外退出。
		timer := time.NewTimer(24 * time.Hour)
		defer timer.Stop()
		<-timer.C
		return nil
	}

	// 正常模式模拟 Application 同步运行，同时逐个完成目标控制邮箱投递的请求。
	for {
		select {
		case <-ctx.Done():
			return waitStopDelay()
		case request, open := <-startRequest.Controls:
			if !open {
				return nil
			}
			if err := handleControlRequest(request); err != nil {
				request.Complete(err)
				continue
			}
			request.Complete(nil)
		}
	}
}

func handleControlRequest(request command.ControlRequest) error {
	if os.Getenv(envMode) == "control-timeout" {
		<-request.Context().Done()
		return request.Context().Err()
	}
	if os.Getenv(envMode) == "control-delay" {
		delay, err := time.ParseDuration(os.Getenv(envControlDelay))
		if err != nil {
			return fmt.Errorf("parse control delay: %w", err)
		}
		timer := time.NewTimer(delay)
		defer timer.Stop()
		select {
		case <-request.Context().Done():
			return request.Context().Err()
		case <-timer.C:
		}
	}
	switch request.Action() {
	case command.ControlActionRetire:
		return appendControlState("retired")
	case command.ControlActionResume:
		return appendControlState("running")
	default:
		return fmt.Errorf("unknown control action %d", request.Action())
	}
}

func appendControlState(state string) error {
	path := os.Getenv(envControlFile)
	if path == "" {
		return fmt.Errorf("%s is required", envControlFile)
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	_, writeErr := fmt.Fprintln(file, state)
	closeErr := file.Close()
	return errors.Join(writeErr, closeErr)
}

func waitStopDelay() error {
	// 可选延迟模拟目标收到请求后仍需执行有限的优雅收尾。
	if delayText := os.Getenv(envStopDelay); delayText != "" {
		delay, err := time.ParseDuration(delayText)
		if err != nil {
			return fmt.Errorf("parse stop delay: %w", err)
		}
		timer := time.NewTimer(delay)
		defer timer.Stop()
		<-timer.C
	}
	return nil
}
