package natsnet

import (
	"context"
	"net"
	"sync"
	"time"
)

// initialDialer 只在初始 Connect 阶段把调用方 Context 传播到 TCP 和 TLS 握手。
//
// nats.go 的 CustomDialer 没有 Context 参数，因此初始 Dial 使用 DialContext，并由一个
// 有明确结束信号的观察 goroutine 在 Context 取消时关闭临时 socket。Connect 返回后，
// finish 会停止并等待全部观察 goroutine，后续自动重连改用普通带超时 Dial。
type initialDialer struct {
	ctx    context.Context
	dialer net.Dialer

	// phaseMu 使“初始 Dial 登记观察者”和 finish 开始 Wait 形成严格先后关系。
	// 不能只用原子状态，否则一个已经读取初始状态的 Dial 可能在 Wait 开始后再执行 Add，
	// 违反 sync.WaitGroup 的使用约束。
	phaseMu sync.RWMutex
	initial bool
	done    chan struct{}
	once    sync.Once
	wait    sync.WaitGroup
}

// newInitialDialer 创建初始 Context 感知、后续重连只使用固定超时的 Dialer。
func newInitialDialer(ctx context.Context, timeout time.Duration) *initialDialer {
	// net.Dialer.Timeout 同时作为初始 Context 无 Deadline 和后续重连的单次上限。
	result := &initialDialer{
		ctx:     ctx,
		dialer:  net.Dialer{Timeout: timeout},
		initial: true,
		done:    make(chan struct{}),
	}
	return result
}

// Dial 实现 nats.CustomDialer。
func (dialer *initialDialer) Dial(network, address string) (net.Conn, error) {
	// 读锁一直覆盖初始 socket 建立和观察者登记；finish 取得写锁后便不会再出现新的 Add。
	dialer.phaseMu.RLock()
	if !dialer.initial {
		dialer.phaseMu.RUnlock()
		// Connect 返回后不再保留初始 Context，自动重连只受固定单次超时控制。
		return dialer.dialer.Dial(network, address)
	}

	// 初始 TCP Dial 直接使用调用方 Context，取消时立即终止系统调用。
	raw, err := dialer.dialer.DialContext(dialer.ctx, network, address)
	if err != nil {
		dialer.phaseMu.RUnlock()
		return nil, err
	}

	// NATS 在 Dial 返回后还会执行 INFO/CONNECT 和可能的 TLS 握手；观察者在 Context
	// 取消时关闭临时 socket，从而打断这些没有原生 Context 参数的阶段。
	dialer.wait.Add(1)
	dialer.phaseMu.RUnlock()
	go func(conn net.Conn) {
		defer dialer.wait.Done()
		select {
		case <-dialer.ctx.Done():
			_ = conn.Close()
		case <-dialer.done:
		}
	}(raw)
	return raw, nil
}

// finish 结束初始阶段并等待全部 Context 观察 goroutine 退出。
func (dialer *initialDialer) finish() {
	// 写锁保证先前初始 Dial 已经完成 WaitGroup.Add，后续 Dial 只能看到普通超时模式。
	dialer.phaseMu.Lock()
	dialer.initial = false
	dialer.once.Do(func() {
		close(dialer.done)
	})
	dialer.phaseMu.Unlock()

	// 此时再等待不会与任何新的 Add 并发，全部 Context 观察者都能确定退出。
	dialer.wait.Wait()
}
