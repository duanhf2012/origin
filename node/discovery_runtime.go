package node

import (
	"context"
	"fmt"
	"reflect"
	"runtime/debug"
	"sort"
	"sync"
	"sync/atomic"

	publicdiscovery "github.com/duanhf2012/origin/v3/discovery"
	"github.com/duanhf2012/origin/v3/errs"
	internaldiscovery "github.com/duanhf2012/origin/v3/internal/discovery"
	originlog "github.com/duanhf2012/origin/v3/log"
	"github.com/duanhf2012/origin/v3/rpc"
	"github.com/duanhf2012/origin/v3/service"
)

// discoveryRuntime 串行接收原始完整快照，并持有当前 Node 的查询、等待和监听状态。
//
// mu 只覆盖发现写入、注册和等待冷路径。RPC 精确查询直接读取 directory 的原子快照，
// 不取得该锁。
type discoveryRuntime struct {
	node      *Node
	directory *internaldiscovery.Directory
	rpcView   atomic.Pointer[rpcDiscoverySnapshot]

	mu        sync.Mutex
	listeners map[*serviceEntry]map[publicdiscovery.ListenerID]*listenerRegistration
	waiters   map[uint64]*discoveryWaiter
	nextID    uint64
	closed    atomic.Bool
}

// rpcDiscoverySnapshot 只包装一个固定的内部目录指针，不复制候选或标签。
type rpcDiscoverySnapshot struct {
	snapshot *internaldiscovery.Snapshot
}

// listenerRegistration 保存一个监听器上次已经同步的不可变实例集合。
type listenerRegistration struct {
	id        publicdiscovery.ListenerID
	listener  publicdiscovery.IListener
	delivered map[internaldiscovery.InstanceKey]*internaldiscovery.Instance
}

// discoveryWaiter 是一次 AwaitService/AwaitNodeService 的无轮询等待项。
type discoveryWaiter struct {
	id          uint64
	owner       *serviceEntry
	nodeID      string
	serviceName string
	result      chan error
}

// discoveryDelivery 保存锁外执行一个监听器的一组稳定顺序回调。
type discoveryDelivery struct {
	owner        *serviceEntry
	registration *listenerRegistration
	actions      []discoveryAction
}

// discoveryAction 表示一次需要交付的公开事件方法和独立 Event 对象。
type discoveryAction struct {
	kind  internaldiscovery.ChangeKind
	event publicdiscovery.Event
}

// newDiscoveryRuntime 创建按当前 Node 冻结关注规则筛选的独立目录。
func newDiscoveryRuntime(
	nodeID string,
	filter internaldiscovery.Filter,
) (*discoveryRuntime, error) {
	directory, err := internaldiscovery.NewDirectory(nodeID, filter)
	if err != nil {
		return nil, err
	}
	result := &discoveryRuntime{
		directory: directory,
		listeners: make(
			map[*serviceEntry]map[publicdiscovery.ListenerID]*listenerRegistration,
		),
		waiters: make(map[uint64]*discoveryWaiter),
	}
	result.rpcView.Store(&rpcDiscoverySnapshot{snapshot: directory.Snapshot()})
	return result, nil
}

// bindNode 在 Node 主对象完成构造后建立只读反向所有权。
func (runtime *discoveryRuntime) bindNode(owner *Node) {
	runtime.node = owner
}

