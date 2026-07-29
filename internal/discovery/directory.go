package discovery

import (
	"fmt"
	"sort"
	"sync"
	"sync/atomic"

	"github.com/duanhf2012/origin/v3/errs"
)

// InstanceKey 是 RPC 热路径直接使用的无分配精确索引键。
type InstanceKey struct {
	NodeID      string
	ServiceName string
}

// Instance 是目录自有、发布后绝不修改的远端 Service 记录。
//
// 同一 Snapshot 的多个索引可以共享该指针。指针不能返回给业务代码，也不能进入对象池。
type Instance struct {
	NodeID              string
	SessionID           uint64
	ServiceName         string
	State               ServiceState
	Labels              map[string]string
	Transport           Transport
	Address             string
	ContractID          uint64
	ContractFingerprint [32]byte
}

// Target 是 TCP 连接对账需要的最小 Node 会话记录。
type Target struct {
	NodeID    string
	SessionID uint64
	Address   string
}

// Snapshot 是一次原子发布的不可变可见目录。
type Snapshot struct {
	version      uint64
	byInstance   map[InstanceKey]*Instance
	byService    map[string][]*Instance
	instances    []*Instance
	targets      []Target
	nodeCount    int
	serviceCount int
}

// Version 返回该不可变快照的内部单调版本。
func (snapshot *Snapshot) Version() uint64 {
	if snapshot == nil {
		return 0
	}
	return snapshot.version
}

// ChangeKind 区分监听交付和连接对账需要观察的目录变化。
type ChangeKind uint8

const (
	// ChangeDiscovered 表示原来不可见的实例现在可见。
	ChangeDiscovered ChangeKind = iota + 1
	// ChangeStateChanged 表示同一会话的 Running/Retired 状态改变。
	ChangeStateChanged
	// ChangeUpdated 表示同一会话的标签、地址或契约元数据改变。
	ChangeUpdated
	// ChangeLost 表示原来可见的实例不再可见。
	ChangeLost
)

// Change 保存一次变化前后的内部不可变记录。
type Change struct {
	Kind   ChangeKind
	Before *Instance
	After  *Instance
}

// ChangeSet 是一次真实目录发布形成的稳定顺序变化集合。
type ChangeSet struct {
	Version uint64
	Entries []Change
}

// Stats 是目录内部诊断和测试使用的紧凑统计。
type Stats struct {
	Version  uint64
	Nodes    int
	Services int
}

// Directory 持有一个 Node 的当前可见目录。
//
// applyMu 只串行化发现更新冷路径；Find、List 和 RPC 解析只执行一次原子指针读取。Node
// 还会在外层用同一把发现运行时锁协调监听器注册，业务热查询不会触碰这些锁。
type Directory struct {
	localNodeID string
	filter      Filter
	applyMu     sync.Mutex
	current     atomic.Pointer[Snapshot]
}

// NewDirectory 创建包含版本零空快照的 Node 私有目录。
func NewDirectory(localNodeID string, filter Filter) (*Directory, error) {
	// 本地 NodeID 是过滤自身和阻止自等待的稳定身份，不能为空。
	if localNodeID == "" {
		return nil, errs.NewMessage(errs.CodeInvalidArgument, "本地 NodeID 不能为空")
	}
	empty := &Snapshot{
		byInstance: make(map[InstanceKey]*Instance),
		byService:  make(map[string][]*Instance),
	}
	result := &Directory{
		localNodeID: localNodeID,
		filter:      filter,
	}
	result.current.Store(empty)
	return result, nil
}

