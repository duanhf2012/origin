// Package application 负责把配置中的多个 Node 编排为一个进程级生命周期。
package application

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/duanhf2012/origin/v3/command"
	originconfig "github.com/duanhf2012/origin/v3/config"
	publicprovider "github.com/duanhf2012/origin/v3/discovery/provider"
	"github.com/duanhf2012/origin/v3/errs"
	"github.com/duanhf2012/origin/v3/internal/bufferpool"
	etcddiscovery "github.com/duanhf2012/origin/v3/internal/discovery/etcd"
	origindiscovery "github.com/duanhf2012/origin/v3/internal/discovery/origin"
	originlog "github.com/duanhf2012/origin/v3/log"
	"github.com/duanhf2012/origin/v3/log/zaplog"
	"github.com/duanhf2012/origin/v3/node"
	"github.com/duanhf2012/origin/v3/rpc"
	"github.com/duanhf2012/origin/v3/service"
)

// Application 是当前进程唯一的 Node 编排和资源所有者。
//
// Application、Node 和 Service 都是一次性对象；进入 Stopped 或 Failed 后不能原地重启。
type Application struct {
	options Options
	catalog serviceCatalog
	adminState

	// state 提供无锁只读快照；生命周期写操作仍由 run/Stop 的互斥路径串行化。
	state atomic.Uint32
	mu    sync.Mutex

	commands     []command.Command
	commandNames map[string]struct{}
	commandRun   bool
	providers    map[string]publicprovider.Factory
	// appName 和 startedAt 在首次 start 冷路径写入，供停止后的最终诊断继续读取。
	appName   string
	startedAt time.Time

	nodes         []*node.Node
	started       []*node.Node
	config        *originconfig.Snapshot
	bufferPool    *bufferpool.Pool
	logRuntime    *originlog.Runtime
	crashOutput   *originlog.CrashOutput
	logger        originlog.Logger
	runCancel     context.CancelFunc
	stopRequested bool
	done          chan struct{}
	doneOnce      sync.Once
	lifecycleErr  error
	// serviceFailures 按首次报告顺序保存运行期真正隔离的 Service。
	//
	// Transport 恢复不写入本列表，也不取消 Application。正式 Stop 完成后，列表中的稳定
	// 摘要才参与最终 errors.Join，避免局部 Service 故障被清理成功掩盖。
	serviceFailures []error
	// resourcesReady/resourcesClosing 把运行时 HTTP Start 与最终资源清理线性化。
	resourcesReady   bool
	resourcesClosing bool
	// Admin 与 pprof 使用独立 Listener、ServeMux、状态锁和 goroutine 所有权。
	pprofHTTP httpRuntime
}