// apply 校验并发布完整原始快照，再唤醒等待者并提交每个 Service 的唯一同步意图。
func (runtime *discoveryRuntime) apply(
	snapshot internaldiscovery.RawSnapshot,
) error {
	if runtime == nil || runtime.closed.Load() {
		return errs.ErrServiceStopped
	}

	// Apply、监听注册和移除共享同一外层锁，首次补发与并发更新不会出现丢失窗口。
	runtime.mu.Lock()
	if runtime.closed.Load() {
		runtime.mu.Unlock()
		return errs.ErrServiceStopped
	}
	_, published, err := runtime.directory.ApplySnapshot(snapshot)
	if err != nil {
		runtime.mu.Unlock()
		return err
	}
	if !published {
		runtime.mu.Unlock()
		return nil
	}
	runtime.rpcView.Store(&rpcDiscoverySnapshot{
		snapshot: runtime.directory.Snapshot(),
	})

	// 已满足等待项在线性化锁内删除并发布成功，Context 取消路径据此裁决唯一结果。
	for id, waiter := range runtime.waiters {
		if runtime.matches(waiter.nodeID, waiter.serviceName) {
			delete(runtime.waiters, id)
			waiter.result <- nil
			close(waiter.result)
		}
	}
	owners := make([]*serviceEntry, 0, len(runtime.listeners))
	for owner, registrations := range runtime.listeners {
		if len(registrations) != 0 {
			owners = append(owners, owner)
		}
	}
	runtime.mu.Unlock()

	// 新快照已经原子可见后先非阻塞对齐 TCP 目标，再提交业务同步任务。
	runtime.reconcileTargets()
	if runtime.node != nil && runtime.node.rpcRuntime != nil {
		runtime.node.rpcRuntime.NotifyRoutesChanged()
	}
	for _, owner := range owners {
		runtime.markOwnerDirty(owner)
	}
	return nil
}

// Snapshot 实现 rpc.RemoteSnapshotResolver，并固定一次 Prepare 使用的完整目录版本。
func (runtime *discoveryRuntime) Snapshot() rpc.RemoteSnapshot {
	if runtime == nil {
		return nil
	}
	return runtime.rpcView.Load()
}

func (snapshot *rpcDiscoverySnapshot) Len(serviceName string) int {
	if snapshot == nil || snapshot.snapshot == nil {
		return 0
	}
	return len(snapshot.snapshot.List(serviceName))
}

func (snapshot *rpcDiscoverySnapshot) Candidate(
	serviceName string,
	index int,
) (rpc.RemoteCandidate, bool) {
	if snapshot == nil || snapshot.snapshot == nil || index < 0 {
		return rpc.RemoteCandidate{}, false
	}
	candidates := snapshot.snapshot.List(serviceName)
	if index >= len(candidates) {
		return rpc.RemoteCandidate{}, false
	}
	return mapRPCCandidate(candidates[index]), true
}

func (snapshot *rpcDiscoverySnapshot) Find(
	nodeID string,
	serviceName string,
) (rpc.RemoteCandidate, bool) {
	if snapshot == nil || snapshot.snapshot == nil {
		return rpc.RemoteCandidate{}, false
	}
	instance, exists := snapshot.snapshot.Find(nodeID, serviceName)
	if !exists {
		return rpc.RemoteCandidate{}, false
	}
	return mapRPCCandidate(instance), true
}

func mapRPCCandidate(instance *internaldiscovery.Instance) rpc.RemoteCandidate {
	if instance == nil {
		return rpc.RemoteCandidate{}
	}
	state := publicdiscovery.StateUnknown
	switch instance.State {
	case internaldiscovery.ServiceStateRunning:
		state = publicdiscovery.StateRunning
	case internaldiscovery.ServiceStateRetired:
		state = publicdiscovery.StateRetired
	}
	transport := ""
	switch instance.Transport {
	case internaldiscovery.TransportTCP:
		transport = rpc.TransportTCP
	case internaldiscovery.TransportNATS:
		transport = rpc.TransportNATS
	}
	return rpc.RemoteCandidate{
		NodeID:      instance.NodeID,
		SessionID:   instance.SessionID,
		ServiceName: instance.ServiceName,
		State:       state,
		Labels:      instance.Labels,
		Transport:   transport,
		Address:     instance.Address,
		ContractID:  rpc.ContractID(instance.ContractID),
		Fingerprint: rpc.ContractFingerprint(instance.ContractFingerprint),
	}
}

