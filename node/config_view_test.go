package node

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	originconfig "github.com/duanhf2012/origin/v3/config"
	"github.com/duanhf2012/origin/v3/internal/bufferpool"
	originlog "github.com/duanhf2012/origin/v3/log"
)

func TestNodeSelectsActualServiceNameConfigAndNodeOverride(t *testing.T) {
	directory := t.TempDir()
	if err := os.WriteFile(filepath.Join(directory, "business.yaml"), []byte(`
services:
  ActualPlayer:
    value: global
  PlayerTemplate:
    value: template
node_services:
  config-node:
    ActualPlayer:
      value: node
`), 0o600); err != nil {
		t.Fatal(err)
	}
	snapshot, err := originconfig.LoadSnapshot(directory)
	if err != nil {
		t.Fatal(err)
	}
	target := &lifecycleService{}
	current, err := New(
		Config{ID: "config-node", Services: []string{"ActualPlayer"}},
		[]ServiceBinding{{
			Name: "ActualPlayer", Template: "PlayerTemplate", Service: target,
		}},
		originlog.NewNop(),
		Options{
			MaxTimersPerNode: 8,
			TimerLocation:    time.UTC,
			BufferPool:       bufferpool.NewPool(bufferpool.Options{}),
			Config:           snapshot,
		},
	)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	t.Cleanup(func() { _ = current.Rollback(t.Context()) })
	configured := struct {
		Value string `config:"value"`
	}{}
	if err := target.ParseServiceConfig(&configured); err != nil {
		t.Fatalf("ParseServiceConfig() error = %v", err)
	}
	if configured.Value != "node" {
		t.Fatalf("Value = %q, want node", configured.Value)
	}
}
