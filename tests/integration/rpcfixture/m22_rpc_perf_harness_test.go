package rpcfixture

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/url"
	"os"
	"os/exec"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	publicprovider "github.com/duanhf2012/origin/v3/discovery/provider"
	"github.com/duanhf2012/origin/v3/internal/bufferpool"
	etcddiscovery "github.com/duanhf2012/origin/v3/internal/discovery/etcd"
	originlog "github.com/duanhf2012/origin/v3/log"
	"github.com/duanhf2012/origin/v3/node"
	"github.com/duanhf2012/origin/v3/rpc"
	"github.com/duanhf2012/origin/v3/service"
	"github.com/nats-io/nats.go"
	clientv3 "go.etcd.io/etcd/client/v3"
	"go.etcd.io/etcd/server/v3/embed"
)

const (
	m22RPCPerfEnabled       = "ORIGIN_M22_RPC_PERF"
	m22RPCPerfShortEnabled  = "ORIGIN_M22_RPC_PERF_SHORT"
	m22RPCPerfHelperEnabled = "ORIGIN_M22_RPC_PERF_HELPER"
	m22RPCPerfReadyLine     = "ORIGIN_M22_RPC_PERF_READY"
	m22RPCPerfOutputPrefix  = "ORIGIN_M22_RPC_PERF_JSON "
	m22RPCPerfMaxSamples    = 20_000
)

type m22RPCPerfEnvironment struct {
	Kind            string    `json:"kind"`
	SchemaVersion   uint32    `json:"schema_version"`
	CollectedAt     time.Time `json:"collected_at"`
	Short           bool      `json:"short"`
	Commit          string    `json:"commit"`
	GoVersion       string    `json:"go_version"`
	GOOS            string    `json:"goos"`
	GOARCH          string    `json:"goarch"`
	CPU             string    `json:"cpu"`
	LogicalCPUs     int       `json:"logical_cpus"`
	GOMAXPROCS      int       `json:"gomaxprocs"`
	MemoryBytes     uint64    `json:"memory_bytes"`
	EtcdVersion     string    `json:"etcd_version"`
	NATSVersion     string    `json:"nats_version"`
	EtcdEndpoints   []string  `json:"etcd_endpoints"`
	NATSURLs        []string  `json:"nats_urls"`
	Warmup          string    `json:"warmup"`
	Measure         string    `json:"measure"`
	Rounds          int       `json:"rounds"`
	ResultCaseCount int       `json:"result_case_count"`
}

type m22RPCPerfResult struct {
	Kind          string         `json:"kind"`
	SchemaVersion uint32         `json:"schema_version"`
	Case          m22RPCPerfCase `json:"case"`
	Round         int            `json:"round"`
	Measure       string         `json:"measure"`
	Completed     uint64         `json:"completed"`
	QPS           float64        `json:"qps"`
	MiBPerSecond  float64        `json:"mib_per_second"`
	P50Micros     float64        `json:"p50_micros"`
	P95Micros     float64        `json:"p95_micros"`
	P99Micros     float64        `json:"p99_micros"`
	Errors        uint64         `json:"errors"`
	Timeouts      uint64         `json:"timeouts"`
	BytesPerOp    float64        `json:"bytes_per_op"`
	AllocsPerOp   float64        `json:"allocs_per_op"`
	Samples       int            `json:"samples"`
	PendingEnd    uint64         `json:"pending_end"`
}

type m22RPCPerfFixture struct {
	callerNode   *node.Node
	caller       *CallerService
	targetNodeID string
	closeOnce    sync.Once
	close        func(testing.TB)
}

type m22RPCPerfProcess struct {
	stdin      io.WriteCloser
	process    *os.Process
	done       <-chan error
	stdout     *m22RPCPerfOutput
	stdoutDone <-chan struct{}
	stderr     *bytes.Buffer
	closeOnce  sync.Once
}

// m22RPCPerfOutput 允许就绪扫描器和后续排空 goroutine 共用同一份诊断输出；Close 只在
// 子进程退出后读取，但互斥仍固定了失败路径的并发可见性，避免测试工具自身触发 Race。
type m22RPCPerfOutput struct {
	mu     sync.Mutex
	buffer bytes.Buffer
}

func (output *m22RPCPerfOutput) Write(payload []byte) (int, error) {
	output.mu.Lock()
	defer output.mu.Unlock()
	return output.buffer.Write(payload)
}

func (output *m22RPCPerfOutput) String() string {
	output.mu.Lock()
	defer output.mu.Unlock()
	return output.buffer.String()
}

type m22RPCPerfWorker struct {
	samples        []int64
	semanticErrors uint64
	finished       chan struct{}
	finishOnce     sync.Once
}

