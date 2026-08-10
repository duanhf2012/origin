package application

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"runtime"
	"sort"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/duanhf2012/origin/v3/admin"
	"github.com/duanhf2012/origin/v3/command"
	"github.com/duanhf2012/origin/v3/errs"
	originlog "github.com/duanhf2012/origin/v3/log"
	"github.com/duanhf2012/origin/v3/node"
	"github.com/duanhf2012/origin/v3/service"
)

var httpLifecycleStartSeen chan bool
var httpLifecycleInitSeen chan bool
var httpLifecycleStopSeen chan bool
var httpLifecycleProviderSeen chan struct{}

type httpLifecycleService struct {
	service.Service
}

// observabilitySystemService 为可观测性共存测试提供真实串行执行槽和执行统计。
// 它不增加业务行为，测试只通过公开 DispatchAsync 投递有界任务。
type observabilitySystemService struct {
	service.Service
}

var invalidAdminLifecycleInitSeen chan struct{}

type invalidAdminLifecycleService struct {
	service.Service
}

func (*invalidAdminLifecycleService) AdminEndpoints() []admin.Endpoint {
	return []admin.Endpoint{admin.Get("invalid_name", nil)}
}

func (*invalidAdminLifecycleService) OnInit() error {
	invalidAdminLifecycleInitSeen <- struct{}{}
	return nil
}

func (*httpLifecycleService) AdminEndpoints() []admin.Endpoint {
	httpLifecycleProviderSeen <- struct{}{}
	return nil
}

func (target *httpLifecycleService) OnInit() error {
	providerBeforeInit := false
	select {
	case <-httpLifecycleProviderSeen:
		providerBeforeInit = true
	default:
	}
	httpLifecycleInitSeen <- providerBeforeInit && adminDiagnosticsReachable(target.Application())
	return nil
}

func (target *httpLifecycleService) OnStart(context.Context) error {
	httpLifecycleStartSeen <- adminDiagnosticsReachable(target.Application())
	return nil
}

func (target *httpLifecycleService) OnStop(context.Context) error {
	application := target.Application()
	_, adminOK := application.AdminAddress()
	_, pprofOK := application.PprofAddress()
	httpLifecycleStopSeen <- adminOK && pprofOK
	return nil
}

func adminDiagnosticsReachable(application service.ApplicationRuntime) bool {
	if application == nil {
		return false
	}
	adminAddress, adminOK := application.AdminAddress()
	_, pprofOK := application.PprofAddress()
	if !adminOK || !pprofOK {
		return false
	}
	client := &http.Client{Timeout: time.Second}
	response, err := client.Get("http://" + adminAddress + "/admin/v1/diagnostics")
	if err != nil {
		return false
	}
	_ = response.Body.Close()
	return response.StatusCode == http.StatusOK
}

