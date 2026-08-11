package websocket

import (
	"context"
	"errors"

	"github.com/duanhf2012/origin/v3/errs"
	"github.com/duanhf2012/origin/v3/internal/wsnet"
	"github.com/duanhf2012/origin/v3/service"
	"github.com/duanhf2012/origin/v3/sysmodule/network"
	"github.com/duanhf2012/origin/v3/sysmodule/network/internal/core"
)

// Dialer 对固定 URL 执行一次 WebSocket 连接尝试，不创建重试 goroutine。
type Dialer struct {
	url     string
	options DialOptions
}

// NewDialer 校验并创建可复用的单次 WebSocket Dialer。
func NewDialer(url string, options DialOptions) (*Dialer, error) {
	if err := validateDialOptions(url, options); err != nil {
		return nil, err
	}
	return &Dialer{url: url, options: freezeDialOptions(options)}, nil
}

// Dial 建立 Session，并等待 OnOpen 已在 owner Service 串行上下文成功完成。
//
// owner 必须处于 Running 或 Retired；返回后的 Session 由调用方持有并负责在 owner 停止前关闭。
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
		network.TransportWebSocket,
		dialer.options.Network,
		ownerRuntime.Logger(),
		false,
	)
	if err != nil {
		return nil, err
	}
	opened := make(chan *core.Session, 1)
	handler := newRuntimeHandler(runtime)
	handler.opened = func(session *core.Session, _ *wsnet.Conn) { opened <- session }
	handler.closed = func(_ *core.Session, _ *wsnet.Conn, _ error) {
		runtime.CancelIfIdle()
	}
	conn, err := wsnet.Dial(
		ctx,
		dialer.url,
		internalDialOptions(runtime, dialer.options),
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
