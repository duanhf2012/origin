package application

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/duanhf2012/origin/v3/command"
	publicprovider "github.com/duanhf2012/origin/v3/discovery/provider"
	"github.com/duanhf2012/origin/v3/errs"
	internaldiscovery "github.com/duanhf2012/origin/v3/internal/discovery"
	originlog "github.com/duanhf2012/origin/v3/log"
	"github.com/duanhf2012/origin/v3/node"
	"github.com/duanhf2012/origin/v3/rpc"
	"github.com/duanhf2012/origin/v3/service"
	"github.com/nats-io/nats-server/v2/server"
)

// lifecycleTestService 允许测试通过 NodeID 制造确定的启动失败。
type lifecycleTestService struct {
	service.Service
	started bool
	stopped bool
}

// globalLogService 验证业务生命周期可以使用进程默认日志外观。
type globalLogService struct {
	service.Service
}

func (*globalLogService) OnInit() error {
	originlog.Info("global log from service init")
	return nil
}

var startupStopEntered chan struct{}

type customDiscoveryProvider struct {
	context publicprovider.Context
}

func (provider *customDiscoveryProvider) Start(context.Context) error {
	if err := provider.context.Host.SetTTL(3 * time.Second); err != nil {
		return err
	}
	if err := provider.context.Host.ReplaceSnapshot(publicprovider.Snapshot{}); err != nil {
		return err
	}
	provider.context.Host.Report(publicprovider.Report{State: publicprovider.StateReady})
	return nil
}

func (*customDiscoveryProvider) Publish(context.Context, publicprovider.Node) error {
	return nil
}

func (*customDiscoveryProvider) Withdraw(context.Context) error { return nil }

func (provider *customDiscoveryProvider) Close(context.Context) error {
	provider.context.Host.Report(publicprovider.Report{State: publicprovider.StateStopped})
	return nil
}

// startupStopService 让测试在 OnStart 内观察正式停止信号。
type startupStopService struct {
	service.Service
	stopped atomic.Bool
}

func (*startupStopService) OnStart(ctx context.Context) error {
	close(startupStopEntered)
	<-ctx.Done()
	return ctx.Err()
}

func (target *startupStopService) OnStop(context.Context) error {
	target.stopped.Store(true)
	return nil
}

func (target *lifecycleTestService) OnInit() error {
	if target.NodeID() == "bad-1" {
		return testInitFailure
	}
	return nil
}

func (target *lifecycleTestService) OnStart(context.Context) error {
	target.started = true
	return nil
}

func (target *lifecycleTestService) OnStop(context.Context) error {
	target.stopped = true
	return nil
}

var testInitFailure = errors.New("test init failure")

// silentHandler 避免单元测试污染控制台，并记录 Runtime 确实完成 Close。
type silentHandler struct {
	closed atomic.Bool
	writes atomic.Uint64
	mu     sync.Mutex
	texts  []string
	fields [][]string
}

func (*silentHandler) Enabled(originlog.Level) bool { return true }
func (handler *silentHandler) Write(record originlog.Record, fields []originlog.Field) error {
	handler.writes.Add(1)
	handler.mu.Lock()
	handler.texts = append(handler.texts, record.Message)
	keys := make([]string, len(fields))
	for index := range fields {
		keys[index] = fields[index].Key()
	}
	handler.fields = append(handler.fields, keys)
	handler.mu.Unlock()
	return nil
}

func (handler *silentHandler) containsField(key string) bool {
	handler.mu.Lock()
	defer handler.mu.Unlock()
	for _, fields := range handler.fields {
		for _, current := range fields {
			if current == key {
				return true
			}
		}
	}
	return false
}
func (*silentHandler) Sync() error { return nil }
func (handler *silentHandler) Close() error {
	handler.closed.Store(true)
	return nil
}

func (handler *silentHandler) contains(message string) bool {
	handler.mu.Lock()
	defer handler.mu.Unlock()
	for _, current := range handler.texts {
		if current == message {
			return true
		}
	}
	return false
}

// TestApplicationInstallsAndClearsDefaultLogger 防止 Service 生命周期中的 log.Info 仍落到 Nop，
// 或 Application 停止后留下已经关闭的进程默认 Logger。
func TestApplicationInstallsAndClearsDefaultLogger(t *testing.T) {
	originlog.SetDefault(originlog.NewNop())
	directory := writeApplicationConfig(t, `
nodes:
  - id: game-1
    services: [globalLogService]
`)
	handler := &silentHandler{}
	app := New(Options{
		LogHandlerFactory: func(originlog.Config) (originlog.Handler, error) {
			return handler, nil
		},
	})
	app.Setup(&globalLogService{})

	runCtx, cancel := context.WithCancel(context.Background())
	result := make(chan error, 1)
	go func() {
		result <- app.run(runCtx, command.StartRequest{
			AppName:   "global-log-test",
			ConfigDir: directory,
		})
	}()
	waitForState(t, app, StateRunning)
	cancel()
	if err := <-result; err != nil {
		t.Fatalf("run() error = %v", err)
	}
	if !handler.contains("global log from service init") {
		t.Fatal("Service OnInit package-level log was not written")
	}
	if handler.containsField("app_name") {
		t.Fatal("app_name leaked into log content instead of remaining a file-name prefix")
	}
	if originlog.Default().Enabled(originlog.InfoLevel) {
		t.Fatal("default logger remains enabled after Application close")
	}
}

func TestServiceFailureDoesNotCancelApplicationAndIsAggregated(t *testing.T) {
	t.Parallel()

	app := New()
	lifecycle, cancel := context.WithCancel(context.Background())
	defer cancel()
	app.mu.Lock()
	app.runCancel = cancel
	app.mu.Unlock()

	first := errors.New("first service failure")
	second := errors.New("second service failure")
	app.handleServiceFailure("game-1", "PlayerService", first)
	app.handleServiceFailure("game-1", "SceneService", second)

	select {
	case <-lifecycle.Done():
		t.Fatal("Service 运行期隔离不应取消 Application 生命周期")
	default:
	}
	recorded := app.serviceFailureResult()
	if !errors.Is(recorded, first) || !errors.Is(recorded, second) {
		t.Fatalf("recorded service failures = %v", recorded)
	}
	if got := errs.CodeOf(recorded); got != errs.CodeServiceFailed {
		t.Fatalf("service failure code = %d", got)
	}
}

