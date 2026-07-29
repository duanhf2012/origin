// Package application 负责把配置中的多个 Node 编排为一个进程级生命周期。
package application

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/duanhf2012/origin/v3/command"
	"github.com/duanhf2012/origin/v3/errs"
	"github.com/duanhf2012/origin/v3/internal/bufferpool"
	internaldiscovery "github.com/duanhf2012/origin/v3/internal/discovery"
	originlog "github.com/duanhf2012/origin/v3/log"
	"github.com/duanhf2012/origin/v3/log/zaplog"
	"github.com/duanhf2012/origin/v3/node"
	"github.com/duanhf2012/origin/v3/service"
)

// Application 是当前进程唯一的 Node 编排和资源所有者。
//
// Application、Node 和 Service 都是一次性对象；进入 Stopped 或 Failed 后不能原地重启。
type Application struct {
	options Options
	catalog serviceCatalog

	// state 提供无锁只读快照；生命周期写操作仍由 run/Stop 的互斥路径串行化。
	state atomic.Uint32
	mu    sync.Mutex

	commands     []command.Command
	commandNames map[string]struct{}
	commandRun   bool

	nodes         []*node.Node
	started       []*node.Node
	config        map[string]any
	bufferPool    *bufferpool.Pool
	logRuntime    *originlog.Runtime
	logger        originlog.Logger
	runCancel     context.CancelFunc
	stopRequested bool
	done          chan struct{}
	doneOnce      sync.Once
	lifecycleErr  error
}

// New 创建一个尚未绑定配置和 Service 类型的 Application。
//
// 不传 Options 使用零值默认：启动和停止都没有框架超时，日志使用内置 Zap Handler。
func New(supplied ...Options) *Application {
	instance := &Application{
		commandNames: make(map[string]struct{}),
		logger:       originlog.NewNop(),
		done:         make(chan struct{}),
	}
	instance.state.Store(uint32(StateCreated))

	// 只接受零个或一个 Options，错误保存到目录并在 Start 时统一报告。
	if len(supplied) > 1 {
		instance.catalog.err = errs.NewMessage(
			errs.CodeInvalidArgument,
			"application.New 最多接受一个 Options",
		)
		return instance
	}
	if len(supplied) == 1 {
		instance.options = supplied[0]
	}
	if instance.options.StartTimeout < 0 || instance.options.StopTimeout < 0 {
		instance.catalog.err = errs.NewMessage(
			errs.CodeInvalidArgument,
			"Application 启停超时不能为负数",
		)
	}
	// Timer Options 只在 Application 创建冷路径归一化一次。Node 接收的始终是完整值，
	// 不再维护第二套默认优先级。
	if instance.options.Timer.MaxTimersPerNode == 0 {
		instance.options.Timer.MaxTimersPerNode = DefaultMaxTimersPerNode
	}
	if instance.options.Timer.MaxTimersPerNode < 0 {
		instance.catalog.err = errors.Join(
			instance.catalog.err,
			errs.NewMessage(
				errs.CodeInvalidArgument,
				"Application Timer 最大数量不能为负数",
			),
		)
	}
	if instance.options.Timer.Location == nil {
		instance.options.Timer.Location = time.Local
	}
	return instance
}

// Setup 把一个或多个零值 Service 样本登记为当前 Application 的类型模板。
func (app *Application) Setup(samples ...service.IService) {
	if app == nil {
		return
	}
	app.mu.Lock()
	allowed := app.State() == StateCreated && !app.commandRun
	app.mu.Unlock()
	if !allowed {
		app.catalog.recordError(errs.NewMessage(
			errs.CodeInvalidArgument,
			"Setup 只能在 Application 创建后、执行命令前调用",
		))
		return
	}
	app.catalog.setup(samples...)
}

// RegisterCommand 在首次执行命令前登记一个 M4 离线自定义命令。
func (app *Application) RegisterCommand(custom command.Command) error {
	if app == nil {
		return errs.NewMessage(errs.CodeInvalidArgument, "Application 不能为空")
	}
	app.mu.Lock()
	defer app.mu.Unlock()
	if app.commandRun {
		return errs.NewMessage(
			errs.CodeInvalidArgument,
			"Application 开始执行命令后不能继续注册命令",
		)
	}
	if _, exists := app.commandNames[custom.Name]; exists {
		return errs.NewMessage(
			errs.CodeInvalidArgument,
			fmt.Sprintf("自定义命令 %q 重复", custom.Name),
		)
	}

	// 借用 M4 Runner 的单一校验规则，避免 Application 复制命名和保留字逻辑。
	validator, err := command.New(command.Options{
		Start: func(context.Context, command.StartRequest) error { return nil },
	})
	if err != nil {
		return err
	}
	if err := validator.Register(custom); err != nil {
		return err
	}
	app.commands = append(app.commands, custom)
	app.commandNames[custom.Name] = struct{}{}
	return nil
}