type m22RPCPerfPhase struct {
	stats          m22RPCPerfStats
	samples        []int64
	semanticErrors uint64
}

type m22RPCPerfStats struct {
	Pending          uint64
	Completed        uint64
	Failed           uint64
	Timeout          uint64
	Rejected         uint64
	PayloadSentBytes uint64
	PayloadRecvBytes uint64
}

// TestM22RPCPerformanceMatrix 是显式发布压测入口。普通 go test 不设置门禁时不会启动
// 长任务；SHORT=1 使用嵌入式基础设施和缩短时长验证所有分支，正式模式严格使用 5s/15s/3 轮。
func TestM22RPCPerformanceMatrix(t *testing.T) {
	short := os.Getenv(m22RPCPerfShortEnabled) == "1"
	full := os.Getenv(m22RPCPerfEnabled) == "1"
	if !short && !full {
		t.Skip("未设置 M22 RPC 性能门禁")
	}
	if short && full {
		t.Fatal("M22 RPC 性能长模式和短模式不能同时启用")
	}

	endpoints := splitM22RPCPerfList(os.Getenv("ORIGIN_M22_ETCD_ENDPOINTS"))
	natsURLs := splitM22RPCPerfList(os.Getenv("ORIGIN_M22_NATS_URLS"))
	if short {
		endpoints = []string{startM22RPCPerfEtcd(t)}
		running := startRPCNATSServer(t)
		natsURLs = []string{running.ClientURL()}
	} else {
		if len(endpoints) == 0 {
			t.Fatal("正式 M22 性能测试必须设置 ORIGIN_M22_ETCD_ENDPOINTS")
		}
		if len(natsURLs) == 0 {
			t.Fatal("正式 M22 性能测试必须设置 ORIGIN_M22_NATS_URLS")
		}
	}

	profile := m22RPCPerfProfile(short)
	emitM22RPCPerfJSON(t, m22RPCPerfEnvironment{
		Kind:            "environment",
		SchemaVersion:   1,
		CollectedAt:     time.Now().UTC(),
		Short:           short,
		Commit:          m22RPCPerfCommit(),
		GoVersion:       runtime.Version(),
		GOOS:            runtime.GOOS,
		GOARCH:          runtime.GOARCH,
		CPU:             m22RPCPerfCPU(),
		LogicalCPUs:     runtime.NumCPU(),
		GOMAXPROCS:      runtime.GOMAXPROCS(0),
		MemoryBytes:     m22RPCPerfMemoryBytes(),
		EtcdVersion:     m22RPCPerfEtcdVersion(endpoints),
		NATSVersion:     m22RPCPerfNATSVersion(natsURLs),
		EtcdEndpoints:   endpoints,
		NATSURLs:        natsURLs,
		Warmup:          profile.Warmup.String(),
		Measure:         profile.Measure.String(),
		Rounds:          profile.Rounds,
		ResultCaseCount: len(m22RPCPerfCases()) * profile.Rounds,
	})

	for _, transport := range []m22RPCPerfTransport{
		m22RPCPerfLocal,
		m22RPCPerfTCP,
		m22RPCPerfNATS,
	} {
		fixture := newM22RPCPerfFixture(t, transport, endpoints, natsURLs)
		for _, current := range m22RPCPerfCases() {
			if current.Transport != transport {
				continue
			}
			for round := 1; round <= profile.Rounds; round++ {
				runM22RPCPerfRound(t, fixture, current, profile, round)
			}
		}
		fixture.Close(t)
	}
}

// TestM22RPCPerfProcessHelper 只由正式性能入口作为独立目标进程启动。
func TestM22RPCPerfProcessHelper(t *testing.T) {
	if os.Getenv(m22RPCPerfHelperEnabled) != "1" {
		return
	}
	transport := m22RPCPerfTransport(os.Getenv("ORIGIN_M22_RPC_PERF_TRANSPORT"))
	endpoints := splitM22RPCPerfList(os.Getenv("ORIGIN_M22_ETCD_ENDPOINTS"))
	natsURLs := splitM22RPCPerfList(os.Getenv("ORIGIN_M22_NATS_URLS"))
	namespace := os.Getenv("ORIGIN_M22_RPC_PERF_NAMESPACE")
	subjectNamespace := os.Getenv("ORIGIN_M22_RPC_PERF_NATS_NAMESPACE")
	targetAddress := os.Getenv("ORIGIN_M22_RPC_PERF_TARGET_ADDRESS")
	config := m22RPCPerfTransportConfig(
		t,
		transport,
		targetAddress,
		natsURLs,
		subjectNamespace,
	)
	pool := bufferpool.NewPool(bufferpool.Options{})
	target := newM22RPCPerfProviderNode(
		t,
		"player-m22-perf",
		config,
		pool,
		endpoints,
		namespace,
		node.ServiceBinding{
			Name:     "PlayerService",
			Template: "PlayerService",
			Service:  &PlayerService{},
		},
	)
	startM22RPCPerfNode(t, target)
	fmt.Printf("%s ready\n", m22RPCPerfReadyLine)
	if _, err := io.Copy(io.Discard, os.Stdin); err != nil {
		t.Fatalf("等待 M22 性能父进程关闭信号: %v", err)
	}
	stopTestNode(t, target)
}