func TestApplicationRunsSelectedNodesAndStopsInPlace(t *testing.T) {
	directory := writeApplicationConfig(t, `
buffer_pool:
  track_usage: true
nodes:
  - id: gateway-1
    services:
      - lifecycleTestService
      - scene-1:lifecycleTestService
  - id: ignored-1
    services:
      - lifecycleTestService
`)
	handler := &silentHandler{}
	app := New(Options{
		LogHandlerFactory: func(originlog.Config) (originlog.Handler, error) {
			return handler, nil
		},
	})
	app.Setup(&lifecycleTestService{})

	runCtx, cancel := context.WithCancel(context.Background())
	result := make(chan error, 1)
	go func() {
		result <- app.run(runCtx, command.StartRequest{
			AppName:   "lifecycle-test",
			ConfigDir: directory,
			NodeIDs:   []string{"gateway-1"},
		})
	}()
	waitForState(t, app, StateRunning)

	// 同一模板在一个 Node 中生成两个运行身份不同、地址也不同的实例。
	current, ok := app.Node("gateway-1")
	if !ok {
		t.Fatal("未找到 gateway-1")
	}
	first, ok := current.Service("lifecycleTestService")
	if !ok {
		t.Fatal("未找到普通 Service")
	}
	second, ok := current.Service("scene-1")
	if !ok {
		t.Fatal("未找到模板 Service")
	}
	if first == second {
		t.Fatal("两个配置实例错误地共享同一指针")
	}
	if len(app.Nodes()) != 1 {
		t.Fatalf("命令行筛选后 Nodes() 数量 = %d", len(app.Nodes()))
	}

	cancel()
	if err := <-result; err != nil {
		t.Fatalf("run() error = %v", err)
	}
	if app.State() != StateStopped {
		t.Fatalf("State() = %v", app.State())
	}
	if !first.(*lifecycleTestService).stopped ||
		!second.(*lifecycleTestService).stopped {
		t.Fatal("正常停止没有调用全部 OnStop")
	}
	if !handler.closed.Load() {
		t.Fatal("日志 Handler 没有关闭")
	}
}

func TestApplicationOriginDiscoveryLifecycle(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("reserve discovery address: %v", err)
	}
	address := listener.Addr().String()
	if err := listener.Close(); err != nil {
		t.Fatalf("close reserved listener: %v", err)
	}
	gameListener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("reserve game RPC address: %v", err)
	}
	gameAddress := gameListener.Addr().String()
	if err := gameListener.Close(); err != nil {
		t.Fatalf("close reserved game listener: %v", err)
	}
	directory := writeApplicationConfig(t, `
rpc:
  transport: tcp
  tcp: {}
discovery:
  type: origin
  origin:
    ttl: 3s
    server:
      node: discovery-1
nodes:
  - id: discovery-1
    rpc:
      tcp:
        listen: `+address+`
        advertise: `+address+`
    services: [DiscoveryService]
  - id: game-1
    rpc:
      tcp:
        listen: `+gameAddress+`
        advertise: `+gameAddress+`
    services: [lifecycleTestService]
`)
	app := newSilentApplication()
	app.Setup(&lifecycleTestService{})
	runCtx, cancelRun := context.WithCancel(context.Background())
	result := make(chan error, 1)
	go func() {
		result <- app.run(runCtx, command.StartRequest{
			AppName:   "origin-discovery-test",
			ConfigDir: directory,
		})
	}()
	waitForState(t, app, StateRunning)

	for _, nodeID := range []string{"discovery-1", "game-1"} {
		current, exists := app.Node(nodeID)
		if !exists {
			t.Fatalf("Node %q 不存在", nodeID)
		}
		status := current.DiscoveryStatus()
		expectedPublication := node.PublicationPublished
		if nodeID == "discovery-1" {
			expectedPublication = node.PublicationNotRequired
		}
		if status.Kind != "origin" || status.State != node.DiscoveryReady ||
			!status.Synchronized || status.Publication != expectedPublication {
			t.Fatalf("Node %q DiscoveryStatus = %+v", nodeID, status)
		}
	}

	cancelRun()
	if err := <-result; err != nil {
		t.Fatalf("run() error = %v", err)
	}
	if stats := app.bufferPool.Stats(); stats.Enabled && stats.InUseBuffers != 0 {
		t.Fatalf("Origin Discovery 遗留 Buffer = %+v", stats)
	}
}

// TestApplicationOriginDiscoveryNATSLifecycle verifies that Origin discovery uses the
// application-level NATS configuration, including the discovery server's NoEcho-safe local path.
func TestApplicationOriginDiscoveryNATSLifecycle(t *testing.T) {
	broker := startApplicationNATSServer(t)
	directory := writeApplicationConfig(t, `
rpc:
  transport: nats
  nats:
    namespace: origin-application-test
    urls: [`+broker.ClientURL()+`]
discovery:
  type: origin
  origin:
    ttl: 3s
    server:
      node: discovery-1
nodes:
  - id: discovery-1
    services: [DiscoveryService]
  - id: game-1
    services: [lifecycleTestService]
`)
	app := newSilentApplication()
	app.Setup(&lifecycleTestService{})
	runCtx, cancelRun := context.WithCancel(context.Background())
	result := make(chan error, 1)
	go func() {
		result <- app.run(runCtx, command.StartRequest{
			AppName:   "origin-discovery-nats-test",
			ConfigDir: directory,
		})
	}()
	waitForState(t, app, StateRunning)
	for _, nodeID := range []string{"discovery-1", "game-1"} {
		current, exists := app.Node(nodeID)
		if !exists {
			t.Fatalf("Node %q 不存在", nodeID)
		}
		status := current.DiscoveryStatus()
		if status.Kind != "origin" || status.State != node.DiscoveryReady ||
			!status.Synchronized {
			t.Fatalf("Node %q DiscoveryStatus = %+v", nodeID, status)
		}
	}
	cancelRun()
	if err := <-result; err != nil {
		t.Fatalf("run() error = %v", err)
	}
}

// TestApplicationOriginDiscoveryAppliesNodeFilters proves that labels and
// allow_discovery are applied to snapshots delivered by the built-in Origin Provider.
func TestApplicationOriginDiscoveryAppliesNodeFilters(t *testing.T) {
	broker := startApplicationNATSServer(t)
	directory := writeApplicationConfig(t, `
rpc:
  transport: nats
  nats:
    namespace: origin-filter-test
    urls: [`+broker.ClientURL()+`]
discovery:
  type: origin
  origin:
    ttl: 3s
    server:
      node: discovery-1
nodes:
  - id: discovery-1
    services: [DiscoveryService]
  - id: observer-1
    allow_discovery:
      - services: [lifecycleTestService]
        node_labels:
          game_type: battle
    services: [lifecycleTestService]
  - id: battle-1
    labels:
      game_type: battle
    services: [lifecycleTestService]
  - id: card-1
    labels:
      game_type: card
    services: [lifecycleTestService]
`)
	app := newSilentApplication()
	app.Setup(&lifecycleTestService{})
	runCtx, cancelRun := context.WithCancel(context.Background())
	result := make(chan error, 1)
	go func() {
		result <- app.run(runCtx, command.StartRequest{
			AppName:   "origin-filter-test",
			ConfigDir: directory,
		})
	}()
	waitForState(t, app, StateRunning)

	observerNode, exists := app.Node("observer-1")
	if !exists {
		t.Fatal("Node observer-1 does not exist")
	}
	instance, exists := observerNode.Service("lifecycleTestService")
	if !exists {
		t.Fatal("observer lifecycleTestService does not exist")
	}
	visible := instance.(*lifecycleTestService).ListDiscoveredServices(
		"lifecycleTestService",
	)
	if len(visible) != 1 || visible[0].NodeID != "battle-1" ||
		visible[0].Labels["game_type"] != "battle" {
		t.Fatalf("Origin filtered services = %+v, want only battle-1", visible)
	}

	cancelRun()
	if err := <-result; err != nil {
		t.Fatalf("run() error = %v", err)
	}
}