// State 返回 Application 当前生命周期状态的无锁快照。
func (app *Application) State() State {
	if app == nil {
		return StateFailed
	}
	return State(app.state.Load())
}

// Node 按 NodeID 查询当前运行快照中的 Node。
func (app *Application) Node(id string) (*node.Node, bool) {
	if app == nil || id == "" {
		return nil, false
	}
	app.mu.Lock()
	defer app.mu.Unlock()
	for _, current := range app.nodes {
		if current.ID() == id {
			return current, true
		}
	}
	return nil, false
}

// Nodes 返回按实际启动声明顺序排列的独立 Slice 快照。
func (app *Application) Nodes() []*node.Node {
	if app == nil {
		return nil
	}
	app.mu.Lock()
	defer app.mu.Unlock()
	return append([]*node.Node(nil), app.nodes...)
}

// Logger 返回 Application 根 Logger；日志初始化前返回安全的 Nop Logger。
func (app *Application) Logger() originlog.Logger {
	if app == nil {
		return originlog.NewNop()
	}
	app.mu.Lock()
	defer app.mu.Unlock()
	return app.logger
}

// Start 解析当前进程参数并同步运行 Application。
//
// 这是框架唯一允许结束进程的公共入口：内部命令、生命周期和日志资源全部清理完成后，
// 非零退出码才调用 os.Exit；测试通过 execute 直接验证而不结束测试进程。
func (app *Application) Start() {
	code, err := app.execute(
		context.Background(),
		os.Args[1:],
		command.Options{},
	)
	if pending := unreportedError(err); pending != nil {
		// 日志尚未建立、命令解析失败或日志关闭失败时使用 stderr；已经写入日志的分支
		// 会从聚合错误树中剔除，避免同一生命周期错误打印两次。
		_, _ = fmt.Fprintln(os.Stderr, pending)
	}
	if code != command.ExitSuccess {
		os.Exit(int(code))
	}
}

// execute 建立 M4 Runner，并把 start 命令转交给 Application 私有生命周期。
func (app *Application) execute(
	ctx context.Context,
	args []string,
	ioOptions command.Options,
) (command.ExitCode, error) {
	if app == nil {
		return command.ExitUsage, errs.NewMessage(
			errs.CodeInvalidArgument,
			"Application 不能为空",
		)
	}
	app.mu.Lock()
	if app.commandRun {
		app.mu.Unlock()
		return command.ExitUsage, errs.NewMessage(
			errs.CodeInvalidArgument,
			"Application 只能执行一次命令",
		)
	}
	app.commandRun = true
	commands := append([]command.Command(nil), app.commands...)
	app.mu.Unlock()

	// 调用方可注入测试 IO；Start Handler 必须由 Application 自身覆盖。
	ioOptions.Start = app.run
	runner, err := command.New(ioOptions)
	if err != nil {
		return command.ExitUsage, err
	}
	for _, custom := range commands {
		if err := runner.Register(custom); err != nil {
			return command.ExitUsage, err
		}
	}
	return runner.Run(ctx, args)
}

