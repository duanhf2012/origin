// 本示例展示业务 Module 组合 Blueprint Module 的完整游戏工作流。
package main

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/duanhf2012/origin/v3/application"
	originlog "github.com/duanhf2012/origin/v3/log"
	"github.com/duanhf2012/origin/v3/service"
	"github.com/duanhf2012/origin/v3/sysmodule/blueprintmodule"
)

var app = application.New()

// battleRPCClient 模拟外部 RPC 客户端。它只保管并恢复 YieldHandle，不访问 Service 业务数据。
// 真实项目应在同一位置替换成自己的 RPC Client，并保持启动、取消和等待退出的资源所有权闭环。
type battleRPCClient struct {
	cancel   context.CancelFunc
	requests chan *blueprintmodule.YieldHandle
	wg       sync.WaitGroup
}

func (client *battleRPCClient) Start() {
	ctx, cancel := context.WithCancel(context.Background())
	client.cancel = cancel
	client.requests = make(chan *blueprintmodule.YieldHandle, 16)
	client.wg.Add(1)
	go func() {
		defer client.wg.Done()
		for {
			select {
			case <-ctx.Done():
				return
			case handle := <-client.requests:
				timer := time.NewTimer(20 * time.Millisecond)
				select {
				case <-ctx.Done():
					if !timer.Stop() {
						<-timer.C
					}
					return
				case <-timer.C:
					// Resume 可从任意 goroutine 调用；底层会把后续蓝图节点投递回 Service。
					_ = handle.Resume()
				}
			}
		}
	}()
}

func (client *battleRPCClient) Submit(handle *blueprintmodule.YieldHandle) error {
	if client == nil || client.cancel == nil || handle == nil {
		return errors.New("battle RPC client is not running")
	}
	select {
	case client.requests <- handle:
		return nil
	default:
		return errors.New("battle RPC request queue is full")
	}
}

func (client *battleRPCClient) Stop() {
	if client == nil || client.cancel == nil {
		return
	}
	client.cancel()
	client.wg.Wait()
	client.cancel = nil
}

// BattleBlueprintModule 把蓝图生命周期、自定义节点和战斗数据收拢为一个业务边界。
// 所有节点 Exec 都在 BattleService 工作协程中运行，因此可直接访问 players，无需额外加锁。
type BattleBlueprintModule struct {
	blueprintmodule.Module
	players  map[int64]int64
	rpc      battleRPCClient
	instance *blueprintmodule.Instance
}

// battleTraceLogger 只写并发安全日志，不访问 players 等 Service 串行业务字段。
type battleTraceLogger struct{ logger originlog.Logger }

func (logger *battleTraceLogger) TraceBlueprintNode(event blueprintmodule.BlueprintTraceEvent) {
	logger.logger.Info(fmt.Sprintf(
		"blueprint trace execution=%d node=%s stage=%s",
		event.ExecutionID, event.NodeName, event.Stage,
	))
}

func (module *BattleBlueprintModule) OnInit() error {
	var current blueprintmodule.Config
	if err := module.GetServiceConfigStrict("blueprint", &current); err != nil {
		return err
	}
	if err := module.Setup(current, blueprintmodule.WithTraceLogger(&battleTraceLogger{logger: module.Logger()})); err != nil {
		return err
	}
	module.players = map[int64]int64{1001: 100}
	return module.RegisterNodes(
		func() blueprintmodule.IExecNode { return &battleEntranceNode{} },
		func() blueprintmodule.IExecNode { return &applyDamageNode{module: module} },
		func() blueprintmodule.IExecNode { return &awaitRewardNode{module: module} },
		func() blueprintmodule.IExecNode { return &returnHPNode{module: module} },
	)
}

func (module *BattleBlueprintModule) OnStart(ctx context.Context) error {
	if err := module.Module.OnStart(ctx); err != nil {
		return err
	}
	module.rpc.Start()
	instance, err := module.Create("battle", blueprintmodule.WithKey("battle:1001"))
	if err != nil {
		module.rpc.Stop()
		_ = module.Module.OnStop(ctx)
		return err
	}
	module.instance = instance
	return nil
}

func (module *BattleBlueprintModule) OnStop(ctx context.Context) error {
	module.rpc.Stop()
	if module.instance != nil {
		_ = module.instance.Close()
		module.instance = nil
	}
	return module.Module.OnStop(ctx)
}

