package discovery

import (
	"sort"
	"sync"

	"github.com/duanhf2012/origin/v3/errs"
)

// SnapshotConsumer 接收 Application 进程内发现源的当前完整原始快照。
//
// 回调在 Source 更新冷路径同步执行，必须在返回前把所需数据复制到所属 Node 的所有权
// 边界，不能保存并修改传入容器。
type SnapshotConsumer func(RawSnapshot) error

// Source 是同一 Application 多 Node 使用的进程内完整快照源。
//
// 它只保存公开 RawNode，不拥有任何 Node 的可见目录，也不替代外部 Discovery Provider。
// dispatchMu 保证 Publish、Withdraw 和晚订阅观察到严格顺序的完整快照。
type Source struct {
	dispatchMu sync.Mutex
	mu         sync.Mutex
	records    map[string]RawNode
	consumers  map[uint64]SnapshotConsumer
	nextID     uint64
}

// Subscription 表示一个 Node 对进程内发现源的订阅所有权。
type Subscription struct {
	source *Source
	id     uint64
	once   sync.Once
}

// NewSource 创建一份没有公开 Node 的进程内发现源。
func NewSource() *Source {
	return &Source{
		records:   make(map[string]RawNode),
		consumers: make(map[uint64]SnapshotConsumer),
	}
}

// Subscribe 登记一个消费者，并在返回前同步交付当前完整快照。
func (source *Source) Subscribe(
	consumer SnapshotConsumer,
) (*Subscription, error) {
	if source == nil || consumer == nil {
		return nil, errs.ErrInvalidArgument
	}

	// 与发布使用同一串行锁，保证“读取当前快照”和“加入后续广播”之间没有更新窗口。
	source.dispatchMu.Lock()
	defer source.dispatchMu.Unlock()
	source.mu.Lock()
	source.nextID++
	if source.nextID == 0 {
		source.mu.Unlock()
		return nil, errs.ErrInternal
	}
	id := source.nextID
	source.consumers[id] = consumer
	snapshot := source.snapshotLocked()
	source.mu.Unlock()

	// 首次交付失败时撤销登记，使调用方不会持有半订阅对象。
	if err := consumer(snapshot); err != nil {
		source.mu.Lock()
		delete(source.consumers, id)
		source.mu.Unlock()
		return nil, err
	}
	return &Subscription{source: source, id: id}, nil
}

// Publish 新增或替换一个 Node 进程会话，并向全部订阅者广播完整快照。
func (source *Source) Publish(node RawNode) error {
	if source == nil {
		return errs.ErrInvalidArgument
	}
	// 在修改 Source 当前记录前完成整条 Node 校验，失败发布不能污染后续完整快照。
	if err := validateRawNode(node); err != nil {
		return invalidSnapshot(err.Error())
	}
	source.dispatchMu.Lock()
	defer source.dispatchMu.Unlock()

	// Source 在保存前复制全部可变容器，Node 发布方返回后可以释放自己的临时 DTO。
	source.mu.Lock()
	previous, hadPrevious := source.records[node.NodeID]
	source.records[node.NodeID] = cloneRawNode(node)
	snapshot := source.snapshotLocked()
	consumers := source.consumersLocked()
	source.mu.Unlock()
	if err := deliverSnapshot(consumers, snapshot); err != nil {
		// 发布失败必须在同一串行边界恢复原事实。健康消费者可能已经观察到暂态完整快照，
		// 因此恢复 Source 后再广播一轮回滚快照，使目录最终与发布返回值保持一致。
		source.mu.Lock()
		if hadPrevious {
			source.records[node.NodeID] = previous
		} else {
			delete(source.records, node.NodeID)
		}
		rollback := source.snapshotLocked()
		source.mu.Unlock()
		_ = deliverSnapshot(consumers, rollback)
		return err
	}
	return nil
}

// Withdraw 只删除 NodeID 和 SessionID 同时匹配的当前记录。
//
// 陈旧进程的迟到停止不能移除同一逻辑 Node 上已经发布的新会话。
func (source *Source) Withdraw(nodeID string, sessionID uint64) bool {
	if source == nil || nodeID == "" || sessionID == 0 {
		return false
	}
	source.dispatchMu.Lock()
	defer source.dispatchMu.Unlock()
	source.mu.Lock()
	current, exists := source.records[nodeID]
	if !exists || current.SessionID != sessionID {
		source.mu.Unlock()
		return false
	}
	delete(source.records, nodeID)
	snapshot := source.snapshotLocked()
	consumers := source.consumersLocked()
	source.mu.Unlock()

	// Application 进程内发现源的消费者只会执行内存快照 Apply；错误属于框架不变量问题。
	// Withdraw 没有 error 返回值，调用方仍能通过 Node 日志和后续测试发现该内部错误。
	_ = deliverSnapshot(consumers, snapshot)
	return true
}

// Close 幂等移除当前订阅，之后不再接收完整快照。
func (subscription *Subscription) Close() {
	if subscription == nil || subscription.source == nil || subscription.id == 0 {
		return
	}
	subscription.once.Do(func() {
		// 与发布串行化，确保 Close 返回后不存在尚未开始的回调。
		subscription.source.dispatchMu.Lock()
		subscription.source.mu.Lock()
		delete(subscription.source.consumers, subscription.id)
		subscription.source.mu.Unlock()
		subscription.source.dispatchMu.Unlock()
	})
}

// snapshotLocked 按 NodeID 稳定排序并为一次广播建立 Source 自有完整快照。
func (source *Source) snapshotLocked() RawSnapshot {
	nodeIDs := make([]string, 0, len(source.records))
	for nodeID := range source.records {
		nodeIDs = append(nodeIDs, nodeID)
	}
	sort.Strings(nodeIDs)
	result := RawSnapshot{Nodes: make([]RawNode, 0, len(nodeIDs))}
	for _, nodeID := range nodeIDs {
		result.Nodes = append(result.Nodes, cloneRawNode(source.records[nodeID]))
	}
	return result
}

// consumersLocked 复制回调表，使任何回调都不会在 Source 状态锁内执行。
func (source *Source) consumersLocked() []SnapshotConsumer {
	result := make([]SnapshotConsumer, 0, len(source.consumers))
	for _, consumer := range source.consumers {
		result = append(result, consumer)
	}
	return result
}

// deliverSnapshot 顺序调用一次广播的全部消费者，并返回第一项失败。
//
// 单个 Node 的目录异常不能阻断同一 Application 中其他健康 Node 对齐最新完整快照；发布
// 方仍取得首个错误，用于中止当前 Node 启动并执行既有回滚。
func deliverSnapshot(
	consumers []SnapshotConsumer,
	snapshot RawSnapshot,
) error {
	var firstErr error
	for _, consumer := range consumers {
		if err := consumer(snapshot); err != nil {
			if firstErr == nil {
				firstErr = err
			}
		}
	}
	return firstErr
}

// cloneRawNode 深复制 Source 需要独占的 Slice 和 Map，字符串与固定数组按值复用。
func cloneRawNode(source RawNode) RawNode {
	result := source
	result.Labels = cloneLabels(source.Labels)
	result.Services = append([]RawService(nil), source.Services...)
	return result
}