// reconcileTargets 把目录去重后的 Node 会话集合交给 TCP Runtime，不回滚发现事实。
func (runtime *discoveryRuntime) reconcileTargets() {
	if runtime.node == nil || runtime.node.rpcRuntime == nil {
		return
	}
	source := runtime.directory.Targets()
	targets := make([]rpc.ConnectionTarget, len(source))
	for index, target := range source {
		targets[index] = rpc.ConnectionTarget{
			NodeID:    target.NodeID,
			SessionID: target.SessionID,
			Address:   target.Address,
		}
	}
	if err := runtime.node.rpcRuntime.ReconcileTargets(targets); err != nil &&
		!errs.IsCode(err, errs.CodeServiceStopped) {
		runtime.node.logger.Warn(
			"reconcile RPC discovery targets failed",
			originlog.Err(err),
		)
	}
}

// ResolveRemote 实现 rpc.RemoteResolver，并在一次无锁快照查询中校验可见契约和 Transport。
func (runtime *discoveryRuntime) ResolveRemote(
	nodeID string,
	serviceName string,
	contractID rpc.ContractID,
	fingerprint rpc.ContractFingerprint,
) (rpc.RemoteRoute, error) {
	if runtime == nil || runtime.closed.Load() {
		return rpc.RemoteRoute{}, errs.ErrServiceStopped
	}
	instance, exists := runtime.directory.Find(nodeID, serviceName)
	if !exists {
		return rpc.RemoteRoute{}, errs.ErrRPCNoRoute
	}
	if instance.ContractID == 0 ||
		instance.ContractID != uint64(contractID) ||
		instance.ContractFingerprint != [32]byte(fingerprint) {
		return rpc.RemoteRoute{}, errs.ErrRPCContractMismatch
	}
	transport := ""
	switch instance.Transport {
	case internaldiscovery.TransportTCP:
		if instance.Address == "" {
			return rpc.RemoteRoute{}, errs.ErrTransportUnavailable
		}
		transport = rpc.TransportTCP
	case internaldiscovery.TransportNATS:
		if instance.Address != "" {
			return rpc.RemoteRoute{}, errs.ErrTransportUnavailable
		}
		transport = rpc.TransportNATS
	default:
		return rpc.RemoteRoute{}, errs.ErrTransportUnavailable
	}
	return rpc.RemoteRoute{
		NodeID:    instance.NodeID,
		SessionID: instance.SessionID,
		Transport: transport,
		Address:   instance.Address,
	}, nil
}

// findPublic 精确查询并复制一份业务可以独立持有的公开实例。
func (runtime *discoveryRuntime) findPublic(
	nodeID string,
	serviceName string,
) (publicdiscovery.Instance, bool) {
	if runtime == nil || runtime.closed.Load() {
		return publicdiscovery.Instance{}, false
	}
	instance, exists := runtime.directory.Find(nodeID, serviceName)
	if !exists {
		return publicdiscovery.Instance{}, false
	}
	return copyPublicInstance(instance), true
}

// listPublic 复制指定 ServiceName 的稳定候选及每个候选的标签 Map。
func (runtime *discoveryRuntime) listPublic(
	serviceName string,
) []publicdiscovery.Instance {
	if runtime == nil || runtime.closed.Load() {
		return nil
	}
	instances := runtime.directory.List(serviceName)
	if len(instances) == 0 {
		return nil
	}
	result := make([]publicdiscovery.Instance, len(instances))
	for index, instance := range instances {
		result[index] = copyPublicInstance(instance)
	}
	return result
}

