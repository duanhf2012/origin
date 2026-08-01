package rpc

import (
	"sync/atomic"

	"github.com/duanhf2012/origin/v3/errs"
)

// TransportStats 是一类 RPC Transport 当前积压和累计结果的只读快照。
//
// 所有字段都是固定低基数标量；Runtime 不按 Method、远端 Node 或错误文本建立动态统计。
type TransportStats struct {
	Pending              uint64
	PendingHighWater     uint64
	OutboundAccepted     uint64
	OutboundCompleted    uint64
	OutboundFailed       uint64
	OutboundTimeout      uint64
	OutboundRejected     uint64
	InboundAccepted      uint64
	InboundCompleted     uint64
	InboundFailed        uint64
	InboundTimeout       uint64
	InboundRejected      uint64
	PayloadSentBytes     uint64
	PayloadReceivedBytes uint64
	Reconnects           uint64
	ConsecutiveFailures  uint64
}

// Stats 按同 Node、TCP 和 NATS 三个固定类别保存一个 Runtime 的 RPC 汇总。
type Stats struct {
	Local TransportStats
	TCP   TransportStats
	NATS  TransportStats
}

// transportCounters 使用固定原子字段维护 RPC 热路径统计，不创建逐请求指标对象。
type transportCounters struct {
	pending              atomic.Uint64
	pendingHighWater     atomic.Uint64
	outboundAccepted     atomic.Uint64
	outboundCompleted    atomic.Uint64
	outboundFailed       atomic.Uint64
	outboundTimeout      atomic.Uint64
	outboundRejected     atomic.Uint64
	inboundAccepted      atomic.Uint64
	inboundCompleted     atomic.Uint64
	inboundFailed        atomic.Uint64
	inboundTimeout       atomic.Uint64
	inboundRejected      atomic.Uint64
	payloadSentBytes     atomic.Uint64
	payloadReceivedBytes atomic.Uint64
	reconnects           atomic.Uint64
	consecutiveFailures  atomic.Uint64
}

// Stats 原子读取三个固定类别；各字段可来自相邻时刻，但每个累计值不会倒退。
func (runtime *Runtime) Stats() Stats {
	if runtime == nil {
		return Stats{}
	}
	local := runtime.rpcStats.local.snapshot()
	// 同 Node 调用的两端属于同一个 Runtime。成功热路径只累计一次 accepted、pending、
	// terminal 和总 payload，避免把同一事实重复做两套原子写；失败终态仍由目标侧单独
	// 累计，因此响应解码失败不会被误报成目标执行失败。
	local.InboundAccepted = local.OutboundAccepted
	local.InboundRejected = local.OutboundRejected
	if local.OutboundAccepted != 0 && local.PendingHighWater == 0 {
		local.PendingHighWater = 1
	}
	outboundUnfinished := local.OutboundFailed + local.OutboundTimeout + local.Pending
	if local.OutboundAccepted >= outboundUnfinished {
		local.OutboundCompleted = local.OutboundAccepted - outboundUnfinished
	}
	inboundUnfinished := local.InboundFailed + local.InboundTimeout + local.Pending
	if local.InboundAccepted >= inboundUnfinished {
		local.InboundCompleted = local.InboundAccepted - inboundUnfinished
	}
	local.PayloadReceivedBytes = local.PayloadSentBytes
	return Stats{
		Local: local,
		TCP:   runtime.rpcStats.tcp.snapshot(),
		NATS:  runtime.rpcStats.nats.snapshot(),
	}
}

func (counters *transportCounters) snapshot() TransportStats {
	if counters == nil {
		return TransportStats{}
	}
	return TransportStats{
		Pending:              counters.pending.Load(),
		PendingHighWater:     counters.pendingHighWater.Load(),
		OutboundAccepted:     counters.outboundAccepted.Load(),
		OutboundCompleted:    counters.outboundCompleted.Load(),
		OutboundFailed:       counters.outboundFailed.Load(),
		OutboundTimeout:      counters.outboundTimeout.Load(),
		OutboundRejected:     counters.outboundRejected.Load(),
		InboundAccepted:      counters.inboundAccepted.Load(),
		InboundCompleted:     counters.inboundCompleted.Load(),
		InboundFailed:        counters.inboundFailed.Load(),
		InboundTimeout:       counters.inboundTimeout.Load(),
		InboundRejected:      counters.inboundRejected.Load(),
		PayloadSentBytes:     counters.payloadSentBytes.Load(),
		PayloadReceivedBytes: counters.payloadReceivedBytes.Load(),
		Reconnects:           counters.reconnects.Load(),
		ConsecutiveFailures:  counters.consecutiveFailures.Load(),
	}
}

func (runtime *Runtime) counters(transport preparedTransport) *transportCounters {
	if runtime == nil {
		return nil
	}
	switch transport {
	case preparedLocal:
		return &runtime.rpcStats.local
	case preparedTCP:
		return &runtime.rpcStats.tcp
	case preparedNATS:
		return &runtime.rpcStats.nats
	default:
		return nil
	}
}

