package discovery

import (
	"errors"
	"reflect"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/duanhf2012/origin/v3/errs"
)

// TestDirectoryApplyAndQuery 验证完整快照筛选、稳定索引和重复内容不发布。
func TestDirectoryApplyAndQuery(t *testing.T) {
	t.Parallel()

	filter, err := CompileFilter(false, nil)
	if err != nil {
		t.Fatalf("CompileFilter() error = %v", err)
	}
	directory, err := NewDirectory("gateway-1", filter)
	if err != nil {
		t.Fatalf("NewDirectory() error = %v", err)
	}
	raw := RawSnapshot{Nodes: []RawNode{
		{
			NodeID:    "game-2",
			SessionID: 2,
			Labels:    map[string]string{"region": "cn-north"},
			Transport: TransportTCP,
			Address:   "127.0.0.1:20002",
			Services: []RawService{{
				ServiceName:         "PlayerService",
				State:               ServiceStateRetired,
				ContractID:          2,
				ContractFingerprint: fingerprint(2),
			}},
		},
		{
			NodeID:    "gateway-1",
			SessionID: 99,
			Transport: TransportTCP,
			Address:   "127.0.0.1:20001",
			Services: []RawService{{
				ServiceName: "GatewayService",
				State:       ServiceStateRunning,
			}},
		},
		{
			NodeID:    "game-1",
			SessionID: 1,
			Labels:    map[string]string{"region": "cn-east"},
			Transport: TransportTCP,
			Address:   "127.0.0.1:20003",
			Services: []RawService{{
				ServiceName:         "PlayerService",
				State:               ServiceStateRunning,
				ContractID:          1,
				ContractFingerprint: fingerprint(1),
			}},
		},
	}}

	changes, published, err := directory.ApplySnapshot(raw)
	if err != nil {
		t.Fatalf("ApplySnapshot() error = %v", err)
	}
	if !published || changes.Version != 1 || len(changes.Entries) != 2 {
		t.Fatalf("首次发布结果错误: published=%v changes=%+v", published, changes)
	}
	if _, exists := directory.Find("gateway-1", "GatewayService"); exists {
		t.Fatal("目录没有过滤当前 Node 自身")
	}
	first, exists := directory.Find("game-1", "PlayerService")
	if !exists || first.SessionID != 1 {
		t.Fatalf("精确查询错误: exists=%v instance=%+v", exists, first)
	}
	list := directory.List("PlayerService")
	if got := []string{list[0].NodeID, list[1].NodeID}; !reflect.DeepEqual(
		got,
		[]string{"game-1", "game-2"},
	) {
		t.Fatalf("List() NodeID = %v", got)
	}

	// 数据源可以在提交后复用自己的 Map；目录必须保留提交时的独立所有权。
	raw.Nodes[2].Labels["region"] = "modified"
	owned, _ := directory.Find("game-1", "PlayerService")
	if owned.Labels["region"] != "cn-east" {
		t.Fatalf("目录引用了调用方 Labels: %v", owned.Labels)
	}

	// 使用等价的新对象再次提交不得推进内部版本或制造业务变化。
	equivalent := RawSnapshot{Nodes: []RawNode{
		rawNode("game-1", 1, "127.0.0.1:20003", "PlayerService", 1),
		rawNodeRetired("game-2", 2, "127.0.0.1:20002", "PlayerService", 2),
	}}
	equivalent.Nodes[0].Labels = map[string]string{"region": "cn-east"}
	equivalent.Nodes[1].Labels = map[string]string{"region": "cn-north"}
	changes, published, err = directory.ApplySnapshot(equivalent)
	if err != nil {
		t.Fatalf("等价 ApplySnapshot() error = %v", err)
	}
	if published || changes.Version != 1 || len(changes.Entries) != 0 {
		t.Fatalf("等价快照产生了发布: published=%v changes=%+v", published, changes)
	}
}

