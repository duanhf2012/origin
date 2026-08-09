// 本示例展示 Service 自定义 Admin GET/POST Endpoint，以及它们与业务任务共用的串行槽。
package main

import (
	"context"
	"fmt"
	"net/http"

	"github.com/duanhf2012/origin/v3/admin"
	"github.com/duanhf2012/origin/v3/application"
	"github.com/duanhf2012/origin/v3/errs"
	"github.com/duanhf2012/origin/v3/service"
)

// app 是当前示例唯一的 Application。
var app = application.New()

// logicSummary 是 summary Endpoint 返回的稳定业务视图。
type logicSummary struct {
	Version   string `json:"version"`
	Reloads   int    `json:"reloads"`
	Refreshes int    `json:"refreshes"`
}

// reloadLogicRequest 是 reload-logic 唯一接受的 JSON 结构；未知字段会被 DecodeJSON 拒绝。
type reloadLogicRequest struct {
	Version string `json:"version"`
}

// refreshPlayerRequest 是异步刷新通知的输入。
type refreshPlayerRequest struct {
	PlayerID string `json:"player_id"`
}

// LogicService 把版本、计数和三个 Endpoint 都归到同一个 Service 实例。
type LogicService struct {
	service.Service
	version           string
	reloads           int
	refreshes         int
	loadLogic         func(context.Context, string) (string, error)
	onPlayerRefreshed func(string)
}

// NewLogicService 创建可独立测试的示例 Service；正式运行实例也会在 OnInit 补齐同样默认值。
func NewLogicService() *LogicService {
	target := &LogicService{}
	target.initializeDefaults()
	return target
}

// OnInit 在 Service 进入运行期前补齐实例自己的初始状态，不使用包级可变数据。
func (target *LogicService) OnInit() error {
	target.initializeDefaults()
	return nil
}

// initializeDefaults 只补零值，便于测试在启动前注入受控加载器和通知观察器。
func (target *LogicService) initializeDefaults() {
	if target.version == "" {
		target.version = "v1"
	}
	if target.loadLogic == nil {
		target.loadLogic = func(_ context.Context, version string) (string, error) {
			return version, nil
		}
	}
	if target.onPlayerRefreshed == nil {
		target.onPlayerRefreshed = func(string) {}
	}
}

// AdminEndpoints 返回只在 Application 启动冷路径收集一次的不可变描述符。
func (target *LogicService) AdminEndpoints() []admin.Endpoint {
	return []admin.Endpoint{
		admin.Get("summary", target.getSummary),
		admin.Post("reload-logic", target.reloadLogic),
		admin.Post(
			"refresh-player",
			target.refreshPlayer,
			admin.WithSuccessStatus(http.StatusAccepted),
		),
	}
}

// getSummary 在 Service 串行槽内读取普通字段，因此不需要额外的锁或原子操作。
func (target *LogicService) getSummary(
	_ context.Context,
	_ admin.Request,
) (admin.Response, error) {
	return admin.JSON(http.StatusOK, logicSummary{
		Version:   target.version,
		Reloads:   target.reloads,
		Refreshes: target.refreshes,
	})
}

// reloadLogic 严格解码输入，在 Await 返回后才把局部加载结果提交到 Service 状态。
func (target *LogicService) reloadLogic(
	ctx context.Context,
	request admin.Request,
) (admin.Response, error) {
	var input reloadLogicRequest
	if err := request.DecodeJSON(&input); err != nil {
		return admin.Response{}, err
	}
	if input.Version == "" {
		return admin.Response{}, errs.NewMessage(errs.CodeInvalidArgument, "version 不能为空")
	}

	// Await 可能暂时释放 Service 执行权。回调只写局部变量，绝不在等待阶段触碰
	// target.version 等 Service 字段；恢复串行执行权后再一次性提交。
	loader := target.loadLogic
	var loadedVersion string
	if err := target.Await(ctx, func(waitCtx context.Context) error {
		var loadErr error
		loadedVersion, loadErr = loader(waitCtx, input.Version)
		return loadErr
	}); err != nil {
		return admin.Response{}, err
	}
	target.version = loadedVersion
	target.reloads++
	target.Logger().Info(fmt.Sprintf("logic reloaded: version=%s", loadedVersion))
	return admin.Empty(http.StatusNoContent), nil
}

// refreshPlayer 把后续工作投递回同一有界队列；202 只表示通知已接受，不表示已经完成。
func (target *LogicService) refreshPlayer(
	_ context.Context,
	request admin.Request,
) (admin.Response, error) {
	var input refreshPlayerRequest
	if err := request.DecodeJSON(&input); err != nil {
		return admin.Response{}, err
	}
	if input.PlayerID == "" {
		return admin.Response{}, errs.NewMessage(errs.CodeInvalidArgument, "player_id 不能为空")
	}
	observer := target.onPlayerRefreshed
	if err := target.DispatchAsync(func(context.Context) {
		target.refreshes++
		observer(input.PlayerID)
		target.Logger().Info(fmt.Sprintf("player refreshed: player_id=%s", input.PlayerID))
	}); err != nil {
		return admin.Response{}, err
	}
	return admin.Empty(http.StatusAccepted), nil
}

// init 只把 Service 类型安装到当前示例 Application；不会创建运行实例或启动 goroutine。
func init() { app.Setup(&LogicService{}) }

// main 交给 Application 解析 start、--admin 等命令行参数并管理完整生命周期。
func main() { app.Start() }