// run 是 start 命令持有 PID 运行权期间的唯一 Application 生命周期控制路径。
func (app *Application) run(
	runCtx context.Context,
	request command.StartRequest,
) (result error) {
	app.mu.Lock()
	if app.State() != StateCreated {
		app.mu.Unlock()
		return errs.NewMessage(
			errs.CodeInvalidArgument,
			"Application 只能从 Created 状态启动",
		)
	}
	app.state.Store(uint32(StateStarting))
	app.mu.Unlock()

	// 无论失败发生在配置、日志还是 Service 阶段，都保存一次最终状态并唤醒 Stop。
	defer func() {
		result = errors.Join(result, app.closeResources())
		if result != nil && app.State() != StateStopped {
			app.state.Store(uint32(StateFailed))
		}
		app.finish(result)
	}()
	if err := app.catalog.freeze(); err != nil {
		return err
	}
	configured, err := loadConfig(request.ConfigDir)
	if err != nil {
		return err
	}
	if err := app.initializeResources(configured, request.AppName); err != nil {
		return err
	}

	selected, err := selectNodes(configured.nodes, request.NodeIDs)
	if err != nil {
		return app.report(err)
	}
	// M14 过渡数据源只覆盖本次实际启动的 Node；每个 Node 仍建立自己的可见目录和 TCP。
	discoverySource := internaldiscovery.NewSource()
	nodes, err := app.buildNodes(selected, discoverySource)
	if err != nil {
		return app.report(err)
	}
	app.mu.Lock()
	app.nodes = nodes
	app.mu.Unlock()

	lifecycleCtx, lifecycleCancel := context.WithCancel(runCtx)
	app.mu.Lock()
	app.runCancel = lifecycleCancel
	stopRequested := app.stopRequested
	app.mu.Unlock()
	defer lifecycleCancel()
	if stopRequested {
		// Stop 可能发生在配置加载阶段；Context 一建立就兑现此前的停止请求。
		lifecycleCancel()
	}
	app.logger.Info("application starting")

	startCtx := lifecycleCtx
	startCancel := func() {}
	if app.options.StartTimeout > 0 {
		startCtx, startCancel = context.WithTimeout(
			lifecycleCtx,
			app.options.StartTimeout,
		)
	}
	err = app.startNodes(startCtx, nodes)
	startCancel()
	if err != nil {
		return app.rollbackStartup(err)
	}

	app.state.Store(uint32(StateRunning))
	app.logger.Info("application running")
	<-lifecycleCtx.Done()
	return app.stopStartedNodes()
}

// Stop 请求当前 Application 停止，并等待唯一生命周期路径完成清理。
func (app *Application) Stop(ctx context.Context) error {
	if app == nil || ctx == nil {
		return errs.NewMessage(errs.CodeInvalidArgument, "Application 和 Context 不能为空")
	}
	app.mu.Lock()
	state := app.State()
	if state == StateCreated {
		app.state.Store(uint32(StateStopped))
		app.doneOnce.Do(func() { close(app.done) })
		app.mu.Unlock()
		return nil
	}
	if state == StateStopped || state == StateFailed {
		app.mu.Unlock()
		// 原始 Start 调用方已经取得失败结果；完成回滚后的重复 Stop 保持幂等。
		return nil
	}
	cancel := app.runCancel
	if cancel == nil {
		// 配置加载期间尚未建立运行 Context，先记录请求，避免丢失并发 Stop。
		app.stopRequested = true
	}
	done := app.done
	app.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	select {
	case <-done:
		app.mu.Lock()
		result := app.lifecycleErr
		app.mu.Unlock()
		return result
	case <-ctx.Done():
		return errs.Wrap(errs.CodeOf(ctx.Err()), ctx.Err())
	}
}

// initializeResources 创建 Application 唯一日志 Runtime 和共享 BufferPool。
func (app *Application) initializeResources(
	configured loadedConfig,
	appName string,
) error {
	factory := app.options.LogHandlerFactory
	if factory == nil {
		factory = func(config originlog.Config) (originlog.Handler, error) {
			// 默认适配器不传 Zap 扩展选项，保持公开工厂签名足够简单。
			return zaplog.NewHandler(config)
		}
	}
	handler, err := factory(configured.log)
	if err != nil {
		return err
	}
	runtime, err := originlog.NewRuntime(configured.log, handler)
	if err != nil {
		// NewRuntime 失败时尚未接管 Handler，调用方负责关闭它。
		return errors.Join(err, handler.Close())
	}
	logger := runtime.Logger().With(originlog.String("app_name", appName))
	pool := bufferpool.NewPool(bufferpool.Options{
		TrackUsage: configured.trackBufferPool,
	})

	app.mu.Lock()
	app.config = configured.root
	app.logRuntime = runtime
	app.logger = logger
	app.bufferPool = pool
	app.mu.Unlock()
	return nil
}