func runM22RPCPerfRound(
	t *testing.T,
	fixture *m22RPCPerfFixture,
	current m22RPCPerfCase,
	profile m22RPCPerfRunProfile,
	round int,
) {
	t.Helper()
	_ = runM22RPCPerfPhase(t, fixture, current, profile.Warmup, false)
	runtime.GC()
	beforeStats := readM22RPCPerfStats(fixture.callerNode, current.Transport)
	var beforeMemory runtime.MemStats
	runtime.ReadMemStats(&beforeMemory)
	phase := runM22RPCPerfPhase(t, fixture, current, profile.Measure, true)
	var afterMemory runtime.MemStats
	runtime.ReadMemStats(&afterMemory)
	delta := phase.stats.Sub(beforeStats)
	if delta.Completed == 0 {
		t.Fatalf("M22 RPC performance case has no completions: %+v", current)
	}
	p50, p95, p99 := m22RPCPerfPercentiles(phase.samples)
	seconds := profile.Measure.Seconds()
	errors := delta.Failed + delta.Rejected + phase.semanticErrors
	result := m22RPCPerfResult{
		Kind:          "result",
		SchemaVersion: 1,
		Case:          current,
		Round:         round,
		Measure:       profile.Measure.String(),
		Completed:     delta.Completed,
		QPS:           float64(delta.Completed) / seconds,
		MiBPerSecond: float64(delta.Completed*uint64(current.PayloadBytes)) /
			(1024 * 1024) / seconds,
		P50Micros: float64(p50) / float64(time.Microsecond),
		P95Micros: float64(p95) / float64(time.Microsecond),
		P99Micros: float64(p99) / float64(time.Microsecond),
		Errors:    errors,
		Timeouts:  delta.Timeout,
		BytesPerOp: float64(afterMemory.TotalAlloc-beforeMemory.TotalAlloc) /
			float64(delta.Completed),
		AllocsPerOp: float64(afterMemory.Mallocs-beforeMemory.Mallocs) /
			float64(delta.Completed),
		Samples:    len(phase.samples),
		PendingEnd: phase.stats.Pending,
	}
	emitM22RPCPerfJSON(t, result)
	if result.Errors != 0 || result.Timeouts != 0 || result.PendingEnd != 0 {
		t.Fatalf("M22 RPC performance case failed: %+v", result)
	}
}

func runM22RPCPerfPhase(
	t *testing.T,
	fixture *m22RPCPerfFixture,
	current m22RPCPerfCase,
	duration time.Duration,
	capture bool,
) m22RPCPerfPhase {
	t.Helper()
	payload := make(OwnedBlob, current.PayloadBytes)
	for index := range payload {
		payload[index] = byte(index)
	}
	workers := make([]*m22RPCPerfWorker, current.Concurrency)
	var stop atomic.Bool
	measureEnd := time.Now().Add(duration)
	samplesPerWorker := m22RPCPerfMaxSamples / current.Concurrency
	if samplesPerWorker < 1 {
		samplesPerWorker = 1
	}
	for index := range workers {
		workers[index] = &m22RPCPerfWorker{
			samples:  make([]int64, 0, samplesPerWorker),
			finished: make(chan struct{}),
		}
		switch current.Method {
		case m22RPCPerfAsync:
			startM22RPCPerfAsyncWorker(
				t,
				fixture,
				workers[index],
				payload,
				&stop,
				measureEnd,
				capture,
				samplesPerWorker,
			)
		case m22RPCPerfAwait:
			startM22RPCPerfAwaitWorker(
				t,
				fixture,
				workers[index],
				payload,
				&stop,
				measureEnd,
				capture,
				samplesPerWorker,
			)
		default:
			t.Fatalf("unknown M22 RPC performance method %q", current.Method)
		}
	}

	timer := time.NewTimer(time.Until(measureEnd))
	<-timer.C
	stop.Store(true)
	endpointStats := readM22RPCPerfStats(fixture.callerNode, current.Transport)
	deadline := time.NewTimer(30 * time.Second)
	defer deadline.Stop()
	for _, worker := range workers {
		select {
		case <-worker.finished:
		case <-deadline.C:
			t.Fatalf("M22 RPC performance worker did not drain: %+v", current)
		}
	}
	waitM22RPCPerfPending(t, fixture.callerNode, current.Transport)
	// 吞吐累计值在线性化测量终点冻结；Pending 则在排空后读取，证明本轮没有把
	// 未完成调用泄漏给下一轮。两者刻意来自不同时间点，含义由字段分别表达。
	endpointStats.Pending = readM22RPCPerfStats(
		fixture.callerNode,
		current.Transport,
	).Pending
	result := m22RPCPerfPhase{stats: endpointStats}
	if capture {
		result.samples = make([]int64, 0, m22RPCPerfMaxSamples)
	}
	for _, worker := range workers {
		result.semanticErrors += worker.semanticErrors
		result.samples = append(result.samples, worker.samples...)
	}
	return result
}

