package rpc

import (
	publicdiscovery "github.com/duanhf2012/origin/v3/discovery"
	publicprovider "github.com/duanhf2012/origin/v3/discovery/provider"
	"github.com/duanhf2012/origin/v3/errs"
)

type routeMode uint8

const (
	routeDefault routeMode = iota
	routeRoundRobin
	routeRandom
	routeKey
	routeCustom
)

type routeSpec struct {
	mode     routeMode
	hash     uint64
	selector RouteSelector
	err      error
}

// routeLabel 保存一个已经从调用方 Map 冻结的精确匹配条件。
//
// string 赋值只复制不可变字符串头；Client 不保留调用方 Map，因此调用方在 WhereLabels
// 返回后替换或删除 Map 项不会影响已经派生的客户端。
type routeLabel struct {
	name  string
	value string
}

// routeLabelFilter 保存按名称稳定排序的 AND 条件。
//
// impossible 表示多次派生为同一个 Key 指定了不同 Value，或条件数量超过单 Node Labels
// 容量；该状态不需要保存原始条件，后续 Prepare 稳定返回无路由。
type routeLabelFilter struct {
	required   []routeLabel
	impossible bool
}

// active 报告客户端是否携带需要影响调用语义的 Labels 条件。
func (filter routeLabelFilter) active() bool {
	return filter.impossible || len(filter.required) != 0
}

// find 按最多 32 项的有界条件执行线性查询。
//
// WhereLabels 是派生冷路径；小型线性扫描比额外索引 Map 更紧凑，并避免给每个客户端建立
// 第二套可变标签结构。
func (filter routeLabelFilter) find(name string) (string, bool) {
	for _, current := range filter.required {
		if current.name == name {
			return current.value, true
		}
	}
	return "", false
}

// matches 对候选已有的不可变 Labels Map 执行精确 AND 匹配。
func (filter routeLabelFilter) matches(labels map[string]string) bool {
	if filter.impossible {
		return false
	}
	for _, required := range filter.required {
		value, exists := labels[required.name]
		if !exists || value != required.value {
			return false
		}
	}
	return true
}

// RouteSelector 从 Runtime 已经筛选的只读候选中选择一个下标。
//
// Selector 必须同步、快速并可安全并发调用。返回 false 表示主动拒绝当前候选。
type RouteSelector interface {
	Select(RouteCandidates) (index int, ok bool)
}

// RouteCandidates 是自定义 RouteSelector 可读取的候选视图。
//
// 候选字段和实际存储由 Runtime 管理，业务只能通过只读方法访问。
type RouteCandidates struct {
	set   candidateSet
	valid bool
}

// Len 返回当前已过滤候选数量。
func (candidates RouteCandidates) Len() int {
	if !candidates.valid {
		return 0
	}
	return candidates.set.count
}

// NodeID 返回指定候选的稳定 NodeID；越界时返回空字符串。
func (candidates RouteCandidates) NodeID(index int) string {
	candidate, exists := candidates.candidate(index)
	if !exists {
		return ""
	}
	return candidate.nodeID
}

// ServiceName 返回指定候选的实际 ServiceName；越界时返回空字符串。
func (candidates RouteCandidates) ServiceName(index int) string {
	candidate, exists := candidates.candidate(index)
	if !exists {
		return ""
	}
	return candidate.serviceName
}

// State 返回指定候选的发现状态；越界时返回 StateUnknown。
func (candidates RouteCandidates) State(index int) publicdiscovery.State {
	candidate, exists := candidates.candidate(index)
	if !exists {
		return publicdiscovery.StateUnknown
	}
	return candidate.state
}

// Label 返回指定候选的不可变标签值。
func (candidates RouteCandidates) Label(
	index int,
	name string,
) (string, bool) {
	candidate, exists := candidates.candidate(index)
	if !exists || name == "" {
		return "", false
	}
	value, exists := candidate.labels[name]
	return value, exists
}

func (candidates RouteCandidates) candidate(
	index int,
) (routeCandidate, bool) {
	if !candidates.valid {
		return routeCandidate{}, false
	}
	return candidates.set.eligibleAt(index)
}

// OnNode 保留当前客户端绑定的 ServiceName，并把目标收窄到指定 Node。
func (client Client) OnNode(nodeID string) Client {
	client.target = ToServiceOnNode(nodeID, client.target.serviceName)
	client.prepared = preparedTarget{}
	client.broadcast = nil
	return client
}