func startApplicationNATSServer(t *testing.T) *server.Server {
	t.Helper()
	running, err := server.NewServer(&server.Options{
		Host:       "127.0.0.1",
		Port:       -1,
		MaxPayload: rpc.DefaultMaxPayloadSize + 1024,
		NoLog:      true,
		NoSigs:     true,
	})
	if err != nil {
		t.Fatalf("server.NewServer() error = %v", err)
	}
	go running.Start()
	if !running.ReadyForConnections(time.Second) {
		running.Shutdown()
		t.Fatal("NATS server did not become ready")
	}
	t.Cleanup(running.Shutdown)
	return running
}

func TestApplicationCustomDiscoveryProviderUsesOnlyPublicSPI(t *testing.T) {
	directory := writeApplicationConfig(t, `
discovery:
  type: consul
  consul:
    address: 127.0.0.1:8500
nodes:
  - id: game-1
    services: [lifecycleTestService]
`)
	app := newSilentApplication()
	app.Setup(&lifecycleTestService{})
	err := app.RegisterDiscoveryProvider(
		"consul",
		func(context publicprovider.Context) (publicprovider.Provider, error) {
			var config struct {
				Address string `json:"address"`
			}
			if err := context.Config.Decode(&config); err != nil {
				return nil, err
			}
			if config.Address == "" {
				return nil, errs.ErrInvalidConfig
			}
			return &customDiscoveryProvider{context: context}, nil
		},
	)
	if err != nil {
		t.Fatalf("RegisterDiscoveryProvider() error = %v", err)
	}
	runCtx, cancelRun := context.WithCancel(context.Background())
	result := make(chan error, 1)
	go func() {
		result <- app.run(runCtx, command.StartRequest{
			AppName:   "custom-discovery-test",
			ConfigDir: directory,
		})
	}()
	waitForState(t, app, StateRunning)
	current, _ := app.Node("game-1")
	status := current.DiscoveryStatus()
	if status.Kind != "consul" || status.State != node.DiscoveryReady ||
		status.Publication != node.PublicationPublished {
		t.Fatalf("DiscoveryStatus = %+v", status)
	}
	cancelRun()
	if err := <-result; err != nil {
		t.Fatalf("run() error = %v", err)
	}
}

func TestApplicationInitFailureRollsBackPreviousNode(t *testing.T) {
	directory := writeApplicationConfig(t, `
nodes:
  - id: good-1
    services: [lifecycleTestService]
  - id: bad-1
    services: [lifecycleTestService]
  - id: later-1
    services: [lifecycleTestService]
`)
	app := newSilentApplication()
	app.Setup(&lifecycleTestService{})
	err := app.run(context.Background(), command.StartRequest{
		AppName:   "failure-test",
		ConfigDir: directory,
	})
	if !errors.Is(err, testInitFailure) {
		t.Fatalf("run() error = %v", err)
	}
	if app.State() != StateFailed {
		t.Fatalf("State() = %v", app.State())
	}

	// 已成功 Node 必须回滚；OnInit 失败 Node 不进入 OnStop。
	good, _ := app.Node("good-1")
	goodService, _ := good.Service("lifecycleTestService")
	if !goodService.(*lifecycleTestService).stopped {
		t.Fatal("此前成功 Node 没有回滚")
	}
	bad, _ := app.Node("bad-1")
	badService, _ := bad.Service("lifecycleTestService")
	if badService.(*lifecycleTestService).stopped {
		t.Fatal("OnInit 失败 Service 不应调用 OnStop")
	}
	// 失败 Node 之后已经完成装配但尚未启动的 Node 也必须回收内部资源。
	later, _ := app.Node("later-1")
	if later.State() != node.StateFailed {
		t.Fatalf("未启动 Node 回滚后 State = %v，期望 Failed", later.State())
	}
	laterService, _ := later.Service("lifecycleTestService")
	if laterService.(*lifecycleTestService).stopped {
		t.Fatal("尚未 OnStart 的后续 Service 不应调用 OnStop")
	}
}

func TestApplicationStopCancelsRunningLifecycle(t *testing.T) {
	directory := writeApplicationConfig(t, `
nodes:
  - id: game-1
    services: [lifecycleTestService]
`)
	app := newSilentApplication()
	app.Setup(&lifecycleTestService{})
	result := make(chan error, 1)
	go func() {
		result <- app.run(context.Background(), command.StartRequest{
			AppName:   "stop-test",
			ConfigDir: directory,
		})
	}()
	waitForState(t, app, StateRunning)

	stopCtx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := app.Stop(stopCtx); err != nil {
		t.Fatalf("Stop() error = %v", err)
	}
	if err := <-result; err != nil {
		t.Fatalf("run() error = %v", err)
	}
	if err := app.Stop(context.Background()); err != nil {
		t.Fatalf("重复 Stop() error = %v", err)
	}
}

func TestApplicationStopDuringStartupIsSuccessfulStop(t *testing.T) {
	directory := writeApplicationConfig(t, `
nodes:
  - id: game-1
    services: [startupStopService]
`)
	startupStopEntered = make(chan struct{})
	app := newSilentApplication()
	app.Setup(&startupStopService{})
	runResult := make(chan error, 1)
	go func() {
		runResult <- app.run(context.Background(), command.StartRequest{
			AppName:   "startup-stop-test",
			ConfigDir: directory,
		})
	}()
	select {
	case <-startupStopEntered:
	case <-time.After(time.Second):
		t.Fatal("OnStart 未执行")
	}

	stopCtx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := app.Stop(stopCtx); err != nil {
		t.Fatalf("Stop() error = %v", err)
	}
	if err := <-runResult; err != nil {
		t.Fatalf("启动期正式停止被误报为失败: %v", err)
	}
	if app.State() != StateStopped {
		t.Fatalf("State() = %v", app.State())
	}
	current, _ := app.Node("game-1")
	instance, _ := current.Service("startupStopService")
	if !instance.(*startupStopService).stopped.Load() {
		t.Fatal("已经进入 OnStart 的 Service 没有执行 OnStop")
	}
}

func TestApplicationCommandRunnerIntegration(t *testing.T) {
	directory := writeApplicationConfig(t, `
nodes:
  - id: game-1
    services: [lifecycleTestService]
`)
	pidDirectory := t.TempDir()
	app := newSilentApplication()
	app.Setup(&lifecycleTestService{})
	ctx, cancel := context.WithCancel(context.Background())
	result := make(chan struct {
		code command.ExitCode
		err  error
	}, 1)
	go func() {
		var stdout bytes.Buffer
		var stderr bytes.Buffer
		code, err := app.execute(ctx, []string{
			"start",
			"--app-name", "m7-integration",
			"--config", directory,
			"--pid-dir", pidDirectory,
		}, command.Options{
			Stdout: &stdout,
			Stderr: &stderr,
		})
		result <- struct {
			code command.ExitCode
			err  error
		}{code: code, err: err}
	}()
	waitForState(t, app, StateRunning)
	cancel()
	execution := <-result
	if execution.code != command.ExitSuccess || execution.err != nil {
		t.Fatalf("execute() = (%v, %v)", execution.code, execution.err)
	}
}

