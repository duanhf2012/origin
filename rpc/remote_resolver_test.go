package rpc

import (
	"errors"
	"testing"

	"github.com/duanhf2012/origin/v3/errs"
	"github.com/duanhf2012/origin/v3/internal/bufferpool"
	originlog "github.com/duanhf2012/origin/v3/log"
)

// remoteResolverFunc 让测试用真实 Runtime 接口表达不同发现结果。
type remoteResolverFunc func(
	nodeID string,
	serviceName string,
	contractID ContractID,
	fingerprint ContractFingerprint,
) (RemoteRoute, error)

func (resolver remoteResolverFunc) ResolveRemote(
	nodeID string,
	serviceName string,
	contractID ContractID,
	fingerprint ContractFingerprint,
) (RemoteRoute, error) {
	return resolver(nodeID, serviceName, contractID, fingerprint)
}

// TestRuntimeRemoteResolverPreservesDiscoveryErrors 验证 RPC 在接触连接表前保留目录错误语义。
func TestRuntimeRemoteResolverPreservesDiscoveryErrors(t *testing.T) {
	runtime, err := NewRuntime(
		"gateway-1",
		bufferpool.NewPool(bufferpool.Options{}),
		originlog.NewNop(),
	)
	if err != nil {
		t.Fatalf("NewRuntime() error = %v", err)
	}
	resolver := remoteResolverFunc(func(
		string,
		string,
		ContractID,
		ContractFingerprint,
	) (RemoteRoute, error) {
		return RemoteRoute{}, errs.ErrRPCNoRoute
	})
	if err := runtime.BindRemoteResolver(resolver); err != nil {
		t.Fatalf("BindRemoteResolver() error = %v", err)
	}

	_, err = runtime.resolveRemote(
		"game-1",
		"PlayerService",
		1,
		ContractFingerprint{1},
	)
	if !errors.Is(err, errs.ErrRPCNoRoute) {
		t.Fatalf("resolveRemote() error = %v", err)
	}
}

// TestRuntimeRemoteResolverReturnsSessionBoundRoute 验证发现解析结果携带连接所需会话。
func TestRuntimeRemoteResolverReturnsSessionBoundRoute(t *testing.T) {
	runtime, _ := NewRuntime(
		"gateway-1",
		bufferpool.NewPool(bufferpool.Options{}),
		originlog.NewNop(),
	)
	config := DefaultConfig()
	config.TCP.Listen = "127.0.0.1:20000"
	config.TCP.Advertise = "127.0.0.1:20000"
	if err := runtime.Configure(&config); err != nil {
		t.Fatalf("Configure() error = %v", err)
	}
	resolver := remoteResolverFunc(func(
		nodeID string,
		serviceName string,
		_ ContractID,
		_ ContractFingerprint,
	) (RemoteRoute, error) {
		return RemoteRoute{
			NodeID:    nodeID,
			SessionID: 7,
			Transport: TransportTCP,
			Address:   "127.0.0.1:20001",
		}, nil
	})
	if err := runtime.BindRemoteResolver(resolver); err != nil {
		t.Fatalf("BindRemoteResolver() error = %v", err)
	}
	route, err := runtime.resolveRemote(
		"game-1",
		"PlayerService",
		1,
		ContractFingerprint{1},
	)
	if err != nil {
		t.Fatalf("resolveRemote() error = %v", err)
	}
	if route.SessionID != 7 || route.NodeID != "game-1" {
		t.Fatalf("resolveRemote() route = %+v", route)
	}
}
