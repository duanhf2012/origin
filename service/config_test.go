package service

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	originconfig "github.com/duanhf2012/origin/v3/config"
	"github.com/duanhf2012/origin/v3/errs"
)

type configTestRuntime struct {
	*testRuntime
	root    originconfig.View
	service originconfig.View
}

func (runtime *configTestRuntime) RootConfig() originconfig.View {
	return runtime.root
}

func (runtime *configTestRuntime) ServiceConfig() originconfig.View {
	return runtime.service
}

func TestServiceBusinessConfigFacade(t *testing.T) {
	directory := t.TempDir()
	if err := os.WriteFile(filepath.Join(directory, "runtime.yaml"), []byte(`
services:
  ActualPlayer:
    timeout: 9
    nested:
      enabled: true
    future: ignored
`), 0o600); err != nil {
		t.Fatal(err)
	}
	snapshot, err := originconfig.LoadSnapshot(directory)
	if err != nil {
		t.Fatal(err)
	}
	view, err := snapshot.Root().Lookup("services.ActualPlayer")
	if err != nil {
		t.Fatal(err)
	}
	target := &Service{}
	if err := BindRuntime(target, &configTestRuntime{
		testRuntime: &testRuntime{nodeID: "player-1", name: "ActualPlayer"},
		root:        snapshot.Root(),
		service:     view,
	}); err != nil {
		t.Fatal(err)
	}

	configured := struct {
		Timeout int `config:"timeout"`
	}{Timeout: 3}
	if err := target.ParseServiceConfig(&configured); err != nil {
		t.Fatalf("ParseServiceConfig() error = %v", err)
	}
	if configured.Timeout != 9 {
		t.Fatalf("Timeout = %d", configured.Timeout)
	}
	nested := struct {
		Enabled bool `config:"enabled"`
	}{}
	if err := target.GetServiceConfig("nested", &nested); err != nil {
		t.Fatalf("GetServiceConfig() error = %v", err)
	}
	if !nested.Enabled {
		t.Fatal("nested.Enabled = false")
	}
	if err := target.GetServiceConfig("missing", &nested); !errors.Is(err, errs.ErrConfigNotFound) {
		t.Fatalf("missing GetServiceConfig() error = %v", err)
	}
	var rootTimeout int
	if err := target.GetConfig("services.ActualPlayer.timeout", &rootTimeout); err != nil {
		t.Fatalf("GetConfig() error = %v", err)
	}
	if rootTimeout != 9 {
		t.Fatalf("rootTimeout = %d", rootTimeout)
	}
	if err := target.GetConfig("", &rootTimeout); !errors.Is(err, errs.ErrInvalidArgument) {
		t.Fatalf("empty GetConfig() error = %v", err)
	}
	for _, path := range []string{"services..timeout", "services.*", "services[0]", `services\timeout`} {
		if err := target.GetConfig(path, &rootTimeout); !errors.Is(err, errs.ErrInvalidArgument) {
			t.Fatalf("GetConfig(%q) error = %v", path, err)
		}
	}
}

// TestServiceStrictBusinessConfigFacade 验证基础设施配置可以在保留默认值的同时拒绝未知字段，
// 并且失败不会把已经解码的前置字段写回调用方。
func TestServiceStrictBusinessConfigFacade(t *testing.T) {
	directory := t.TempDir()
	if err := os.WriteFile(filepath.Join(directory, "runtime.yaml"), []byte(`
services:
  ActualPlayer:
    network:
      timeout: 9
      misspelled_timeout: 10
`), 0o600); err != nil {
		t.Fatal(err)
	}
	snapshot, err := originconfig.LoadSnapshot(directory)
	if err != nil {
		t.Fatal(err)
	}
	view, err := snapshot.Root().Lookup("services.ActualPlayer")
	if err != nil {
		t.Fatal(err)
	}
	target := &Service{}
	if err := BindRuntime(target, &configTestRuntime{
		testRuntime: &testRuntime{nodeID: "player-1", name: "ActualPlayer"},
		root:        snapshot.Root(),
		service:     view,
	}); err != nil {
		t.Fatal(err)
	}

	// 目标预填默认值；严格解码失败后必须保持原值，避免使用半份基础设施配置。
	configured := struct{ Timeout int }{Timeout: 3}
	err = target.GetServiceConfigStrict("network", &configured)
	if !errs.IsCode(err, errs.CodeInvalidConfig) {
		t.Fatalf("GetServiceConfigStrict() error = %v", err)
	}
	if configured.Timeout != 3 {
		t.Fatalf("失败后 Timeout = %d", configured.Timeout)
	}

	// Module 外观必须委托同一严格语义，不能建立第二套解析规则。
	module := &testModule{}
	module.owner = target
	err = module.GetServiceConfigStrict("network", &configured)
	if !errs.IsCode(err, errs.CodeInvalidConfig) {
		t.Fatalf("Module.GetServiceConfigStrict() error = %v", err)
	}

	// 严格读取仍遵守统一路径规则，并对不存在节点返回稳定 ConfigNotFound。
	if err := target.GetServiceConfigStrict("", &configured); !errors.Is(err, errs.ErrInvalidArgument) {
		t.Fatalf("empty GetServiceConfigStrict() error = %v", err)
	}
	if err := target.GetServiceConfigStrict("missing", &configured); !errors.Is(err, errs.ErrConfigNotFound) {
		t.Fatalf("missing GetServiceConfigStrict() error = %v", err)
	}

	// 没有未知字段时严格模式提交覆盖值。
	var exact struct{ Timeout int }
	if err := target.GetServiceConfigStrict("network.timeout", &exact.Timeout); err != nil {
		t.Fatalf("scalar GetServiceConfigStrict() error = %v", err)
	}
	if exact.Timeout != 9 {
		t.Fatalf("strict Timeout = %d", exact.Timeout)
	}
}

