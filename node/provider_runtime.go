package node

import (
	"context"
	"errors"
	"fmt"
	"runtime/debug"
	"sync"
	"time"

	publicprovider "github.com/duanhf2012/origin/v3/discovery/provider"
	"github.com/duanhf2012/origin/v3/errs"
	internaldiscovery "github.com/duanhf2012/origin/v3/internal/discovery"
)

const (
	minDiscoveryTTL = 3 * time.Second
	maxDiscoveryTTL = 5 * time.Minute
)

// providerRuntime 把公开 Provider 契约接入当前 Node 的私有目录和健康状态。
//
// Provider 拥有后端；本对象拥有 TTL 旧快照、状态快照和公共 DTO 到内部 DTO 的边界。
type providerRuntime struct {
	node     *Node
	kind     string
	instance publicprovider.Provider

	// callMu 串行 Provider 生命周期方法；Host 回调不取得该锁，避免 Start 内回调死锁。
	callMu sync.Mutex
	// applyMu 串行 Provider 快照与 TTL 到期空快照，保证 Directory 只观察完整顺序。
	applyMu sync.Mutex
	mu      sync.Mutex

	ttl             time.Duration
	ttlConfigured   bool
	hostUsed        bool
	synchronized    bool
	publication     PublicationState
	state           DiscoveryState
	reconnects      uint64
	failures        uint32
	errorCode       errs.Code
	lastSnapshot    time.Time
	expiredSnapshot bool
	closed          bool

	wake   chan struct{}
	stop   chan struct{}
	done   chan struct{}
	start  sync.Once
	finish sync.Once
}

// newProviderRuntime 构造 Host 后调用一次 Factory；此阶段不建立连接或 goroutine。
func newProviderRuntime(
	node *Node,
	kind string,
	config publicprovider.Config,
	factory publicprovider.Factory,
) (*providerRuntime, error) {
	if node == nil || kind == "" || factory == nil {
		return nil, invalidConfig("Discovery Provider 构造参数无效")
	}
	runtime := &providerRuntime{
		node:        node,
		kind:        kind,
		publication: PublicationNotRequired,
		state:       DiscoveryStarting,
		wake:        make(chan struct{}, 1),
		stop:        make(chan struct{}),
		done:        make(chan struct{}),
	}
	host := publicprovider.NewHost(
		runtime.setTTL,
		runtime.replaceSnapshot,
		runtime.report,
	)
	instance, err := factory(publicprovider.Context{
		NodeID:    node.id,
		SessionID: node.sessionID,
		Config:    config,
		Host:      host,
		Logger:    node.logger,
	})
	if err != nil {
		return nil, err
	}
	if instance == nil {
		return nil, invalidConfig("Discovery Provider Factory 返回 nil")
	}
	runtime.instance = instance
	runtime.publishStatusLocked()
	return runtime, nil
}

// startProvider 启动公共 TTL 所有者并等待 Provider 完成首次权威同步。
func (runtime *providerRuntime) startProvider(ctx context.Context) error {
	if runtime == nil {
		return nil
	}
	runtime.start.Do(func() { go runtime.expiryLoop() })
	err := runtime.callProvider("Start", func() error {
		return runtime.instance.Start(ctx)
	})
	if err != nil {
		return err
	}
	runtime.mu.Lock()
	defer runtime.mu.Unlock()
	if !runtime.ttlConfigured {
		return invalidConfig("Discovery Provider 未在首次同步前调用 Host.SetTTL")
	}
	if !runtime.synchronized {
		return errs.NewMessage(
			errs.CodeDiscoveryUnavailable,
			"Discovery Provider Start 返回前未提交首次完整快照",
		)
	}
	if runtime.state != DiscoveryReady {
		return errs.NewMessage(
			errs.CodeDiscoveryUnavailable,
			"Discovery Provider Start 返回前未报告 Ready",
		)
	}
	return nil
}

// publish 把当前完整期望交给 Provider，并维护公共发布屏障。
func (runtime *providerRuntime) publish(
	ctx context.Context,
	node publicprovider.Node,
) error {
	if runtime == nil {
		return nil
	}
	runtime.mu.Lock()
	runtime.publication = PublicationPending
	runtime.publishStatusLocked()
	runtime.mu.Unlock()

	err := runtime.callProvider("Publish", func() error {
		return runtime.instance.Publish(ctx, node)
	})

	runtime.mu.Lock()
	if err == nil {
		runtime.publication = PublicationPublished
	} else {
		runtime.publication = PublicationPending
		runtime.errorCode = errs.CodeOf(err)
	}
	runtime.publishStatusLocked()
	runtime.mu.Unlock()
	runtime.node.refreshHealth()
	return err
}