// await 等待当前或后续完整快照出现指定远端 Service，不创建轮询 Timer。
func (runtime *discoveryRuntime) await(
	ctx context.Context,
	owner *serviceEntry,
	nodeID string,
	serviceName string,
) error {
	if runtime == nil || ctx == nil || owner == nil || serviceName == "" {
		return errs.ErrInvalidArgument
	}
	if runtime.closed.Load() {
		return errs.ErrServiceStopped
	}

	// 同一把锁内完成再次查询和等待登记，避免快照恰好在两步之间更新而永久漏唤醒。
	runtime.mu.Lock()
	if runtime.closed.Load() {
		runtime.mu.Unlock()
		return errs.ErrServiceStopped
	}
	if runtime.matches(nodeID, serviceName) {
		runtime.mu.Unlock()
		return nil
	}
	runtime.nextID++
	if runtime.nextID == 0 {
		runtime.mu.Unlock()
		return errs.ErrInternal
	}
	waiter := &discoveryWaiter{
		id:          runtime.nextID,
		owner:       owner,
		nodeID:      nodeID,
		serviceName: serviceName,
		result:      make(chan error, 1),
	}
	runtime.waiters[waiter.id] = waiter
	runtime.mu.Unlock()

	select {
	case result := <-waiter.result:
		return result
	case <-ctx.Done():
		// 若等待项仍存在，本取消取得线性化所有权；否则 Apply/removeOwner 已经发布终态。
		runtime.mu.Lock()
		if _, exists := runtime.waiters[waiter.id]; exists {
			delete(runtime.waiters, waiter.id)
			runtime.mu.Unlock()
			return errs.Wrap(errs.CodeOf(ctx.Err()), ctx.Err())
		}
		runtime.mu.Unlock()
		return <-waiter.result
	}
}

// addListener 原子登记监听器，并把空已交付状态同步到当前最新快照。
func (runtime *discoveryRuntime) addListener(
	owner *serviceEntry,
	listener publicdiscovery.IListener,
) (publicdiscovery.ListenerID, error) {
	if runtime == nil || owner == nil || isNilListener(listener) {
		return 0, errs.ErrInvalidArgument
	}
	runtime.mu.Lock()
	if runtime.closed.Load() {
		runtime.mu.Unlock()
		return 0, errs.ErrServiceStopped
	}
	// 状态检查与登记必须位于和 removeOwner 相同的线性化锁内：若 Stop 已经把
	// Service 切到 Stopping，不能在自动清理之后又留下一个永远不会交付的监听器。
	state := owner.loadState().State
	switch state {
	case service.StateInitializing, service.StateInitialized,
		service.StateStarting, service.StateRunning, service.StateRetired:
		// Retired 仍是可执行状态；Runner 未激活时只保留同步意图。
	default:
		runtime.mu.Unlock()
		if state == service.StateStopping {
			return 0, errs.ErrServiceStopping
		}
		return 0, errs.ErrServiceStopped
	}
	runtime.nextID++
	if runtime.nextID == 0 {
		runtime.mu.Unlock()
		return 0, errs.ErrInternal
	}
	id := publicdiscovery.ListenerID(runtime.nextID)
	registrations := runtime.listeners[owner]
	if registrations == nil {
		registrations = make(
			map[publicdiscovery.ListenerID]*listenerRegistration,
		)
		runtime.listeners[owner] = registrations
	}
	registrations[id] = &listenerRegistration{
		id:        id,
		listener:  listener,
		delivered: make(map[internaldiscovery.InstanceKey]*internaldiscovery.Instance),
	}
	runtime.mu.Unlock()

	// Scheduler 尚未 Prepare 时 Mark 会返回 NotReady；登记状态由 Start 在 Prepare 后补交。
	runtime.markOwnerDirty(owner)
	return id, nil
}

// removeListener 删除精确 ID，成功后清零业务持有值。
func (runtime *discoveryRuntime) removeListener(
	owner *serviceEntry,
	id *publicdiscovery.ListenerID,
) bool {
	if runtime == nil || owner == nil || id == nil || *id == 0 {
		return false
	}
	runtime.mu.Lock()
	registrations := runtime.listeners[owner]
	if _, exists := registrations[*id]; !exists {
		runtime.mu.Unlock()
		return false
	}
	delete(registrations, *id)
	if len(registrations) == 0 {
		delete(runtime.listeners, owner)
	}
	runtime.mu.Unlock()
	*id = 0
	return true
}

// activateOwner 在 Scheduler Prepare 后兑现 OnInit 阶段保留的首次同步意图。
func (runtime *discoveryRuntime) activateOwner(owner *serviceEntry) {
	if runtime == nil || owner == nil {
		return
	}
	runtime.mu.Lock()
	hasListeners := len(runtime.listeners[owner]) != 0
	runtime.mu.Unlock()
	if hasListeners {
		runtime.markOwnerDirty(owner)
	}
}

