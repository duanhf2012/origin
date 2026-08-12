package blueprintmodule

import (
	"context"
	"sync"
	"testing"

	blueprint "github.com/duanhf2012/OriginBlueprint/engine/go/blueprint"
)

type traceCapture struct {
	mu     sync.Mutex
	events []blueprint.BlueprintTraceEvent
}

type diagnosticCapture struct {
	mu     sync.Mutex
	events []blueprint.BlueprintError
}

func (capture *diagnosticCapture) ReportBlueprintError(event blueprint.BlueprintError) {
	capture.mu.Lock()
	capture.events = append(capture.events, event)
	capture.mu.Unlock()
}

func (capture *traceCapture) TraceBlueprintNode(event blueprint.BlueprintTraceEvent) {
	capture.mu.Lock()
	capture.events = append(capture.events, event)
	capture.mu.Unlock()
}

func TestDiagnosticOptionAndDefaultSinkAreSafe(t *testing.T) {
	capture := &diagnosticCapture{}
	root := t.TempDir()
	if _, err := New(Config{NodeDir: root, GraphDir: root}, WithDiagnosticSink(capture)); err != nil {
		t.Fatal(err)
	}
	// 默认 Sink 使用 Nop Logger 时也必须安全接受完整结构化错误。
	(&moduleDiagnosticSink{}).ReportBlueprintError(blueprint.BlueprintError{
		Stage: blueprint.BlueprintStageExecute, GraphName: "battle", GraphID: 1,
		EntranceID: 2, ExecutionID: 3, NodeID: "node", NodeName: "Node", Cause: context.Canceled,
	})
}

func TestOptionsRejectDuplicatesAndTypedNil(t *testing.T) {
	root := t.TempDir()
	trace := &traceCapture{}
	if _, err := New(Config{NodeDir: root, GraphDir: root}, WithTraceLogger(trace), WithTraceLogger(trace)); err == nil {
		t.Fatal("duplicate Trace Logger was accepted")
	}
	diagnostic := &diagnosticCapture{}
	if _, err := New(Config{NodeDir: root, GraphDir: root}, WithDiagnosticSink(diagnostic), WithDiagnosticSink(diagnostic)); err == nil {
		t.Fatal("duplicate Diagnostic Sink was accepted")
	}
	var typedNil *traceCapture
	if _, err := New(Config{NodeDir: root, GraphDir: root}, WithTraceLogger(typedNil)); err == nil {
		t.Fatal("typed nil Trace Logger was accepted")
	}
}

func TestTraceIsDisabledByDefaultAndCanBeEnabled(t *testing.T) {
	nodeDir, graphDir := writeLifecycleFixture(t)
	capture := &traceCapture{}
	module, err := New(Config{NodeDir: nodeDir, GraphDir: graphDir}, WithTraceLogger(capture))
	if err != nil {
		t.Fatal(err)
	}
	if err = module.RegisterNodes(
		func() IExecNode { return &lifecycleNode{} },
		func() IExecNode { return &lifecycleAsyncFixtureNode{} },
	); err != nil {
		t.Fatal(err)
	}
	if err = module.OnStart(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer module.OnStop(context.Background())

	// 直接用底层测试路径运行，验证开关本身，不把 Service 协程约束与 Trace 单元测试混在一起。
	instance, err := module.Create("lifecycle")
	if err != nil {
		t.Fatal(err)
	}
	defer instance.Close()
	if _, err = instance.Start(context.Background(), 1); err != nil {
		t.Fatal(err)
	}
	capture.mu.Lock()
	count := len(capture.events)
	capture.mu.Unlock()
	if count != 0 {
		t.Fatalf("default trace events = %d", count)
	}
	if err = module.SetTraceEnabled(true); err != nil {
		t.Fatal(err)
	}
	if _, err = instance.Start(context.Background(), 1); err != nil {
		t.Fatal(err)
	}
	capture.mu.Lock()
	count = len(capture.events)
	capture.mu.Unlock()
	if count == 0 {
		t.Fatal("trace did not record enabled execution")
	}
}
