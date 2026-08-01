package config

import (
	"errors"
	"testing"

	"github.com/duanhf2012/origin/v3/errs"
)

func TestSnapshotViewDecodesBusinessConfigLeniently(t *testing.T) {
	directory := t.TempDir()
	writeConfig(t, directory, "runtime.yaml", `
services:
  PlayerService:
    timeout: 3
    future_option: enabled
`)

	snapshot, err := LoadSnapshot(directory)
	if err != nil {
		t.Fatalf("LoadSnapshot() error = %v", err)
	}
	view, err := snapshot.Root().Lookup("services.PlayerService")
	if err != nil {
		t.Fatalf("Lookup() error = %v", err)
	}
	configured := struct {
		Timeout int    `config:"timeout"`
		Mode    string `config:"mode"`
	}{Timeout: 1, Mode: "safe"}
	if err := view.Decode(&configured); err != nil {
		t.Fatalf("View.Decode() error = %v", err)
	}
	if configured.Timeout != 3 || configured.Mode != "safe" {
		t.Fatalf("configured = %+v", configured)
	}

	strict := struct {
		Timeout int `config:"timeout"`
	}{}
	if err := view.DecodeStrict(&strict); !errs.IsCode(err, errs.CodeInvalidConfig) {
		t.Fatalf("View.DecodeStrict() error = %v", err)
	}
}

func TestSnapshotViewMissingAndAtomicDecode(t *testing.T) {
	directory := t.TempDir()
	writeConfig(t, directory, "runtime.json", `{"service":{"count":"bad"}}`)

	snapshot, err := LoadSnapshot(directory)
	if err != nil {
		t.Fatalf("LoadSnapshot() error = %v", err)
	}
	if !snapshot.Root().Valid() {
		t.Fatal("Root().Valid() = false")
	}
	if _, err := snapshot.Root().Lookup("service.missing"); !errors.Is(err, errs.ErrConfigNotFound) {
		t.Fatalf("missing Lookup() error = %v", err)
	}

	view, err := snapshot.Root().Lookup("service")
	if err != nil {
		t.Fatalf("Lookup(service) error = %v", err)
	}
	target := struct {
		Count int `config:"count"`
	}{Count: 7}
	if err := view.Decode(&target); !errs.IsCode(err, errs.CodeInvalidConfig) {
		t.Fatalf("View.Decode() error = %v", err)
	}
	if target.Count != 7 {
		t.Fatalf("失败解码修改了目标: %+v", target)
	}

	var absent View
	untouched := struct{ Count int }{Count: 11}
	if err := absent.Decode(&untouched); err != nil {
		t.Fatalf("invalid View.Decode() error = %v", err)
	}
	if untouched.Count != 11 {
		t.Fatalf("invalid View.Decode() 修改了目标: %+v", untouched)
	}
}

func TestLoadDirStillRejectsUnknownFieldsThroughSnapshot(t *testing.T) {
	directory := t.TempDir()
	writeConfig(t, directory, "runtime.yaml", "known: 1\nunknown: 2\n")
	target := struct {
		Known int `config:"known"`
	}{}
	if err := LoadDir(directory, &target); !errs.IsCode(err, errs.CodeInvalidConfig) {
		t.Fatalf("LoadDir() error = %v", err)
	}
}
