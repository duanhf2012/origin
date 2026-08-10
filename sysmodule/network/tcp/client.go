package tcp

import (
	"context"
	"errors"
	"fmt"
	"math/rand/v2"
	"runtime/debug"
	"sync"
	"time"

	"github.com/duanhf2012/origin/v3/internal/tcpnet"
	originlog "github.com/duanhf2012/origin/v3/log"
	"github.com/duanhf2012/origin/v3/service"
	"github.com/duanhf2012/origin/v3/sysmodule/network"
	"github.com/duanhf2012/origin/v3/sysmodule/network/internal/core"
)

// Client 是由 Service 生命周期托管的单连接 TCP Client Module。
type Client struct {
	service.Module

	address string
	options ClientOptions

	mu      sync.RWMutex
	runtime *core.Runtime
	handler *runtimeHandler
	conn    *tcpnet.Conn
	session *core.Session
	state   network.ClientStateSnapshot

	ctx    context.Context
	cancel context.CancelFunc
	closed chan closeEvent
	wg     sync.WaitGroup
}

type closeEvent struct {
	conn  *tcpnet.Conn
	cause error
}

// NewClient 校验并创建尚未绑定 Service 的 TCP Client Module。
func NewClient(address string, options ClientOptions) (*Client, error) {
	if err := validateAddress(address); err != nil {
		return nil, err
	}
	if err := validateClientOptions(options); err != nil {
		return nil, err
	}
	return &Client{
		address: address,
		options: options,
		state: network.ClientStateSnapshot{
			State: network.ClientStopped,
		},
	}, nil
}

// OnInit 冻结 Router 等构造期注册表。
func (client *Client) OnInit() error {
	if freezer, ok := client.options.Dial.Network.Handler.(network.Freezer); ok {
		return freezer.Freeze()
	}
	return nil
}

// OnStart 建立 Runtime，并执行一次初始拨号。
func (client *Client) OnStart(ctx context.Context) error {
	runtime, err := core.NewRuntime(
		client.Service(),
		network.TransportTCP,
		client.options.Dial.Network,
		client.Logger(),
		false,
	)
	if err != nil {
		return err
	}
	clientCtx, cancel := context.WithCancel(context.Background())
	handler := newRuntimeHandler(runtime)
	handler.opened = client.onOpened
	handler.closed = client.onClosed
	client.mu.Lock()
	client.runtime = runtime
	client.handler = handler
	client.ctx = clientCtx
	client.cancel = cancel
	client.closed = make(chan closeEvent, 1)
	client.state = network.ClientStateSnapshot{State: network.ClientConnecting, Attempt: 1}
	client.mu.Unlock()
	client.notifyState(ctx, client.State())

	conn, dialErr := tcpnet.Dial(
		clientCtx,
		client.address,
		connectionOptions(runtime, client.options.Dial),
		handler,
	)
	if dialErr != nil && !client.options.Reconnect.Enabled {
		cancel()
		runtime.BeginStop()
		_ = runtime.Finalize(ctx)
		client.setStateDirect(ctx, network.ClientStateSnapshot{
			State:     network.ClientStopped,
			Attempt:   1,
			LastError: dialErr,
		})
		return dialErr
	}
	client.mu.Lock()
	client.conn = conn
	client.mu.Unlock()
	client.wg.Add(1)
	if err := client.GoSafe(func() {
		defer client.wg.Done()
		client.connectionLoop(dialErr)
	}); err != nil {
		client.wg.Done()
		if conn != nil {
			conn.Close()
		}
		cancel()
		return err
	}
	return nil
}

// OnStop 取消拨号/退避，关闭活动连接，等待唯一重连 Worker，再交付最终关闭事件。
func (client *Client) OnStop(ctx context.Context) error {
	client.mu.RLock()
	runtime := client.runtime
	cancel := client.cancel
	conn := client.conn
	client.mu.RUnlock()
	if runtime == nil {
		return nil
	}
	runtime.BeginStop()
	if cancel != nil {
		cancel()
	}
	if conn != nil {
		conn.Close()
	}
	waitDone := make(chan struct{})
	go func() {
		client.wg.Wait()
		close(waitDone)
	}()
	var waitErr error
	select {
	case <-waitDone:
	case <-ctx.Done():
		waitErr = ctx.Err()
	}
	if conn != nil {
		select {
		case <-conn.Done():
		case <-ctx.Done():
			waitErr = errors.Join(waitErr, ctx.Err())
		}
	}
	finalizeErr := runtime.Finalize(ctx)
	client.setStateDirect(ctx, network.ClientStateSnapshot{State: network.ClientStopped})
	return errors.Join(waitErr, finalizeErr)
}

// Session 返回当前已完成 OnOpen 的活动 Session。
func (client *Client) Session() (network.Session, bool) {
	if client == nil {
		return nil, false
	}
	client.mu.RLock()
	session := client.session
	client.mu.RUnlock()
	if session == nil {
		return nil, false
	}
	select {
	case <-session.Done():
		return nil, false
	default:
		return session, true
	}
}