// buildNodes 在启动任何回调前完成全部 Service 实例化和 Runtime 绑定。
func (app *Application) buildNodes(
	configs []node.Config,
	discoverySource *internaldiscovery.Source,
) ([]*node.Node, error) {
	result := make([]*node.Node, 0, len(configs))
	for _, configured := range configs {
		bindings := make([]node.ServiceBinding, 0, len(configured.Services))
		names := make(map[string]struct{}, len(configured.Services))
		for _, declaration := range configured.Services {
			name, template, private, err := parseServiceDeclaration(declaration)
			if err != nil {
				return nil, rollbackBuiltNodes(result, fmt.Errorf(
					"Node %q: %w",
					configured.ID,
					err,
				))
			}
			if _, duplicate := names[name]; duplicate {
				return nil, rollbackBuiltNodes(result, invalidConfigf(
					"Node %q 的 ServiceName %q 规范化后重复",
					configured.ID,
					name,
				))
			}
			instance, err := app.catalog.instantiate(template)
			if err != nil {
				return nil, rollbackBuiltNodes(result, fmt.Errorf(
					"Node %q Service %q: %w",
					configured.ID,
					name,
					err,
				))
			}
			bindings = append(bindings, node.ServiceBinding{
				Name:     name,
				Template: template,
				Private:  private,
				Service:  instance,
			})
			names[name] = struct{}{}
		}
		current, err := node.New(
			configured,
			bindings,
			app.logger,
			node.Options{
				MaxTimersPerNode: app.options.Timer.MaxTimersPerNode,
				TimerLocation:    app.options.Timer.Location,
				BufferPool:       app.bufferPool,
				DiscoverySource:  discoverySource,
			},
		)
		if err != nil {
			return nil, rollbackBuiltNodes(result, err)
		}
		result = append(result, current)
	}
	return result, nil
}

// rollbackBuiltNodes 释放装配阶段已经创建、但尚未启动的 Node 底层资源。
func rollbackBuiltNodes(nodes []*node.Node, primary error) error {
	result := primary
	// Node 尚未执行 OnStart，因此 Rollback 不会触发业务 OnStop，只会反序关闭已创建资源。
	for index := len(nodes) - 1; index >= 0; index-- {
		result = errors.Join(result, nodes[index].Rollback(context.Background()))
	}
	return result
}

// startNodes 按选中顺序启动 Node，并记录真正 Ready 的 Node。
func (app *Application) startNodes(ctx context.Context, nodes []*node.Node) error {
	for _, current := range nodes {
		if err := contextError(ctx); err != nil {
			return err
		}
		if err := current.Start(ctx); err != nil {
			// 失败 Node 仍保留给 rollbackStartup 清理已经进入 OnStart 的 Service。
			app.mu.Lock()
			app.nodes = nodes
			app.mu.Unlock()
			return err
		}
		app.started = append(app.started, current)
	}
	return contextError(ctx)
}

// rollbackStartup 清理失败 Node，再反序停止此前已经 Ready 的 Node。
func (app *Application) rollbackStartup(primary error) error {
	stopCtx, cancel := app.newStopContext()
	defer cancel()
	result := primary

	// buildNodes 会先创建全部选中 Node。启动中途失败时，从最后一个尚未 Ready 的 Node
	// 反序关闭到当前失败 Node，避免失败位置之后尚未启动的 TimerEngine 等资源泄漏。
	for index := len(app.nodes) - 1; index >= len(app.started); index-- {
		result = errors.Join(result, app.nodes[index].Rollback(stopCtx))
	}
	// 已经 Ready 的 Node 再按真实启动顺序严格反序执行完整 Stop。
	for index := len(app.started) - 1; index >= 0; index-- {
		result = errors.Join(result, app.started[index].Stop(stopCtx))
	}
	app.started = app.started[:0]
	app.state.Store(uint32(StateFailed))
	return app.report(result)
}

// stopStartedNodes 使用独立于运行取消信号的 Context 反序停止全部 Ready Node。
func (app *Application) stopStartedNodes() error {
	app.state.Store(uint32(StateStopping))
	app.logger.Info("application stopping")
	stopCtx, cancel := app.newStopContext()
	defer cancel()
	var result error
	for index := len(app.started) - 1; index >= 0; index-- {
		result = errors.Join(result, app.started[index].Stop(stopCtx))
	}
	app.started = app.started[:0]
	if result != nil {
		app.state.Store(uint32(StateFailed))
		return app.report(result)
	}
	app.state.Store(uint32(StateStopped))
	app.logger.Info("application stopped")
	return nil
}

// newStopContext 创建不会继承已取消运行 Context 的停止时间边界。
func (app *Application) newStopContext() (context.Context, context.CancelFunc) {
	if app.options.StopTimeout > 0 {
		return context.WithTimeout(context.Background(), app.options.StopTimeout)
	}
	return context.WithCancel(context.Background())
}

// closeResources 最后关闭日志 Runtime；BufferPool 在 M7 没有后台资源。
func (app *Application) closeResources() error {
	app.mu.Lock()
	runtime := app.logRuntime
	app.mu.Unlock()
	if runtime == nil {
		return nil
	}
	ctx := context.Background()
	cancel := func() {}
	if app.options.StopTimeout > 0 {
		ctx, cancel = context.WithTimeout(ctx, app.options.StopTimeout)
	}
	defer cancel()
	return runtime.Close(ctx)
}

