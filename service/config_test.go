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
	if err := target.DecodeConfig(&configured); err != nil {
		t.Fatalf("DecodeConfig() error = %v", err)
	}
	if configured.Timeout != 9 {
		t.Fatalf("Timeout = %d", configured.Timeout)
	}
	nested := struct {
		Enabled bool `config:"enabled"`
	}{}
	if err := target.DecodeConfigAt("nested", &nested); err != nil {
		t.Fatalf("DecodeConfigAt() error = %v", err)
	}
	if !nested.Enabled {
		t.Fatal("nested.Enabled = false")
	}
	if err := target.DecodeConfigAt("missing", &nested); !errors.Is(err, errs.ErrConfigNotFound) {
		t.Fatalf("missing DecodeConfigAt() error = %v", err)
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
	if err := target.DecodeConfig(&configured); err != nil {
		t.Fatalf("DecodeConfig() error = %v", err)
	}
	if configured.Timeout != 3 {
		t.Fatalf("Timeout = %d", configured.Timeout)
	}
	if err := target.DecodeConfigAt("nested", &configured); !errors.Is(err, errs.ErrConfigNotFound) {
		t.Fatalf("DecodeConfigAt() error = %v", err)
	}
}