func startM22RPCPerfAwaitWorker(
	t *testing.T,
	fixture *m22RPCPerfFixture,
	worker *m22RPCPerfWorker,
	payload OwnedBlob,
	stop *atomic.Bool,
	measureEnd time.Time,
	capture bool,
	maxSamples int,
) {
	t.Helper()
	if err := fixture.caller.DispatchAsync(func(ctx context.Context) {
		defer worker.finish()
		client := fixture.Client()
		for !stop.Load() {
			started := time.Now()
			value, err := client.AwaitRoundTripBlob(ctx, payload)
			worker.record(started, time.Now(), len(value), len(payload), err, measureEnd, capture, maxSamples)
		}
	}); err != nil {
		t.Fatalf("start M22 Await worker: %v", err)
	}
}

func startM22RPCPerfAsyncWorker(
	t *testing.T,
	fixture *m22RPCPerfFixture,
	worker *m22RPCPerfWorker,
	payload OwnedBlob,
	stop *atomic.Bool,
	measureEnd time.Time,
	capture bool,
	maxSamples int,
) {
	t.Helper()
	var issue func(context.Context)
	issue = func(ctx context.Context) {
		if stop.Load() {
			worker.finish()
			return
		}
		started := time.Now()
		err := fixture.Client().AsyncRoundTripBlob(
			ctx,
			payload,
			func(callbackCtx context.Context, value OwnedBlob, callErr error) {
				worker.record(
					started,
					time.Now(),
					len(value),
					len(payload),
					callErr,
					measureEnd,
					capture,
					maxSamples,
				)
				issue(callbackCtx)
			},
		)
		if err == nil {
			return
		}
		worker.record(started, time.Now(), 0, len(payload), err, measureEnd, capture, maxSamples)
		if stop.Load() {
			worker.finish()
			return
		}
		if dispatchErr := fixture.caller.DispatchAsync(issue); dispatchErr != nil {
			worker.semanticErrors++
			worker.finish()
		}
	}
	if err := fixture.caller.DispatchAsync(issue); err != nil {
		t.Fatalf("start M22 Async worker: %v", err)
	}
}

func (worker *m22RPCPerfWorker) record(
	started time.Time,
	finished time.Time,
	resultBytes int,
	wantBytes int,
	err error,
	measureEnd time.Time,
	capture bool,
	maxSamples int,
) {
	if finished.After(measureEnd) {
		return
	}
	if err == nil && resultBytes != wantBytes {
		worker.semanticErrors++
	}
	if capture && len(worker.samples) < maxSamples {
		worker.samples = append(worker.samples, finished.Sub(started).Nanoseconds())
	}
}

func (worker *m22RPCPerfWorker) finish() {
	worker.finishOnce.Do(func() { close(worker.finished) })
}

func newM22RPCPerfFixture(
	t *testing.T,
	transport m22RPCPerfTransport,
	endpoints []string,
	natsURLs []string,
) *m22RPCPerfFixture {
	t.Helper()
	if transport == m22RPCPerfLocal {
		return newM22RPCPerfLocalFixture(t)
	}
	return newM22RPCPerfRemoteFixture(t, transport, endpoints, natsURLs)
}

