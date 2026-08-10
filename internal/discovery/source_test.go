package discovery

import (
	"errors"
	"reflect"
	"testing"
)

// TestSourcePublishesFullOwnedSnapshots 验证进程内发现源始终广播完整快照并独占输入容器。
func TestSourcePublishesFullOwnedSnapshots(t *testing.T) {
	t.Parallel()

	source := NewSource()
	var received []RawSnapshot
	subscription, err := source.Subscribe(func(snapshot RawSnapshot) error {
		received = append(received, snapshot)
		return nil
	})
	if err != nil {
		t.Fatalf("Subscribe() error = %v", err)
	}
	defer subscription.Close()
	if len(received) != 1 || len(received[0].Nodes) != 0 {
		t.Fatalf("首次订阅没有收到空完整快照: %+v", received)
	}

	first := rawNode(
		"game-2",
		2,
		"127.0.0.1:20002",
		"PlayerService",
		2,
	)
	first.Labels = map[string]string{"region": "cn-north"}
	if err := source.Publish(first); err != nil {
		t.Fatalf("Publish(first) error = %v", err)
	}
	first.Labels["region"] = "modified"

	second := rawNode(
		"game-1",
		1,
		"127.0.0.1:20001",
		"PlayerService",
		1,
	)
	if err := source.Publish(second); err != nil {
		t.Fatalf("Publish(second) error = %v", err)
	}
	latest := received[len(received)-1]
	if got := []string{latest.Nodes[0].NodeID, latest.Nodes[1].NodeID}; !reflect.DeepEqual(got, []string{"game-1", "game-2"}) {
		t.Fatalf("完整快照顺序 = %v", got)
	}
	if latest.Nodes[1].Labels["region"] != "cn-north" {
		t.Fatalf("Source 引用了发布方 Labels: %v", latest.Nodes[1].Labels)
	}

	if !source.Withdraw("game-2", 2) {
		t.Fatal("Withdraw() 没有删除精确会话")
	}
	latest = received[len(received)-1]
	if len(latest.Nodes) != 1 || latest.Nodes[0].NodeID != "game-1" {
		t.Fatalf("Withdraw 后完整快照 = %+v", latest)
	}
	if source.Withdraw("game-1", 999) {
		t.Fatal("陈旧 SessionID 删除了当前记录")
	}
}

// TestSourceLateSubscriberReceivesCurrentSnapshot 验证后启动 Node 立即获得已 Ready 前置 Node。
func TestSourceLateSubscriberReceivesCurrentSnapshot(t *testing.T) {
	t.Parallel()

	source := NewSource()
	if err := source.Publish(rawNode(
		"db-1",
		3,
		"127.0.0.1:20001",
		"DBService",
		1,
	)); err != nil {
		t.Fatalf("Publish() error = %v", err)
	}

	var latest RawSnapshot
	subscription, err := source.Subscribe(func(snapshot RawSnapshot) error {
		latest = snapshot
		return nil
	})
	if err != nil {
		t.Fatalf("Subscribe() error = %v", err)
	}
	defer subscription.Close()
	if len(latest.Nodes) != 1 || latest.Nodes[0].NodeID != "db-1" {
		t.Fatalf("晚订阅快照 = %+v", latest)
	}
}

// TestSourceRejectsInvalidReplacementWithoutPoisoningState 验证进程内 Source 在修改当前记录前完成
// 单 Node 校验，失败发布不能让后续订阅者收到半合法完整快照。
func TestSourceRejectsInvalidReplacementWithoutPoisoningState(t *testing.T) {
	t.Parallel()

	source := NewSource()
	valid := rawNode(
		"db-1",
		7,
		"127.0.0.1:20001",
		"DBService",
		1,
	)
	if err := source.Publish(valid); err != nil {
		t.Fatalf("Publish(valid) error = %v", err)
	}
	invalid := valid
	invalid.SessionID = 0
	if err := source.Publish(invalid); err == nil {
		t.Fatal("Publish(invalid) 没有返回错误")
	}

	var latest RawSnapshot
	subscription, err := source.Subscribe(func(snapshot RawSnapshot) error {
		latest = snapshot
		return nil
	})
	if err != nil {
		t.Fatalf("Subscribe() error = %v", err)
	}
	defer subscription.Close()
	if len(latest.Nodes) != 1 ||
		latest.Nodes[0].SessionID != 7 {
		t.Fatalf("非法替换污染 Source: %+v", latest)
	}
}