func TestLoadConfigRejectsUnsupportedFrameworkSection(t *testing.T) {
	directory := writeApplicationConfig(t, `
timer: {}
nodes:
  - id: game-1
    services: [lifecycleTestService]
`)
	_, err := loadConfig(directory)
	if !errs.IsCode(err, errs.CodeInvalidConfig) {
		t.Fatalf("loadConfig() error = %v", err)
	}
}

// TestLoadConfigApplicationNATSRPC verifies that connection-level NATS settings
// are configured once for the application and copied into every Node runtime.
func TestLoadConfigApplicationNATSRPC(t *testing.T) {
	directory := writeApplicationConfig(t, `
rpc:
  transport: nats
  max_payload_size: 2M
  max_broadcast_size: 128M
  nats:
    namespace: game-prod
    urls: [nats://127.0.0.1:4222]
    receive_queue_messages: 2048
nodes:
  - id: game-1
    services: [lifecycleTestService]
  - id: game-2
    services: [lifecycleTestService]
`)

	loaded, err := loadConfig(directory)
	if err != nil {
		t.Fatalf("loadConfig() error = %v", err)
	}
	for _, configured := range loaded.nodes {
		if configured.RPC == nil ||
			configured.RPC.Transport != rpc.TransportNATS ||
			configured.RPC.MaxPayloadSize != 2*1024*1024 ||
			configured.RPC.MaxBroadcastSize != 128*1024*1024 ||
			configured.RPC.NATS == nil ||
			configured.RPC.NATS.Namespace != "game-prod" ||
			len(configured.RPC.NATS.URLs) != 1 ||
			configured.RPC.NATS.ReceiveQueueMessages != 2048 {
			t.Fatalf("Node %q 的应用级 NATS RPC 配置 = %+v", configured.ID, configured.RPC)
		}
	}
}

// TestTutorialRPCAndOriginDiscoveryConfigurationsLoad keeps the executable tutorials aligned
// with the strict application-level RPC grammar. It intentionally only loads configuration:
// NATS and TCP endpoints are documentation examples and need not be available during unit tests.
func TestTutorialRPCAndOriginDiscoveryConfigurationsLoad(t *testing.T) {
	for _, relative := range []string{
		"../examples/07-remote-rpc/01-tcp-two-nodes/config",
		"../examples/07-remote-rpc/02-nats-two-nodes/config",
		"../examples/07-remote-rpc/03-route-and-broadcast/config",
		"../examples/08-discovery/01-origin-provider/config",
	} {
		directory, err := filepath.Abs(relative)
		if err != nil {
			t.Fatalf("filepath.Abs(%q) error = %v", relative, err)
		}
		if _, err := loadConfig(directory); err != nil {
			t.Fatalf("loadConfig(%q) error = %v", relative, err)
		}
	}
}

func TestLoadConfigSchedulerDefaultsAndOverrides(t *testing.T) {
	directory := writeApplicationConfig(t, `
nodes:
  - id: default-1
    services: [lifecycleTestService]
  - id: custom-1
    scheduler:
      max_tasks: 1234
      max_await_tasks: 321
      default_await_timeout: 3s
    services: [lifecycleTestService]
`)
	loaded, err := loadConfig(directory)
	if err != nil {
		t.Fatalf("loadConfig() error = %v", err)
	}

	// 省略 scheduler 使用统一默认值；显式配置则完整转换为运行时 time.Duration。
	if loaded.nodes[0].Scheduler != service.DefaultSchedulerConfig() {
		t.Fatalf("默认 Scheduler = %+v", loaded.nodes[0].Scheduler)
	}
	custom := loaded.nodes[1].Scheduler
	if custom.MaxTasks != 1234 || custom.MaxAwaitTasks != 321 ||
		custom.DefaultAwaitTimeout != 3*time.Second {
		t.Fatalf("自定义 Scheduler = %+v", custom)
	}
}

func TestLoadConfigSchedulerPartialOverrideAndValidation(t *testing.T) {
	partialDirectory := writeApplicationConfig(t, `
nodes:
  - id: game-1
    scheduler:
      default_await_timeout: 2s
    services: [lifecycleTestService]
`)
	loaded, err := loadConfig(partialDirectory)
	if err != nil {
		t.Fatalf("partial loadConfig() error = %v", err)
	}
	if loaded.nodes[0].Scheduler.MaxTasks != service.DefaultMaxTasks ||
		loaded.nodes[0].Scheduler.MaxAwaitTasks != service.DefaultMaxAwaitTasks ||
		loaded.nodes[0].Scheduler.DefaultAwaitTimeout != 2*time.Second {
		t.Fatalf("部分覆盖 Scheduler = %+v", loaded.nodes[0].Scheduler)
	}

	// 零容量、Await 超过总任务、零超时和未知字段都必须在配置加载阶段拒绝。
	for _, content := range []string{
		`nodes:
  - id: game-1
    scheduler: {max_tasks: 0}
    services: [lifecycleTestService]
`,
		`nodes:
  - id: game-1
    scheduler: {max_tasks: 10, max_await_tasks: 11}
    services: [lifecycleTestService]
`,
		`nodes:
  - id: game-1
    scheduler: {default_await_timeout: 0s}
    services: [lifecycleTestService]
`,
		`nodes:
  - id: game-1
    scheduler: {unknown: 1}
    services: [lifecycleTestService]
`,
	} {
		directory := writeApplicationConfig(t, content)
		if _, err := loadConfig(directory); !errs.IsCode(err, errs.CodeInvalidConfig) {
			t.Fatalf("非法 scheduler loadConfig() error = %v", err)
		}
	}
}

func TestLoadConfigApplicationTCPRPCDefaultsAndOverrides(t *testing.T) {
	directory := writeApplicationConfig(t, `
rpc:
  transport: tcp
  max_payload_size: 2M
  max_broadcast_size: 128M
  tcp:
    send_queue_messages: 2048
    read_idle_timeout: 0s
    write_timeout: 3s
nodes:
  - id: tcp-1
    rpc:
      tcp:
        listen: 127.0.0.1:17001
        advertise: 127.0.0.1:17001
    services: [lifecycleTestService]
  - id: tcp-2
    rpc:
      tcp:
        listen: 127.0.0.1:17002
        advertise: 127.0.0.1:17002
    services: [lifecycleTestService]
`)
	loaded, err := loadConfig(directory)
	if err != nil {
		t.Fatalf("loadConfig() error = %v", err)
	}
	configured := loaded.nodes[0].RPC
	if configured == nil ||
		configured.Transport != rpc.TransportTCP ||
		configured.MaxPayloadSize != 2*1024*1024 ||
		configured.MaxBroadcastSize != 128*1024*1024 ||
		configured.TCP.Listen != "127.0.0.1:17001" ||
		configured.TCP.Advertise != "127.0.0.1:17001" ||
		configured.TCP.SendQueueMessages != 2048 ||
		configured.TCP.ReadIdleTimeout != 0 ||
		configured.TCP.WriteTimeout != 3*time.Second {
		t.Fatalf("Node RPC 配置 = %+v", configured)
	}
	if loaded.nodes[1].RPC == configured ||
		loaded.nodes[1].RPC.TCP == configured.TCP ||
		loaded.nodes[1].RPC.TCP.Listen != "127.0.0.1:17002" {
		t.Fatalf("Node RPC 没有得到独立地址快照: %+v", loaded.nodes[1].RPC)
	}
}