// State 返回 Client 当前状态快照。
func (client *Client) State() network.ClientStateSnapshot {
	if client == nil {
		return network.ClientStateSnapshot{State: network.ClientStopped}
	}
	client.mu.RLock()
	state := client.state
	client.mu.RUnlock()
	return state
}

// Stats 返回 Client Runtime 固定统计快照。
func (client *Client) Stats() network.EndpointStats {
	if client == nil {
		return network.EndpointStats{}
	}
	client.mu.RLock()
	runtime := client.runtime
	client.mu.RUnlock()
	if runtime == nil {
		return network.EndpointStats{}
	}
	return runtime.Stats()
}

func (client *Client) onOpened(session *core.Session, conn *tcpnet.Conn) {
	client.mu.Lock()
	if client.conn != nil && client.conn != conn {
		client.mu.Unlock()
		session.Close(nil)
		return
	}
	client.conn = conn
	client.session = session
	client.state = network.ClientStateSnapshot{State: network.ClientConnected}
	state := client.state
	client.mu.Unlock()
	client.dispatchState(state)
}

func (client *Client) onClosed(session *core.Session, conn *tcpnet.Conn, cause error) {
	if finalCause := session.Cause(); finalCause != nil {
		cause = finalCause
	}
	client.mu.Lock()
	if client.conn == conn {
		client.conn = nil
	}
	if client.session == session {
		client.session = nil
	}
	closed := client.closed
	client.mu.Unlock()
	select {
	case closed <- closeEvent{conn: conn, cause: cause}:
	default:
	}
}

func (client *Client) connectionLoop(initialError error) {
	cause := initialError
	for {
		if cause == nil {
			select {
			case <-client.ctx.Done():
				return
			case event := <-client.closed:
				cause = event.cause
				client.mu.Lock()
				if client.conn == event.conn {
					client.conn = nil
				}
				client.mu.Unlock()
			}
		}
		if !client.options.Reconnect.Enabled {
			client.updateState(network.ClientStateSnapshot{
				State:     network.ClientStopped,
				LastError: cause,
			})
			return
		}
		connected := false
		for attempt := 1; attempt <= client.options.Reconnect.MaxAttempts; attempt++ {
			delay := client.retryDelay(attempt)
			client.updateState(network.ClientStateSnapshot{
				State:     network.ClientReconnecting,
				Attempt:   attempt,
				NextDelay: delay,
				LastError: cause,
			})
			timer := time.NewTimer(delay)
			select {
			case <-timer.C:
			case <-client.ctx.Done():
				if !timer.Stop() {
					select {
					case <-timer.C:
					default:
					}
				}
				return
			}
			conn, err := tcpnet.Dial(
				client.ctx,
				client.address,
				connectionOptions(client.runtime, client.options.Dial),
				client.handler,
			)
			if err != nil {
				cause = err
				continue
			}
			client.mu.Lock()
			if client.ctx.Err() == nil {
				client.conn = conn
				connected = true
			}
			client.mu.Unlock()
			if !connected {
				conn.Close()
				return
			}
			break
		}
		if !connected {
			client.updateState(network.ClientStateSnapshot{
				State:     network.ClientStopped,
				Attempt:   client.options.Reconnect.MaxAttempts,
				LastError: cause,
			})
			return
		}
		cause = nil
	}
}

func (client *Client) retryDelay(attempt int) time.Duration {
	delay := client.options.Reconnect.InitialDelay
	for index := 1; index < attempt && delay < client.options.Reconnect.MaxDelay; index++ {
		if delay > client.options.Reconnect.MaxDelay/2 {
			delay = client.options.Reconnect.MaxDelay
			break
		}
		delay *= 2
	}
	if jitter := client.options.Reconnect.Jitter; jitter > 0 {
		factor := 1 - jitter + rand.Float64()*(2*jitter)
		delay = time.Duration(float64(delay) * factor)
	}
	return delay
}

func (client *Client) updateState(state network.ClientStateSnapshot) {
	client.mu.Lock()
	client.state = state
	client.mu.Unlock()
	client.dispatchState(state)
}

func (client *Client) dispatchState(state network.ClientStateSnapshot) {
	if client.options.StateChange == nil {
		return
	}
	_ = client.Service().DispatchAsync(func(ctx context.Context) {
		client.notifyState(ctx, state)
	})
}

func (client *Client) setStateDirect(ctx context.Context, state network.ClientStateSnapshot) {
	client.mu.Lock()
	client.state = state
	client.session = nil
	client.conn = nil
	client.mu.Unlock()
	client.notifyState(ctx, state)
}

func (client *Client) notifyState(ctx context.Context, state network.ClientStateSnapshot) {
	if client.options.StateChange == nil {
		return
	}
	defer func() {
		if value := recover(); value != nil {
			client.Logger().ErrorStack(
				"TCP Client StateChange panic",
				originlog.String("panic", fmt.Sprint(value)),
				originlog.String("panic_stack", string(debug.Stack())),
			)
		}
	}()
	client.options.StateChange(ctx, state)
}

var _ service.IModule = (*Client)(nil)