// TestSourceDeliveryContinuesAfterConsumerFailure 验证单个异常 Node 不会阻断其他健康 Node
// 收到同一轮完整快照，同时发布方仍能取得首个错误并执行启动回滚。
func TestSourceDeliveryContinuesAfterConsumerFailure(t *testing.T) {
	t.Parallel()

	expected := errors.New("consumer failure")
	failingCalls := 0
	healthyCalls := 0
	consumers := []SnapshotConsumer{
		func(RawSnapshot) error {
			failingCalls++
			return expected
		},
		func(RawSnapshot) error {
			healthyCalls++
			return nil
		},
	}

	// 固定把失败消费者放在第一位，确保旧实现会稳定提前返回而无法通过本测试。
	deliveryErr := deliverSnapshot(consumers, RawSnapshot{})
	if !errors.Is(deliveryErr, expected) {
		t.Fatalf("deliverSnapshot() error = %v, want consumer failure", deliveryErr)
	}
	if failingCalls != 1 || healthyCalls != 1 {
		t.Fatalf(
			"广播没有遍历全部消费者: failing=%d healthy=%d",
			failingCalls,
			healthyCalls,
		)
	}
}

// TestSourcePublishRollsBackAfterConsumerFailure 验证广播失败会恢复 Source 旧记录，并让已经
// 观察到暂态快照的健康消费者再收到回滚后的完整事实。
func TestSourcePublishRollsBackAfterConsumerFailure(t *testing.T) {
	t.Parallel()

	source := NewSource()
	expected := errors.New("consumer failure")
	failDelivery := false
	failing, err := source.Subscribe(func(RawSnapshot) error {
		if failDelivery {
			return expected
		}
		return nil
	})
	if err != nil {
		t.Fatalf("Subscribe(failing) error = %v", err)
	}
	defer failing.Close()

	var healthySnapshots []RawSnapshot
	healthy, err := source.Subscribe(func(snapshot RawSnapshot) error {
		healthySnapshots = append(healthySnapshots, snapshot)
		return nil
	})
	if err != nil {
		t.Fatalf("Subscribe(healthy) error = %v", err)
	}
	defer healthy.Close()

	// 第一次新记录广播失败；健康消费者应先观察暂态记录，再收到恢复为空的完整快照。
	failDelivery = true
	publishErr := source.Publish(
		rawNode("game-1", 1, "127.0.0.1:20001", "PlayerService", 1),
	)
	if !errors.Is(publishErr, expected) {
		t.Fatalf("Publish() error = %v, want consumer failure", publishErr)
	}
	if len(healthySnapshots) != 3 ||
		len(healthySnapshots[1].Nodes) != 1 ||
		len(healthySnapshots[2].Nodes) != 0 {
		t.Fatalf("健康消费者没有收到暂态与回滚快照: %+v", healthySnapshots)
	}

	// 恢复失败消费者后增加晚订阅者，确认 Source 自身也没有保留失败发布记录。
	failDelivery = false
	var latest RawSnapshot
	late, err := source.Subscribe(func(snapshot RawSnapshot) error {
		latest = snapshot
		return nil
	})
	if err != nil {
		t.Fatalf("Subscribe(late) error = %v", err)
	}
	defer late.Close()
	if len(latest.Nodes) != 0 {
		t.Fatalf("失败发布污染 Source 当前记录: %+v", latest)
	}
}