func (runtime *Runtime) recordOutboundAccepted(
	transport preparedTransport,
) {
	counters := runtime.counters(transport)
	if counters == nil {
		return
	}
	counters.outboundAccepted.Add(1)
	pending := counters.pending.Add(1)
	// Local 的顺序 Await 是最敏感路径。pending=1 时快照可由 accepted 精确派生首个水位，
	// 只有真实并发才需要触碰 high-water 原子；远端仍保持统一直接更新。
	if transport != preparedLocal || pending > 1 {
		updateHighWater(&counters.pendingHighWater, pending)
	}
}

func (runtime *Runtime) recordOutboundFinished(
	transport preparedTransport,
	result error,
	requestBytes int,
	responseBytes int,
) {
	counters := runtime.counters(transport)
	if counters == nil {
		return
	}
	counters.pending.Add(^uint64(0))
	if transport == preparedLocal {
		if payloadBytes := requestBytes + responseBytes; payloadBytes > 0 {
			counters.payloadSentBytes.Add(uint64(payloadBytes))
		}
	} else {
		if requestBytes > 0 {
			counters.payloadSentBytes.Add(uint64(requestBytes))
		}
		if responseBytes > 0 {
			counters.payloadReceivedBytes.Add(uint64(responseBytes))
		}
	}
	if transport == preparedLocal {
		if result != nil {
			recordResult(
				result,
				&counters.outboundCompleted,
				&counters.outboundFailed,
				&counters.outboundTimeout,
			)
		}
	} else {
		recordResult(
			result,
			&counters.outboundCompleted,
			&counters.outboundFailed,
			&counters.outboundTimeout,
		)
	}
}

func (runtime *Runtime) recordOutboundRejected(transport preparedTransport) {
	if counters := runtime.counters(transport); counters != nil {
		counters.outboundRejected.Add(1)
	}
}

func (runtime *Runtime) recordOutboundNotify(
	transport preparedTransport,
	payloadBytes int,
) {
	counters := runtime.counters(transport)
	if counters == nil {
		return
	}
	counters.outboundAccepted.Add(1)
	if transport != preparedLocal {
		counters.outboundCompleted.Add(1)
	}
	if payloadBytes > 0 {
		counters.payloadSentBytes.Add(uint64(payloadBytes))
	}
}

func (runtime *Runtime) recordInboundAccepted(
	transport preparedTransport,
	payloadBytes int,
) {
	counters := runtime.counters(transport)
	if counters == nil {
		return
	}
	if transport == preparedLocal {
		return
	}
	counters.inboundAccepted.Add(1)
	if payloadBytes > 0 {
		counters.payloadReceivedBytes.Add(uint64(payloadBytes))
	}
}

func (runtime *Runtime) recordInboundFinished(
	transport preparedTransport,
	result error,
	payloadBytes int,
) {
	counters := runtime.counters(transport)
	if counters == nil {
		return
	}
	if transport == preparedLocal {
		if result == nil {
			return
		}
		recordResult(
			result,
			&counters.inboundCompleted,
			&counters.inboundFailed,
			&counters.inboundTimeout,
		)
		return
	}
	if payloadBytes > 0 {
		counters.payloadSentBytes.Add(uint64(payloadBytes))
	}
	recordResult(
		result,
		&counters.inboundCompleted,
		&counters.inboundFailed,
		&counters.inboundTimeout,
	)
}

func (runtime *Runtime) recordInboundRejected(transport preparedTransport) {
	if transport == preparedLocal {
		return
	}
	if counters := runtime.counters(transport); counters != nil {
		counters.inboundRejected.Add(1)
	}
}

func (runtime *Runtime) recordInboundNotify(
	transport preparedTransport,
	payloadBytes int,
) {
	counters := runtime.counters(transport)
	if counters == nil {
		return
	}
	if transport == preparedLocal {
		return
	}
	counters.inboundAccepted.Add(1)
	counters.inboundCompleted.Add(1)
	if payloadBytes > 0 {
		counters.payloadReceivedBytes.Add(uint64(payloadBytes))
	}
}

func (runtime *Runtime) recordTransportEvent(event TransportEvent) {
	if runtime == nil {
		return
	}
	var counters *transportCounters
	switch event.Kind {
	case TransportKindTCP:
		counters = &runtime.rpcStats.tcp
	case TransportKindNATS:
		counters = &runtime.rpcStats.nats
	default:
		return
	}
	counters.reconnects.Store(event.Reconnects)
	counters.consecutiveFailures.Store(event.ConsecutiveFailures)
}

func recordResult(
	result error,
	completed *atomic.Uint64,
	failed *atomic.Uint64,
	timeout *atomic.Uint64,
) {
	if result == nil {
		completed.Add(1)
		return
	}
	if errs.CodeOf(result) == errs.CodeDeadlineExceeded {
		timeout.Add(1)
		return
	}
	failed.Add(1)
}

func updateHighWater(highWater *atomic.Uint64, current uint64) {
	for observed := highWater.Load(); current > observed; observed = highWater.Load() {
		if highWater.CompareAndSwap(observed, current) {
			return
		}
	}
}