// ApplySnapshot 完整校验、复制、筛选并原子发布一份新的可见目录。
func (directory *Directory) ApplySnapshot(
	raw RawSnapshot,
) (ChangeSet, bool, error) {
	if directory == nil {
		return ChangeSet{}, false, errs.ErrInvalidArgument
	}

	// 完整快照更新必须串行，以保证版本、Diff 和原子 Store 来自同一个旧状态。
	directory.applyMu.Lock()
	defer directory.applyMu.Unlock()

	old := directory.current.Load()
	candidates, nodeCount, err := directory.normalize(raw)
	if err != nil {
		return ChangeSet{Version: old.version}, false, err
	}

	// 先按稳定键比较并复用完全未变化的旧 Instance 指针。
	keys := make([]InstanceKey, 0, len(candidates))
	for key, candidate := range candidates {
		keys = append(keys, key)
		if previous := old.byInstance[key]; previous != nil &&
			instanceEqual(previous, candidate) {
			candidates[key] = previous
		}
	}
	sort.Slice(keys, func(left, right int) bool {
		if keys[left].NodeID != keys[right].NodeID {
			return keys[left].NodeID < keys[right].NodeID
		}
		return keys[left].ServiceName < keys[right].ServiceName
	})

	// 按逻辑位置生成变化；会话替换在同一个键内固定先 Lost、再 Discovered。
	changes := make([]Change, 0)
	for _, key := range keys {
		current := candidates[key]
		previous := old.byInstance[key]
		switch {
		case previous == nil:
			changes = append(changes, Change{Kind: ChangeDiscovered, After: current})
		case previous == current:
			// 完全相同的记录已经复用指针，不产生版本或事件。
		case previous.SessionID != current.SessionID:
			changes = append(changes,
				Change{Kind: ChangeLost, Before: previous},
				Change{Kind: ChangeDiscovered, After: current},
			)
		case previous.State != current.State:
			changes = append(changes, Change{
				Kind:   ChangeStateChanged,
				Before: previous,
				After:  current,
			})
		default:
			changes = append(changes, Change{
				Kind:   ChangeUpdated,
				Before: previous,
				After:  current,
			})
		}
	}

	// 旧快照中已经不存在的逻辑位置按稳定键顺序追加 Lost。
	oldOnly := make([]InstanceKey, 0)
	for key := range old.byInstance {
		if _, exists := candidates[key]; !exists {
			oldOnly = append(oldOnly, key)
		}
	}
	sort.Slice(oldOnly, func(left, right int) bool {
		if oldOnly[left].NodeID != oldOnly[right].NodeID {
			return oldOnly[left].NodeID < oldOnly[right].NodeID
		}
		return oldOnly[left].ServiceName < oldOnly[right].ServiceName
	})
	for _, key := range oldOnly {
		changes = append(changes, Change{
			Kind:   ChangeLost,
			Before: old.byInstance[key],
		})
	}
	if len(changes) == 0 {
		return ChangeSet{Version: old.version}, false, nil
	}

	// 只有真实变化才建立按服务候选和 TCP 目标索引，避免心跳式重复快照产生分配。
	next := buildSnapshot(old.version+1, candidates, keys, nodeCount)
	directory.current.Store(next)
	return ChangeSet{
		Version: next.version,
		Entries: changes,
	}, true, nil
}

// Find 在当前不可变快照中执行无锁精确查询。
func (directory *Directory) Find(nodeID, serviceName string) (*Instance, bool) {
	if directory == nil || nodeID == "" || serviceName == "" {
		return nil, false
	}
	instance, exists := directory.current.Load().byInstance[InstanceKey{
		NodeID:      nodeID,
		ServiceName: serviceName,
	}]
	return instance, exists
}

// List 返回当前快照内部只读候选 Slice。
//
// 调用方不能修改返回值；业务公开层必须另行复制，RPC/路由内部可直接遍历。
func (directory *Directory) List(serviceName string) []*Instance {
	if directory == nil || serviceName == "" {
		return nil
	}
	return directory.current.Load().byService[serviceName]
}

// All 返回当前快照按 NodeID、ServiceName 排序的内部只读实例 Slice。
//
// 该入口只供 Node 监听状态同步使用，业务公开层不能直接返回或修改其中任一对象。
func (directory *Directory) All() []*Instance {
	if directory == nil {
		return nil
	}
	return directory.current.Load().instances
}