func newM22RPCPerfLocalFixture(t *testing.T) *m22RPCPerfFixture {
	t.Helper()
	pool := bufferpool.NewPool(bufferpool.Options{})
	caller := &CallerService{}
	instance, err := node.New(
		node.Config{ID: "local-m22-perf", Scheduler: service.DefaultSchedulerConfig()},
		[]node.ServiceBinding{
			{Name: "CallerService", Template: "CallerService", Service: caller},
			{Name: "PlayerService", Template: "PlayerService", Service: &PlayerService{}},
		},
		originlog.NewNop(),
		node.Options{
			MaxTimersPerNode: 1024,
			TimerLocation:    time.Local,
			BufferPool:       pool,
		},
	)
	if err != nil {
		t.Fatalf("create local M22 performance Node: %v", err)
	}
	startM22RPCPerfNode(t, instance)
	fixture := &m22RPCPerfFixture{callerNode: instance, caller: caller}
	fixture.close = func(tb testing.TB) { stopTestNode(tb, instance) }
	return fixture
}

func newM22RPCPerfRemoteFixture(
	t *testing.T,
	transport m22RPCPerfTransport,
	endpoints []string,
	natsURLs []string,
) *m22RPCPerfFixture {
	t.Helper()
	unique := strconv.FormatInt(time.Now().UnixNano(), 10)
	namespace := "/origin-m22-perf-" + unique
	subjectNamespace := "origin-m22-perf-" + unique
	targetAddress := ""
	if transport == m22RPCPerfTCP {
		targetAddress = reserveM22RPCPerfAddress(t)
	}
	process := startM22RPCPerfProcess(
		t,
		transport,
		endpoints,
		natsURLs,
		namespace,
		subjectNamespace,
		targetAddress,
	)

	callerAddress := ""
	if transport == m22RPCPerfTCP {
		callerAddress = reserveM22RPCPerfAddress(t)
	}
	config := m22RPCPerfTransportConfig(
		t,
		transport,
		callerAddress,
		natsURLs,
		subjectNamespace,
	)
	pool := bufferpool.NewPool(bufferpool.Options{})
	caller := &CallerService{}
	callerNode := newM22RPCPerfProviderNode(
		t,
		"gateway-m22-perf",
		config,
		pool,
		endpoints,
		namespace,
		node.ServiceBinding{
			Name:     "CallerService",
			Template: "CallerService",
			Service:  caller,
		},
	)
	startM22RPCPerfNode(t, callerNode)
	fixture := &m22RPCPerfFixture{
		callerNode:   callerNode,
		caller:       caller,
		targetNodeID: "player-m22-perf",
	}
	fixture.close = func(tb testing.TB) {
		stopTestNode(tb, callerNode)
		process.Close(tb)
	}
	waitM22RPCPerfRoute(t, fixture)
	return fixture
}

func newM22RPCPerfProviderNode(
	t testing.TB,
	nodeID string,
	config rpc.Config,
	pool *bufferpool.Pool,
	endpoints []string,
	namespace string,
	binding node.ServiceBinding,
) *node.Node {
	t.Helper()
	rawConfig := map[string]any{
		"endpoints":     endpoints,
		"namespace":     namespace,
		"local_network": "origin",
		"ttl":           "15s",
	}
	username := strings.TrimSpace(os.Getenv("ORIGIN_M22_ETCD_USERNAME"))
	password := os.Getenv("ORIGIN_M22_ETCD_PASSWORD")
	token := os.Getenv("ORIGIN_M22_ETCD_TOKEN")
	if username != "" || password != "" || token != "" {
		rawConfig["auth"] = map[string]any{
			"username": username,
			"password": password,
			"token":    token,
		}
	}
	providerConfig, err := publicprovider.NewConfig(rawConfig)
	if err != nil {
		t.Fatalf("create M22 etcd provider config: %v", err)
	}
	instance, err := node.New(
		node.Config{ID: nodeID, Scheduler: service.DefaultSchedulerConfig(), RPC: &config},
		[]node.ServiceBinding{binding},
		originlog.NewNop(),
		node.Options{
			MaxTimersPerNode: 1024,
			TimerLocation:    time.Local,
			BufferPool:       pool,
			DiscoveryKind:    "etcd",
			DiscoveryConfig:  providerConfig,
			DiscoveryFactory: etcddiscovery.NewFactory(""),
		},
	)
	if err != nil {
		t.Fatalf("create M22 performance Node %q: %v", nodeID, err)
	}
	return instance
}

func m22RPCPerfTransportConfig(
	t testing.TB,
	transport m22RPCPerfTransport,
	address string,
	natsURLs []string,
	subjectNamespace string,
) rpc.Config {
	t.Helper()
	switch transport {
	case m22RPCPerfTCP:
		config := rpc.DefaultConfig()
		config.TCP.Listen = address
		config.TCP.Advertise = address
		return config
	case m22RPCPerfNATS:
		config := rpc.Config{
			Transport:        rpc.TransportNATS,
			MaxPayloadSize:   rpc.DefaultMaxPayloadSize,
			MaxBroadcastSize: rpc.DefaultMaxBroadcastSize,
			NATS:             rpc.DefaultNATSConfig(),
		}
		config.NATS.Namespace = subjectNamespace
		config.NATS.URLs = append([]string(nil), natsURLs...)
		config.NATS.Auth.Username = os.Getenv("ORIGIN_M22_NATS_USERNAME")
		config.NATS.Auth.Password = os.Getenv("ORIGIN_M22_NATS_PASSWORD")
		config.NATS.Auth.Token = os.Getenv("ORIGIN_M22_NATS_TOKEN")
		return config
	default:
		t.Fatalf("unsupported remote M22 performance transport %q", transport)
		return rpc.Config{}
	}
}

