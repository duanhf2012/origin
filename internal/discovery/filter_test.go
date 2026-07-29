package discovery

import (
	"errors"
	"testing"

	"github.com/duanhf2012/origin/v3/errs"
)

// TestCompileFilterDefaultAndExplicitEmpty 锁定省略配置与显式空列表完全不同的语义。
func TestCompileFilterDefaultAndExplicitEmpty(t *testing.T) {
	t.Parallel()

	// 未配置时应保持 v2 的默认可见性，允许全部远端公开 Service。
	allowAll, err := CompileFilter(false, nil)
	if err != nil {
		t.Fatalf("CompileFilter(false) error = %v", err)
	}
	if !allowAll.Match(
		RawNode{NodeID: "game-1"},
		RawService{ServiceName: "PlayerService"},
	) {
		t.Fatal("未配置 allow_discovery 时公开 Service 不可见")
	}

	// 显式空列表表示业务有意关闭全部远端发现，不得退回默认允许规则。
	denyAll, err := CompileFilter(true, []Rule{})
	if err != nil {
		t.Fatalf("CompileFilter(true, empty) error = %v", err)
	}
	if denyAll.Match(
		RawNode{NodeID: "game-1"},
		RawService{ServiceName: "PlayerService"},
	) {
		t.Fatal("显式 allow_discovery: [] 仍然放行了 Service")
	}
}

// TestCompileFilterCombinedRules 验证规则间 OR、规则内不同维度 AND 和同一标签值 OR。
func TestCompileFilterCombinedRules(t *testing.T) {
	t.Parallel()

	services := []string{"PlayerService", "ChatService"}
	labels := map[string][]string{
		"region": {"cn-east", "cn-north"},
		"stage":  {"prod"},
	}
	filter, err := CompileFilter(true, []Rule{{
		Services:   &services,
		NodeLabels: &labels,
	}})
	if err != nil {
		t.Fatalf("CompileFilter() error = %v", err)
	}

	tests := []struct {
		name    string
		node    RawNode
		service RawService
		want    bool
	}{
		{
			name: "全部维度匹配",
			node: RawNode{
				NodeID: "game-1",
				Labels: map[string]string{"region": "cn-east", "stage": "prod"},
			},
			service: RawService{ServiceName: "PlayerService"},
			want:    true,
		},
		{
			name: "同一标签第二个值匹配",
			node: RawNode{
				NodeID: "game-2",
				Labels: map[string]string{"region": "cn-north", "stage": "prod"},
			},
			service: RawService{ServiceName: "ChatService"},
			want:    true,
		},
		{
			name: "服务名不匹配",
			node: RawNode{
				NodeID: "game-3",
				Labels: map[string]string{"region": "cn-east", "stage": "prod"},
			},
			service: RawService{ServiceName: "DBService"},
			want:    false,
		},
		{
			name: "不同标签必须同时匹配",
			node: RawNode{
				NodeID: "game-4",
				Labels: map[string]string{"region": "cn-east", "stage": "dev"},
			},
			service: RawService{ServiceName: "PlayerService"},
			want:    false,
		},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			if got := filter.Match(test.node, test.service); got != test.want {
				t.Fatalf("Match() = %v, want %v", got, test.want)
			}
		})
	}
}

// TestCompileFilterRulesUseOR 锁定多条规则之间为 OR，避免后来误改成全部规则同时满足。
func TestCompileFilterRulesUseOR(t *testing.T) {
	t.Parallel()

	// 第一条规则只关注 PlayerService，第二条规则关注 prod 标签；任意一条命中即可放行。
	services := []string{"PlayerService"}
	labels := map[string][]string{"stage": {"prod"}}
	filter, err := CompileFilter(true, []Rule{
		{Services: &services},
		{NodeLabels: &labels},
	})
	if err != nil {
		t.Fatalf("CompileFilter() error = %v", err)
	}

	tests := []struct {
		name    string
		node    RawNode
		service RawService
		want    bool
	}{
		{
			name:    "仅第一条服务规则命中",
			node:    RawNode{Labels: map[string]string{"stage": "dev"}},
			service: RawService{ServiceName: "PlayerService"},
			want:    true,
		},
		{
			name:    "仅第二条标签规则命中",
			node:    RawNode{Labels: map[string]string{"stage": "prod"}},
			service: RawService{ServiceName: "DBService"},
			want:    true,
		},
		{
			name:    "全部规则均未命中",
			node:    RawNode{Labels: map[string]string{"stage": "dev"}},
			service: RawService{ServiceName: "DBService"},
			want:    false,
		},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			if got := filter.Match(test.node, test.service); got != test.want {
				t.Fatalf("Match() = %v, want %v", got, test.want)
			}
		})
	}
}

// TestCompileFilterRejectsEmptyRule 验证容易产生歧义的空维度在启动冷路径立即失败。
func TestCompileFilterRejectsEmptyRule(t *testing.T) {
	t.Parallel()

	emptyServices := []string{}
	emptyLabels := map[string][]string{}
	emptyServiceName := []string{""}
	emptyLabelKey := map[string][]string{"": {"prod"}}
	emptyLabelValues := map[string][]string{"stage": {}}
	emptyLabelValue := map[string][]string{"stage": {""}}
	tests := []struct {
		name  string
		rules []Rule
	}{
		{name: "空规则", rules: []Rule{{}}},
		{name: "显式空服务", rules: []Rule{{Services: &emptyServices}}},
		{name: "显式空标签", rules: []Rule{{NodeLabels: &emptyLabels}}},
		{name: "空服务名", rules: []Rule{{Services: &emptyServiceName}}},
		{name: "空标签名", rules: []Rule{{NodeLabels: &emptyLabelKey}}},
		{name: "空标签值列表", rules: []Rule{{NodeLabels: &emptyLabelValues}}},
		{name: "空标签值", rules: []Rule{{NodeLabels: &emptyLabelValue}}},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			_, err := CompileFilter(true, test.rules)
			if !errors.Is(err, errs.ErrInvalidConfig) {
				t.Fatalf("CompileFilter() error = %v, want invalid config", err)
			}
		})
	}
}