// RunDemo 必须由 Service task 调用。Run/Reload 内部使用 Await，等待期间不会占住 Service 执行权。
func (module *BattleBlueprintModule) RunDemo(ctx context.Context) error {
	// Trace 会复制端口值，只围绕一个明确诊断窗口开启，并确保所有返回路径都会关闭。
	if err := module.SetTraceEnabled(true); err != nil {
		return fmt.Errorf("enable trace: %w", err)
	}
	returns, err := module.Run(ctx, "battle", 1)
	disableTraceErr := module.SetTraceEnabled(false)
	if err != nil {
		return fmt.Errorf("temporary Run: %w", err)
	}
	if disableTraceErr != nil {
		return fmt.Errorf("disable trace: %w", disableTraceErr)
	}
	module.Logger().Info(fmt.Sprintf("temporary Run returned HP=%d", firstInt(returns)))

	returns, err = module.instance.Run(ctx, 1)
	if err != nil {
		return fmt.Errorf("instance Run: %w", err)
	}
	module.Logger().Info(fmt.Sprintf("reusable Instance.Run returned HP=%d", firstInt(returns)))

	execution, err := module.instance.Start(ctx, 1)
	if err != nil {
		return fmt.Errorf("instance Start: %w", err)
	}
	if err = execution.OnComplete(func(_ context.Context, result blueprintmodule.PortArray, completionErr error) {
		// 回调已经回到 Service 工作协程，可安全访问 players 等串行业务状态。
		if completionErr != nil {
			module.Logger().Error("blueprint completion failed: " + completionErr.Error())
			return
		}
		module.Logger().Info(fmt.Sprintf("Start/OnComplete returned HP=%d", firstInt(result)))
	}); err != nil {
		execution.Cancel()
		return fmt.Errorf("register completion: %w", err)
	}

	reloaded, err := module.Reload(ctx)
	if err != nil {
		return fmt.Errorf("reload applied=%t: %w", reloaded.Applied, err)
	}
	module.Logger().Info(fmt.Sprintf("reloaded graph_count=%d", reloaded.GraphCount))
	return nil
}

func firstInt(returns blueprintmodule.PortArray) int64 {
	if len(returns) == 0 {
		return 0
	}
	return int64(returns[0].IntVal)
}

type battleEntranceNode struct{ blueprintmodule.BaseExecNode }

func (*battleEntranceNode) GetName() string    { return "BattleEntrance" }
func (*battleEntranceNode) Exec() (int, error) { return 0, nil }

type applyDamageNode struct {
	blueprintmodule.BaseExecNode
	module *BattleBlueprintModule
}

func (*applyDamageNode) GetName() string { return "ApplyDamage" }
func (node *applyDamageNode) Exec() (int, error) {
	playerID, ok := node.GetInPortInt(1)
	if !ok {
		return -1, errors.New("ApplyDamage requires player_id")
	}
	damage, ok := node.GetInPortInt(2)
	if !ok || damage < 0 {
		return -1, errors.New("ApplyDamage requires non-negative damage")
	}
	node.module.players[int64(playerID)] -= int64(damage)
	return 0, nil
}

type awaitRewardNode struct {
	blueprintmodule.BaseExecNode
	module *BattleBlueprintModule
}

func (*awaitRewardNode) GetName() string { return "AwaitReward" }
func (node *awaitRewardNode) Exec() (int, error) {
	handle, err := node.Yield(0)
	if err != nil {
		return -1, err
	}
	if err = node.module.rpc.Submit(handle); err != nil {
		return -1, err
	}
	return -1, blueprintmodule.ErrExecutionSuspended
}

type returnHPNode struct {
	blueprintmodule.BaseExecNode
	module *BattleBlueprintModule
}

func (*returnHPNode) GetName() string { return "ReturnHP" }
func (node *returnHPNode) Exec() (int, error) {
	playerID, ok := node.GetInPortInt(1)
	if !ok {
		return -1, errors.New("ReturnHP requires player_id")
	}
	node.GetAndCreateReturnPort().AppendArrayValInt(blueprintmodule.PortInt(node.module.players[int64(playerID)]))
	return 0, nil
}

type BattleService struct {
	service.Service
	blueprints *BattleBlueprintModule
}

func (target *BattleService) OnInit() error {
	target.blueprints = &BattleBlueprintModule{}
	return target.AddModule(target.blueprints)
}

func (target *BattleService) OnStart(context.Context) error {
	if id := target.AfterFunc(100*time.Millisecond, func(ctx context.Context, _ service.TimerID) {
		if err := target.blueprints.RunDemo(ctx); err != nil {
			target.Logger().Error(err.Error())
		}
	}); id == service.InvalidTimerID {
		return errors.New("schedule blueprint demo failed")
	}
	return nil
}

func init() { app.Setup(&BattleService{}) }
func main() { app.Start() }
