// Package tutorialwatcher 提供服务发现示例共用的监听 Service。
package tutorialwatcher

import (
	"context"
	"fmt"

	"github.com/duanhf2012/origin/v3/discovery"
	"github.com/duanhf2012/origin/v3/service"
)

// Service 直接实现发现监听接口，并把监听生命周期交给所属 Service 管理。
type Service struct{ service.Service }

// OnInit 注册同时接收发现、状态变化和 Lost 的统一监听器。
func (target *Service) OnInit() error {
	_, err := target.AddDiscoveryListener(target)
	return err
}

// OnDiscovered 在远端 Node 首次进入当前目录时调用。
func (target *Service) OnDiscovered(_ context.Context, event discovery.Event) {
	target.Logger().Info(fmt.Sprintf("discovered node=%s services=%v", event.NodeID, event.Services))
}

// OnStateChanged 在已知远端实例的 Running/Retired 等状态变化时调用。
func (target *Service) OnStateChanged(_ context.Context, event discovery.Event) {
	target.Logger().Info(fmt.Sprintf("state changed node=%s services=%v", event.NodeID, event.Services))
}

// OnLost 在权威快照移除远端 Node 时立即调用。
func (target *Service) OnLost(_ context.Context, event discovery.Event) {
	target.Logger().Info(fmt.Sprintf("lost node=%s services=%v", event.NodeID, event.Services))
}