// TestDirectoryStatsCountsServiceStates 固定零快照和一次发布后的 Running/Retired 计数；四个
// 计数必须来自同一个不可变 Snapshot。
func TestDirectoryStatsCountsServiceStates(t *testing.T) {
	t.Parallel()

	filter, _ := CompileFilter(false, nil)
	directory, _ := NewDirectory("gateway-1", filter)
	if stats := directory.Stats(); stats.Version != 0 || stats.Nodes != 0 ||
		stats.Services != 0 || stats.Running != 0 || stats.Retired != 0 {
		t.Fatalf("empty Stats() = %+v", stats)
	}
	if _, _, err := directory.ApplySnapshot(directoryStatsRawSnapshot(1, 3, 2)); err != nil {
		t.Fatal(err)
	}
	stats := directory.Stats()
	if stats.Version != 1 || stats.Nodes != 1 || stats.Services != 5 ||
		stats.Running != 3 || stats.Retired != 2 {
		t.Fatalf("published Stats() = %+v", stats)
	}
}

// TestDirectoryStatsConcurrentApplyIsOneSnapshot 交替发布不同总量和状态组成，证明一次 Stats
// 不会把 Version/Services 与另一版本的状态计数拼接。
func TestDirectoryStatsConcurrentApplyIsOneSnapshot(t *testing.T) {
	t.Parallel()

	filter, _ := CompileFilter(false, nil)
	directory, _ := NewDirectory("gateway-1", filter)
	first := directoryStatsRawSnapshot(11, 1, 1)
	second := directoryStatsRawSnapshot(12, 5, 3)
	if _, _, err := directory.ApplySnapshot(first); err != nil {
		t.Fatal(err)
	}

	var readers sync.WaitGroup
	var stop atomic.Bool
	var invalid atomic.Bool
	for range 4 {
		readers.Add(1)
		go func() {
			defer readers.Done()
			for !stop.Load() {
				stats := directory.Stats()
				if stats.Running+stats.Retired != stats.Services {
					invalid.Store(true)
					return
				}
			}
		}()
	}
	for index := range 500 {
		candidate := first
		if index%2 == 0 {
			candidate = second
		}
		if _, _, err := directory.ApplySnapshot(candidate); err != nil {
			stop.Store(true)
			readers.Wait()
			t.Fatal(err)
		}
	}
	stop.Store(true)
	readers.Wait()
	if invalid.Load() {
		t.Fatal("Stats() observed state counts from different snapshots")
	}
}

// TestDirectorySnapshotPinsOneAtomicView 验证一次取得的 Snapshot 不会在后续发布中混入新会话。
func TestDirectorySnapshotPinsOneAtomicView(t *testing.T) {
	t.Parallel()

	filter, _ := CompileFilter(false, nil)
	directory, _ := NewDirectory("gateway-1", filter)
	if _, _, err := directory.ApplySnapshot(RawSnapshot{Nodes: []RawNode{
		rawNode("game-1", 31, "127.0.0.1:20001", "PlayerService", 1),
	}}); err != nil {
		t.Fatalf("首次 ApplySnapshot() error = %v", err)
	}
	first := directory.Snapshot()

	if _, _, err := directory.ApplySnapshot(RawSnapshot{Nodes: []RawNode{
		rawNode("game-1", 32, "127.0.0.1:20002", "PlayerService", 1),
	}}); err != nil {
		t.Fatalf("替换 ApplySnapshot() error = %v", err)
	}
	second := directory.Snapshot()

	oldInstance, oldExists := first.Find("game-1", "PlayerService")
	newInstance, newExists := second.Find("game-1", "PlayerService")
	if !oldExists || oldInstance.SessionID != 31 {
		t.Fatalf("旧 Snapshot = exists %v, instance %+v", oldExists, oldInstance)
	}
	if !newExists || newInstance.SessionID != 32 {
		t.Fatalf("新 Snapshot = exists %v, instance %+v", newExists, newInstance)
	}
	oldList := first.List("PlayerService")
	if len(oldList) != 1 || oldList[0] != oldInstance {
		t.Fatalf("旧 Snapshot List = %+v", oldList)
	}
}

