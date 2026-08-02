// Package tutorialwatcher 提供服务发现示例共用的监听 Service。
package tutorialwatcher

import (
	"context"
	"fmt"

	"github.com/duanhf2012/origin/v3/discovery"
	"github.com/duanhf2012/origin/v3/service"
)

// Service 保存监听注册 ID，以便在停止阶段显式移除注册。
type Service struct {
	service.Service
	listenerID discovery.ListenerID
}

// OnInit 注册同时接收发现、状态变化和 Lost 的统一监听器。
func (target *Service) OnInit() error {
	listenerID, err := target.AddDiscoveryListener(listener{owner: target})
	if err != nil {
		return err
	}
	target.listenerID = listenerID
	return nil
}

// OnStop 演示 RemoveDiscoveryListener，并让框架把外部 ID 清零。
func (target *Service) OnStop(context.Context) error {
	target.RemoveDiscoveryListener(&target.listenerID)
	return nil
}

// listener 只持有所属 Service，用它的 Logger 记录回调结果。
type listener struct{ owner *Service }

// OnDiscovered 在远端 Node 首次进入当前目录时调用。
func (target listener) OnDiscovered(_ context.Context, event discovery.Event) {
	target.owner.Logger().Info(fmt.Sprintf("discovered node=%s services=%v", event.NodeID, event.Services))
}

// OnStateChanged 在已知远端实例的 Running/Retired 等状态变化时调用。
func (target listener) OnStateChanged(_ context.Context, event discovery.Event) {
	target.owner.Logger().Info(fmt.Sprintf("state changed node=%s services=%v", event.NodeID, event.Services))
}

// OnLost 在权威快照移除远端 Node 时立即调用。
func (target listener) OnLost(_ context.Context, event discovery.Event) {
	target.owner.Logger().Info(fmt.Sprintf("lost node=%s services=%v", event.NodeID, event.Services))
}