// removeOwner 在 Service 停止前取消等待并删除其全部监听器。
func (runtime *discoveryRuntime) removeOwner(owner *serviceEntry, cause error) {
	if runtime == nil || owner == nil {
		return
	}
	runtime.mu.Lock()
	delete(runtime.listeners, owner)
	for id, waiter := range runtime.waiters {
		if waiter.owner == owner {
			delete(runtime.waiters, id)
			waiter.result <- cause
			close(waiter.result)
		}
	}
	runtime.mu.Unlock()
}

// close 使目录业务外观失效，并完成尚未结束的全部等待项。
func (runtime *discoveryRuntime) close() {
	if runtime == nil || !runtime.closed.CompareAndSwap(false, true) {
		return
	}
	runtime.mu.Lock()
	for id, waiter := range runtime.waiters {
		delete(runtime.waiters, id)
		waiter.result <- errs.ErrServiceStopped
		close(waiter.result)
	}
	clear(runtime.listeners)
	runtime.mu.Unlock()
}

// markOwnerDirty 把一次目录变化合并到所属 Service 的唯一 FIFO 发现任务。
func (runtime *discoveryRuntime) markOwnerDirty(owner *serviceEntry) {
	if owner == nil || owner.discoveryRun == nil {
		return
	}
	err := service.MarkDiscoveryDirty(owner.instance, owner.discoveryRun)
	if err == nil ||
		errs.IsCode(err, errs.CodeServiceNotReady) ||
		errs.IsCode(err, errs.CodeServiceStopping) ||
		errs.IsCode(err, errs.CodeServiceStopped) {
		return
	}
	owner.logger.Error("mark discovery task failed", originlog.Err(err))
}

// deliver 在所属 Service Runner 中把每个监听器从已交付状态同步到当前最新快照。
func (runtime *discoveryRuntime) deliver(
	ctx context.Context,
	owner *serviceEntry,
) {
	if runtime == nil || owner == nil || runtime.closed.Load() {
		return
	}

	// 当前实例 Map 只引用不可变对象，并可由本轮多个监听器共享为新的已交付基线。
	runtime.mu.Lock()
	current := make(
		map[internaldiscovery.InstanceKey]*internaldiscovery.Instance,
		runtime.directory.Stats().Services,
	)
	for _, instance := range runtime.directory.All() {
		current[internaldiscovery.InstanceKey{
			NodeID:      instance.NodeID,
			ServiceName: instance.ServiceName,
		}] = instance
	}
	registrations := runtime.listeners[owner]
	deliveries := make([]discoveryDelivery, 0, len(registrations))
	for _, registration := range registrations {
		actions := buildDiscoveryActions(registration.delivered, current)
		registration.delivered = current
		if len(actions) != 0 {
			deliveries = append(deliveries, discoveryDelivery{
				owner:        owner,
				registration: registration,
				actions:      actions,
			})
		}
	}
	runtime.mu.Unlock()

	// 每个监听器和每个回调独立隔离 panic；一个错误业务监听器不能阻断其他状态同步。
	for _, delivery := range deliveries {
		for _, action := range delivery.actions {
			if !runtime.listenerActive(owner, delivery.registration) {
				break
			}
			runtime.callListener(ctx, delivery.registration.listener, action, owner)
		}
	}
}

// listenerActive 阻止已经移除的监听器继续收到同一同步任务尚未开始的回调。
func (runtime *discoveryRuntime) listenerActive(
	owner *serviceEntry,
	registration *listenerRegistration,
) bool {
	runtime.mu.Lock()
	defer runtime.mu.Unlock()
	return runtime.listeners[owner][registration.id] == registration
}

