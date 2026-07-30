package rpc

import (
	"errors"
	"testing"

	"github.com/duanhf2012/origin/v3/errs"
)

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
