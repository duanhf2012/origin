package application

import (
	"runtime"
	"time"

	"github.com/duanhf2012/origin/v3/diagnostics"
	originlog "github.com/duanhf2012/origin/v3/log"
	"github.com/duanhf2012/origin/v3/node"
)

// Diagnostics 聚合当前 Application、Go Runtime、BufferPool 和全部 Node 的不可变快照。
//
// 方法只在 Application 锁内复制稳定指针和身份，随后释放锁顺序采集各叶子，避免诊断冷
// 路径暂停 Node Scheduler、RPC 或网络收发。
func (app *Application) Diagnostics() diagnostics.Snapshot {
	collectedAt := time.Now()
	result := diagnostics.Snapshot{
		SchemaVersion: 1,
		CollectedAt:   collectedAt,
		Nodes:         make([]diagnostics.NodeSnapshot, 0),
	}
	if app == nil {
		result.Application.State = "failed"
		result.Application.DiagnosticsServer.State = "stopped"
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
		DiagnosticsServer: app.diagnosticsHTTP.snapshot(),
		Pprof:             app.pprofHTTP.snapshot(),
	}
	nodes := append([]*node.Node(nil), app.nodes...)
	pool := app.bufferPool
	logRuntime := app.logRuntime
	app.mu.Unlock()

	if status, err := logRuntime.OutputStatus(); err == nil {
		result.Log = diagnostics.LogSnapshot{
			Console: logOutputSnapshot(status.Console),
			File:    logOutputSnapshot(status.File),
		}
	}
	result.Runtime = collectRuntimeSnapshot()
	if pool != nil {
		stats := pool.Stats()
		result.BufferPool = diagnostics.BufferPoolSnapshot{
			Enabled:            stats.Enabled,
			InUseBuffers:       stats.InUseBuffers,
			InUseCapacityBytes: stats.InUseCapacityBytes,
			ZeroSizeInUse:      stats.ZeroSizeInUse,
			OversizeInUse:      stats.OversizeInUse,
			OversizeBytes:      stats.OversizeBytes,
		}
	}
	result.Nodes = make([]diagnostics.NodeSnapshot, len(nodes))
	for index, current := range nodes {
		result.Nodes[index] = current.Diagnostics()
	}
	result.CollectCost = diagnostics.Duration(time.Since(collectedAt))
	return result
}

// logOutputSnapshot 把日志包的当前状态复制为稳定、无反向依赖的诊断 DTO。
func logOutputSnapshot(status originlog.OutputStatus) diagnostics.LogOutputSnapshot {
	return diagnostics.LogOutputSnapshot{
		Available:   status.Available,
		Enabled:     status.Enabled,
		Level:       status.Level.String(),
		ConfigLevel: status.ConfigLevel.String(),
	}
}

func collectRuntimeSnapshot() diagnostics.RuntimeSnapshot {
	var memory runtime.MemStats
	runtime.ReadMemStats(&memory)
	result := diagnostics.RuntimeSnapshot{
		Goroutines:      runtime.NumGoroutine(),
		GOMAXPROCS:      runtime.GOMAXPROCS(0),
		HeapAllocBytes:  memory.HeapAlloc,
		HeapObjects:     memory.HeapObjects,
		NextGCBytes:     memory.NextGC,
		TotalAllocBytes: memory.TotalAlloc,
		GCCycles:        memory.NumGC,
		GCPauseTotal:    diagnostics.Duration(memory.PauseTotalNs),
	}
	if memory.LastGC != 0 {
		result.LastGC = time.Unix(0, int64(memory.LastGC))
		result.LastGCPause = diagnostics.Duration(
			memory.PauseNs[(memory.NumGC+255)%256],
		)
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