func startM22RPCPerfNode(t testing.TB, instance *node.Node) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := instance.Start(ctx); err != nil {
		t.Fatalf("start M22 performance Node %q: %v", instance.ID(), err)
	}
}

func (fixture *m22RPCPerfFixture) Client() PlayerRPCClient {
	client := BindPlayerRPC(fixture.caller)
	if fixture.targetNodeID != "" {
		client = client.OnNode(fixture.targetNodeID)
	}
	return client
}

func (fixture *m22RPCPerfFixture) Close(t testing.TB) {
	if fixture == nil {
		return
	}
	fixture.closeOnce.Do(func() {
		if fixture.close != nil {
			fixture.close(t)
		}
	})
}

func waitM22RPCPerfRoute(t *testing.T, fixture *m22RPCPerfFixture) {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for {
		done := make(chan error, 1)
		if err := fixture.caller.DispatchAsync(func(ctx context.Context) {
			value, callErr := fixture.Client().AwaitRoundTripBlob(ctx, OwnedBlob{1})
			if callErr == nil && len(value) != 1 {
				callErr = fmt.Errorf("probe response size = %d", len(value))
			}
			done <- callErr
		}); err != nil {
			t.Fatalf("dispatch M22 performance probe: %v", err)
		}
		err := <-done
		if err == nil {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("M22 performance route did not become ready: %v", err)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func startM22RPCPerfProcess(
	t *testing.T,
	transport m22RPCPerfTransport,
	endpoints []string,
	natsURLs []string,
	namespace string,
	subjectNamespace string,
	targetAddress string,
) *m22RPCPerfProcess {
	t.Helper()
	command := exec.Command(
		os.Args[0],
		"-test.run=^TestM22RPCPerfProcessHelper$",
		"-test.v",
	)
	command.Env = append(
		os.Environ(),
		m22RPCPerfHelperEnabled+"=1",
		"ORIGIN_M22_RPC_PERF_TRANSPORT="+string(transport),
		"ORIGIN_M22_ETCD_ENDPOINTS="+strings.Join(endpoints, ","),
		"ORIGIN_M22_NATS_URLS="+strings.Join(natsURLs, ","),
		"ORIGIN_M22_RPC_PERF_NAMESPACE="+namespace,
		"ORIGIN_M22_RPC_PERF_NATS_NAMESPACE="+subjectNamespace,
		"ORIGIN_M22_RPC_PERF_TARGET_ADDRESS="+targetAddress,
	)
	stdin, err := command.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	stdout, err := command.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	stderr := &bytes.Buffer{}
	stdoutOutput := &m22RPCPerfOutput{}
	command.Stderr = stderr
	if err := command.Start(); err != nil {
		t.Fatalf("start M22 target process: %v", err)
	}
	done := make(chan error, 1)
	go func() { done <- command.Wait() }()
	process := &m22RPCPerfProcess{
		stdin:   stdin,
		process: command.Process,
		done:    done,
		stdout:  stdoutOutput,
		stderr:  stderr,
	}
	t.Cleanup(func() { process.Close(t) })

	scanner := bufio.NewScanner(io.TeeReader(stdout, stdoutOutput))
	for scanner.Scan() {
		if strings.HasPrefix(scanner.Text(), m22RPCPerfReadyLine+" ") {
			stdoutDone := make(chan struct{})
			process.stdoutDone = stdoutDone
			go func() {
				defer close(stdoutDone)
				_, _ = io.Copy(stdoutOutput, stdout)
			}()
			return process
		}
	}
	process.Close(t)
	t.Fatalf(
		"M22 target process did not become ready: scan=%v stdout=%s stderr=%s",
		scanner.Err(),
		stdoutOutput.String(),
		stderr.String(),
	)
	return nil
}

func (process *m22RPCPerfProcess) Close(t testing.TB) {
	if process == nil {
		return
	}
	process.closeOnce.Do(func() {
		_ = process.stdin.Close()
		select {
		case err := <-process.done:
			if process.stdoutDone != nil {
				<-process.stdoutDone
			}
			if err != nil {
				t.Errorf(
					"M22 target process exit: %v\nstdout=%s\nstderr=%s",
					err,
					process.stdout.String(),
					process.stderr.String(),
				)
			}
		case <-time.After(15 * time.Second):
			_ = process.process.Kill()
			t.Errorf(
				"M22 target process did not stop\nstdout=%s\nstderr=%s",
				process.stdout.String(),
				process.stderr.String(),
			)
		}
	})
}

func readM22RPCPerfStats(
	instance *node.Node,
	transport m22RPCPerfTransport,
) m22RPCPerfStats {
	snapshot := instance.Diagnostics().RPC
	var current struct {
		Pending              uint64
		OutboundCompleted    uint64
		OutboundFailed       uint64
		OutboundTimeout      uint64
		OutboundRejected     uint64
		PayloadSentBytes     uint64
		PayloadReceivedBytes uint64
	}
	switch transport {
	case m22RPCPerfLocal:
		current.Pending = snapshot.Local.Pending
		current.OutboundCompleted = snapshot.Local.OutboundCompleted
		current.OutboundFailed = snapshot.Local.OutboundFailed
		current.OutboundTimeout = snapshot.Local.OutboundTimeout
		current.OutboundRejected = snapshot.Local.OutboundRejected
		current.PayloadSentBytes = snapshot.Local.PayloadSentBytes
		current.PayloadReceivedBytes = snapshot.Local.PayloadReceivedBytes
	case m22RPCPerfTCP:
		current.Pending = snapshot.TCP.Pending
		current.OutboundCompleted = snapshot.TCP.OutboundCompleted
		current.OutboundFailed = snapshot.TCP.OutboundFailed
		current.OutboundTimeout = snapshot.TCP.OutboundTimeout
		current.OutboundRejected = snapshot.TCP.OutboundRejected
		current.PayloadSentBytes = snapshot.TCP.PayloadSentBytes
		current.PayloadReceivedBytes = snapshot.TCP.PayloadReceivedBytes
	case m22RPCPerfNATS:
		current.Pending = snapshot.NATS.Pending
		current.OutboundCompleted = snapshot.NATS.OutboundCompleted
		current.OutboundFailed = snapshot.NATS.OutboundFailed
		current.OutboundTimeout = snapshot.NATS.OutboundTimeout
		current.OutboundRejected = snapshot.NATS.OutboundRejected
		current.PayloadSentBytes = snapshot.NATS.PayloadSentBytes
		current.PayloadReceivedBytes = snapshot.NATS.PayloadReceivedBytes
	}
	return m22RPCPerfStats{
		Pending:          current.Pending,
		Completed:        current.OutboundCompleted,
		Failed:           current.OutboundFailed,
		Timeout:          current.OutboundTimeout,
		Rejected:         current.OutboundRejected,
		PayloadSentBytes: current.PayloadSentBytes,
		PayloadRecvBytes: current.PayloadReceivedBytes,
	}
}

func (stats m22RPCPerfStats) Sub(before m22RPCPerfStats) m22RPCPerfStats {
	return m22RPCPerfStats{
		Pending:          stats.Pending,
		Completed:        stats.Completed - before.Completed,
		Failed:           stats.Failed - before.Failed,
		Timeout:          stats.Timeout - before.Timeout,
		Rejected:         stats.Rejected - before.Rejected,
		PayloadSentBytes: stats.PayloadSentBytes - before.PayloadSentBytes,
		PayloadRecvBytes: stats.PayloadRecvBytes - before.PayloadRecvBytes,
	}
}

func waitM22RPCPerfPending(
	t testing.TB,
	instance *node.Node,
	transport m22RPCPerfTransport,
) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		stats := readM22RPCPerfStats(instance, transport)
		if stats.Pending == 0 {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("M22 RPC performance pending did not drain: %+v", stats)
		}
		time.Sleep(time.Millisecond)
	}
}

func m22RPCPerfPercentiles(samples []int64) (int64, int64, int64) {
	if len(samples) == 0 {
		return 0, 0, 0
	}
	sort.Slice(samples, func(i int, j int) bool { return samples[i] < samples[j] })
	value := func(percent int) int64 {
		index := (len(samples)*percent + 99) / 100
		if index == 0 {
			index = 1
		}
		return samples[index-1]
	}
	return value(50), value(95), value(99)
}

func emitM22RPCPerfJSON(t testing.TB, value any) {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("marshal M22 RPC performance JSON: %v", err)
	}
	fmt.Printf("%s%s\n", m22RPCPerfOutputPrefix, data)
}