// TestLoadConfigApplicationNATSDefaultsAndOverrides 验证 NATS 最小公开配置能完整冻结到所有 Node。
func TestLoadConfigApplicationNATSDefaultsAndOverrides(t *testing.T) {
	directory := writeApplicationConfig(t, `
rpc:
  transport: nats
  max_payload_size: 2M
  max_broadcast_size: 128M
  nats:
    namespace: game-prod
    urls: [nats://127.0.0.1:4222]
    receive_queue_messages: 2048
    auth:
      username: game
      password: secret
    tls:
      enabled: false
nodes:
  - id: game-1
    services: [lifecycleTestService]
`)
	loaded, err := loadConfig(directory)
	if err != nil {
		t.Fatalf("loadConfig() error = %v", err)
	}
	configured := loaded.nodes[0].RPC
	if configured == nil ||
		configured.Transport != rpc.TransportNATS ||
		configured.MaxPayloadSize != 2*1024*1024 ||
		configured.MaxBroadcastSize != 128*1024*1024 ||
		configured.TCP != nil ||
		configured.NATS == nil ||
		configured.NATS.Namespace != "game-prod" ||
		len(configured.NATS.URLs) != 1 ||
		configured.NATS.ReceiveQueueMessages != 2048 ||
		configured.NATS.Auth.Username != "game" ||
		configured.NATS.Auth.Password != "secret" {
		t.Fatalf("Node NATS RPC 配置 = %+v", configured)
	}
}

func TestLoadConfigRejectsInvalidNodeRPC(t *testing.T) {
	for _, content := range []string{
		`nodes:
  - id: game-1
    rpc:
      tcp: {listen: "127.0.0.1:17001", advertise: "127.0.0.1:17001"}
    services: [lifecycleTestService]
`,
		`rpc:
  transport: nats
  nats:
    namespace: game-prod
    urls: [nats://127.0.0.1:4222]
nodes:
  - id: game-1
    rpc:
      tcp: {listen: "127.0.0.1:17001", advertise: "127.0.0.1:17001"}
    services: [lifecycleTestService]
`,
		`rpc:
  transport: tcp
  tcp: {}
nodes:
  - id: game-1
    services: [lifecycleTestService]
`,
		`rpc:
  transport: tcp
  tcp:
    listen: "127.0.0.1:17001"
nodes:
  - id: game-1
    rpc:
      tcp: {listen: "127.0.0.1:17001", advertise: "127.0.0.1:17001"}
    services: [lifecycleTestService]
`,
		`nodes:
  - id: game-1
    rpc:
      transport: nats
      tcp: {listen: "127.0.0.1:17001", advertise: "127.0.0.1:17001"}
    services: [lifecycleTestService]
`,
		`nodes:
  - id: game-1
    rpc:
      transport: tcp
      tcp: {listen: "127.0.0.1:17001", advertise: "0.0.0.0:17001"}
    services: [lifecycleTestService]
`,
		`nodes:
  - id: game-1
    rpc:
      transport: tcp
      tcp:
        listen: "127.0.0.1:17001"
        advertise: "127.0.0.1:17001"
        send_queue_messages: 70000
    services: [lifecycleTestService]
`,
		`nodes:
  - id: game-1
    rpc:
      transport: tcp
      tcp:
        listen: "127.0.0.1:17001"
        advertise: "127.0.0.1:17001"
        unknown: true
    services: [lifecycleTestService]
`,
		`nodes:
  - id: game-1
    rpc:
      transport: tcp
      max_broadcast_size: 2G
      tcp: {listen: "127.0.0.1:17001", advertise: "127.0.0.1:17001"}
    services: [lifecycleTestService]
`,
		`nodes:
  - id: game-1
    rpc:
      transport: tcp
      max_broadcast_size: 0B
      tcp: {listen: "127.0.0.1:17001", advertise: "127.0.0.1:17001"}
    services: [lifecycleTestService]
`,
		`nodes:
  - id: game-1
    rpc:
      transport: tcp
      max_broadcast_size: 67108864
      tcp: {listen: "127.0.0.1:17001", advertise: "127.0.0.1:17001"}
    services: [lifecycleTestService]
`,
		`nodes:
  - id: game-1
    rpc:
      transport: tcp
      max_message_size: 2M
      tcp: {listen: "127.0.0.1:17001", advertise: "127.0.0.1:17001"}
    services: [lifecycleTestService]
`,
		`nodes:
  - id: game-1
    rpc:
      transport: nats
      nats:
        namespace: game-prod
        urls: [nats://127.0.0.1:4222]
      tcp: {listen: "127.0.0.1:17001", advertise: "127.0.0.1:17001"}
    services: [lifecycleTestService]
`,
	} {
		directory := writeApplicationConfig(t, content)
		if _, err := loadConfig(directory); !errs.IsCode(err, errs.CodeInvalidConfig) {
			t.Fatalf("非法 Node RPC loadConfig() error = %v", err)
		}
	}
}

// TestLoadConfigDiscoveryFilter 验证 Node 标签与关注规则能够从 YAML 冻结为精确匹配器。
func TestLoadConfigDiscoveryFilter(t *testing.T) {
	directory := writeApplicationConfig(t, `
nodes:
  - id: default-1
    services: [lifecycleTestService]
  - id: none-1
    allow_discovery: []
    services: [lifecycleTestService]
  - id: filtered-1
    labels:
      region: cn-east
      stage: dev
    allow_discovery:
      - services: [PlayerService, ChatService]
        node_labels:
          region: [cn-east, cn-north]
          stage: prod
    services: [lifecycleTestService]
`)
	loaded, err := loadConfig(directory)
	if err != nil {
		t.Fatalf("loadConfig() error = %v", err)
	}
	if loaded.nodes[2].Labels["region"] != "cn-east" ||
		loaded.nodes[2].Labels["stage"] != "dev" {
		t.Fatalf("Node Labels = %v", loaded.nodes[2].Labels)
	}

	target := internaldiscovery.RawNode{
		NodeID: "game-1",
		Labels: map[string]string{"region": "cn-north", "stage": "prod"},
	}
	player := internaldiscovery.RawService{ServiceName: "PlayerService"}
	if !loaded.nodes[0].DiscoveryFilter.Match(target, player) {
		t.Fatal("省略 allow_discovery 没有允许公开远端 Service")
	}
	if loaded.nodes[1].DiscoveryFilter.Match(target, player) {
		t.Fatal("显式空 allow_discovery 没有拒绝全部远端 Service")
	}
	if !loaded.nodes[2].DiscoveryFilter.Match(target, player) {
		t.Fatal("组合关注规则没有匹配单值/多值标签")
	}
}