// TestDirectorySessionReplacementOrder 锁定同一逻辑位置会话替换的 Lost/Discovered 顺序。
func TestDirectorySessionReplacementOrder(t *testing.T) {
	t.Parallel()

	filter, _ := CompileFilter(false, nil)
	directory, _ := NewDirectory("gateway-1", filter)
	oldSnapshot := RawSnapshot{Nodes: []RawNode{
		rawNode("game-1", 10, "127.0.0.1:20001", "PlayerService", 1),
	}}
	if _, _, err := directory.ApplySnapshot(oldSnapshot); err != nil {
		t.Fatalf("首次 ApplySnapshot() error = %v", err)
	}

	newSnapshot := RawSnapshot{Nodes: []RawNode{
		rawNode("game-1", 11, "127.0.0.1:20001", "PlayerService", 1),
	}}
	changes, published, err := directory.ApplySnapshot(newSnapshot)
	if err != nil {
		t.Fatalf("替换 ApplySnapshot() error = %v", err)
	}
	if !published || len(changes.Entries) != 2 {
		t.Fatalf("会话替换变化数错误: published=%v changes=%+v", published, changes)
	}
	if changes.Entries[0].Kind != ChangeLost ||
		changes.Entries[0].Before.SessionID != 10 {
		t.Fatalf("第一项不是旧会话 Lost: %+v", changes.Entries[0])
	}
	if changes.Entries[1].Kind != ChangeDiscovered ||
		changes.Entries[1].After.SessionID != 11 {
		t.Fatalf("第二项不是新会话 Discovered: %+v", changes.Entries[1])
	}
}

// TestDirectoryTargetsDeduplicateServicesByNode 验证一个远端 Node 的多个 RPC Service 只形成
// 一条绑定 SessionID 的 TCP 连接需求。
func TestDirectoryTargetsDeduplicateServicesByNode(t *testing.T) {
	t.Parallel()

	filter, _ := CompileFilter(false, nil)
	directory, _ := NewDirectory("gateway-1", filter)
	node := rawNode(
		"game-1",
		12,
		"127.0.0.1:20001",
		"PlayerService",
		1,
	)
	node.Services = append(node.Services, RawService{
		ServiceName:         "ChatService",
		State:               ServiceStateRetired,
		ContractID:          2,
		ContractFingerprint: fingerprint(2),
	})
	if _, _, err := directory.ApplySnapshot(RawSnapshot{
		Nodes: []RawNode{node},
	}); err != nil {
		t.Fatalf("ApplySnapshot() error = %v", err)
	}
	targets := directory.Targets()
	if len(targets) != 1 ||
		targets[0].NodeID != "game-1" ||
		targets[0].SessionID != 12 {
		t.Fatalf("Targets() = %+v", targets)
	}
}

// TestDirectoryRejectsInvalidSnapshotAtomically 验证非法完整快照不污染已经发布的目录。
func TestDirectoryRejectsInvalidSnapshotAtomically(t *testing.T) {
	t.Parallel()

	filter, _ := CompileFilter(false, nil)
	directory, _ := NewDirectory("gateway-1", filter)
	valid := RawSnapshot{Nodes: []RawNode{
		rawNode("game-1", 1, "127.0.0.1:20001", "PlayerService", 1),
	}}
	if _, _, err := directory.ApplySnapshot(valid); err != nil {
		t.Fatalf("首次 ApplySnapshot() error = %v", err)
	}

	invalid := RawSnapshot{Nodes: []RawNode{
		rawNode("game-1", 2, "", "PlayerService", 1),
	}}
	if _, _, err := directory.ApplySnapshot(invalid); !errors.Is(
		err,
		errs.ErrInvalidArgument,
	) {
		t.Fatalf("非法 ApplySnapshot() error = %v", err)
	}
	instance, exists := directory.Find("game-1", "PlayerService")
	if !exists || instance.SessionID != 1 || directory.Version() != 1 {
		t.Fatalf("非法快照污染了旧状态: exists=%v instance=%+v version=%d",
			exists, instance, directory.Version())
	}
}

