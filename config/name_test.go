package config

import (
	"reflect"
	"testing"
)

func TestCollectStructFields(t *testing.T) {
	t.Parallel()

	// 目标同时包含匿名嵌入、字段原名、json 改名、忽略和无效 yaml Tag。
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

	// 收集字段模型并确认合法结构不会产生模型错误。
	fields := collectStructFields(reflect.TypeFor[Target]())
	if fields.err != nil {
		t.Fatalf("collectStructFields 返回错误: %v", fields.err)
	}
	// 这些名称必须映射成功。
	for _, name := range []string{"NodeID", "DefaultTimeout", "listen", "ActualField"} {
		if _, exists := fields.byName[name]; !exists {
			t.Errorf("缺少字段映射 %q", name)
		}
	}
	// json 忽略字段和 yaml 专属名称都不得进入模型。
	for _, name := range []string{"Ignored", "wrong_name", "node_id", "default_timeout"} {
		if _, exists := fields.byName[name]; exists {
			t.Errorf("不应存在字段映射 %q", name)
		}
	}
}

func TestCollectStructFieldsRejectsConflict(t *testing.T) {
	t.Parallel()

	// 匿名字段原名与显式 json 名称故意制造冲突。
	type Embedded struct {
		Name string
	}
	type Target struct {
		Embedded
		Other string `json:"Name"`
	}

	// 模型收集阶段必须拒绝，不能按字段顺序选择其中一个。
	fields := collectStructFields(reflect.TypeFor[Target]())
	if fields.err == nil {
		t.Fatal("重复字段映射应返回错误")
	}
}
