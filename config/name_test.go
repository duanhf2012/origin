package config

import (
	"reflect"
	"testing"
)

func TestSnakeCase(t *testing.T) {
	t.Parallel()

	tests := map[string]string{
		"DefaultTimeout": "default_timeout",
		"NodeID":         "node_id",
		"RPCConfig":      "rpc_config",
		"HTTPServerURL":  "http_server_url",
		"Name":           "name",
	}
	for input, want := range tests {
		if got := snakeCase(input); got != want {
			t.Errorf("snakeCase(%q) = %q，期望 %q", input, got, want)
		}
	}
}

func TestCollectStructFields(t *testing.T) {
	t.Parallel()

	type Embedded struct {
		NodeID string
	}
	type Target struct {
		Embedded
		DefaultTimeout Duration
		ListenAddress  string `json:"listen,omitempty"`
		Ignored        string `json:"-"`
		ActualField    string `yaml:"wrong_name"`
	}

	fields := collectStructFields(reflect.TypeFor[Target]())
	if fields.err != nil {
		t.Fatalf("collectStructFields 返回错误: %v", fields.err)
	}
	for _, name := range []string{"node_id", "default_timeout", "listen", "actual_field"} {
		if _, exists := fields.byName[name]; !exists {
			t.Errorf("缺少字段映射 %q", name)
		}
	}
	for _, name := range []string{"ignored", "wrong_name"} {
		if _, exists := fields.byName[name]; exists {
			t.Errorf("不应存在字段映射 %q", name)
		}
	}
}

func TestCollectStructFieldsRejectsConflict(t *testing.T) {
	t.Parallel()

	type Embedded struct {
		Name string
	}
	type Target struct {
		Embedded
		Other string `json:"name"`
	}

	fields := collectStructFields(reflect.TypeFor[Target]())
	if fields.err == nil {
		t.Fatal("重复字段映射应返回错误")
	}
}