// callListener 调用一个明确事件方法，并在所属 Service 日志中保留原始 panic 堆栈。
func (runtime *discoveryRuntime) callListener(
	ctx context.Context,
	listener publicdiscovery.IListener,
	action discoveryAction,
	owner *serviceEntry,
) {
	defer func() {
		if value := recover(); value != nil {
			owner.logger.ErrorStack(
				"discovery listener panic",
				originlog.String("panic", fmt.Sprint(value)),
				originlog.String("panic_stack", string(debug.Stack())),
			)
		}
	}()
	switch action.kind {
	case internaldiscovery.ChangeDiscovered:
		listener.OnDiscovered(ctx, action.event)
	case internaldiscovery.ChangeStateChanged:
		listener.OnStateChanged(ctx, action.event)
	case internaldiscovery.ChangeLost:
		listener.OnLost(ctx, action.event)
	}
}

// buildDiscoveryActions 计算单个监听器从旧状态到当前状态的稳定净变化。
func buildDiscoveryActions(
	delivered map[internaldiscovery.InstanceKey]*internaldiscovery.Instance,
	current map[internaldiscovery.InstanceKey]*internaldiscovery.Instance,
) []discoveryAction {
	// 先按 Node 聚合新旧状态。合法目录保证同一 Node 的全部 Service 使用相同 SessionID；
	// 以 Node 为单位处理可以同时满足批量事件和“旧会话全部 Lost 后新会话才 Discovered”。
	nodes := make(map[string]struct{})
	deliveredByNode := make(map[string][]*internaldiscovery.Instance)
	currentByNode := make(map[string][]*internaldiscovery.Instance)
	for _, instance := range delivered {
		nodes[instance.NodeID] = struct{}{}
		deliveredByNode[instance.NodeID] = append(
			deliveredByNode[instance.NodeID],
			instance,
		)
	}
	for _, instance := range current {
		nodes[instance.NodeID] = struct{}{}
		currentByNode[instance.NodeID] = append(
			currentByNode[instance.NodeID],
			instance,
		)
	}
	nodeIDs := make([]string, 0, len(nodes))
	for nodeID := range nodes {
		nodeIDs = append(nodeIDs, nodeID)
	}
	sort.Strings(nodeIDs)

	actions := make([]discoveryAction, 0)
	for _, nodeID := range nodeIDs {
		beforeNode := deliveredByNode[nodeID]
		afterNode := currentByNode[nodeID]
		sortInstancesByService(beforeNode)
		sortInstancesByService(afterNode)

		// SessionID 是 Node 进程会话，不是单个 Service 会话。替换时旧 Node 的完整可见状态
		// 必须先作为一个或多个批量 Lost 交付，再整体发布新 Node，不能按 Service 交错。
		if len(beforeNode) != 0 &&
			len(afterNode) != 0 &&
			beforeNode[0].SessionID != afterNode[0].SessionID {
			for _, before := range beforeNode {
				actions = appendDiscoveryAction(
					actions,
					internaldiscovery.ChangeLost,
					before,
				)
			}
			for _, after := range afterNode {
				actions = appendDiscoveryAction(
					actions,
					internaldiscovery.ChangeDiscovered,
					after,
				)
			}
			continue
		}

		// 同一会话内只同步真正的净变化；标签、地址和契约更新不伪装成业务状态事件。
		for _, after := range afterNode {
			key := internaldiscovery.InstanceKey{
				NodeID:      after.NodeID,
				ServiceName: after.ServiceName,
			}
			before := delivered[key]
			switch {
			case before == nil:
				actions = appendDiscoveryAction(
					actions,
					internaldiscovery.ChangeDiscovered,
					after,
				)
			case before.State != after.State:
				actions = appendDiscoveryAction(
					actions,
					internaldiscovery.ChangeStateChanged,
					after,
				)
			}
		}
		for _, before := range beforeNode {
			key := internaldiscovery.InstanceKey{
				NodeID:      before.NodeID,
				ServiceName: before.ServiceName,
			}
			if _, exists := current[key]; !exists {
				actions = appendDiscoveryAction(
					actions,
					internaldiscovery.ChangeLost,
					before,
				)
			}
		}
	}
	return actions
}

// sortInstancesByService 为同一 Node 的批量事件建立稳定 ServiceName 顺序。
func sortInstancesByService(instances []*internaldiscovery.Instance) {
	sort.Slice(instances, func(left, right int) bool {
		return instances[left].ServiceName < instances[right].ServiceName
	})
}

