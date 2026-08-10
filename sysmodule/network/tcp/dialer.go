package tcp

import (
	"context"
	"errors"

	"github.com/duanhf2012/origin/v3/errs"
	"github.com/duanhf2012/origin/v3/internal/tcpnet"
	"github.com/duanhf2012/origin/v3/service"
	"github.com/duanhf2012/origin/v3/sysmodule/network"
	"github.com/duanhf2012/origin/v3/sysmodule/network/internal/core"
)

// Dialer 对固定地址执行一次连接尝试，不创建重试 goroutine。
type Dialer struct {
	address string
	options DialOptions
}

// NewDialer 校验并创建可复用的单次 TCP Dialer。
func NewDialer(address string, options DialOptions) (*Dialer, error) {
	if err := validateAddress(address); err != nil {
		return nil, err
	}
	if err := validateDialOptions(options); err != nil {
		return nil, err
	}
	return &Dialer{address: address, options: options}, nil
}

// Dial 建立 Session，并等待 OnOpen 已在 owner Service 串行上下文成功完成。
//
// owner 必须处于 Running；返回后的 Session 由调用方持有并负责在 owner 停止前关闭。
// 需要由 Module 自动完成停止和重连时应使用 Client。
func (dialer *Dialer) Dial(
	ctx context.Context,
	owner service.IService,
) (network.Session, error) {
	if dialer == nil || ctx == nil || owner == nil {
		return nil, errs.ErrInvalidArgument
	}
	if freezer, ok := dialer.options.Network.Handler.(network.Freezer); ok {
		if err := freezer.Freeze(); err != nil {
			return nil, err
		}
	}
	ownerRuntime := service.RuntimeOf(owner)
	if ownerRuntime == nil {
		return nil, errs.ErrServiceNotReady
	}
	switch ownerRuntime.State() {
	case service.StateRunning, service.StateRetired:
	case service.StateStopping:
		return nil, errs.ErrServiceStopping
	case service.StateStopped:
		return nil, errs.ErrServiceStopped
	default:
		return nil, errs.ErrServiceNotReady
	}
	runtime, err := core.NewRuntime(
		owner,
		network.TransportTCP,
		dialer.options.Network,
		ownerRuntime.Logger(),
		false,
	)
	if err != nil {
		return nil, err
	}
	opened := make(chan *core.Session, 1)
	handler := newRuntimeHandler(runtime)
	handler.opened = func(session *core.Session, _ *tcpnet.Conn) {
		opened <- session
	}
	handler.closed = func(_ *core.Session, _ *tcpnet.Conn, _ error) {
		runtime.CancelIfIdle()
	}
	conn, err := tcpnet.Dial(
		ctx,
		dialer.address,
		connectionOptions(runtime, dialer.options),
		handler,
	)
	if err != nil {
		runtime.BeginStop()
		_ = runtime.Finalize(ctx)
		return nil, err
	}
	select {
	case session := <-opened:
		return session, nil
	case <-conn.Done():
		return nil, conn.Cause()
	case <-ctx.Done():
		conn.Close()
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return nil, errs.Wrap(errs.CodeDeadlineExceeded, ctx.Err())
		}
		return nil, errs.Wrap(errs.CodeCanceled, ctx.Err())
	}
}