// WhereLabels 派生按 Node Labels 精确筛选候选的值客户端。
//
// 多次调用把条件按 AND 合并；同 Key 同 Value 幂等，同 Key 不同 Value 形成稳定的无路由
// 条件。nil 或空 Map 是无操作。OnNode 与 Labels 不互斥，精确目标仍必须满足全部条件。
func (client Client) WhereLabels(labels map[string]string) Client {
	// 空条件不改变范围，也不清除客户端此前已经冻结的过滤条件。
	if len(labels) == 0 || client.labels.impossible {
		return client
	}
	if len(labels) > publicprovider.MaxLabelsPerNode {
		return client.withImpossibleLabels()
	}

	// 第一遍只计算真正新增的条件并识别冲突，避免幂等派生产生新 Slice。
	additions := 0
	for name, value := range labels {
		current, exists := client.labels.find(name)
		if exists {
			if current != value {
				return client.withImpossibleLabels()
			}
			continue
		}
		additions++
	}
	if additions == 0 {
		return client
	}
	if len(client.labels.required)+additions > publicprovider.MaxLabelsPerNode {
		return client.withImpossibleLabels()
	}

	// 冻结调用方 Map，之后候选热路径只读取这一份有界、不可变条件。
	required := make(
		[]routeLabel,
		len(client.labels.required),
		len(client.labels.required)+additions,
	)
	copy(required, client.labels.required)
	for name, value := range labels {
		if _, exists := client.labels.find(name); exists {
			continue
		}
		required = append(required, routeLabel{name: name, value: value})
	}
	sortRouteLabels(required)
	client.labels = routeLabelFilter{required: required}
	client.prepared = preparedTarget{}
	client.broadcast = nil
	return client
}

// withImpossibleLabels 丢弃不再需要的条件引用，并保留其他客户端派生维度。
func (client Client) withImpossibleLabels() Client {
	client.labels = routeLabelFilter{impossible: true}
	client.prepared = preparedTarget{}
	client.broadcast = nil
	return client
}

// sortRouteLabels 使用有界插入排序固定条件顺序，不为最多 32 项的派生冷路径建立闭包或索引。
func sortRouteLabels(labels []routeLabel) {
	for index := 1; index < len(labels); index++ {
		current := labels[index]
		position := index
		for position > 0 && labels[position-1].name > current.name {
			labels[position] = labels[position-1]
			position--
		}
		labels[position] = current
	}
}

// IncludeRetired 派生一个在自动选择范围中同时接受 Running 和 Retired 的值客户端。
//
// 该方法不改变基础客户端；精确 OnNode 原本就允许 Retired，因此重复调用和派生顺序都只保留
// 同一个布尔标志，不增加第二套生命周期筛选语义。
func (client Client) IncludeRetired() Client {
	client.includeRetired = true
	client.prepared = preparedTarget{}
	client.broadcast = nil
	return client
}

// RouteRoundRobin 派生显式使用 Runtime 级轮询策略的值客户端。
func (client Client) RouteRoundRobin() Client {
	client.route = routeSpec{mode: routeRoundRobin}
	client.prepared = preparedTarget{}
	client.broadcast = nil
	return client
}

// RouteRandom 派生使用 Runtime 级低竞争随机策略的值客户端。
func (client Client) RouteRandom() Client {
	client.route = routeSpec{mode: routeRandom}
	client.prepared = preparedTarget{}
	client.broadcast = nil
	return client
}

// Route 派生按稳定业务 Key 选择候选的值客户端。
func (client Client) Route(key any) Client {
	hash, err := normalizeRouteKey(key)
	client.route = routeSpec{
		mode: routeKey,
		hash: hash,
		err:  err,
	}
	client.prepared = preparedTarget{}
	client.broadcast = nil
	return client
}

// RouteBy 派生使用业务自定义 Selector 的值客户端。
func (client Client) RouteBy(selector RouteSelector) Client {
	client.route = routeSpec{
		mode:     routeCustom,
		selector: selector,
	}
	client.prepared = preparedTarget{}
	client.broadcast = nil
	return client
}

func normalizeRouteKey(key any) (uint64, error) {
	switch value := key.(type) {
	case string:
		return fnv1aString(value), nil
	case []byte:
		return fnv1aBytes(value), nil
	case int:
		return uint64(value), nil
	case int8:
		return uint64(value), nil
	case int16:
		return uint64(value), nil
	case int32:
		return uint64(value), nil
	case int64:
		return uint64(value), nil
	case uint:
		return uint64(value), nil
	case uint8:
		return uint64(value), nil
	case uint16:
		return uint64(value), nil
	case uint32:
		return uint64(value), nil
	case uint64:
		return value, nil
	default:
		return 0, errs.ErrRPCInvalidRouteKey
	}
}

func fnv1aString(value string) uint64 {
	const (
		offset = uint64(14695981039346656037)
		prime  = uint64(1099511628211)
	)
	hash := offset
	for index := 0; index < len(value); index++ {
		hash ^= uint64(value[index])
		hash *= prime
	}
	return hash
}

func fnv1aBytes(value []byte) uint64 {
	const (
		offset = uint64(14695981039346656037)
		prime  = uint64(1099511628211)
	)
	hash := offset
	for _, current := range value {
		hash ^= uint64(current)
		hash *= prime
	}
	return hash
}