// TestModuleBusinessConfigFacade 验证 Module 不建立第二套配置语义，而是读取所属
// Service 已冻结的根配置和有效业务配置。
func TestModuleBusinessConfigFacade(t *testing.T) {
	directory := t.TempDir()
	if err := os.WriteFile(filepath.Join(directory, "runtime.yaml"), []byte(`
services:
  ActualPlayer:
    timeout: 9
`), 0o600); err != nil {
		t.Fatal(err)
	}
	snapshot, err := originconfig.LoadSnapshot(directory)
	if err != nil {
		t.Fatal(err)
	}
	view, err := snapshot.Root().Lookup("services.ActualPlayer")
	if err != nil {
		t.Fatal(err)
	}
	owner := &Service{}
	if err := BindRuntime(owner, &configTestRuntime{
		testRuntime: &testRuntime{nodeID: "player-1", name: "ActualPlayer"},
		root:        snapshot.Root(),
		service:     view,
	}); err != nil {
		t.Fatal(err)
	}
	module := &testModule{}
	module.owner = owner

	var serviceTimeout int
	if err := module.GetServiceConfig("timeout", &serviceTimeout); err != nil {
		t.Fatalf("Module.GetServiceConfig() error = %v", err)
	}
	var rootTimeout int
	if err := module.GetConfig("services.ActualPlayer.timeout", &rootTimeout); err != nil {
		t.Fatalf("Module.GetConfig() error = %v", err)
	}
	if serviceTimeout != 9 || rootTimeout != 9 {
		t.Fatalf("Module config values = service:%d root:%d", serviceTimeout, rootTimeout)
	}
}

func TestServiceMissingBusinessConfigKeepsDefaults(t *testing.T) {
	target := &Service{}
	if err := BindRuntime(target, &configTestRuntime{
		testRuntime: &testRuntime{nodeID: "player-1", name: "ActualPlayer"},
	}); err != nil {
		t.Fatal(err)
	}
	configured := struct{ Timeout int }{Timeout: 3}
	if err := target.ParseServiceConfig(&configured); err != nil {
		t.Fatalf("ParseServiceConfig() error = %v", err)
	}
	if configured.Timeout != 3 {
		t.Fatalf("Timeout = %d", configured.Timeout)
	}
	if err := target.GetServiceConfig("nested", &configured); !errors.Is(err, errs.ErrConfigNotFound) {
		t.Fatalf("GetServiceConfig() error = %v", err)
	}
}

func TestServiceBusinessConfigRejectedAfterRelease(t *testing.T) {
	runtime := &configTestRuntime{
		testRuntime: &testRuntime{nodeID: "player-1", name: "ActualPlayer", state: StateStopped},
	}
	target := &Service{}
	if err := BindRuntime(target, runtime); err != nil {
		t.Fatal(err)
	}
	var configured struct{}
	if err := target.ParseServiceConfig(&configured); !errors.Is(err, errs.ErrServiceStopped) {
		t.Fatalf("ParseServiceConfig() error = %v", err)
	}
	runtime.state = StateFailed
	if err := target.GetConfig("server.value", &configured); !errors.Is(err, errs.ErrServiceFailed) {
		t.Fatalf("GetConfig() error = %v", err)
	}
}