// New 创建一个尚未绑定配置和 Service 类型的 Application。
//
// 不传 Options 使用零值默认：启动和停止都没有框架超时，日志使用内置 Zap Handler。
func New(supplied ...Options) *Application {
	instance := &Application{
		commandNames: make(map[string]struct{}),
		providers:    make(map[string]publicprovider.Factory),
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

// RegisterDiscoveryProvider 为当前 Application 登记一个自定义服务发现 Provider。
//
// 注册必须发生在首次执行命令前；内置 origin/etcd 名称不能覆盖。
func (app *Application) RegisterDiscoveryProvider(
	name string,
	factory publicprovider.Factory,
) error {
	if app == nil || factory == nil {
		return errs.NewMessage(
			errs.CodeInvalidArgument,
			"Application 和 Discovery Provider Factory 不能为空",
		)
	}
	name = strings.TrimSpace(name)
	if !validProviderName(name) {
		return errs.NewMessage(
			errs.CodeInvalidArgument,
			"Discovery Provider 名称必须是 63 字节以内的小写 kebab-case",
		)
	}
	if name == "origin" || name == "etcd" {
		return errs.NewMessage(
			errs.CodeInvalidArgument,
			fmt.Sprintf("Discovery Provider 名称 %q 由框架保留", name),
		)
	}
	app.mu.Lock()
	defer app.mu.Unlock()
	if app.commandRun || app.State() != StateCreated {
		return errs.NewMessage(
			errs.CodeInvalidArgument,
			"Discovery Provider 只能在配置加载或启动前注册",
		)
	}
	if _, exists := app.providers[name]; exists {
		return errs.NewMessage(
			errs.CodeInvalidArgument,
			fmt.Sprintf("Discovery Provider %q 重复", name),
		)
	}
	app.providers[name] = factory
	return nil
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
//
// Deprecated: 业务普通日志使用 log.Xxx，Service 与 Module 使用各自的 Logger。该方法仅为
// v3.0 源码兼容保留，并将在下一主版本删除。
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
	// cleanupCtx 是当前 Application 整次收尾唯一的时间预算。只有真正进入清理时才惰性
	// 创建，随后 Node、Service、Transport、Buffer 诊断、Crash 和日志全部共享剩余时间。
	var cleanupCtx context.Context
	var cleanupCancel context.CancelFunc
	ensureCleanupContext := func() context.Context {
		if cleanupCtx == nil {
			cleanupCtx, cleanupCancel = app.newStopContext()
		}
		return cleanupCtx
	}

	app.mu.Lock()
	if app.State() != StateCreated {
		app.mu.Unlock()
		return errs.NewMessage(
			errs.CodeInvalidArgument,
			"Application 只能从 Created 状态启动",
		)
	}
	app.state.Store(uint32(StateStarting))
	app.startedAt = time.Now()
	app.mu.Unlock()

	// 无论失败发生在配置、日志还是 Service 阶段，都保存一次最终状态并唤醒 Stop。
	defer func() {
		// 即使配置或日志初始化失败，也只建立这一份清理 Context；没有对应资源的关闭操作
		// 保持幂等成功。
		result = errors.Join(result, app.closeResources(ensureCleanupContext()))
		if cleanupCancel != nil {
			cleanupCancel()
		}
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
	if err := validateSelectedDiscoveryOrder(configured.discovery, selected); err != nil {
		return app.report(err)
	}
	nodes, err := app.buildNodes(selected, configured.discovery)
	if err != nil {
		return app.report(err)
	}
	app.mu.Lock()
	app.nodes = nodes
	app.mu.Unlock()
	// Admin Provider 必须绑定真实 Service 实例，并在任何 OnInit 前一次冻结。命令行只决定
	// Admin/pprof Listener 的初始状态；运行中仍可通过公开 API 独立关闭或重新开启。
	if err := app.freezeAdminRoutes(nodes); err != nil {
		return app.rollbackStartup(ensureCleanupContext(), err, false)
	}
	if request.AdminAddress != "" {
		if err := app.StartAdminServer(request.AdminAddress); err != nil {
			return app.rollbackStartup(ensureCleanupContext(), err, false)
		}
	}
	if request.PprofAddress != "" {
		if err := app.StartPprof(request.PprofAddress); err != nil {
			return app.rollbackStartup(ensureCleanupContext(), err, false)
		}
	}

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
		// 明确 Stop/信号在启动期间到达时，context.Canceled 只是停止意图而不是启动故障。
		// StartTimeout 的 DeadlineExceeded 和同时存在的业务错误仍保留非零结果。
		stopDuringStartup := errors.Is(lifecycleCtx.Err(), context.Canceled) &&
			(errors.Is(err, context.Canceled) ||
				errs.IsCode(err, errs.CodeCanceled))
		if stopDuringStartup {
			return app.rollbackStartup(ensureCleanupContext(), nil, true)
		}
		return app.rollbackStartup(ensureCleanupContext(), err, false)
	}

	app.state.Store(uint32(StateRunning))
	app.logger.Info("application running")
	controls := request.Controls
	for lifecycleCtx.Err() == nil {
		select {
		case <-lifecycleCtx.Done():
		case control, open := <-controls:
			if !open {
				controls = nil
				continue
			}
			if control == nil {
				continue
			}
			if lifecycleCtx.Err() != nil {
				control.Complete(errs.ErrServiceStopping)
				continue
			}
			control.Complete(app.handleControlRequest(lifecycleCtx, control))
		}
	}
	stopErr := app.stopStartedNodes(ensureCleanupContext())
	serviceFailures := app.serviceFailureResult()
	finalResult := stopErr
	if finalResult == nil {
		// 正常 Scheduler Failed 清理会把同一根因随 Node.Stop 返回。该兜底只覆盖未来某个
		// 隔离适配器完成清理却没有返回根因的情况，避免同一 Service 在 errors.Join 中重复。
		finalResult = serviceFailures
	}
	if finalResult == nil {
		return nil
	}
	app.state.Store(uint32(StateFailed))
	return app.report(finalResult)
}

// validateSelectedDiscoveryOrder 保持同进程共置发现端先启动、最后停止的显式顺序。
func validateSelectedDiscoveryOrder(
	selection *discoverySelection,
	selected []node.Config,
) error {
	if selection == nil || selection.kind != "origin" || len(selected) < 2 {
		return nil
	}
	config, err := origindiscovery.DecodeConfig(selection.config)
	if err != nil {
		return err
	}
	serverIndex := -1
	for index := range selected {
		if selected[index].ID == config.Server.Node {
			serverIndex = index
			break
		}
	}
	if serverIndex > 0 {
		return invalidConfigf(
			"同一进程选择多个 Node 时，DiscoveryService Node %q 必须位于显式启动顺序第一位",
			config.Server.Node,
		)
	}
	return nil
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
	// 每个 Application 在创建 Handler 和 Crash 输出前派生活动路径；归档 Writer 会自然使用
	// 同一 stem，因此活动、归档和 Crash 三类文件不会与复用配置的其他进程冲突。
	logConfig := configured.log
	if logConfig.File.Enabled {
		logConfig.File.Path = applicationLogPath(appName, logConfig.File.Path)
	}
	factory := app.options.LogHandlerFactory
	if factory == nil {
		factory = func(config originlog.Config) (originlog.Handler, error) {
			// 默认适配器不传 Zap 扩展选项，保持公开工厂签名足够简单。
			return zaplog.NewHandler(config)
		}
	}
	handler, err := factory(logConfig)
	if err != nil {
		return err
	}
	runtime, err := originlog.NewRuntime(logConfig, handler)
	if err != nil {
		// NewRuntime 失败时尚未接管 Handler，调用方负责关闭它。
		return errors.Join(err, handler.Close())
	}
	// app_name 只用于文件路径派生，不进入日志内容；根 Logger 因此不预绑定归属字段。
	logger := runtime.Logger()
	pool := bufferpool.NewPool(bufferpool.Options{
		TrackUsage: configured.trackBufferPool,
	})

	app.mu.Lock()
	app.config = configured.root
	app.appName = appName
	app.logRuntime = runtime
	app.logger = logger
	app.bufferPool = pool
	app.resourcesReady = true
	app.resourcesClosing = false
	app.mu.Unlock()
	// 资源字段完整发布后再安装进程默认入口，确保任何 Service 生命周期中的 log.Xxx 都复用
	// 当前 Application 的唯一 Runtime。Runtime.Close 会按所有者清理，旧实例不能误删新值。
	originlog.SetDefault(logger)

	// 文件日志启用时同时安装 Go 进程级 Crash 输出。它独立于异步日志队列，因此即使进程
	// 遭遇未恢复 panic，runtime 仍可把现场直接写入同目录的 .crash.log。
	if logConfig.File.Enabled {
		crashOutput, crashErr := originlog.InstallCrashOutput(logConfig.File)
		if crashErr != nil {
			return crashErr
		}
		app.mu.Lock()
		app.crashOutput = crashOutput
		app.mu.Unlock()
	}
	return nil
}

// applicationLogPath 把 Application 名称作为活动文件 basename 前缀，并保持重复调用幂等。
func applicationLogPath(appName, configuredPath string) string {
	cleaned := filepath.Clean(configuredPath)
	if appName == "" {
		return cleaned
	}
	directory := filepath.Dir(cleaned)
	base := filepath.Base(cleaned)
	prefix := appName + "-"
	if strings.HasPrefix(base, prefix) {
		return cleaned
	}
	return filepath.Join(directory, prefix+base)
}

// buildNodes 在启动任何回调前完成全部 Service 实例化和 Runtime 绑定。
func (app *Application) buildNodes(
	configs []node.Config,
	discovery *discoverySelection,
) ([]*node.Node, error) {
	var factory publicprovider.Factory
	var originConfig origindiscovery.Config
	var originSystemTarget rpc.SystemTarget
	var discoveryKind string
	var discoveryConfig publicprovider.Config
	if discovery != nil {
		discoveryKind = discovery.kind
		discoveryConfig = discovery.config
		switch discovery.kind {
		case "origin":
			var err error
			originConfig, err = origindiscovery.DecodeConfig(discovery.config)
			if err != nil {
				return nil, err
			}
			for _, configured := range configs {
				if configured.ID != originConfig.Server.Node {
					continue
				}
				if configured.RPC == nil {
					return nil, invalidConfigf("使用 discovery.origin 时必须配置顶层 rpc")
				}
				originSystemTarget.NodeID = configured.ID
				if configured.RPC.Transport == rpc.TransportTCP {
					originSystemTarget.Address = configured.RPC.TCP.Advertise
				}
				break
			}
			if originSystemTarget.NodeID == "" {
				return nil, invalidConfigf(
					"discovery.origin.server.node 必须存在并包含唯一 DiscoveryService",
				)
			}
		case "etcd":
			factory = etcddiscovery.NewFactory(discovery.configRoot)
		default:
			app.mu.Lock()
			factory = app.providers[discovery.kind]
			app.mu.Unlock()
			if factory == nil {
				return nil, invalidConfigf(
					"Discovery Provider %q 未注册",
					discovery.kind,
				)
			}
		}
	}
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
			var instance service.IService
			if template == "DiscoveryService" {
				if discovery == nil || discovery.kind != "origin" ||
					name != "DiscoveryService" || private ||
					configured.ID != originConfig.Server.Node {
					return nil, rollbackBuiltNodes(result, invalidConfigf(
						"DiscoveryService 只能以公开原名配置在 discovery.origin.server.node",
					))
				}
				instance = origindiscovery.NewService(
					originConfig,
					app.bufferPool,
					app.logger.WithScope(configured.ID, "DiscoveryService"),
				)
			} else {
				var err error
				instance, err = app.catalog.instantiate(template)
				if err != nil {
					return nil, rollbackBuiltNodes(result, fmt.Errorf(
						"Node %q Service %q: %w",
						configured.ID,
						name,
						err,
					))
				}
			}
			bindings = append(bindings, node.ServiceBinding{
				Name:     name,
				Template: template,
				// DiscoveryService 是框架基础设施：它参与 Node 生命周期，
				// 但不能作为业务 RPC Service 发布给其他 Node。
				Private: private || template == "DiscoveryService",
				Service: instance,
			})
			names[name] = struct{}{}
		}
		current, err := node.New(
			configured,
			bindings,
			app.logger,
			node.Options{
				Application:           app,
				Config:                app.config,
				MaxTimersPerNode:      app.options.Timer.MaxTimersPerNode,
				TimerLocation:         app.options.Timer.Location,
				BufferPool:            app.bufferPool,
				DiscoveryKind:         discoveryKind,
				DiscoveryConfig:       discoveryConfig,
				DiscoveryFactory:      factory,
				DiscoverySystemTarget: originSystemTarget,
				ServiceFailure:        app.handleServiceFailure,
			},
		)
		if err != nil {
			return nil, rollbackBuiltNodes(result, err)
		}
		result = append(result, current)
	}
	return result, nil
}