// Targets 返回当前快照内部只读的 TCP Node 连接目标。
func (directory *Directory) Targets() []Target {
	if directory == nil {
		return nil
	}
	return directory.current.Load().targets
}

// Version 返回当前内部目录版本。
func (directory *Directory) Version() uint64 {
	if directory == nil {
		return 0
	}
	return directory.current.Load().version
}

// Stats 返回来自同一不可变快照的内部诊断计数。
func (directory *Directory) Stats() Stats {
	if directory == nil {
		return Stats{}
	}
	current := directory.current.Load()
	return Stats{
		Version:  current.version,
		Nodes:    current.nodeCount,
		Services: current.serviceCount,
	}
}

// normalize 完整校验原始数据，并建立由目录独占的可见实例 Map。
func (directory *Directory) normalize(
	raw RawSnapshot,
) (map[InstanceKey]*Instance, int, error) {
	result := make(map[InstanceKey]*Instance)
	nodeIDs := make(map[string]struct{}, len(raw.Nodes))
	visibleNodes := make(map[string]struct{})
	for nodeIndex, node := range raw.Nodes {
		// 每个 Node 必须先通过与过渡 Source 相同的公共校验，筛选规则不能掩盖坏数据。
		if err := validateRawNode(node); err != nil {
			return nil, 0, invalidSnapshot(fmt.Sprintf(
				"nodes[%d]: %v",
				nodeIndex,
				err,
			))
		}
		if _, duplicate := nodeIDs[node.NodeID]; duplicate {
			return nil, 0, invalidSnapshot(fmt.Sprintf(
				"NodeID %q 重复",
				node.NodeID,
			))
		}
		nodeIDs[node.NodeID] = struct{}{}
		for _, service := range node.Services {
			// 当前 Node 只保留远端实例；关注规则只作用于已经通过完整校验的公开记录。
			if node.NodeID == directory.localNodeID ||
				!directory.filter.Match(node, service) {
				continue
			}
			key := InstanceKey{
				NodeID:      node.NodeID,
				ServiceName: service.ServiceName,
			}
			result[key] = &Instance{
				NodeID:              node.NodeID,
				SessionID:           node.SessionID,
				ServiceName:         service.ServiceName,
				State:               service.State,
				Labels:              cloneLabels(node.Labels),
				Transport:           node.Transport,
				Address:             node.Address,
				ContractID:          service.ContractID,
				ContractFingerprint: service.ContractFingerprint,
			}
			visibleNodes[node.NodeID] = struct{}{}
		}
	}
	return result, len(visibleNodes), nil
}

// buildSnapshot 建立真实变化后的不可变候选和去重 TCP 目标索引。
func buildSnapshot(
	version uint64,
	instances map[InstanceKey]*Instance,
	keys []InstanceKey,
	nodeCount int,
) *Snapshot {
	byService := make(map[string][]*Instance)
	all := make([]*Instance, 0, len(keys))
	targetByNode := make(map[string]Target)
	for _, key := range keys {
		instance := instances[key]
		all = append(all, instance)
		byService[instance.ServiceName] = append(
			byService[instance.ServiceName],
			instance,
		)
		if instance.Transport == TransportTCP &&
			instance.ContractID != 0 {
			targetByNode[instance.NodeID] = Target{
				NodeID:    instance.NodeID,
				SessionID: instance.SessionID,
				Address:   instance.Address,
			}
		}
	}

	// keys 已按 NodeID、ServiceName 排序，因此每个 byService 候选天然按 NodeID 稳定。
	targets := make([]Target, 0, len(targetByNode))
	for _, target := range targetByNode {
		targets = append(targets, target)
	}
	sort.Slice(targets, func(left, right int) bool {
		return targets[left].NodeID < targets[right].NodeID
	})
	return &Snapshot{
		version:      version,
		byInstance:   instances,
		byService:    byService,
		instances:    all,
		targets:      targets,
		nodeCount:    nodeCount,
		serviceCount: len(instances),
	}
}