// finish 保存唯一最终结果并唤醒所有 Stop 等待者。
func (app *Application) finish(result error) {
	app.mu.Lock()
	app.lifecycleErr = result
	app.runCancel = nil
	app.mu.Unlock()
	app.doneOnce.Do(func() { close(app.done) })
}

// report 通过结构化日志只报告一次最终生命周期错误。
func (app *Application) report(err error) error {
	if err == nil {
		return nil
	}
	fields := []originlog.Field{
		originlog.Uint32("error_code", uint32(errs.CodeOf(err))),
		originlog.Err(err),
	}
	var located interface {
		LifecycleContext() (nodeID, serviceName, phase string)
	}
	if errors.As(err, &located) {
		nodeID, serviceName, phase := located.LifecycleContext()
		fields = append(fields,
			originlog.String("node_id", nodeID),
			originlog.String("service_name", serviceName),
			originlog.String("lifecycle_phase", phase),
		)
	}
	var panicked interface{ PanicStack() string }
	if errors.As(err, &panicked) && panicked.PanicStack() != "" {
		app.logger.ErrorStack("application lifecycle failed", fields...)
	} else {
		app.logger.Error("application lifecycle failed", fields...)
	}
	return reportedError{cause: err}
}

// selectNodes 按命令行顺序选择 Node；空参数保持配置顺序。
func selectNodes(configured []node.Config, requested []string) ([]node.Config, error) {
	if len(requested) == 0 {
		return append([]node.Config(nil), configured...), nil
	}
	available := make(map[string]node.Config, len(configured))
	for _, current := range configured {
		available[current.ID] = current
	}
	result := make([]node.Config, 0, len(requested))
	seen := make(map[string]struct{}, len(requested))
	for _, id := range requested {
		if _, duplicate := seen[id]; duplicate {
			return nil, invalidConfigf("启动参数中的 NodeID %q 重复", id)
		}
		current, exists := available[id]
		if !exists {
			return nil, invalidConfigf("启动参数中的 NodeID %q 不存在", id)
		}
		result = append(result, current)
		seen[id] = struct{}{}
	}
	return result, nil
}

// parseServiceDeclaration 解析普通、私有、模板和私有模板四种稳定外观。
func parseServiceDeclaration(value string) (name, template string, private bool, err error) {
	value = strings.TrimSpace(value)
	if value == "" || value == "_" || strings.Count(value, ":") > 1 {
		return "", "", false, invalidConfigf("Service 声明 %q 无效", value)
	}
	parts := strings.Split(value, ":")
	actual := strings.TrimSpace(parts[0])
	if strings.HasPrefix(actual, "_") {
		private = true
		actual = strings.TrimPrefix(actual, "_")
	}
	if actual == "" {
		return "", "", false, invalidConfigf("Service 声明 %q 的实际名称为空", value)
	}
	template = actual
	if len(parts) == 2 {
		template = strings.TrimSpace(parts[1])
		if template == "" {
			return "", "", false, invalidConfigf("Service 声明 %q 的模板名为空", value)
		}
	}
	return actual, template, private, nil
}

// contextError 把 Context 原因稳定映射到 Origin 错误码。
func contextError(ctx context.Context) error {
	if ctx == nil || ctx.Err() == nil {
		return nil
	}
	return errs.Wrap(errs.CodeOf(ctx.Err()), ctx.Err())
}

// reportedError 标记错误已经写入日志，避免 Start 再向 stderr 重复输出。
type reportedError struct {
	cause error
}

func (failure reportedError) Error() string { return failure.cause.Error() }
func (failure reportedError) Unwrap() error { return failure.cause }

// unreportedError 从 errors.Join 聚合树中剔除已经写入日志的错误分支。
func unreportedError(err error) error {
	if err == nil {
		return nil
	}
	if _, ok := err.(reportedError); ok {
		return nil
	}
	if joined, ok := err.(interface{ Unwrap() []error }); ok {
		branches := joined.Unwrap()
		pending := make([]error, 0, len(branches))
		for _, branch := range branches {
			if remainder := unreportedError(branch); remainder != nil {
				pending = append(pending, remainder)
			}
		}
		return errors.Join(pending...)
	}
	if wrapped, ok := err.(interface{ Unwrap() error }); ok {
		if unreportedError(wrapped.Unwrap()) == nil {
			return nil
		}
	}
	return err
}
