package application

import (
	"runtime"
	runtimemetrics "runtime/metrics"
	"time"

	"github.com/duanhf2012/origin/v3/diagnostics"
	"github.com/duanhf2012/origin/v3/internal/bufferpool"
	"github.com/duanhf2012/origin/v3/node"
)

const (
	runtimeRunnableGoroutinesMetric = "/sched/goroutines/runnable:goroutines"
	runtimeGCCPUSecondsMetric       = "/cpu/classes/gc/total:cpu-seconds"
	runtimeMutexWaitSecondsMetric   = "/sync/mutex/wait/total:seconds"
	runtimeMemoryLimitMetric        = "/gc/gomemlimit:bytes"
)

// runtimeMetricValues 是一次固定 runtime/metrics 读取的内部强类型结果。
// 缺失名称和 KindBad 保持零值，调用方无需承担 Value getter panic 风险。
type runtimeMetricValues struct {
	runnableGoroutines    uint64
	gcCPUSecondsTotal     float64
	mutexWaitSecondsTotal float64
	memoryLimitBytes      int64
}

// Diagnostics 聚合当前 Application、Go Runtime、BufferPool 和全部 Node 的不可变快照。
//
// 方法只在 Application 锁内复制稳定指针和身份，随后释放锁顺序采集各叶子，避免诊断冷
// 路径暂停 Node Scheduler、RPC 或网络收发。
func (app *Application) Diagnostics() diagnostics.Snapshot {
	collectedAt := time.Now()
	result := diagnostics.Snapshot{
		// v2 移除了与诊断主体无关的日志输出状态；日志管理改用 log.CurrentStatus。
		SchemaVersion: 2,
		CollectedAt:   collectedAt,
		Nodes:         make([]diagnostics.NodeSnapshot, 0),
	}
	if app == nil {
		result.Application.State = "failed"
		result.Application.DiagnosticsServer.State = "stopped"
		result.Application.AdminServer.State = "stopped"
		result.Application.Pprof.State = "stopped"
		result.Runtime = collectRuntimeSnapshot()
		result.CollectCost = diagnostics.Duration(time.Since(collectedAt))
		return result
	}

	app.mu.Lock()
	result.StartedAt = app.startedAt
	result.Application = diagnostics.ApplicationSnapshot{
		Name:              app.appName,
		State:             applicationStateText(app.State()),
		DiagnosticsServer: diagnostics.ServerSnapshot{State: "stopped"},
		AdminServer:       app.adminHTTP.snapshot(),
		Pprof:             app.pprofHTTP.snapshot(),
	}
	nodes := append([]*node.Node(nil), app.nodes...)
	pool := app.bufferPool
	app.mu.Unlock()

	result.Runtime = collectRuntimeSnapshot()
	if pool != nil {
		result.BufferPool = mapBufferPoolStats(pool.Stats())
	}
	result.Nodes = make([]diagnostics.NodeSnapshot, len(nodes))
	for index, current := range nodes {
		result.Nodes[index] = current.Diagnostics()
	}
	result.CollectCost = diagnostics.Duration(time.Since(collectedAt))
	return result
}

// mapBufferPoolStats 让 Summary 和 Full 共享同一组已公开 BufferPool 字段口径。
func mapBufferPoolStats(stats bufferpool.Stats) diagnostics.BufferPoolSnapshot {
	return diagnostics.BufferPoolSnapshot{
		Enabled:            stats.Enabled,
		InUseBuffers:       stats.InUseBuffers,
		InUseCapacityBytes: stats.InUseCapacityBytes,
		ZeroSizeInUse:      stats.ZeroSizeInUse,
		OversizeInUse:      stats.OversizeInUse,
		OversizeBytes:      stats.OversizeBytes,
	}
}

// DiagnosticsSummary 聚合当前 Application 的低基数监控摘要。
//
// 方法只在 Application 锁内复制身份、Listener 状态、Node 指针和 BufferPool 指针；Runtime、
// Pool 和 Node 叶子采集全部在解锁后执行，避免监控请求延长 Application 生命周期锁临界区。
func (app *Application) DiagnosticsSummary() diagnostics.Summary {
	collectedAt := time.Now()
	result := diagnostics.Summary{
		SchemaVersion: 1,
		CollectedAt:   collectedAt,
		Nodes:         make([]diagnostics.NodeSummary, 0),
	}
	if app == nil {
		result.Application.State = "failed"
		result.Application.AdminServer.State = "stopped"
		result.Application.Pprof.State = "stopped"
		result.Runtime = collectRuntimeSummary()
		result.CollectCost = diagnostics.Duration(time.Since(collectedAt))
		return result
	}

	app.mu.Lock()
	result.StartedAt = app.startedAt
	result.Application = diagnostics.ApplicationSummary{
		Name:        app.appName,
		State:       applicationStateText(app.State()),
		AdminServer: app.adminHTTP.snapshot(),
		Pprof:       app.pprofHTTP.snapshot(),
	}
	nodes := append([]*node.Node(nil), app.nodes...)
	pool := app.bufferPool
	app.mu.Unlock()

	result.Runtime = collectRuntimeSummary()
	if pool != nil {
		result.BufferPool = mapBufferPoolStats(pool.Stats())
	}
	result.Nodes = make([]diagnostics.NodeSummary, len(nodes))
	for index, current := range nodes {
		result.Nodes[index] = current.DiagnosticsSummary()
	}
	result.CollectCost = diagnostics.Duration(time.Since(collectedAt))
	return result
}

