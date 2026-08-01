package rpc

import (
	publicdiscovery "github.com/duanhf2012/origin/v3/discovery"
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
	return client
}

// IncludeRetired 派生一个在自动选择范围中同时接受 Running 和 Retired 的值客户端。
//
// 该方法不改变基础客户端；精确 OnNode 原本就允许 Retired，因此重复调用和派生顺序都只保留
// 同一个布尔标志，不增加第二套生命周期筛选语义。
func (client Client) IncludeRetired() Client {
	client.includeRetired = true
	client.prepared = preparedTarget{}
	return client
}

// RouteRoundRobin 派生显式使用 Runtime 级轮询策略的值客户端。
func (client Client) RouteRoundRobin() Client {
	client.route = routeSpec{mode: routeRoundRobin}
	client.prepared = preparedTarget{}
	return client
}

// RouteRandom 派生使用 Runtime 级低竞争随机策略的值客户端。
func (client Client) RouteRandom() Client {
	client.route = routeSpec{mode: routeRandom}
	client.prepared = preparedTarget{}
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
	return client
}

// RouteBy 派生使用业务自定义 Selector 的值客户端。
func (client Client) RouteBy(selector RouteSelector) Client {
	client.route = routeSpec{
		mode:     routeCustom,
		selector: selector,
	}
	client.prepared = preparedTarget{}
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
