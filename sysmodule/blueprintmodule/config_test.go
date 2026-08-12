package blueprintmodule

import (
	"errors"
	"path/filepath"
	"testing"
)

func TestNewNormalizesConfigAndRejectsRepeatSetup(t *testing.T) {
	root := t.TempDir()
	module, err := New(Config{NodeDir: root, GraphDir: filepath.Join(root, ".")})
	if err != nil {
		t.Fatal(err)
	}
	if !filepath.IsAbs(module.config.NodeDir) || !filepath.IsAbs(module.config.GraphDir) {
		t.Fatalf("config was not converted to absolute paths: %+v", module.config)
	}
	if err = module.configure(Config{NodeDir: root, GraphDir: root}); !errors.Is(err, ErrAlreadySetup) {
		t.Fatalf("repeat configure error = %v", err)
	}
}

func TestNewRejectsInvalidConfigAndOptions(t *testing.T) {
	root := t.TempDir()
	tests := []struct {
		name    string
		config  Config
		options []Option
	}{
		{name: "empty node dir", config: Config{GraphDir: root}},
		{name: "empty graph dir", config: Config{NodeDir: root}},
		{name: "nil option", config: Config{NodeDir: root, GraphDir: root}, options: []Option{nil}},
		{name: "nil trace logger", config: Config{NodeDir: root, GraphDir: root}, options: []Option{WithTraceLogger(nil)}},
		{name: "nil diagnostic sink", config: Config{NodeDir: root, GraphDir: root}, options: []Option{WithDiagnosticSink(nil)}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, err := New(test.config, test.options...); !errors.Is(err, ErrInvalidConfig) {
				t.Fatalf("New() error = %v", err)
			}
		})
	}
}

func TestSetupRequiresBoundModule(t *testing.T) {
	root := t.TempDir()
	if err := (&Module{}).Setup(Config{NodeDir: root, GraphDir: root}); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("Setup() error = %v", err)
	}
}
