package rpc

import (
	"errors"
	"strconv"
	"testing"

	"github.com/duanhf2012/origin/v3/errs"
)

// TestClientWhereLabelsFreezesAndMerges 锁定调用方 Map 所有权、稳定顺序和多次 AND 派生。
func TestClientWhereLabelsFreezesAndMerges(t *testing.T) {
	base := Client{target: ToService("PlayerService")}
	source := map[string]string{
		"scope":        "area",
		"real_area_id": "1",
	}
	filtered := base.WhereLabels(source)

	// 派生完成后修改调用方 Map，已经冻结的条件必须保持原值且按 Key 稳定排序。
	source["scope"] = "public"
	delete(source, "real_area_id")
	if base.labels.active() {
		t.Fatal("WhereLabels 修改了基础客户端")
	}
	if len(filtered.labels.required) != 2 ||
		filtered.labels.required[0] != (routeLabel{name: "real_area_id", value: "1"}) ||
		filtered.labels.required[1] != (routeLabel{name: "scope", value: "area"}) {
		t.Fatalf("frozen labels = %+v", filtered.labels)
	}

	// 空条件和完全重复条件都是幂等无操作；新增条件继续与旧条件合并。
	if got := filtered.WhereLabels(nil); len(got.labels.required) != 2 {
		t.Fatalf("nil labels changed filter: %+v", got.labels)
	}
	if got := filtered.WhereLabels(map[string]string{"scope": "area"}); len(got.labels.required) != 2 {
		t.Fatalf("duplicate labels changed filter: %+v", got.labels)
	}
	merged := filtered.WhereLabels(map[string]string{"game_type": "world"})
	if len(merged.labels.required) != 3 ||
		merged.labels.required[0].name != "game_type" ||
		merged.labels.required[1].name != "real_area_id" ||
		merged.labels.required[2].name != "scope" {
		t.Fatalf("merged labels = %+v", merged.labels)
	}

	// OnNode 和所有第二阶段策略只改变各自职责，不能清除已经冻结的候选条件。
	selector := fixedPrepareTestSelector{index: 0, ok: true}
	derived := []Client{
		merged.OnNode("game-1"),
		merged.IncludeRetired(),
		merged.RouteRoundRobin(),
		merged.RouteRandom(),
		merged.Route(uint64(1)),
		merged.RouteBy(selector),
		base.OnNode("game-1").WhereLabels(map[string]string{"scope": "area"}),
	}
	for index, client := range derived {
		if !client.labels.active() {
			t.Fatalf("派生 %d 丢失 Labels", index)
		}
	}
}

// TestClientWhereLabelsMarksUnsatisfiableConditions 锁定冲突和超发现容量条件的无路由状态。
func TestClientWhereLabelsMarksUnsatisfiableConditions(t *testing.T) {
	base := Client{target: ToService("PlayerService")}
	conflict := base.
		WhereLabels(map[string]string{"scope": "area"}).
		WhereLabels(map[string]string{"scope": "public"})
	if !conflict.labels.impossible || len(conflict.labels.required) != 0 {
		t.Fatalf("conflicting labels = %+v", conflict.labels)
	}

	overCapacity := make(map[string]string, 33)
	for index := 0; index < 33; index++ {
		overCapacity["label_"+strconv.Itoa(index)] = "value"
	}
	tooMany := base.WhereLabels(overCapacity)
	if !tooMany.labels.impossible || len(tooMany.labels.required) != 0 {
		t.Fatalf("over-capacity labels = %+v", tooMany.labels)
	}
}

type namedRouteInt int64

func TestClientRouteDerivationKeepsBaseImmutable(t *testing.T) {
	base := Client{
		target: ToService("PlayerService"),
	}

	key := int64(-7)
	exact := base.OnNode("player-2")
	keyed := base.Route(key)

	if base.target.mode != targetService ||
		base.target.nodeID != "" ||
		base.target.serviceName != "PlayerService" {
		t.Fatalf("base target changed: %+v", base.target)
	}
	if exact.target.mode != targetServiceOnNode ||
		exact.target.nodeID != "player-2" ||
		exact.target.serviceName != "PlayerService" {
		t.Fatalf("exact target = %+v", exact.target)
	}
	if keyed.route.mode != routeKey ||
		keyed.route.hash != uint64(key) {
		t.Fatalf("key route = %+v", keyed.route)
	}
}

