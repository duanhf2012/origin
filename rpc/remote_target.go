package rpc

import (
	"context"
	"sync"
	"time"

	"github.com/duanhf2012/origin/v3/errs"
	"github.com/duanhf2012/origin/v3/internal/tcpnet"
)

// remoteTarget 管理一个明确 NodeID/address 的单向出站连接。
//
// 每个目标只有一个管理 goroutine；连接失败后按指数退避重试，但绝不重发已经提交过的
// 业务 Request。current 只在 ORP1 握手成功后发布。
type remoteTarget struct {
	remote    *remoteRuntime
	nodeID    string
	sessionID uint64
	address   string

	ctx    context.Context
	cancel context.CancelFunc
	done   chan struct{}

	startOnce sync.Once
	mu        sync.Mutex
	current   *outboundSession
}

// newRemoteTarget 创建尚未运行的目标管理器。
func newRemoteTarget(
	remote *remoteRuntime,
	nodeID string,
	sessionID uint64,
	address string,
) *remoteTarget {
	ctx, cancel := context.WithCancel(context.Background())
	return &remoteTarget{
		remote:    remote,
		nodeID:    nodeID,
		sessionID: sessionID,
		address:   address,
		ctx:       ctx,
		cancel:    cancel,
		done:      make(chan struct{}),
	}
}

// requestStop 非阻塞取消发现目录已经删除或替换的连接目标。
func (target *remoteTarget) requestStop() {
	target.start()
	target.cancel()
	target.mu.Lock()
	session := target.current
	target.mu.Unlock()
	if session != nil {
		session.close()
	}
}

// start 幂等启动唯一连接管理 goroutine。
func (target *remoteTarget) start() {
	target.startOnce.Do(func() {
		go target.run()
	})
}

// stop 发出关闭信号、打断当前 socket，并等待管理 goroutine 退出。
func (target *remoteTarget) stop(ctx context.Context) error {
	if ctx == nil {
		return errs.ErrInvalidArgument
	}
	target.start()
	target.cancel()
	target.mu.Lock()
	session := target.current
	target.mu.Unlock()
	if session != nil {
		session.close()
	}
	select {
	case <-target.done:
		return nil
	case <-ctx.Done():
		return contextError(context.Cause(ctx))
	}
}

// currentSession 返回当前握手完成且仍属于目标的会话。
func (target *remoteTarget) currentSession() *outboundSession {
	target.mu.Lock()
	session := target.current
	target.mu.Unlock()
	return session
}

// run 串行执行 Dial、握手、运行、断线和退避。
func (target *remoteTarget) run() {
	defer func() {
		close(target.done)
		target.remote.targetDone(target)
	}()
	delay := reconnectInitialDelay
	random := uint64(1469598103934665603)
	for {
		if target.ctx.Err() != nil {
			return
		}
		session, err := target.connect()
		if err != nil {
			if target.ctx.Err() != nil {
				return
			}
			target.remote.logReconnectFailure(target.nodeID, target.address, err)
			if !target.waitBackoff(jitterDelay(delay, &random)) {
				return
			}
			delay *= 2
			if delay > reconnectMaximumDelay {
				delay = reconnectMaximumDelay
			}
			continue
		}

		// 一次成功连接即重置退避；后续偶发断线可以快速恢复。
		delay = reconnectInitialDelay
		target.mu.Lock()
		target.current = session
		target.mu.Unlock()
		target.runConnected(session)
		target.mu.Lock()
		if target.current == session {
			target.current = nil
		}
		target.mu.Unlock()
		session.close()
	}
}

// connect 执行一次有界 Dial 和 ORP1 握手。
func (target *remoteTarget) connect() (*outboundSession, error) {
	session := newOutboundSession(
		target.remote,
		target.nodeID,
		target.sessionID,
	)
	dialCtx, cancel := context.WithTimeout(target.ctx, DefaultDialTimeout)
	conn, err := tcpnet.Dial(
		dialCtx,
		target.address,
		target.remote.connectionOptions(),
		session,
	)
	cancel()
	if err != nil {
		return nil, err
	}
	if err := session.waitHandshake(target.ctx); err != nil {
		conn.Close()
		return nil, err
	}
	return session, nil
}

// runConnected 维持心跳，直到连接或目标生命周期结束。
func (target *remoteTarget) runConnected(session *outboundSession) {
	session.mu.Lock()
	conn := session.conn
	session.mu.Unlock()
	if conn == nil {
		return
	}

	interval := target.remote.heartbeatInterval()
	if interval == 0 {
		select {
		case <-target.ctx.Done():
		case <-conn.Done():
		}
		return
	}

	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-target.ctx.Done():
			return
		case <-conn.Done():
			return
		case <-ticker.C:
			if err := session.sendPing(); err != nil {
				conn.Close()
				return
			}
		}
	}
}

// waitBackoff 使用可取消 Timer 等待下一次连接尝试。
func (target *remoteTarget) waitBackoff(delay time.Duration) bool {
	timer := time.NewTimer(delay)
	defer func() {
		if !timer.Stop() {
			select {
			case <-timer.C:
			default:
			}
		}
	}()
	select {
	case <-timer.C:
		return true
	case <-target.ctx.Done():
		return false
	}
}

// jitterDelay 为多个 Node 同时恢复增加确定的 ±20% 抖动。
func jitterDelay(base time.Duration, state *uint64) time.Duration {
	// xorshift64 只维护目标私有状态，不使用全局 rand 锁或额外分配。
	value := *state
	value ^= value << 13
	value ^= value >> 7
	value ^= value << 17
	*state = value

	// 取 0～4000 映射到 80%～120%，整数计算保持退避冷路径简单。
	factor := int64(8000 + value%4001)
	return time.Duration(int64(base) * factor / 10000)
}