// instanceEqual 比较全部影响发现、路由、连接或业务观察的字段。
func instanceEqual(left, right *Instance) bool {
	if left == nil || right == nil {
		return left == right
	}
	return left.NodeID == right.NodeID &&
		left.SessionID == right.SessionID &&
		left.ServiceName == right.ServiceName &&
		left.State == right.State &&
		left.Transport == right.Transport &&
		left.Address == right.Address &&
		left.ContractID == right.ContractID &&
		left.ContractFingerprint == right.ContractFingerprint &&
		labelsEqual(left.Labels, right.Labels)
}

// labelsEqual 使用键和值精确比较，不依赖 Go Map 的随机迭代顺序。
func labelsEqual(left, right map[string]string) bool {
	if len(left) != len(right) {
		return false
	}
	for key, value := range left {
		if right[key] != value {
			return false
		}
	}
	return true
}

// cloneLabels 在目录所有权边界深复制可变 Map。
func cloneLabels(source map[string]string) map[string]string {
	if len(source) == 0 {
		return nil
	}
	result := make(map[string]string, len(source))
	for key, value := range source {
		result[key] = value
	}
	return result
}

// ValidateNodeLabels 拒绝无法形成稳定精确匹配语义的空键和值。
//
// 该函数只向框架内部的配置层和 Node 装配层公开，使无效标签在业务生命周期开始前失败；
// 正式业务 API 仍不能借此修改本地目录。
func ValidateNodeLabels(labels map[string]string) error {
	for key, value := range labels {
		if key == "" || value == "" {
			return fmt.Errorf("Node 标签的键和值不能为空")
		}
	}
	return nil
}

// validateRawNode 校验一条完整 Node 记录，并拒绝重复 ServiceName。
func validateRawNode(node RawNode) error {
	if node.NodeID == "" || node.SessionID == 0 {
		return fmt.Errorf("NodeID 和 SessionID 不能为空")
	}
	if err := validateTransport(node.Transport, node.Address); err != nil {
		return err
	}
	if len(node.Services) == 0 {
		return fmt.Errorf("Node %q 没有公开 Service", node.NodeID)
	}
	if err := ValidateNodeLabels(node.Labels); err != nil {
		return err
	}
	serviceNames := make(map[string]struct{}, len(node.Services))
	for index, service := range node.Services {
		if err := validateRawService(service); err != nil {
			return fmt.Errorf("services[%d]: %v", index, err)
		}
		if _, duplicate := serviceNames[service.ServiceName]; duplicate {
			return fmt.Errorf(
				"ServiceName %q 重复",
				service.ServiceName,
			)
		}
		serviceNames[service.ServiceName] = struct{}{}
	}
	return nil
}

// validateTransport 校验当前传输所需的地址边界。
func validateTransport(transport Transport, address string) error {
	switch transport {
	case TransportNone, TransportNATS:
		if address != "" {
			return fmt.Errorf("非 TCP Transport 不能携带地址")
		}
	case TransportTCP:
		if address == "" {
			return fmt.Errorf("TCP Transport 必须携带公开地址")
		}
	default:
		return fmt.Errorf("Transport %d 无效", transport)
	}
	return nil
}

// validateRawService 校验公开状态和 RPC 契约的成对零值规则。
func validateRawService(service RawService) error {
	if service.ServiceName == "" {
		return fmt.Errorf("ServiceName 不能为空")
	}
	if service.State != ServiceStateRunning &&
		service.State != ServiceStateRetired {
		return fmt.Errorf("ServiceState %d 无效", service.State)
	}
	fingerprintZero := service.ContractFingerprint == [32]byte{}
	if (service.ContractID == 0) != fingerprintZero {
		return fmt.Errorf("ContractID 和 ContractFingerprint 必须同时为零或同时非零")
	}
	return nil
}

// invalidSnapshot 为 Provider 或框架装配层保留稳定参数错误码。
func invalidSnapshot(message string) error {
	return errs.NewMessage(errs.CodeInvalidArgument, message)
}