// TestDirectoryConcurrentApplyAndRead 验证冷路径发布与无锁查询可以长期交叉，不暴露半成品。
//
// 本测试主要交给 -race 检查 Map、Slice 与 Instance 的不可变约束；普通测试同时断言读者
// 只能观察两个完整会话之一，绝不能看到空 SessionID 或不完整候选。
func TestDirectoryConcurrentApplyAndRead(t *testing.T) {
	t.Parallel()

	filter, _ := CompileFilter(false, nil)
	directory, _ := NewDirectory("gateway-1", filter)
	first := RawSnapshot{Nodes: []RawNode{
		rawNode("game-1", 21, "127.0.0.1:20001", "PlayerService", 1),
	}}
	second := RawSnapshot{Nodes: []RawNode{
		rawNode("game-1", 22, "127.0.0.1:20002", "PlayerService", 1),
	}}
	if _, _, err := directory.ApplySnapshot(first); err != nil {
		t.Fatalf("首次 ApplySnapshot() error = %v", err)
	}

	// 多个读者持续读取同一个原子快照；失败通过原子标记汇总，避免从 goroutine 调用 Fatal。
	var readers sync.WaitGroup
	var stop atomic.Bool
	var invalid atomic.Bool
	for readerIndex := 0; readerIndex < 4; readerIndex++ {
		readers.Add(1)
		go func() {
			defer readers.Done()
			for !stop.Load() {
				instance, exists := directory.Find("game-1", "PlayerService")
				if !exists ||
					(instance.SessionID != 21 &&
						instance.SessionID != 22) {
					invalid.Store(true)
					return
				}
				candidates := directory.List("PlayerService")
				if len(candidates) != 1 ||
					candidates[0].SessionID == 0 {
					invalid.Store(true)
					return
				}
			}
		}()
	}

	// 写者交替发布两份完整快照，覆盖 Instance 替换、Diff 和候选索引整体更新。
	for updateIndex := 0; updateIndex < 200; updateIndex++ {
		candidate := first
		if updateIndex%2 == 0 {
			candidate = second
		}
		if _, _, err := directory.ApplySnapshot(candidate); err != nil {
			stop.Store(true)
			readers.Wait()
			t.Fatalf("ApplySnapshot() error = %v", err)
		}
	}
	stop.Store(true)
	readers.Wait()
	if invalid.Load() {
		t.Fatal("并发读者观察到了半发布或非法目录状态")
	}
}

// rawNode 创建一个具有有效 TCP RPC 契约的测试 Node。
func rawNode(
	nodeID string,
	sessionID uint64,
	address string,
	serviceName string,
	contract byte,
) RawNode {
	return RawNode{
		NodeID:    nodeID,
		SessionID: sessionID,
		Transport: TransportTCP,
		Address:   address,
		Services: []RawService{{
			ServiceName:         serviceName,
			State:               ServiceStateRunning,
			ContractID:          uint64(contract),
			ContractFingerprint: fingerprint(contract),
		}},
	}
}

// rawNodeRetired 创建一个处于 Retired、但仍具有 RPC 路由能力的测试 Node。
func rawNodeRetired(
	nodeID string,
	sessionID uint64,
	address string,
	serviceName string,
	contract byte,
) RawNode {
	node := rawNode(nodeID, sessionID, address, serviceName, contract)
	node.Services[0].State = ServiceStateRetired
	return node
}

// directoryStatsRawSnapshot 构造一个远端 Node 的可辨认状态组成。
func directoryStatsRawSnapshot(sessionID uint64, running, retired int) RawSnapshot {
	services := make([]RawService, 0, running+retired)
	for index := range running {
		services = append(services, RawService{
			ServiceName: "running-" + strconv.Itoa(index),
			State:       ServiceStateRunning,
		})
	}
	for index := range retired {
		services = append(services, RawService{
			ServiceName: "retired-" + strconv.Itoa(index),
			State:       ServiceStateRetired,
		})
	}
	return RawSnapshot{Nodes: []RawNode{{
		NodeID:    "game-1",
		SessionID: sessionID,
		Transport: TransportNone,
		Services:  services,
	}}}
}

// fingerprint 构造非零且容易辨认的稳定测试指纹。
func fingerprint(value byte) [32]byte {
	var result [32]byte
	result[0] = value
	return result
}