func splitM22RPCPerfList(raw string) []string {
	parts := strings.Split(raw, ",")
	result := make([]string, 0, len(parts))
	for _, part := range parts {
		if value := strings.TrimSpace(part); value != "" {
			result = append(result, value)
		}
	}
	return result
}

func reserveM22RPCPerfAddress(t testing.TB) string {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("reserve M22 RPC performance address: %v", err)
	}
	address := listener.Addr().String()
	if err := listener.Close(); err != nil {
		t.Fatalf("release M22 RPC performance address: %v", err)
	}
	return address
}

func startM22RPCPerfEtcd(t *testing.T) string {
	t.Helper()
	clientURL := reserveM22RPCPerfURL(t)
	peerURL := reserveM22RPCPerfURL(t)
	config := embed.NewConfig()
	config.Dir = t.TempDir()
	config.LogLevel = "error"
	config.LogOutputs = []string{os.DevNull}
	config.ListenClientUrls = []url.URL{clientURL}
	config.AdvertiseClientUrls = []url.URL{clientURL}
	config.ListenPeerUrls = []url.URL{peerURL}
	config.AdvertisePeerUrls = []url.URL{peerURL}
	config.InitialCluster = config.InitialClusterFromName(config.Name)
	server, err := embed.StartEtcd(config)
	if err != nil {
		t.Fatalf("start M22 embedded etcd: %v", err)
	}
	t.Cleanup(server.Close)
	select {
	case <-server.Server.ReadyNotify():
	case <-time.After(10 * time.Second):
		server.Server.Stop()
		t.Fatal("M22 embedded etcd did not become ready")
	}
	return clientURL.String()
}

