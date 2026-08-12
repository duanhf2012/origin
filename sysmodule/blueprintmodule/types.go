package blueprintmodule

import blueprint "github.com/duanhf2012/OriginBlueprint/engine/go/blueprint"

// NodeFactory 为每次加载检查和节点执行创建一个全新的业务节点。
//
// 工厂可能在非 Service goroutine 中被调用，只能构造节点和注入并发安全引用，不能访问 Service 串行
// 业务状态。业务状态只能在节点 Exec 中访问。
type NodeFactory = func() IExecNode

// IExecNode 是自定义蓝图执行节点必须实现的最小接口。
type IExecNode = blueprint.IExecNode

// BaseExecNode 提供端口读取、结果写入和 Yield 等节点执行辅助能力。
type BaseExecNode = blueprint.BaseExecNode

// YieldHandle 是异步节点的一次性恢复句柄。
type YieldHandle = blueprint.YieldHandle

// PortArray 是蓝图数组端口值。
type PortArray = blueprint.PortArray

// PortInt 是蓝图整数端口值，底层固定为 int64。
type PortInt = blueprint.PortInt

// PortFloat 是蓝图浮点端口值。
type PortFloat = blueprint.PortFloat

// PortString 是蓝图字符串端口值。
type PortString = blueprint.PortString

// PortBool 是蓝图布尔端口值。
type PortBool = blueprint.PortBool

// ArrayData 是数组端口中的单个元素。
type ArrayData = blueprint.ArrayData

// ExecutionState 表示一次蓝图执行的当前状态。
type ExecutionState = blueprint.ExecutionState

// Execution 状态常量与底层引擎完全一致，可直接用于状态分支和日志。
const (
	ExecutionPending   = blueprint.ExecutionPending
	ExecutionRunning   = blueprint.ExecutionRunning
	ExecutionSuspended = blueprint.ExecutionSuspended
	ExecutionCompleted = blueprint.ExecutionCompleted
	ExecutionCanceled  = blueprint.ExecutionCanceled
	ExecutionFailed    = blueprint.ExecutionFailed
)

// BlueprintError 为解析、编译和执行错误保留图、入口、节点和程序计数器等定位信息。
type BlueprintError = blueprint.BlueprintError

// BlueprintTraceLogger 接收开启 Trace 后的逐节点结构化事件。
type BlueprintTraceLogger = blueprint.BlueprintTraceLogger

// BlueprintTraceEvent 是 Trace Logger 接收的单个节点结构化执行事件。
type BlueprintTraceEvent = blueprint.BlueprintTraceEvent

// BlueprintTracePortValue 是 Trace 事件中的单个输入或输出端口快照。
type BlueprintTracePortValue = blueprint.BlueprintTracePortValue

// BlueprintDiagnosticSink 接收蓝图执行的终态失败。
type BlueprintDiagnosticSink = blueprint.BlueprintDiagnosticSink

// 高频执行状态保持为引擎原始哨兵，确保 errors.Is 可以跨包装层使用。
var (
	ErrExecutionSuspended      = blueprint.ErrExecutionSuspended
	ErrExecutionPending        = blueprint.ErrExecutionPending
	ErrExecutionCanceled       = blueprint.ErrExecutionCanceled
	ErrExecutionCompleted      = blueprint.ErrExecutionCompleted
	ErrExecutionBudgetExceeded = blueprint.ErrExecutionBudgetExceeded
	ErrBlueprintClosed         = blueprint.ErrBlueprintClosed
	ErrGraphNotFound           = blueprint.ErrGraphNotFound
	ErrEntranceNotFound        = blueprint.ErrEntranceNotFound
	ErrGraphReleased           = blueprint.ErrGraphReleased
	ErrYieldResumed            = blueprint.ErrYieldResumed
	ErrYieldInvalid            = blueprint.ErrYieldInvalid
)