// handleServiceFailure 保存单个运行期 Failed Service 的稳定摘要。
//
// 该回调只走故障冷路径，不取消 Application，也不直接执行 Stop。Node 已经隔离并撤销该
// Service；这里保留最终退出结果需要的证据，同时让同进程其他 Service 继续提供服务。
func (app *Application) handleServiceFailure(
	nodeID string,
	serviceName string,
	cause error,
) {
	if app == nil || nodeID == "" || serviceName == "" || cause == nil {
		return
	}
	wrapped := errs.Wrap(
		errs.CodeServiceFailed,
		fmt.Errorf(
			"Node %q Service %q 运行期隔离: %w",
			nodeID,
			serviceName,
			cause,
		),
	)
	app.mu.Lock()
	app.serviceFailures = append(app.serviceFailures, wrapped)
	logger := app.logger
	app.mu.Unlock()
	logger.WithScope(nodeID, serviceName).Error(
		"service entered failed state",
		originlog.Uint32("error_code", uint32(errs.CodeServiceFailed)),
		originlog.Err(cause),
	)
}

// serviceFailureResult 复制当前不可恢复 Service 摘要并按首次报告顺序聚合。
func (app *Application) serviceFailureResult() error {
	if app == nil {
		return nil
	}
	app.mu.Lock()
	failures := append([]error(nil), app.serviceFailures...)
	app.mu.Unlock()
	return errors.Join(failures...)
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
func (app *Application) rollbackStartup(
	stopCtx context.Context,
	primary error,
	stopRequested bool,
) error {
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
	if stopRequested && result == nil {
		app.state.Store(uint32(StateStopped))
		app.logger.Info("application stopped during startup")
		return nil
	}
	app.state.Store(uint32(StateFailed))
	return app.report(result)
}

// stopStartedNodes 使用独立于运行取消信号的 Context 反序停止全部 Ready Node。
func (app *Application) stopStartedNodes(stopCtx context.Context) error {
	app.state.Store(uint32(StateStopping))
	app.logger.Info("application stopping")
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

// closeResources 使用总体停止 Context 完成 Admin、pprof、Buffer 诊断、Crash 注销和日志关闭。
func (app *Application) closeResources(ctx context.Context) error {
	app.mu.Lock()
	app.resourcesClosing = true
	runtime := app.logRuntime
	crashOutput := app.crashOutput
	pool := app.bufferPool
	logger := app.logger
	app.resourcesReady = false
	app.mu.Unlock()
	// Node Stop/Rollback 已在调用方完成；先关闭 Admin，再关闭 pprof，随后检查 Buffer 并关闭
	// Crash/日志。即使 ctx 已耗尽，httpRuntime.stop 也会强制 Close Listener。
	adminErr := app.adminHTTP.stopWithErrors(ctx, adminHTTPRuntimeErrors())
	pprofErr := app.pprofHTTP.stop(ctx)
	if runtime == nil {
		return errors.Join(adminErr, pprofErr)
	}

	// BufferPool 没有 Close；只有开启统计时才在全部 Node 回收后读取一次最终快照。
	// 非零值表示框架或适配器仍持有 Buffer，记录容量便于定位，但不能跳过后续日志 Flush。
	if stats := pool.Stats(); stats.Enabled && stats.InUseBuffers != 0 {
		logger.Warn(
			"buffer pool contains unreleased buffers",
			originlog.Int64("in_use_buffers", stats.InUseBuffers),
			originlog.Int64("in_use_capacity_bytes", stats.InUseCapacityBytes),
			originlog.Int64("oversize_buffers", stats.OversizeInUse),
		)
	}
	crashErr := crashOutput.Close()
	logErr := runtime.Close(ctx)
	return errors.Join(adminErr, pprofErr, crashErr, logErr)
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
	logger := app.logger
	if errors.As(err, &located) {
		nodeID, serviceName, phase := located.LifecycleContext()
		fields = append(fields, originlog.String("lifecycle_phase", phase))
		// 位置信息属于框架归属字段，必须走 WithScope，不能作为可伪造业务 Field 追加。
		logger = logger.WithScope(nodeID, serviceName)
	}
	var panicked interface{ PanicStack() string }
	if errors.As(err, &panicked) && panicked.PanicStack() != "" {
		logger.ErrorStack("application lifecycle failed", fields...)
	} else {
		logger.Error("application lifecycle failed", fields...)
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