// TestLoadConfigRejectsInvalidDiscoveryConfiguration 锁定空标签、null、空规则和空维度的
// 启动期失败。
func TestLoadConfigRejectsInvalidDiscoveryConfiguration(t *testing.T) {
	tests := []string{
		`nodes:
  - id: game-1
    labels:
      "": cn-east
    services: [lifecycleTestService]
`,
		`nodes:
  - id: game-1
    labels:
      region: ""
    services: [lifecycleTestService]
`,
		`nodes:
  - id: game-1
    allow_discovery: null
    services: [lifecycleTestService]
`,
		`nodes:
  - id: game-1
    allow_discovery: [{}]
    services: [lifecycleTestService]
`,
		`nodes:
  - id: game-1
    allow_discovery:
      - services: []
    services: [lifecycleTestService]
`,
		`nodes:
  - id: game-1
    allow_discovery:
      - node_labels: {}
    services: [lifecycleTestService]
`,
		`nodes:
  - id: game-1
    allow_discovery:
      - node_labels:
          region: []
    services: [lifecycleTestService]
`,
	}
	for _, content := range tests {
		directory := writeApplicationConfig(t, content)
		if _, err := loadConfig(directory); !errs.IsCode(err, errs.CodeInvalidConfig) {
			t.Fatalf("非法发现配置 loadConfig() error = %v", err)
		}
	}
}

func TestCatalogRejectsNonZeroTemplate(t *testing.T) {
	app := New()
	app.Setup(&lifecycleTestService{started: true})
	err := app.catalog.freeze()
	if !errs.IsCode(err, errs.CodeInvalidArgument) {
		t.Fatalf("非零模板 error = %v", err)
	}
}

func TestDecodeLogConfigFullConfiguration(t *testing.T) {
	configured, err := decodeLogConfig(map[string]any{
		"mode": "sync",
		"console": map[string]any{
			"enabled": true,
			"level":   "debug",
			"format":  "json",
			"context_fields": map[string]any{
				"node_id":      false,
				"service_name": true,
			},
		},
		"file": map[string]any{
			"enabled": true,
			"level":   "warn",
			"format":  "text",
			"path":    "logs/game.log",
			"context_fields": map[string]any{
				"node_id":      true,
				"service_name": false,
			},
			"rotation": map[string]any{
				"max_size": "4M",
				"by_date":  false,
				"timezone": "UTC",
			},
			"retention": map[string]any{
				"max_age":   "48h",
				"max_files": 7,
				"compress":  false,
			},
		},
	})
	if err != nil {
		t.Fatalf("decodeLogConfig() error = %v", err)
	}
	if configured.Mode != originlog.SyncMode ||
		configured.Console.Level != originlog.DebugLevel ||
		configured.Console.Format != originlog.JSONFormat ||
		configured.Console.ContextFields.NodeID ||
		!configured.Console.ContextFields.ServiceName {
		t.Fatalf("控制台配置 = %+v", configured.Console)
	}
	if configured.File.Rotation.MaxSizeMB != 4 ||
		configured.File.Rotation.Timezone != originlog.UTCTime ||
		configured.File.Retention.MaxAgeDays != 2 ||
		!configured.File.ContextFields.NodeID ||
		configured.File.ContextFields.ServiceName {
		t.Fatalf("文件配置 = %+v", configured.File)
	}
}

// TestDecodeLogConfigDefaultsContextFieldsToVisible 防止空对象或省略字段意外隐藏归属信息。
func TestDecodeLogConfigDefaultsContextFieldsToVisible(t *testing.T) {
	t.Parallel()

	for _, raw := range []map[string]any{
		{},
		{
			"console": map[string]any{"context_fields": map[string]any{}},
			"file":    map[string]any{"context_fields": map[string]any{}},
		},
	} {
		configured, err := decodeLogConfig(raw)
		if err != nil {
			t.Fatalf("decodeLogConfig(%v) error = %v", raw, err)
		}
		if !configured.Console.ContextFields.NodeID ||
			!configured.Console.ContextFields.ServiceName ||
			!configured.File.ContextFields.NodeID ||
			!configured.File.ContextFields.ServiceName {
			t.Fatalf(
				"decodeLogConfig(%v) context fields = %+v / %+v",
				raw,
				configured.Console.ContextFields,
				configured.File.ContextFields,
			)
		}
	}
}

// TestLoggingTutorialConfigurationsLoad 把 v3.1 独立日志示例纳入配置回归，确保教程中的
// YAML 不会因为字段改名、默认值或校验规则演进而只在使用者运行时才失败。
func TestLoggingTutorialConfigurationsLoad(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		directory string
		check     func(*testing.T, loadedConfig)
	}{
		{
			name:      "global and service",
			directory: filepath.Join("..", "examples", "03-logging", "01-global-and-service", "config"),
			check: func(t *testing.T, loaded loadedConfig) {
				if loaded.log.Mode != originlog.SyncMode || !loaded.log.Console.Enabled {
					t.Fatalf("unexpected log config: %+v", loaded.log)
				}
			},
		},
		{
			name:      "formats and context",
			directory: filepath.Join("..", "examples", "03-logging", "02-formats-and-context", "config"),
			check: func(t *testing.T, loaded loadedConfig) {
				if loaded.log.Console.ContextFields.ServiceName ||
					!loaded.log.File.ContextFields.ServiceName ||
					loaded.log.File.Format != originlog.JSONFormat {
					t.Fatalf("unexpected context/format config: %+v", loaded.log)
				}
			},
		},
		{
			name:      "runtime control",
			directory: filepath.Join("..", "examples", "03-logging", "04-runtime-control", "config"),
			check: func(t *testing.T, loaded loadedConfig) {
				if !loaded.log.Console.Enabled || !loaded.log.File.Enabled ||
					loaded.log.File.Level != originlog.DebugLevel {
					t.Fatalf("unexpected runtime control config: %+v", loaded.log)
				}
			},
		},
		{
			name:      "file rotation",
			directory: filepath.Join("..", "examples", "03-logging", "03-file-rotation", "config"),
			check: func(t *testing.T, loaded loadedConfig) {
				if loaded.log.File.Rotation.MaxSizeMB != 1 ||
					loaded.log.File.Retention.MaxFiles != 10 ||
					loaded.log.File.Retention.MaxAgeDays != 7 {
					t.Fatalf("unexpected rotation config: %+v", loaded.log.File)
				}
			},
		},
		{
			name:      "custom handler",
			directory: filepath.Join("..", "examples", "03-logging", "05-custom-handler", "config"),
			check: func(t *testing.T, loaded loadedConfig) {
				if loaded.log.Mode != originlog.SyncMode || loaded.log.Console.Level != originlog.InfoLevel {
					t.Fatalf("unexpected custom handler config: %+v", loaded.log)
				}
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			loaded, err := loadConfig(test.directory)
			if err != nil {
				t.Fatalf("loadConfig(%q) error = %v", test.directory, err)
			}
			test.check(t, loaded)
		})
	}
}

