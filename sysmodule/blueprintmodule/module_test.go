package blueprintmodule

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

type lifecycleNode struct{ BaseExecNode }

func (*lifecycleNode) GetName() string    { return "LifecycleNode" }
func (*lifecycleNode) Exec() (int, error) { return 0, nil }

func TestRegisterNodesRejectsInvalidFactories(t *testing.T) {
	module := configuredModuleForTest(t)
	if err := module.RegisterNodes(nil); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("nil factory error = %v", err)
	}
	if err := module.RegisterNodes(func() IExecNode { return nil }); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("nil node error = %v", err)
	}
	if err := module.RegisterNodes(
		func() IExecNode { return &lifecycleNode{} },
		func() IExecNode { return &lifecycleNode{} },
	); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("duplicate node error = %v", err)
	}
}

func TestLifecycleLoadsGraphAndFreezesRegistration(t *testing.T) {
	nodeDir, graphDir := writeLifecycleFixture(t)
	module, err := New(Config{NodeDir: nodeDir, GraphDir: graphDir})
	if err != nil {
		t.Fatal(err)
	}
	if err = module.RegisterNodes(
		func() IExecNode { return &lifecycleNode{} },
		func() IExecNode { return &lifecycleAsyncFixtureNode{} },
	); err != nil {
		t.Fatal(err)
	}
	if err = module.OnInit(); err != nil {
		t.Fatal(err)
	}
	if err = module.OnStart(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err = module.RegisterNodes(func() IExecNode { return &lifecycleNode{} }); !errors.Is(err, ErrNotRunning) {
		t.Fatalf("register after start error = %v", err)
	}
	if err = module.OnStop(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err = module.OnStop(context.Background()); err != nil {
		t.Fatalf("repeat stop error = %v", err)
	}
}

type lifecycleAsyncFixtureNode struct{ BaseExecNode }

func (*lifecycleAsyncFixtureNode) GetName() string    { return "LifecycleAsyncNode" }
func (*lifecycleAsyncFixtureNode) Exec() (int, error) { return 0, nil }

func TestLifecycleRejectsMissingConfigAndBrokenDirectories(t *testing.T) {
	if err := (&Module{}).OnInit(); !errors.Is(err, ErrNotSetup) {
		t.Fatalf("OnInit() error = %v", err)
	}
	root := t.TempDir()
	module, err := New(Config{NodeDir: filepath.Join(root, "missing-nodes"), GraphDir: filepath.Join(root, "missing-graphs")})
	if err != nil {
		t.Fatal(err)
	}
	if err = module.OnStart(context.Background()); err == nil {
		t.Fatal("OnStart() succeeded with missing directories")
	}
}

func configuredModuleForTest(t *testing.T) *Module {
	t.Helper()
	root := t.TempDir()
	module, err := New(Config{NodeDir: root, GraphDir: root})
	if err != nil {
		t.Fatal(err)
	}
	return module
}

func writeLifecycleFixture(t testing.TB) (string, string) {
	t.Helper()
	root := t.TempDir()
	nodeDir := filepath.Join(root, "nodes")
	graphDir := filepath.Join(root, "graphs")
	if err := os.MkdirAll(nodeDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(graphDir, 0o755); err != nil {
		t.Fatal(err)
	}
	definitions := `[
		{"name":"LifecycleNode","inputs":[{"type":"exec","port_id":0}],"outputs":[]},
		{"name":"LifecycleAsyncNode","inputs":[],"outputs":[{"type":"exec","port_id":0}]}
	]`
	if err := os.WriteFile(filepath.Join(nodeDir, "nodes.json"), []byte(definitions), 0o644); err != nil {
		t.Fatal(err)
	}
	graph := `{"nodes":[{"id":"entry","class":"LifecycleNode_1"}],"edges":[]}`
	if err := os.WriteFile(filepath.Join(graphDir, "lifecycle.vgf"), []byte(graph), 0o644); err != nil {
		t.Fatal(err)
	}
	asyncGraph := `{"nodes":[{"id":"entry","class":"LifecycleAsyncNode_1"}],"edges":[]}`
	if err := os.WriteFile(filepath.Join(graphDir, "lifecycle_async.vgf"), []byte(asyncGraph), 0o644); err != nil {
		t.Fatal(err)
	}
	return nodeDir, graphDir
}
