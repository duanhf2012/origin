// Package providertest 提供所有服务发现 Provider 可以复用的公共一致性测试。
package providertest

import (
	"context"
	"fmt"
	"reflect"
	"sync"
	"testing"
	"time"

	publicprovider "github.com/duanhf2012/origin/v3/discovery/provider"
	"github.com/duanhf2012/origin/v3/errs"
	originlog "github.com/duanhf2012/origin/v3/log"
)

// Harness 描述一套已经启动且可由两个 Provider 共享的后端。
type Harness struct {
	Factory publicprovider.Factory
	Config  publicprovider.Config
	Timeout time.Duration
}

type recorder struct {
	mu       sync.Mutex
	ttl      time.Duration
	snapshot publicprovider.Snapshot
	closed   bool
	changed  chan struct{}
}

// Run 验证首次空快照、完整记录传播、更新、幂等操作、身份边界和关闭失效。
func Run(t *testing.T, harness Harness) {
	t.Helper()
	if harness.Factory == nil {
		t.Fatal("providertest: Factory 不能为空")
	}
	if harness.Timeout <= 0 {
		harness.Timeout = 10 * time.Second
	}
	publisherRecorder := newRecorder()
	observerRecorder := newRecorder()
	publisher := newProvider(t, harness, "publisher-1", 101, publisherRecorder)
	observer := newProvider(t, harness, "observer-1", 202, observerRecorder)

	startProvider(t, publisher, harness.Timeout)
	startProvider(t, observer, harness.Timeout)
	t.Cleanup(func() {
		_ = observer.Close(context.Background())
		_ = publisher.Close(context.Background())
	})

	node := publicprovider.Node{
		NodeID:    "publisher-1",
		SessionID: 101,
		Labels:    map[string]string{"game_type": "battle"},
		Transport: publicprovider.TransportNATS,
		Services: []publicprovider.Service{{
			ServiceName:         "ProviderContractService",
			State:               publicprovider.ServiceStateRunning,
			ContractID:          101,
			ContractFingerprint: [32]byte{1},
		}},
	}
	operationCtx, cancel := context.WithTimeout(context.Background(), harness.Timeout)
	if err := publisher.Publish(operationCtx, node); err != nil {
		cancel()
		t.Fatalf("providertest: Publish() error = %v", err)
	}
	cancel()
	observerRecorder.awaitNode(t, harness.Timeout, node)

	// Provider 不能允许调用者冒充其他 Context 身份发布记录。
	wrongIdentity := node
	wrongIdentity.NodeID = "intruder-1"
	operationCtx, cancel = context.WithTimeout(context.Background(), harness.Timeout)
	err := publisher.Publish(operationCtx, wrongIdentity)
	cancel()
	if !errs.IsCode(err, errs.CodeDiscoverySnapshotInvalid) {
		t.Fatalf("providertest: 身份不一致 Publish() error = %v", err)
	}

	operationCtx, cancel = context.WithTimeout(context.Background(), harness.Timeout)
	if err := publisher.Publish(operationCtx, node); err != nil {
		cancel()
		t.Fatalf("providertest: 重复 Publish() error = %v", err)
	}
	cancel()

	// 同一 Session 的完整更新必须同时传播标签和 Service 状态，不能只更新存在性。
	updated := node
	updated.Labels = map[string]string{
		"game_type": "battle",
		"game_mode": "ranked",
	}
	updated.Services = append([]publicprovider.Service(nil), node.Services...)
	updated.Services[0].State = publicprovider.ServiceStateRetired
	operationCtx, cancel = context.WithTimeout(context.Background(), harness.Timeout)
	if err := publisher.Publish(operationCtx, updated); err != nil {
		cancel()
		t.Fatalf("providertest: 更新 Publish() error = %v", err)
	}
	cancel()
	observerRecorder.awaitNode(t, harness.Timeout, updated)

	operationCtx, cancel = context.WithTimeout(context.Background(), harness.Timeout)
	if err := publisher.Withdraw(operationCtx); err != nil {
		cancel()
		t.Fatalf("providertest: Withdraw() error = %v", err)
	}
	cancel()
	observerRecorder.await(t, harness.Timeout, "publisher-1", false)

	operationCtx, cancel = context.WithTimeout(context.Background(), harness.Timeout)
	if err := publisher.Withdraw(operationCtx); err != nil {
		cancel()
		t.Fatalf("providertest: 重复 Withdraw() error = %v", err)
	}
	cancel()

	// 未显式 Withdraw 的异常关闭也必须最终让其他观察者移除该 Session。
	operationCtx, cancel = context.WithTimeout(context.Background(), harness.Timeout)
	if err := publisher.Publish(operationCtx, updated); err != nil {
		cancel()
		t.Fatalf("providertest: Close 前 Publish() error = %v", err)
	}
	cancel()
	observerRecorder.awaitNode(t, harness.Timeout, updated)
	if err := publisher.Close(context.Background()); err != nil {
		t.Fatalf("providertest: publisher Close() error = %v", err)
	}
	observerRecorder.await(t, harness.Timeout, "publisher-1", false)

	if err := observer.Close(context.Background()); err != nil {
		t.Fatalf("providertest: observer Close() error = %v", err)
	}
	observerRecorder.markClosed()
	time.Sleep(20 * time.Millisecond)
}