// withdraw 幂等撤销当前 Session；失败保留 Pending 以暴露尚未确认的发布状态。
func (runtime *providerRuntime) withdraw(ctx context.Context) error {
	if runtime == nil {
		return nil
	}
	err := runtime.callProvider("Withdraw", func() error {
		return runtime.instance.Withdraw(ctx)
	})
	runtime.mu.Lock()
	if err == nil {
		runtime.publication = PublicationNotRequired
	} else {
		runtime.publication = PublicationPending
		runtime.errorCode = errs.CodeOf(err)
	}
	runtime.publishStatusLocked()
	runtime.mu.Unlock()
	return err
}

// closeProvider 发起取消、等待 Provider 资源并停止公共 TTL goroutine。
func (runtime *providerRuntime) closeProvider(ctx context.Context) error {
	if runtime == nil {
		return nil
	}
	runtime.start.Do(func() { go runtime.expiryLoop() })
	err := runtime.callProvider("Close", func() error {
		return runtime.instance.Close(ctx)
	})
	runtime.finish.Do(func() { close(runtime.stop) })
	<-runtime.done
	runtime.mu.Lock()
	runtime.closed = true
	runtime.state = DiscoveryStopped
	runtime.errorCode = 0
	runtime.publishStatusLocked()
	runtime.mu.Unlock()
	runtime.node.updateDiscoveryAvailable(false)
	return err
}

func (runtime *providerRuntime) callProvider(
	operation string,
	call func() error,
) (result error) {
	runtime.callMu.Lock()
	defer runtime.callMu.Unlock()
	defer func() {
		if recovered := recover(); recovered != nil {
			result = errs.NewMessage(
				errs.CodeDiscoveryUnavailable,
				fmt.Sprintf(
					"Discovery Provider %s panic: %v\n%s",
					operation,
					recovered,
					debug.Stack(),
				),
			)
		}
	}()
	return call()
}

// setTTL 冻结 Provider TTL；它必须先于其他 Host 能力调用。
func (runtime *providerRuntime) setTTL(ttl time.Duration) error {
	if ttl < minDiscoveryTTL || ttl > maxDiscoveryTTL {
		return invalidConfig("Discovery Provider TTL 必须位于 3s～5m")
	}
	runtime.mu.Lock()
	defer runtime.mu.Unlock()
	if runtime.ttlConfigured {
		if runtime.ttl == ttl {
			return nil
		}
		return invalidConfig("Discovery Provider 不能修改已经冻结的 TTL")
	}
	if runtime.hostUsed {
		return invalidConfig("Host.SetTTL 必须在首次快照或状态上报前调用")
	}
	runtime.ttl = ttl
	runtime.ttlConfigured = true
	return nil
}

// replaceSnapshot 校验并复制公开 DTO，然后原子应用到 M14 Directory。
func (runtime *providerRuntime) replaceSnapshot(snapshot publicprovider.Snapshot) error {
	normalized, err := publicprovider.NormalizeSnapshot(snapshot)
	if err != nil {
		return err
	}
	raw := internaldiscovery.RawSnapshot{
		Nodes: make([]internaldiscovery.RawNode, len(normalized.Nodes)),
	}
	for index, node := range normalized.Nodes {
		raw.Nodes[index] = rawProviderNode(node)
	}

	runtime.mu.Lock()
	if !runtime.ttlConfigured {
		runtime.mu.Unlock()
		return invalidConfig("Discovery Provider 必须先调用 Host.SetTTL")
	}
	if runtime.closed {
		runtime.mu.Unlock()
		return errs.ErrServiceStopped
	}
	runtime.hostUsed = true
	runtime.mu.Unlock()

	runtime.applyMu.Lock()
	err = runtime.node.discovery.apply(raw)
	runtime.applyMu.Unlock()
	if err != nil {
		return err
	}

	runtime.mu.Lock()
	runtime.synchronized = true
	runtime.lastSnapshot = time.Now()
	runtime.expiredSnapshot = false
	runtime.publishStatusLocked()
	runtime.mu.Unlock()
	runtime.signal()
	return nil
}