func reserveM22RPCPerfURL(t *testing.T) url.URL {
	t.Helper()
	parsed, err := url.Parse("http://" + reserveM22RPCPerfAddress(t))
	if err != nil {
		t.Fatalf("parse M22 embedded etcd URL: %v", err)
	}
	return *parsed
}

func m22RPCPerfCommit() string {
	command := exec.Command("git", "rev-parse", "HEAD")
	data, err := command.Output()
	if err != nil {
		return "unknown"
	}
	return strings.TrimSpace(string(data))
}

func m22RPCPerfCPU() string {
	if runtime.GOOS == "linux" {
		data, err := os.ReadFile("/proc/cpuinfo")
		if err == nil {
			for _, line := range strings.Split(string(data), "\n") {
				key, value, exists := strings.Cut(line, ":")
				if exists && strings.TrimSpace(key) == "model name" {
					return strings.TrimSpace(value)
				}
			}
		}
	}
	if value := strings.TrimSpace(os.Getenv("PROCESSOR_IDENTIFIER")); value != "" {
		return value
	}
	return runtime.GOARCH
}

func m22RPCPerfMemoryBytes() uint64 {
	if runtime.GOOS != "linux" {
		return 0
	}
	data, err := os.ReadFile("/proc/meminfo")
	if err != nil {
		return 0
	}
	for _, line := range strings.Split(string(data), "\n") {
		fields := strings.Fields(line)
		if len(fields) >= 2 && fields[0] == "MemTotal:" {
			value, parseErr := strconv.ParseUint(fields[1], 10, 64)
			if parseErr == nil {
				return value * 1024
			}
		}
	}
	return 0
}

func m22RPCPerfEtcdVersion(endpoints []string) string {
	if len(endpoints) == 0 {
		return "unknown"
	}
	config := clientv3.Config{Endpoints: endpoints, DialTimeout: 3 * time.Second}
	config.Username = os.Getenv("ORIGIN_M22_ETCD_USERNAME")
	config.Password = os.Getenv("ORIGIN_M22_ETCD_PASSWORD")
	client, err := clientv3.New(config)
	if err != nil {
		return "unknown"
	}
	defer client.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	status, err := client.Status(ctx, endpoints[0])
	if err != nil {
		return "unknown"
	}
	return status.Version
}

func m22RPCPerfNATSVersion(urls []string) string {
	if len(urls) == 0 {
		return "unknown"
	}
	options := []nats.Option{nats.Timeout(3 * time.Second), nats.NoReconnect()}
	username := os.Getenv("ORIGIN_M22_NATS_USERNAME")
	password := os.Getenv("ORIGIN_M22_NATS_PASSWORD")
	token := os.Getenv("ORIGIN_M22_NATS_TOKEN")
	if username != "" {
		options = append(options, nats.UserInfo(username, password))
	}
	if token != "" {
		options = append(options, nats.Token(token))
	}
	connection, err := nats.Connect(strings.Join(urls, ","), options...)
	if err != nil {
		return "unknown"
	}
	defer connection.Close()
	return connection.ConnectedServerVersion()
}