// appendDiscoveryAction 合并相邻同类同 Node 变化，保持 Session 替换的 Lost/Discovered 顺序。
func appendDiscoveryAction(
	actions []discoveryAction,
	kind internaldiscovery.ChangeKind,
	instance *internaldiscovery.Instance,
) []discoveryAction {
	serviceState := publicState(instance.State)
	if len(actions) > 0 {
		last := &actions[len(actions)-1]
		if last.kind == kind && last.event.NodeID == instance.NodeID {
			last.event.Services = append(last.event.Services, publicdiscovery.Service{
				ServiceName: instance.ServiceName,
				State:       serviceState,
			})
			return actions
		}
	}
	return append(actions, discoveryAction{
		kind: kind,
		event: publicdiscovery.Event{
			NodeID: instance.NodeID,
			Services: []publicdiscovery.Service{{
				ServiceName: instance.ServiceName,
				State:       serviceState,
			}},
		},
	})
}

// matches 报告当前原子快照是否包含等待目标；空 NodeID 表示任意远端 Node。
func (runtime *discoveryRuntime) matches(nodeID, serviceName string) bool {
	if nodeID != "" {
		_, exists := runtime.directory.Find(nodeID, serviceName)
		return exists
	}
	return len(runtime.directory.List(serviceName)) != 0
}

// copyPublicInstance 在业务所有权边界复制 Instance 和 Labels。
func copyPublicInstance(
	source *internaldiscovery.Instance,
) publicdiscovery.Instance {
	return publicdiscovery.Instance{
		NodeID:      source.NodeID,
		SessionID:   source.SessionID,
		ServiceName: source.ServiceName,
		State:       publicState(source.State),
		Labels:      cloneLabels(source.Labels),
	}
}

// publicState 把内部紧凑状态映射为业务只读枚举。
func publicState(state internaldiscovery.ServiceState) publicdiscovery.State {
	switch state {
	case internaldiscovery.ServiceStateRunning:
		return publicdiscovery.StateRunning
	case internaldiscovery.ServiceStateRetired:
		return publicdiscovery.StateRetired
	default:
		return publicdiscovery.StateUnknown
	}
}

// isNilListener 识别接口中保存的有类型 nil 监听器指针。
func isNilListener(listener publicdiscovery.IListener) bool {
	if listener == nil {
		return true
	}
	value := reflect.ValueOf(listener)
	switch value.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map,
		reflect.Pointer, reflect.Slice:
		return value.IsNil()
	default:
		return false
	}
}

// FindDiscoveredService 实现 service 的可选发现桥，并返回业务独立副本。
func (runtime *serviceRuntime) FindDiscoveredService(
	nodeID string,
	serviceName string,
) (publicdiscovery.Instance, bool) {
	return runtime.node.discovery.findPublic(nodeID, serviceName)
}

// ListDiscoveredServices 实现 service 的可选发现桥，并按 NodeID 返回稳定副本。
func (runtime *serviceRuntime) ListDiscoveredServices(
	serviceName string,
) []publicdiscovery.Instance {
	return runtime.node.discovery.listPublic(serviceName)
}

// AwaitDiscoveredService 实现 service 的无轮询发现等待桥。
func (runtime *serviceRuntime) AwaitDiscoveredService(
	ctx context.Context,
	nodeID string,
	serviceName string,
) error {
	return runtime.node.discovery.await(
		ctx,
		runtime.entry,
		nodeID,
		serviceName,
	)
}

// AddDiscoveryListener 实现 service 的多监听器登记桥。
func (runtime *serviceRuntime) AddDiscoveryListener(
	listener publicdiscovery.IListener,
) (publicdiscovery.ListenerID, error) {
	return runtime.node.discovery.addListener(runtime.entry, listener)
}

// RemoveDiscoveryListener 实现 service 的精确监听取消桥。
func (runtime *serviceRuntime) RemoveDiscoveryListener(
	id *publicdiscovery.ListenerID,
) bool {
	return runtime.node.discovery.removeListener(runtime.entry, id)
}