func collectRuntimeSnapshot() diagnostics.RuntimeSnapshot {
	var memory runtime.MemStats
	runtime.ReadMemStats(&memory)
	metricValues := collectRuntimeMetricValues()
	result := diagnostics.RuntimeSnapshot{
		Goroutines:       runtime.NumGoroutine(),
		GOMAXPROCS:       runtime.GOMAXPROCS(0),
		HeapAllocBytes:   memory.HeapAlloc,
		HeapObjects:      memory.HeapObjects,
		NextGCBytes:      memory.NextGC,
		TotalAllocBytes:  memory.TotalAlloc,
		GCCycles:         memory.NumGC,
		GCPauseTotal:     diagnostics.Duration(memory.PauseTotalNs),
		MemoryLimitBytes: metricValues.memoryLimitBytes,
	}
	if memory.LastGC != 0 {
		result.LastGC = time.Unix(0, int64(memory.LastGC))
		result.LastGCPause = diagnostics.Duration(
			memory.PauseNs[(memory.NumGC+255)%256],
		)
	}
	return result
}

// collectRuntimeSummary 读取一次真实 Runtime 状态并映射到监控 DTO。
func collectRuntimeSummary() diagnostics.RuntimeSummary {
	var memory runtime.MemStats
	runtime.ReadMemStats(&memory)
	return runtimeSummaryFrom(memory, collectRuntimeMetricValues())
}

// runtimeSummaryFrom 将同一次 MemStats 与固定 metrics 读取组合成稳定口径。
func runtimeSummaryFrom(
	memory runtime.MemStats,
	metricValues runtimeMetricValues,
) diagnostics.RuntimeSummary {
	goMemoryUsed := uint64(0)
	if memory.Sys >= memory.HeapReleased {
		goMemoryUsed = memory.Sys - memory.HeapReleased
	}
	return diagnostics.RuntimeSummary{
		Goroutines:            runtime.NumGoroutine(),
		RunnableGoroutines:    metricValues.runnableGoroutines,
		GOMAXPROCS:            runtime.GOMAXPROCS(0),
		GoMemoryUsedBytes:     goMemoryUsed,
		MemoryLimitBytes:      metricValues.memoryLimitBytes,
		HeapAllocBytes:        memory.HeapAlloc,
		HeapObjects:           memory.HeapObjects,
		TotalAllocBytes:       memory.TotalAlloc,
		GCCycles:              memory.NumGC,
		GCPauseTotal:          diagnostics.Duration(memory.PauseTotalNs),
		GCCPUSecondsTotal:     metricValues.gcCPUSecondsTotal,
		MutexWaitSecondsTotal: metricValues.mutexWaitSecondsTotal,
	}
}

// collectRuntimeMetricValues 按固定 Go 1.26.5 名称读取监控指标，不扫描全部 metrics 描述符。
func collectRuntimeMetricValues() runtimeMetricValues {
	samples := [...]runtimemetrics.Sample{
		{Name: runtimeRunnableGoroutinesMetric},
		{Name: runtimeGCCPUSecondsMetric},
		{Name: runtimeMutexWaitSecondsMetric},
		{Name: runtimeMemoryLimitMetric},
	}
	runtimemetrics.Read(samples[:])
	return runtimeMetricValuesFrom(samples[:])
}

// runtimeMetricValuesFrom 检查每个 Value 的 Kind 后读取；未知名称产生的 KindBad 保持零值。
func runtimeMetricValuesFrom(samples []runtimemetrics.Sample) runtimeMetricValues {
	var result runtimeMetricValues
	for index := range samples {
		sample := &samples[index]
		switch sample.Name {
		case runtimeRunnableGoroutinesMetric:
			if sample.Value.Kind() == runtimemetrics.KindUint64 {
				result.runnableGoroutines = sample.Value.Uint64()
			}
		case runtimeGCCPUSecondsMetric:
			if sample.Value.Kind() == runtimemetrics.KindFloat64 {
				result.gcCPUSecondsTotal = sample.Value.Float64()
			}
		case runtimeMutexWaitSecondsMetric:
			if sample.Value.Kind() == runtimemetrics.KindFloat64 {
				result.mutexWaitSecondsTotal = sample.Value.Float64()
			}
		case runtimeMemoryLimitMetric:
			if sample.Value.Kind() == runtimemetrics.KindUint64 {
				// Go 内存上限由 debug.SetMemoryLimit 的 int64 契约产生，metrics 仅以
				// Uint64 传输同一个非负值，因此这里恢复其原始类型。
				result.memoryLimitBytes = int64(sample.Value.Uint64())
			}
		}
	}
	return result
}

func applicationStateText(state State) string {
	switch state {
	case StateCreated:
		return "created"
	case StateStarting:
		return "starting"
	case StateRunning:
		return "running"
	case StateStopping:
		return "stopping"
	case StateStopped:
		return "stopped"
	case StateFailed:
		return "failed"
	default:
		return "unknown"
	}
}