func TestDecodeLogConfigRejectsInvalidValues(t *testing.T) {
	tests := []struct {
		name string
		raw  map[string]any
	}{
		{name: "mode", raw: map[string]any{"mode": "fast"}},
		{name: "console level", raw: map[string]any{
			"console": map[string]any{"level": "trace"},
		}},
		{name: "file level", raw: map[string]any{
			"file": map[string]any{"level": "trace"},
		}},
		{name: "console format", raw: map[string]any{
			"console": map[string]any{"format": "xml"},
		}},
		{name: "file format", raw: map[string]any{
			"file": map[string]any{"format": "xml"},
		}},
		{name: "unaligned size", raw: map[string]any{
			"file": map[string]any{
				"rotation": map[string]any{"max_size": "1KB"},
			},
		}},
		{name: "timezone", raw: map[string]any{
			"file": map[string]any{
				"rotation": map[string]any{"timezone": "Asia/Shanghai"},
			},
		}},
		{name: "max age", raw: map[string]any{
			"file": map[string]any{
				"retention": map[string]any{"max_age": "1h"},
			},
		}},
		{name: "unknown field", raw: map[string]any{"queue_size": 10}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, err := decodeLogConfig(test.raw); !errs.IsCode(err, errs.CodeInvalidConfig) {
				t.Fatalf("decodeLogConfig() error = %v", err)
			}
		})
	}
}

// TestInitializeResourcesPrefixesLogFilesWithApplicationName 防止多个 Application 复用同一份
// 配置时写入同一个活动、归档或 Crash 文件。
func TestInitializeResourcesPrefixesLogFilesWithApplicationName(t *testing.T) {
	directory := t.TempDir()
	configured := loadedConfig{log: originlog.DefaultConfig()}
	configured.log.File.Enabled = true
	configured.log.File.Path = filepath.Join(directory, "origin.log")
	var received originlog.Config
	app := New(Options{
		LogHandlerFactory: func(config originlog.Config) (originlog.Handler, error) {
			received = config
			return &silentHandler{}, nil
		},
	})
	if err := app.initializeResources(configured, "game"); err != nil {
		t.Fatalf("initializeResources() error = %v", err)
	}
	t.Cleanup(func() {
		if err := app.closeResources(context.Background()); err != nil {
			t.Errorf("closeResources() error = %v", err)
		}
	})

	wantActive := filepath.Join(directory, "game-origin.log")
	if received.File.Path != wantActive {
		t.Fatalf("handler file path = %q, want %q", received.File.Path, wantActive)
	}
	wantCrash := filepath.Join(directory, "game-origin.crash.log")
	if _, err := os.Stat(wantCrash); err != nil {
		t.Fatalf("crash file %q was not created: %v", wantCrash, err)
	}
}

// TestApplicationLogPathPrefixIsStable 固定显式文件名、无扩展名和已带前缀的冷路径映射。
func TestApplicationLogPathPrefixIsStable(t *testing.T) {
	t.Parallel()

	tests := []struct {
		path string
		want string
	}{
		{path: "logs/origin.log", want: filepath.Join("logs", "game-origin.log")},
		{path: "logs/server.log", want: filepath.Join("logs", "game-server.log")},
		{path: "logs/output", want: filepath.Join("logs", "game-output")},
		{path: "logs/game-origin.log", want: filepath.Join("logs", "game-origin.log")},
	}
	for _, test := range tests {
		if got := applicationLogPath("game", test.path); got != test.want {
			t.Errorf("applicationLogPath(game, %q) = %q, want %q", test.path, got, test.want)
		}
	}
}

func TestServiceDeclarationFormsAndErrors(t *testing.T) {
	valid := []struct {
		value    string
		name     string
		template string
		private  bool
	}{
		{value: "PlayerService", name: "PlayerService", template: "PlayerService"},
		{value: "_DebugService", name: "DebugService", template: "DebugService", private: true},
		{value: "scene-1:SceneService", name: "scene-1", template: "SceneService"},
		{value: "_scene-2:SceneService", name: "scene-2", template: "SceneService", private: true},
	}
	for _, test := range valid {
		name, template, private, err := parseServiceDeclaration(test.value)
		if err != nil {
			t.Fatalf("parseServiceDeclaration(%q): %v", test.value, err)
		}
		if name != test.name || template != test.template || private != test.private {
			t.Fatalf(
				"parseServiceDeclaration(%q) = %q, %q, %v",
				test.value,
				name,
				template,
				private,
			)
		}
	}
	for _, value := range []string{"", "_", "a:b:c", ":Template", "actual:"} {
		if _, _, _, err := parseServiceDeclaration(value); err == nil {
			t.Fatalf("parseServiceDeclaration(%q) 未返回错误", value)
		}
	}
}

func TestRegisterCommandAndHelp(t *testing.T) {
	app := New()
	called := false
	custom := command.Command{
		Name:    "inspect",
		Summary: "检查测试数据",
		Usage:   "test inspect",
		Run: func(command.Context, []string) error {
			called = true
			return nil
		},
	}
	err := app.RegisterCommand(custom)
	if err != nil {
		t.Fatalf("RegisterCommand() error = %v", err)
	}
	if err := app.RegisterCommand(custom); !errs.IsCode(err, errs.CodeInvalidArgument) {
		t.Fatalf("重复 RegisterCommand() error = %v", err)
	}
	var stdout bytes.Buffer
	code, err := app.execute(
		context.Background(),
		[]string{"inspect"},
		command.Options{Stdout: &stdout},
	)
	if err != nil || code != command.ExitSuccess || !called {
		t.Fatalf("execute() = %v, %v, called=%v", code, err, called)
	}
	if err := app.RegisterCommand(command.Command{
		Name: "later",
	}); err == nil {
		t.Fatal("执行命令后 RegisterCommand() 未返回错误")
	}
}

func TestApplicationSetupValidatesServiceTemplates(t *testing.T) {
	for _, test := range []struct {
		name   string
		sample service.IService
	}{
		{name: "typed nil", sample: (*lifecycleTestService)(nil)},
		{name: "nonzero sample", sample: &lifecycleTestService{started: true}},
		{name: "anonymous struct", sample: &struct{ service.Service }{}},
	} {
		t.Run(test.name, func(t *testing.T) {
			app := New()
			app.Setup(test.sample)
			if err := app.catalog.freeze(); !errs.IsCode(err, errs.CodeInvalidArgument) {
				t.Fatalf("Setup() error = %v", err)
			}
		})
	}

	empty := New()
	empty.Setup()
	if err := empty.catalog.freeze(); !errs.IsCode(err, errs.CodeInvalidArgument) {
		t.Fatalf("empty Setup() error = %v", err)
	}

	idempotent := New()
	idempotent.Setup(&lifecycleTestService{}, &lifecycleTestService{})
	if err := idempotent.catalog.freeze(); err != nil {
		t.Fatalf("idempotent Setup() error = %v", err)
	}
	if _, err := idempotent.catalog.instantiate("lifecycleTestService"); err != nil {
		t.Fatalf("instantiate() error = %v", err)
	}
}

func TestApplicationDiscoveryProviderRegistrationValidation(t *testing.T) {
	factory := func(publicprovider.Context) (publicprovider.Provider, error) {
		return nil, nil
	}
	for _, name := range []string{"", "Consul", "origin", "etcd"} {
		app := New()
		if err := app.RegisterDiscoveryProvider(name, factory); !errs.IsCode(err, errs.CodeInvalidArgument) {
			t.Fatalf("RegisterDiscoveryProvider(%q) error = %v", name, err)
		}
	}
	if err := New().RegisterDiscoveryProvider("consul", nil); !errs.IsCode(err, errs.CodeInvalidArgument) {
		t.Fatalf("nil factory error = %v", err)
	}

	app := New()
	if err := app.RegisterDiscoveryProvider("consul", factory); err != nil {
		t.Fatalf("RegisterDiscoveryProvider() error = %v", err)
	}
	if err := app.RegisterDiscoveryProvider("consul", factory); !errs.IsCode(err, errs.CodeInvalidArgument) {
		t.Fatalf("duplicate provider error = %v", err)
	}
}

