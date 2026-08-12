package blueprintmodule

import (
	"errors"
	"testing"

	blueprint "github.com/duanhf2012/OriginBlueprint/engine/go/blueprint"
)

// compileTimeNode 只用于证明业务节点可以只导入 blueprintmodule 实现引擎接口。
type compileTimeNode struct{ BaseExecNode }

func (*compileTimeNode) GetName() string    { return "CompileTimeNode" }
func (*compileTimeNode) Exec() (int, error) { return 0, nil }

var _ IExecNode = (*compileTimeNode)(nil)

func TestPublicAliasesPreserveEngineTypes(t *testing.T) {
	// 类型别名必须允许业务值与引擎值零转换互用，不能建立第二份运行时类型。
	var state ExecutionState = blueprint.ExecutionSuspended
	if state != blueprint.ExecutionSuspended {
		t.Fatalf("ExecutionState = %v", state)
	}
	values := PortArray{{IntVal: PortInt(7)}}
	var engineValues blueprint.PortArray = values
	if engineValues[0].IntVal != 7 {
		t.Fatalf("PortArray = %+v", engineValues)
	}
	var traceEvent BlueprintTraceEvent = blueprint.BlueprintTraceEvent{NodeName: "ApplyDamage"}
	var tracePort BlueprintTracePortValue = blueprint.BlueprintTracePortValue{Type: "整数", Value: int64(7)}
	if traceEvent.NodeName != "ApplyDamage" || tracePort.Value != int64(7) {
		t.Fatalf("trace aliases = %+v %+v", traceEvent, tracePort)
	}
}

func TestPublicErrorsPreserveErrorsIs(t *testing.T) {
	// 包装层错误别名必须保留 errors.Is，业务才能稳定区分等待、取消和恢复冲突。
	if !errors.Is(ErrExecutionPending, blueprint.ErrExecutionPending) {
		t.Fatal("ErrExecutionPending does not preserve engine sentinel")
	}
	if !errors.Is(ErrYieldResumed, blueprint.ErrYieldResumed) {
		t.Fatal("ErrYieldResumed does not preserve engine sentinel")
	}
}