// TestInitialHTTPServersSurroundNodeLifecycle 防止命令行 Listener 在 Node 启动后才绑定，或在
// Node OnStop 之前提前关闭。run 返回时两个实际端口必须已经释放。
func TestInitialHTTPServersSurroundNodeLifecycle(t *testing.T) {
	directory := writeApplicationConfig(t, `
nodes:
  - id: gateway-1
    services:
      - httpLifecycleService
`)
	httpLifecycleProviderSeen = make(chan struct{}, 1)
	httpLifecycleInitSeen = make(chan bool, 1)
	httpLifecycleStartSeen = make(chan bool, 1)
	httpLifecycleStopSeen = make(chan bool, 1)
	app := New(Options{
		LogHandlerFactory: func(originlog.Config) (originlog.Handler, error) {
			return &silentHandler{}, nil
		},
	})
	app.Setup(&httpLifecycleService{})

	runCtx, cancel := context.WithCancel(context.Background())
	result := make(chan error, 1)
	go func() {
		result <- app.run(runCtx, command.StartRequest{
			AppName:      "http-lifecycle",
			ConfigDir:    directory,
			AdminAddress: "127.0.0.1:0",
			PprofAddress: "127.0.0.1:0",
		})
	}()
	if !receiveApplicationValue(t, httpLifecycleInitSeen) {
		t.Fatal("Admin Provider was not frozen before OnInit or Listener was unreachable from OnInit")
	}
	if !receiveApplicationValue(t, httpLifecycleStartSeen) {
		t.Fatal("HTTP servers were unavailable from Service.OnStart")
	}
	adminAddress, adminOK := app.AdminAddress()
	pprofAddress, pprofOK := app.PprofAddress()
	if !adminOK || !pprofOK {
		t.Fatalf("addresses admin=%q/%v pprof=%q/%v", adminAddress, adminOK, pprofAddress, pprofOK)
	}

	cancel()
	if !receiveApplicationValue(t, httpLifecycleStopSeen) {
		t.Fatal("HTTP servers were unavailable from Service.OnStop")
	}
	select {
	case err := <-result:
		if err != nil {
			t.Fatalf("run() error = %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("Application run() did not return")
	}
	if _, ok := app.AdminAddress(); ok {
		t.Fatal("Admin remained active after run")
	}
	if _, ok := app.PprofAddress(); ok {
		t.Fatal("pprof remained active after run")
	}
	assertAddressReleased(t, adminAddress)
	assertAddressReleased(t, pprofAddress)
}

// TestObservabilityTrafficKeepsServiceSchedulingResponsive 验证日志、Admin Diagnostics、
// pprof、执行统计和真实 Service 调度同时工作时不会互相阻塞或遗留资源。延迟上限只防止
// 数量级异常和死锁，不构成脱离当前测试环境的业务 SLA。
func TestObservabilityTrafficKeepsServiceSchedulingResponsive(t *testing.T) {
	const sampleCount = 5_000
	baselineGoroutines := runtime.NumGoroutine()
	directory := writeApplicationConfig(t, `
nodes:
  - id: observability-1
    services:
      - observabilitySystemService
`)
	app := New(Options{
		LogHandlerFactory: func(originlog.Config) (originlog.Handler, error) {
			return &silentHandler{}, nil
		},
	})
	app.Setup(&observabilitySystemService{})

	// 通过正式 run 事务启动 Node、Admin 和 pprof；stopOnce 保证任何断言失败也会回收资源。
	runCtx, cancelRun := context.WithCancel(context.Background())
	runResult := make(chan error, 1)
	go func() {
		runResult <- app.run(runCtx, command.StartRequest{
			AppName:      "observability-system",
			ConfigDir:    directory,
			AdminAddress: "127.0.0.1:0",
			PprofAddress: "127.0.0.1:0",
		})
	}()
	var stopOnce sync.Once
	stopApplication := func() {
		stopOnce.Do(func() {
			cancelRun()
			select {
			case err := <-runResult:
				if err != nil {
					t.Errorf("observability Application run() error = %v", err)
				}
			case <-time.After(3 * time.Second):
				t.Error("observability Application run() did not return")
			}
		})
	}
	defer stopApplication()
	waitForState(t, app, StateRunning)

	adminAddress, adminOK := app.AdminAddress()
	pprofAddress, pprofOK := app.PprofAddress()
	if !adminOK || !pprofOK {
		t.Fatalf("observability addresses admin=%q/%v pprof=%q/%v", adminAddress, adminOK, pprofAddress, pprofOK)
	}
	nodes := app.Nodes()
	if len(nodes) != 1 {
		t.Fatalf("observability Nodes() = %d, want 1", len(nodes))
	}
	bound, exists := nodes[0].Service("observabilitySystemService")
	if !exists {
		t.Fatal("observability Service was not bound")
	}
	target, ok := bound.(*observabilitySystemService)
	if !ok {
		t.Fatalf("observability Service type = %T", bound)
	}

	// 两类 HTTP Worker 使用同一个有界 Client；错误 Channel 容量等于 Worker 数，不会阻塞退出。
	workerCtx, cancelWorkers := context.WithCancel(context.Background())
	client := &http.Client{Timeout: 2 * time.Second}
	var adminRequests atomic.Uint64
	var pprofRequests atomic.Uint64
	workerErrors := make(chan error, 3)
	var workers sync.WaitGroup
	startWorker := func(targetURL string, completed *atomic.Uint64) {
		workers.Add(1)
		go func() {
			defer workers.Done()
			for {
				request, err := http.NewRequestWithContext(workerCtx, http.MethodGet, targetURL, nil)
				if err != nil {
					workerErrors <- err
					return
				}
				response, err := client.Do(request)
				if err != nil {
					if workerCtx.Err() != nil {
						return
					}
					workerErrors <- err
					return
				}
				_, readErr := io.Copy(io.Discard, response.Body)
				closeErr := response.Body.Close()
				if response.StatusCode != http.StatusOK {
					workerErrors <- fmt.Errorf(
						"GET %s status=%s read_error=%v close_error=%v",
						targetURL,
						response.Status,
						readErr,
						closeErr,
					)
					return
				}
				if readErr != nil || closeErr != nil {
					// 主流程结束时会取消正在读取的 pprof 响应；这是 Worker 的预期退出路径。
					if workerCtx.Err() != nil {
						return
					}
					workerErrors <- fmt.Errorf(
						"GET %s status=%s read_error=%v close_error=%v",
						targetURL,
						response.Status,
						readErr,
						closeErr,
					)
					return
				}
				completed.Add(1)
			}
		}()
	}
	startWorker("http://"+adminAddress+"/admin/v1/diagnostics", &adminRequests)
	startWorker("http://"+adminAddress+"/admin/v1/diagnostics?detail=full", &adminRequests)
	startWorker("http://"+pprofAddress+"/debug/pprof/goroutine?debug=0", &pprofRequests)

	// 在开始业务采样前证明 Admin 与 pprof 都已经真实完成请求，避免只验证了启动未验证共存。
	requestReadyDeadline := time.Now().Add(3 * time.Second)
	for adminRequests.Load() == 0 || pprofRequests.Load() == 0 {
		select {
		case err := <-workerErrors:
			cancelWorkers()
			workers.Wait()
			t.Fatalf("observability request failed before sampling: %v", err)
		default:
		}
		if time.Now().After(requestReadyDeadline) {
			cancelWorkers()
			workers.Wait()
			t.Fatal("observability requests did not become ready")
		}
		time.Sleep(time.Millisecond)
	}

	// 顺序等待每个任务开始，使样本表示单次提交到执行延迟，而不是人为制造的队列积压。
	latencies := make([]time.Duration, 0, sampleCount)
	for index := 0; index < sampleCount; index++ {
		started := time.Now()
		executed := make(chan time.Duration, 1)
		if err := target.DispatchAsync(func(context.Context) {
			executed <- time.Since(started)
		}); err != nil {
			cancelWorkers()
			workers.Wait()
			t.Fatalf("observability DispatchAsync(%d) error = %v", index, err)
		}
		select {
		case latency := <-executed:
			latencies = append(latencies, latency)
		case <-time.After(time.Second):
			cancelWorkers()
			workers.Wait()
			t.Fatalf("observability task %d exceeded one second", index)
		}
	}
	cancelWorkers()
	workers.Wait()
	client.CloseIdleConnections()
	close(workerErrors)
	for err := range workerErrors {
		if err != nil {
			t.Errorf("observability request error = %v", err)
		}
	}
	if adminRequests.Load() == 0 || pprofRequests.Load() == 0 {
		t.Fatalf("observability request counts admin=%d pprof=%d", adminRequests.Load(), pprofRequests.Load())
	}

	// 使用 nearest-rank 记录当前环境的尾延迟，同时只用一秒硬上限阻止卡死或数量级退化。
	sort.Slice(latencies, func(left, right int) bool { return latencies[left] < latencies[right] })
	percentile := func(percent int) time.Duration {
		position := (len(latencies)*percent + 99) / 100
		return latencies[position-1]
	}
	p50, p95, p99 := percentile(50), percentile(95), percentile(99)
	if p99 >= time.Second {
		t.Fatalf("observability task P99 = %s, want < 1s", p99)
	}
	t.Logf(
		"observability tasks=%d admin_requests=%d pprof_requests=%d P50=%s P95=%s P99=%s",
		len(latencies),
		adminRequests.Load(),
		pprofRequests.Load(),
		p50,
		p95,
		p99,
	)
	if stats := target.ExecutionStats(); stats.CompletedTotal < sampleCount {
		t.Fatalf("observability ExecutionStats() = %+v, want at least %d completed", stats, sampleCount)
	}

	// 正式停止后两个端口必须可立即重绑，所有测试 Worker 和运行时 goroutine 最终回落。
	stopApplication()
	assertAddressReleased(t, adminAddress)
	assertAddressReleased(t, pprofAddress)
	resourceDeadline := time.Now().Add(3 * time.Second)
	for {
		runtime.GC()
		if runtime.NumGoroutine() <= baselineGoroutines+12 {
			break
		}
		if time.Now().After(resourceDeadline) {
			t.Fatalf(
				"observability goroutines = %d, baseline = %d",
				runtime.NumGoroutine(),
				baselineGoroutines,
			)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// TestInitialAdminBindFailureRollsBackBuiltNodes 防止 Admin Listener 绑定失败后进入任何
// Service 生命周期，并固定已装配 Node 的底层资源由 Rollback 回收。
func TestInitialAdminBindFailureRollsBackBuiltNodes(t *testing.T) {
	directory := writeApplicationConfig(t, `
nodes:
  - id: gateway-1
    services:
      - httpLifecycleService
`)
	occupied, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer occupied.Close()
	httpLifecycleProviderSeen = make(chan struct{}, 1)
	httpLifecycleInitSeen = make(chan bool, 1)
	httpLifecycleStartSeen = make(chan bool, 1)
	httpLifecycleStopSeen = make(chan bool, 1)
	app := New(Options{
		LogHandlerFactory: func(originlog.Config) (originlog.Handler, error) {
			return &silentHandler{}, nil
		},
	})
	app.Setup(&httpLifecycleService{})
	err = app.run(context.Background(), command.StartRequest{
		AppName:      "http-rollback",
		ConfigDir:    directory,
		AdminAddress: occupied.Addr().String(),
		PprofAddress: "127.0.0.1:0",
	})
	if !errors.Is(err, errs.ErrAdminUnavailable) {
		t.Fatalf("run() error = %v", err)
	}
	select {
	case <-httpLifecycleInitSeen:
		t.Fatal("Service.OnInit entered after Admin bind failure")
	default:
	}
	select {
	case <-httpLifecycleStartSeen:
		t.Fatal("Service.OnStart entered after Admin bind failure")
	default:
	}
	if _, ok := app.AdminAddress(); ok {
		t.Fatal("Admin leaked after bind failure")
	}
	if _, ok := app.PprofAddress(); ok {
		t.Fatal("pprof reported active after bind failure")
	}
	nodes := app.Nodes()
	if len(nodes) != 1 || nodes[0].State() != node.StateFailed {
		t.Fatalf("built Node rollback states = %#v", nodes)
	}
}

// TestInitialPprofBindFailureRollsBackAdminAndBuiltNodes 防止第二个 Listener 绑定失败时
// 泄漏已启动的 Admin，或让任何 Service 生命周期越过启动事务边界。
func TestInitialPprofBindFailureRollsBackAdminAndBuiltNodes(t *testing.T) {
	directory := writeApplicationConfig(t, `
nodes:
  - id: gateway-1
    services:
      - httpLifecycleService
`)
	occupied, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer occupied.Close()
	httpLifecycleProviderSeen = make(chan struct{}, 1)
	httpLifecycleInitSeen = make(chan bool, 1)
	httpLifecycleStartSeen = make(chan bool, 1)
	httpLifecycleStopSeen = make(chan bool, 1)
	app := New(Options{
		LogHandlerFactory: func(originlog.Config) (originlog.Handler, error) {
			return &silentHandler{}, nil
		},
	})
	app.Setup(&httpLifecycleService{})
	err = app.run(context.Background(), command.StartRequest{
		AppName:      "pprof-rollback",
		ConfigDir:    directory,
		AdminAddress: "127.0.0.1:0",
		PprofAddress: occupied.Addr().String(),
	})
	if !errors.Is(err, errs.ErrDiagnosticsUnavailable) {
		t.Fatalf("run() error = %v", err)
	}
	select {
	case <-httpLifecycleInitSeen:
		t.Fatal("Service.OnInit entered after pprof bind failure")
	default:
	}
	select {
	case <-httpLifecycleStartSeen:
		t.Fatal("Service.OnStart entered after pprof bind failure")
	default:
	}
	if _, ok := app.AdminAddress(); ok {
		t.Fatal("Admin leaked after pprof bind failure")
	}
	if _, ok := app.PprofAddress(); ok {
		t.Fatal("pprof reported active after bind failure")
	}
	nodes := app.Nodes()
	if len(nodes) != 1 || nodes[0].State() != node.StateFailed {
		t.Fatalf("built Node rollback states = %#v", nodes)
	}
}

// TestAdminFreezeFailureRollsBackBuiltNodesBeforeLifecycle 固定 Provider 收集或端点校验失败
// 与 Listener 绑定失败共享同一启动事务：不得进入 OnInit，构建资源必须回滚。
func TestAdminFreezeFailureRollsBackBuiltNodesBeforeLifecycle(t *testing.T) {
	directory := writeApplicationConfig(t, `
nodes:
  - id: gateway-1
    services:
      - invalidAdminLifecycleService
`)
	invalidAdminLifecycleInitSeen = make(chan struct{}, 1)
	app := New(Options{
		LogHandlerFactory: func(originlog.Config) (originlog.Handler, error) {
			return &silentHandler{}, nil
		},
	})
	app.Setup(&invalidAdminLifecycleService{})
	err := app.run(context.Background(), command.StartRequest{
		AppName:      "freeze-rollback",
		ConfigDir:    directory,
		AdminAddress: "127.0.0.1:0",
	})
	if !errors.Is(err, errs.ErrInvalidConfig) {
		t.Fatalf("run() error = %v", err)
	}
	select {
	case <-invalidAdminLifecycleInitSeen:
		t.Fatal("Service.OnInit entered after Admin freeze failure")
	default:
	}
	if _, ok := app.AdminAddress(); ok {
		t.Fatal("Admin reported active after freeze failure")
	}
	nodes := app.Nodes()
	if len(nodes) != 1 || nodes[0].State() != node.StateFailed {
		t.Fatalf("built Node rollback states = %#v", nodes)
	}
}

func receiveApplicationValue[T any](t *testing.T, source <-chan T) T {
	t.Helper()
	select {
	case value := <-source:
		return value
	case <-time.After(2 * time.Second):
		t.Fatal("waiting for Application lifecycle value timed out")
		var zero T
		return zero
	}
}

func assertAddressReleased(t *testing.T, address string) {
	t.Helper()
	listener, err := net.Listen("tcp", address)
	if err != nil {
		t.Fatalf("address %q not released: %v", address, err)
	}
	_ = listener.Close()
}