func TestClientRouteModesAreExplicitValueDerivations(t *testing.T) {
	base := Client{target: ToService("PlayerService")}
	roundRobin := base.RouteRoundRobin()
	random := base.RouteRandom()

	if roundRobin.route.mode != routeRoundRobin {
		t.Fatalf("round robin mode = %d", roundRobin.route.mode)
	}
	if random.route.mode != routeRandom {
		t.Fatalf("random mode = %d", random.route.mode)
	}
	if base.route.mode != routeDefault {
		t.Fatalf("base route mode = %d", base.route.mode)
	}
}

// TestClientIncludeRetiredPreservesValueDerivations 锁定退休范围在全部客户端派生顺序中的值语义。
func TestClientIncludeRetiredPreservesValueDerivations(t *testing.T) {
	base := Client{target: ToService("PlayerService")}
	included := base.IncludeRetired()
	if base.includeRetired {
		t.Fatal("IncludeRetired 修改了基础客户端")
	}
	if !included.includeRetired || !included.IncludeRetired().includeRetired {
		t.Fatal("IncludeRetired 没有返回幂等的包含退休范围")
	}

	// 路由策略与精确 Node 只改变各自职责，不能清除已经显式选择的退休范围。
	selector := prepareTestSelector{region: "east"}
	derived := []Client{
		included.OnNode("player-1"),
		included.RouteRoundRobin(),
		included.RouteRandom(),
		included.Route(uint64(1)),
		included.RouteBy(selector),
		base.OnNode("player-1").IncludeRetired(),
		base.RouteRoundRobin().IncludeRetired(),
		base.RouteRandom().IncludeRetired(),
		base.Route(uint64(1)).IncludeRetired(),
		base.RouteBy(selector).IncludeRetired(),
	}
	for index, client := range derived {
		if !client.includeRetired {
			t.Fatalf("派生 %d 丢失 IncludeRetired 标志", index)
		}
	}
}

func TestRouteKeyNormalizationUsesStableLiterals(t *testing.T) {
	tests := []struct {
		name string
		key  any
		want uint64
	}{
		{name: "string", key: "a", want: 0xaf63dc4c8601ec8c},
		{name: "bytes", key: []byte("a"), want: 0xaf63dc4c8601ec8c},
		{name: "empty string", key: "", want: 0xcbf29ce484222325},
		{name: "int", key: int(-1), want: ^uint64(0)},
		{name: "int8", key: int8(-2), want: ^uint64(1)},
		{name: "int16", key: int16(-3), want: ^uint64(2)},
		{name: "int32", key: int32(-4), want: ^uint64(3)},
		{name: "int64", key: int64(-5), want: ^uint64(4)},
		{name: "uint", key: uint(6), want: 6},
		{name: "uint8", key: uint8(7), want: 7},
		{name: "uint16", key: uint16(8), want: 8},
		{name: "uint32", key: uint32(9), want: 9},
		{name: "uint64", key: uint64(10), want: 10},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			client := (Client{}).Route(test.key)
			if client.route.mode != routeKey {
				t.Fatalf("mode = %d", client.route.mode)
			}
			if client.route.hash != test.want {
				t.Fatalf("hash = %#x, want %#x", client.route.hash, test.want)
			}
			if client.route.err != nil {
				t.Fatalf("route error = %v", client.route.err)
			}
		})
	}
}

func TestRouteRejectsUnsupportedAndNamedKeys(t *testing.T) {
	tests := []struct {
		name string
		key  any
	}{
		{name: "nil", key: nil},
		{name: "named integer", key: namedRouteInt(1)},
		{name: "uintptr", key: uintptr(1)},
		{name: "struct", key: struct{ ID int }{ID: 1}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			client := (Client{}).Route(test.key)
			if client.route.mode != routeKey {
				t.Fatalf("mode = %d", client.route.mode)
			}
			if !errors.Is(client.route.err, errs.ErrRPCInvalidRouteKey) {
				t.Fatalf("route error = %v", client.route.err)
			}
		})
	}
}

func TestRouteValueDerivationsDoNotAllocate(t *testing.T) {
	base := Client{target: ToService("PlayerService")}
	selector := prepareTestSelector{region: "east"}
	allocations := testing.AllocsPerRun(1000, func() {
		_ = base.OnNode("player-1")
		_ = base.RouteRoundRobin()
		_ = base.RouteRandom()
		_ = base.Route(uint64(42))
		_ = base.Route("player")
		_ = base.RouteBy(selector)
		_ = base.IncludeRetired()
	})
	if allocations != 0 {
		t.Fatalf("route derivation allocations = %v", allocations)
	}
}