func TestApplicationStartUsesProcessArguments(t *testing.T) {
	previousArgs := os.Args
	previousStdout := os.Stdout
	readOutput, writeOutput, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe() error = %v", err)
	}
	os.Args = []string{"origin-test", "help"}
	os.Stdout = writeOutput
	t.Cleanup(func() {
		os.Args = previousArgs
		os.Stdout = previousStdout
		_ = readOutput.Close()
		_ = writeOutput.Close()
	})

	New().Start()
	if err := writeOutput.Close(); err != nil {
		t.Fatalf("close stdout pipe: %v", err)
	}
	output, err := io.ReadAll(readOutput)
	if err != nil {
		t.Fatalf("read stdout pipe: %v", err)
	}
	if !bytes.Contains(output, []byte("Usage:\n  origin-test <command> [options]")) {
		t.Fatalf("Start() output = %q", output)
	}
}

func TestApplicationStartExitsOnInvalidArguments(t *testing.T) {
	const helperEnvironment = "ORIGIN_TEST_APPLICATION_START_EXIT"
	if os.Getenv(helperEnvironment) == "1" {
		os.Args = []string{"origin-test"}
		New().Start()
		t.Fatal("Start() returned after an invalid command")
	}

	process := exec.Command(os.Args[0], "-test.run=^TestApplicationStartExitsOnInvalidArguments$")
	process.Env = append(os.Environ(), helperEnvironment+"=1")
	output, err := process.CombinedOutput()
	var exitError *exec.ExitError
	if !errors.As(err, &exitError) || exitError.ExitCode() != int(command.ExitUsage) {
		t.Fatalf("Start() process error = %v, output = %q", err, output)
	}
	if !bytes.Contains(output, []byte("command is required")) {
		t.Fatalf("Start() process output = %q", output)
	}
}

func TestApplicationConstructionAndCreatedStopEdges(t *testing.T) {
	if app := New(Options{}, Options{}); app.catalog.freeze() == nil {
		t.Fatal("多个 Options 未记录错误")
	}
	if app := New(Options{StartTimeout: -time.Second}); app.catalog.freeze() == nil {
		t.Fatal("负 StartTimeout 未记录错误")
	}
	app := New()
	if app.Logger().Enabled(originlog.InfoLevel) {
		t.Fatal("初始化前 Logger 不应启用")
	}
	if err := app.Stop(context.Background()); err != nil {
		t.Fatalf("Created Stop() error = %v", err)
	}
	app.Setup(&lifecycleTestService{})
	if err := app.catalog.freeze(); err == nil {
		t.Fatal("Stopped 后 Setup 未记录错误")
	}
}

func TestApplicationTimerOptionsDefaultsAndValidation(t *testing.T) {
	// 零值 Options 必须冻结稳定的三百万额度和创建 Application 时的本地时区，
	// 业务无需为普通场景增加配置。
	app := New()
	if app.options.Timer.MaxTimersPerNode != DefaultMaxTimersPerNode {
		t.Fatalf(
			"MaxTimersPerNode = %d，期望 %d",
			app.options.Timer.MaxTimersPerNode,
			DefaultMaxTimersPerNode,
		)
	}
	if app.options.Timer.Location != time.Local {
		t.Fatal("默认 Timer Location 未冻结为 time.Local")
	}

	// 负数额度没有“无限”或“禁用”的稳定含义，必须在首次启动前记录参数错误。
	invalid := New(Options{
		Timer: TimerOptions{MaxTimersPerNode: -1},
	})
	if invalid.catalog.freeze() == nil {
		t.Fatal("负 Timer 额度未记录错误")
	}

	// 显式 Location 必须原样保留，不能被默认本地时区覆盖。
	location := time.FixedZone("test-zone", 8*60*60)
	explicit := New(Options{
		Timer: TimerOptions{
			MaxTimersPerNode: 16,
			Location:         location,
		},
	})
	if explicit.options.Timer.MaxTimersPerNode != 16 ||
		explicit.options.Timer.Location != location {
		t.Fatalf("显式 Timer Options 被修改: %+v", explicit.options.Timer)
	}
}

func TestRollbackBuiltNodesPreservesPrimaryError(t *testing.T) {
	// 直接构造一个尚未启动的 Node，模拟后续 Node 在装配阶段失败的部分初始化场景。
	target := &lifecycleTestService{}
	built, err := node.New(
		node.Config{ID: "built-1", Services: []string{"LifecycleService"}},
		[]node.ServiceBinding{{
			Name:     "LifecycleService",
			Template: "LifecycleService",
			Service:  target,
		}},
		originlog.NewNop(),
		node.Options{
			MaxTimersPerNode: DefaultMaxTimersPerNode,
			TimerLocation:    time.Local,
		},
	)
	if err != nil {
		t.Fatalf("node.New() error = %v", err)
	}
	primary := errors.New("next node build failed")

	// 回滚必须保留原始失败，同时把已创建 Node 置为不可复用的 Failed。
	result := rollbackBuiltNodes([]*node.Node{built}, primary)
	if !errors.Is(result, primary) {
		t.Fatalf("rollbackBuiltNodes() 丢失原始错误: %v", result)
	}
	if built.State() != node.StateFailed {
		t.Fatalf("回滚后 Node State = %v，期望 Failed", built.State())
	}
}

func TestUnreportedErrorFiltersOnlyLoggedBranches(t *testing.T) {
	logged := reportedError{cause: errors.New("already logged")}
	pending := errors.New("close failed")
	result := unreportedError(errors.Join(logged, pending))
	if !errors.Is(result, pending) {
		t.Fatalf("未保留未报告错误: %v", result)
	}
	if result == nil || result.Error() != pending.Error() {
		t.Fatalf("过滤结果 = %v", result)
	}
	if result := unreportedError(logged); result != nil {
		t.Fatalf("已报告错误仍需输出: %v", result)
	}
}

func newSilentApplication() *Application {
	return New(Options{
		LogHandlerFactory: func(originlog.Config) (originlog.Handler, error) {
			return &silentHandler{}, nil
		},
	})
}

func writeApplicationConfig(t *testing.T, content string) string {
	t.Helper()
	directory := t.TempDir()
	path := filepath.Join(directory, "application.yaml")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("写入配置: %v", err)
	}
	return directory
}

func waitForState(t *testing.T, app *Application, expected State) {
	t.Helper()
	deadline := time.NewTimer(3 * time.Second)
	defer deadline.Stop()
	ticker := time.NewTicker(time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-deadline.C:
			t.Fatalf("等待状态 %v 超时，当前状态 %v", expected, app.State())
		case <-ticker.C:
			if app.State() == expected {
				return
			}
			if app.State() == StateFailed {
				t.Fatalf("等待状态 %v 时 Application 已失败", expected)
			}
		}
	}
}
