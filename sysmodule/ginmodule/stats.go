package ginmodule

import "sync/atomic"

// ServerStats 是 Gin Module 的低基数运行统计快照。
type ServerStats struct {
	ActiveRequests   int64
	TotalRequests    uint64
	RejectedRequests uint64
	TimedOutRequests uint64
	PanicTotal       uint64
}

type serverCounters struct {
	active   atomic.Int64
	total    atomic.Uint64
	rejected atomic.Uint64
	timedOut atomic.Uint64
	panics   atomic.Uint64
}

func (counters *serverCounters) snapshot() ServerStats {
	return ServerStats{
		ActiveRequests:   counters.active.Load(),
		TotalRequests:    counters.total.Load(),
		RejectedRequests: counters.rejected.Load(),
		TimedOutRequests: counters.timedOut.Load(),
		PanicTotal:       counters.panics.Load(),
	}
}