// report 更新紧凑状态，并在 Recovering 时启动一个 TTL 的旧快照倒计时。
func (runtime *providerRuntime) report(report publicprovider.Report) {
	runtime.mu.Lock()
	if !runtime.ttlConfigured || runtime.closed {
		runtime.mu.Unlock()
		return
	}
	runtime.hostUsed = true
	runtime.state = mapDiscoveryState(report.State)
	runtime.reconnects = report.Reconnects
	runtime.failures = report.ConsecutiveFailures
	runtime.errorCode = report.ErrorCode
	if runtime.state == DiscoveryReady && runtime.synchronized {
		// Ready 报告同时表示后端刚确认一项有效控制面活动，例如心跳、Watch progress 或
		// Lease KeepAlive；用它刷新旧快照 TTL，而不是只按很久以前的内容变化计时。
		runtime.lastSnapshot = time.Now()
	}
	available := runtime.state == DiscoveryReady && runtime.synchronized
	runtime.publishStatusLocked()
	runtime.mu.Unlock()
	runtime.node.updateDiscoveryAvailable(available)
	runtime.signal()
}

// expiryLoop 只在 Recovering 且最后权威快照已超过 TTL 时提交一次空远端快照。
func (runtime *providerRuntime) expiryLoop() {
	defer close(runtime.done)
	timer := time.NewTimer(time.Hour)
	if !timer.Stop() {
		<-timer.C
	}
	defer timer.Stop()
	for {
		runtime.mu.Lock()
		var wait time.Duration
		active := runtime.state == DiscoveryRecovering &&
			runtime.synchronized && !runtime.expiredSnapshot
		if active {
			deadline := runtime.lastSnapshot.Add(runtime.ttl)
			wait = time.Until(deadline)
		}
		runtime.mu.Unlock()

		if !active {
			select {
			case <-runtime.wake:
				continue
			case <-runtime.stop:
				return
			}
		}
		if wait > 0 {
			timer.Reset(wait)
			select {
			case <-timer.C:
			case <-runtime.wake:
				if !timer.Stop() {
					select {
					case <-timer.C:
					default:
					}
				}
				continue
			case <-runtime.stop:
				if !timer.Stop() {
					select {
					case <-timer.C:
					default:
					}
				}
				return
			}
		}

		runtime.mu.Lock()
		expire := runtime.state == DiscoveryRecovering &&
			runtime.synchronized && !runtime.expiredSnapshot &&
			!time.Now().Before(runtime.lastSnapshot.Add(runtime.ttl))
		if expire {
			runtime.expiredSnapshot = true
		}
		runtime.mu.Unlock()
		if expire {
			runtime.applyMu.Lock()
			err := runtime.node.discovery.apply(internaldiscovery.RawSnapshot{})
			runtime.applyMu.Unlock()
			if err != nil {
				runtime.node.logger.Error("清空过期发现快照失败")
			}
		}
	}
}

func (runtime *providerRuntime) signal() {
	select {
	case runtime.wake <- struct{}{}:
	default:
	}
}

func (runtime *providerRuntime) publishStatusLocked() {
	runtime.node.discoveryStatus.Store(&discoveryStatusSnapshot{
		value: DiscoveryStatus{
			Kind:                runtime.kind,
			State:               runtime.state,
			Synchronized:        runtime.synchronized,
			Publication:         runtime.publication,
			Reconnects:          runtime.reconnects,
			ConsecutiveFailures: runtime.failures,
			ErrorCode:           runtime.errorCode,
		},
	})
}

func rawProviderNode(node publicprovider.Node) internaldiscovery.RawNode {
	result := internaldiscovery.RawNode{
		NodeID:    node.NodeID,
		SessionID: node.SessionID,
		Labels:    node.Labels,
		Transport: internaldiscovery.Transport(node.Transport - 1),
		Address:   node.Address,
		Services:  make([]internaldiscovery.RawService, len(node.Services)),
	}
	for index, service := range node.Services {
		result.Services[index] = internaldiscovery.RawService{
			ServiceName:         service.ServiceName,
			State:               internaldiscovery.ServiceState(service.State),
			ContractID:          service.ContractID,
			ContractFingerprint: service.ContractFingerprint,
		}
	}
	return result
}

func mapDiscoveryState(state publicprovider.State) DiscoveryState {
	switch state {
	case publicprovider.StateReady:
		return DiscoveryReady
	case publicprovider.StateRecovering:
		return DiscoveryRecovering
	case publicprovider.StateStopped:
		return DiscoveryStopped
	default:
		return DiscoveryStarting
	}
}

// joinProviderClose 保留 Provider 与其他 Node 清理步骤的全部错误。
func joinProviderClose(primary error, runtime *providerRuntime, ctx context.Context) error {
	if runtime == nil {
		return primary
	}
	return errors.Join(primary, runtime.closeProvider(ctx))
}
