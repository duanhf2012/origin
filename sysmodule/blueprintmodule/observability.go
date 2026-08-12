package blueprintmodule

import (
	blueprint "github.com/duanhf2012/OriginBlueprint/engine/go/blueprint"
	originlog "github.com/duanhf2012/origin/v3/log"
)

type moduleDiagnosticSink struct{ logger originlog.Logger }

// ReportBlueprintError 只记录结构化定位字段和根因，不输出端口值或业务参数。
func (sink *moduleDiagnosticSink) ReportBlueprintError(event blueprint.BlueprintError) {
	if sink == nil {
		return
	}
	sink.logger.Error(
		"Blueprint Execution 失败",
		originlog.String("stage", string(event.Stage)),
		originlog.String("source_path", event.SourcePath),
		originlog.String("graph_name", event.GraphName),
		originlog.Int64("graph_id", event.GraphID),
		originlog.Int64("entrance_id", event.EntranceID),
		originlog.Uint64("execution_id", event.ExecutionID),
		originlog.String("node_id", event.NodeID),
		originlog.String("node_name", event.NodeName),
		originlog.Int32("pc", int32(event.PC)),
		originlog.Err(event.Cause),
	)
}

// SetTraceEnabled 在运行期切换逐节点 Trace。
//
// Trace 默认关闭，开启后会复制端口值并可能暴露业务数据，只应在明确的短期诊断窗口使用。
func (module *Module) SetTraceEnabled(enabled bool) error {
	engine, err := module.runningEngine()
	if err != nil {
		return err
	}
	engine.SetTraceEnabled(enabled)
	return nil
}