func (recorder *recorder) awaitNode(
	t *testing.T,
	timeout time.Duration,
	expected publicprovider.Node,
) {
	t.Helper()
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	for {
		recorder.mu.Lock()
		var current *publicprovider.Node
		for index := range recorder.snapshot.Nodes {
			if recorder.snapshot.Nodes[index].NodeID == expected.NodeID {
				copyNode := recorder.snapshot.Nodes[index]
				current = &copyNode
				break
			}
		}
		recorder.mu.Unlock()
		if current != nil && reflect.DeepEqual(*current, expected) {
			return
		}
		select {
		case <-recorder.changed:
		case <-timer.C:
			t.Fatalf(
				"providertest: 等待完整 Node 超时，got=%+v want=%+v",
				current,
				expected,
			)
		}
	}
}

func newProvider(
	t *testing.T,
	harness Harness,
	nodeID string,
	sessionID uint64,
	recorder *recorder,
) publicprovider.Provider {
	t.Helper()
	instance, err := harness.Factory(publicprovider.Context{
		NodeID:    nodeID,
		SessionID: sessionID,
		Config:    harness.Config,
		Host:      recorder.host(t),
		Logger:    originlog.NewNop(),
	})
	if err != nil {
		t.Fatalf("providertest: Factory(%s) error = %v", nodeID, err)
	}
	if instance == nil {
		t.Fatalf("providertest: Factory(%s) 返回 nil", nodeID)
	}
	return instance
}

func startProvider(t *testing.T, instance publicprovider.Provider, timeout time.Duration) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	if err := instance.Start(ctx); err != nil {
		t.Fatalf("providertest: Start() error = %v", err)
	}
}

func newRecorder() *recorder {
	return &recorder{changed: make(chan struct{}, 32)}
}

func (recorder *recorder) host(t *testing.T) publicprovider.Host {
	return publicprovider.NewHost(
		func(ttl time.Duration) error {
			recorder.mu.Lock()
			defer recorder.mu.Unlock()
			if recorder.closed {
				t.Errorf("providertest: Close 后调用 SetTTL")
				return errs.ErrServiceStopped
			}
			if recorder.ttl != 0 && recorder.ttl != ttl {
				return errs.ErrInvalidConfig
			}
			recorder.ttl = ttl
			return nil
		},
		func(snapshot publicprovider.Snapshot) error {
			normalized, err := publicprovider.NormalizeSnapshot(snapshot)
			if err != nil {
				return err
			}
			recorder.mu.Lock()
			if recorder.closed {
				recorder.mu.Unlock()
				t.Errorf("providertest: Close 后提交 Snapshot")
				return errs.ErrServiceStopped
			}
			recorder.snapshot = normalized
			recorder.mu.Unlock()
			select {
			case recorder.changed <- struct{}{}:
			default:
			}
			return nil
		},
		func(publicprovider.Report) {
			recorder.mu.Lock()
			closed := recorder.closed
			recorder.mu.Unlock()
			if closed {
				t.Errorf("providertest: Close 后提交 Report")
			}
		},
	)
}

func (recorder *recorder) await(
	t *testing.T,
	timeout time.Duration,
	nodeID string,
	present bool,
) {
	t.Helper()
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	for {
		recorder.mu.Lock()
		found := false
		for _, node := range recorder.snapshot.Nodes {
			if node.NodeID == nodeID {
				found = true
				break
			}
		}
		recorder.mu.Unlock()
		if found == present {
			return
		}
		select {
		case <-recorder.changed:
		case <-timer.C:
			t.Fatal(fmt.Sprintf(
				"providertest: 等待 Node %q present=%v 超时",
				nodeID,
				present,
			))
		}
	}
}

func (recorder *recorder) markClosed() {
	recorder.mu.Lock()
	recorder.closed = true
	recorder.mu.Unlock()
}
