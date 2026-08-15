package rpc

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"testing"
	"time"

	publicdiscovery "github.com/duanhf2012/origin/v3/discovery"
	"github.com/duanhf2012/origin/v3/errs"
	"github.com/duanhf2012/origin/v3/internal/bufferpool"
	"github.com/duanhf2012/origin/v3/internal/natsnet"
	originlog "github.com/duanhf2012/origin/v3/log"
	"github.com/duanhf2012/origin/v3/service"
	"github.com/nats-io/nats-server/v2/server"
)

var benchmarkClientSink Client

func BenchmarkTargetConstruction(b *testing.B) {
	b.ReportAllocs()
	for b.Loop() {
		target := ToServiceOnNode("game-1", "PlayerService")
		if !target.valid() {
			b.Fatal("Target unexpectedly invalid")
		}
	}
}

func BenchmarkClientOnNode(b *testing.B) {
	client := Client{target: ToService("PlayerService")}
	b.ReportAllocs()
	for b.Loop() {
		benchmarkClientSink = client.OnNode("player-1")
	}
}

// BenchmarkClientIncludeRetired 锁定轻量值派生不产生堆分配。
func BenchmarkClientIncludeRetired(b *testing.B) {
	client := Client{target: ToService("PlayerService")}
	b.ReportAllocs()
	for b.Loop() {
		benchmarkClientSink = client.IncludeRetired()
	}
}

// BenchmarkClientWhereLabels 记录调用方 Map 冻结为有界条件 Slice 的派生成本。
func BenchmarkClientWhereLabels(b *testing.B) {
	client := Client{target: ToService("PlayerService")}
	labels := map[string]string{
		"scope":        "area",
		"real_area_id": "1",
	}
	b.ReportAllocs()
	for b.Loop() {
		benchmarkClientSink = client.WhereLabels(labels)
	}
}

func BenchmarkRouteRoundRobin(b *testing.B) {
	client := Client{target: ToService("PlayerService")}
	b.ReportAllocs()
	for b.Loop() {
		benchmarkClientSink = client.RouteRoundRobin()
	}
}

func BenchmarkRouteRandom(b *testing.B) {
	client := Client{target: ToService("PlayerService")}
	b.ReportAllocs()
	for b.Loop() {
		benchmarkClientSink = client.RouteRandom()
	}
}

func BenchmarkRouteKeyInt(b *testing.B) {
	client := Client{target: ToService("PlayerService")}
	b.ReportAllocs()
	for b.Loop() {
		benchmarkClientSink = client.Route(uint64(42))
	}
}

func BenchmarkRouteKeyString(b *testing.B) {
	client := Client{target: ToService("PlayerService")}
	b.ReportAllocs()
	for b.Loop() {
		benchmarkClientSink = client.Route("player")
	}
}

func BenchmarkPrepareStrategies(b *testing.B) {
	runtime := newPrepareTestRuntime(b, "gateway-1", "", nil)
	addPrepareTestLocal(
		b,
		runtime,
		"PlayerService",
		service.StateRunning,
		&runtimeTestDispatcher{},
	)
	if err := runtime.Freeze(); err != nil {
		b.Fatalf("Freeze() error = %v", err)
	}
	base := prepareTestClient(runtime, ToService("PlayerService"))
	cases := []struct {
		name   string
		client Client
	}{
		{name: "round-robin", client: base.RouteRoundRobin()},
		{name: "random", client: base.RouteRandom()},
		{name: "integer-key", client: base.Route(uint64(42))},
		{name: "string-key", client: base.Route("player")},
		{
			name: "custom",
			client: base.RouteBy(
				fixedPrepareTestSelector{index: 0, ok: true},
			),
		},
	}
	ctx := context.Background()
	for _, current := range cases {
		b.Run(current.name, func(b *testing.B) {
			if _, err := current.client.PrepareNotify(ctx, 1); err != nil {
				b.Fatalf("warm PrepareNotify() error = %v", err)
			}
			b.ReportAllocs()
			b.ResetTimer()
			for b.Loop() {
				prepared, err := current.client.PrepareNotify(ctx, 1)
				if err != nil {
					b.Fatal(err)
				}
				benchmarkClientSink = prepared
			}
		})
	}
}

type prepareBenchmarkSnapshot struct {
	candidates []RemoteCandidate
}

func (snapshot *prepareBenchmarkSnapshot) Len(serviceName string) int {
	if serviceName != "PlayerService" {
		return 0
	}
	return len(snapshot.candidates)
}

func (snapshot *prepareBenchmarkSnapshot) Candidate(
	serviceName string,
	index int,
) (RemoteCandidate, bool) {
	if serviceName != "PlayerService" ||
		index < 0 ||
		index >= len(snapshot.candidates) {
		return RemoteCandidate{}, false
	}
	return snapshot.candidates[index], true
}

func (snapshot *prepareBenchmarkSnapshot) Find(
	nodeID string,
	serviceName string,
) (RemoteCandidate, bool) {
	if serviceName != "PlayerService" {
		return RemoteCandidate{}, false
	}
	for _, candidate := range snapshot.candidates {
		if candidate.NodeID == nodeID {
			return candidate, true
		}
	}
	return RemoteCandidate{}, false
}

type prepareBenchmarkResolver struct {
	snapshot *prepareBenchmarkSnapshot
}

func (resolver *prepareBenchmarkResolver) Snapshot() RemoteSnapshot {
	return resolver.snapshot
}

func (resolver *prepareBenchmarkResolver) ResolveRemote(
	nodeID string,
	serviceName string,
	contractID ContractID,
	fingerprint ContractFingerprint,
) (RemoteRoute, error) {
	candidate, exists := resolver.snapshot.Find(nodeID, serviceName)
	if !exists {
		return RemoteRoute{}, errs.ErrRPCNoRoute
	}
	return RemoteRoute{
		NodeID:    candidate.NodeID,
		SessionID: candidate.SessionID,
		Transport: candidate.Transport,
		Address:   candidate.Address,
	}, nil
}

func newRemotePrepareBenchmarkClient(
	b *testing.B,
	count int,
	labelCount int,
) Client {
	b.Helper()
	runtime, err := NewRuntime(
		"gateway-1",
		bufferpool.NewPool(bufferpool.Options{}),
		originlog.NewNop(),
	)
	if err != nil {
		b.Fatalf("NewRuntime() error = %v", err)
	}
	config := DefaultConfig()
	config.Transport = TransportTCP
	config.TCP.Listen = "127.0.0.1:21001"
	config.TCP.Advertise = "127.0.0.1:21001"
	if err := runtime.Configure(&config); err != nil {
		b.Fatalf("Configure() error = %v", err)
	}

	snapshot := &prepareBenchmarkSnapshot{
		candidates: make([]RemoteCandidate, count),
	}
	labels := make(map[string]string, labelCount)
	for index := 0; index < labelCount; index++ {
		labels[fmt.Sprintf("label_%02d", index)] = fmt.Sprintf("value_%02d", index)
	}
	for index := 0; index < count; index++ {
		nodeID := "player-" + strconv.Itoa(index)
		sessionID := uint64(index + 1)
		snapshot.candidates[index] = RemoteCandidate{
			NodeID:      nodeID,
			SessionID:   sessionID,
			ServiceName: "PlayerService",
			State:       publicdiscovery.StateRunning,
			Labels:      labels,
			Transport:   TransportTCP,
			Address:     "127.0.0.1:24001",
			ContractID:  1,
			Fingerprint: runtimeTestFingerprint,
		}
		target := newRemoteTarget(
			runtime.remote,
			nodeID,
			sessionID,
			"127.0.0.1:24001",
		)
		target.current.Store(&outboundSession{})
		runtime.remote.targets[nodeID] = target
	}
	runtime.remote.publishTargetsLocked()
	if err := runtime.BindRemoteResolver(
		&prepareBenchmarkResolver{snapshot: snapshot},
	); err != nil {
		b.Fatalf("BindRemoteResolver() error = %v", err)
	}
	if err := runtime.Freeze(); err != nil {
		b.Fatalf("Freeze() error = %v", err)
	}
	client := prepareTestClient(
		runtime,
		ToService("PlayerService"),
	).Route(uint64(count / 2))
	if labelCount != 0 {
		client = client.WhereLabels(labels)
	}
	return client
}

func newBroadcastPrepareBenchmarkClient(
	b *testing.B,
	count int,
) Client {
	b.Helper()
	candidates := make([]RemoteCandidate, count)
	runtime := newPrepareTestRuntime(b, "gateway-1", TransportTCP, nil)
	for index := range candidates {
		nodeID := fmt.Sprintf("player-%05d", index)
		sessionID := uint64(index + 1)
		address := fmt.Sprintf("127.0.0.1:%d", 30000+index)
		candidates[index] = RemoteCandidate{
			NodeID:      nodeID,
			SessionID:   sessionID,
			ServiceName: "PlayerService",
			State:       publicdiscovery.StateRunning,
			Transport:   TransportTCP,
			Address:     address,
			ContractID:  1,
			Fingerprint: runtimeTestFingerprint,
		}
		target := newRemoteTarget(runtime.remote, nodeID, sessionID, address)
		target.current.Store(newOutboundSession(runtime.remote, nodeID, sessionID))
		runtime.remote.targets[nodeID] = target
	}
	runtime.remote.publishTargetsLocked()
	if err := runtime.BindRemoteResolver(&broadcastTestResolver{
		snapshot: &broadcastTestSnapshot{candidates: candidates},
	}); err != nil {
		b.Fatalf("BindRemoteResolver() error = %v", err)
	}
	if err := runtime.Freeze(); err != nil {
		b.Fatalf("Freeze() error = %v", err)
	}
	return prepareTestClient(runtime, ToService("PlayerService"))
}

// BenchmarkPrepareBroadcast 记录 1、100、1000 和 8192 个固定目标的 O(N) Prepare 成本。
func BenchmarkPrepareBroadcast(b *testing.B) {
	for _, count := range []int{1, 100, 1000, maxRemoteTargets} {
		b.Run(strconv.Itoa(count), func(b *testing.B) {
			client := newBroadcastPrepareBenchmarkClient(b, count)
			ctx := context.Background()
			b.ReportAllocs()
			b.ResetTimer()
			for b.Loop() {
				prepared, err := client.PrepareBroadcast(ctx, 1)
				if err != nil {
					b.Fatal(err)
				}
				benchmarkClientSink = prepared
			}
		})
	}
}

func startBroadcastBenchmarkNATS(b *testing.B) *server.Server {
	b.Helper()
	running, err := server.NewServer(&server.Options{
		Host:   "127.0.0.1",
		Port:   -1,
		NoLog:  true,
		NoSigs: true,
	})
	if err != nil {
		b.Fatalf("server.NewServer() error = %v", err)
	}
	running.Start()
	if !running.ReadyForConnections(5 * time.Second) {
		running.Shutdown()
		b.Fatal("NATS benchmark server 未就绪")
	}
	b.Cleanup(func() {
		running.Shutdown()
		running.WaitForShutdown()
	})
	return running
}

func newNATSBroadcastBenchmarkClient(
	b *testing.B,
	running *server.Server,
	count int,
) Client {
	b.Helper()
	pool := bufferpool.NewPool(bufferpool.Options{})
	runtime, err := NewRuntime("gateway-1", pool, originlog.NewNop())
	if err != nil {
		b.Fatal(err)
	}
	config := Config{
		Transport:        TransportNATS,
		MaxPayloadSize:   DefaultMaxPayloadSize,
		MaxBroadcastSize: DefaultMaxBroadcastSize,
		NATS:             DefaultNATSConfig(),
	}
	config.NATS.Namespace = "m20-bench"
	config.NATS.URLs = []string{running.ClientURL()}
	if err := runtime.Configure(&config); err != nil {
		b.Fatal(err)
	}
	options := natsnet.DefaultOptions("m20.broadcast.benchmark", running.ClientURL())
	conn, err := natsnet.Connect(context.Background(), options, nil)
	if err != nil {
		b.Fatalf("natsnet.Connect() error = %v", err)
	}
	b.Cleanup(conn.Close)
	view := &natsConnectionView{conn: conn, generation: 1}
	runtime.nats.activeConnection.Store(view)

	candidates := make([]RemoteCandidate, count)
	for index := range candidates {
		candidates[index] = RemoteCandidate{
			NodeID:      fmt.Sprintf("player-%05d", index),
			SessionID:   uint64(index + 1),
			ServiceName: "PlayerService",
			State:       publicdiscovery.StateRunning,
			Transport:   TransportNATS,
			ContractID:  1,
			Fingerprint: runtimeTestFingerprint,
		}
	}
	if err := runtime.BindRemoteResolver(&broadcastTestResolver{
		snapshot: &broadcastTestSnapshot{candidates: candidates},
	}); err != nil {
		b.Fatal(err)
	}
	if err := runtime.Freeze(); err != nil {
		b.Fatal(err)
	}
	return prepareTestClient(runtime, ToService("PlayerService"))
}

// BenchmarkBroadcastFanoutNATS 通过真实 Broker 记录 1/100/1000/8192 目标完整扇出成本。
func BenchmarkBroadcastFanoutNATS(b *testing.B) {
	running := startBroadcastBenchmarkNATS(b)
	for _, count := range []int{1, 100, 1000, maxRemoteTargets} {
		b.Run(strconv.Itoa(count), func(b *testing.B) {
			client := newNATSBroadcastBenchmarkClient(b, running, count)
			ctx := context.Background()
			send := func() error {
				prepared, err := client.PrepareBroadcast(ctx, 1)
				if err != nil {
					return err
				}
				request, err := prepared.AllocateRequest(32, CallNotify)
				if err != nil {
					return err
				}
				for index := range request.Bytes() {
					request.Bytes()[index] = byte(index)
				}
				return prepared.Broadcast(ctx, 1, request)
			}
			if err := send(); err != nil {
				b.Fatalf("warm Broadcast() error = %v", err)
			}
			b.ReportAllocs()
			b.SetBytes(int64(32 * count))
			b.ResetTimer()
			for b.Loop() {
				if err := send(); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

func BenchmarkPrepareCandidateScale(b *testing.B) {
	for _, count := range []int{100, 1000, 8192} {
		b.Run(strconv.Itoa(count), func(b *testing.B) {
			client := newRemotePrepareBenchmarkClient(b, count, 0)
			ctx := context.Background()
			if _, err := client.PrepareNotify(ctx, 1); err != nil {
				b.Fatalf("warm PrepareNotify() error = %v", err)
			}
			b.ReportAllocs()
			b.ResetTimer()
			for b.Loop() {
				prepared, err := client.PrepareNotify(ctx, 1)
				if err != nil {
					b.Fatal(err)
				}
				benchmarkClientSink = prepared
			}
		})
	}
}

// BenchmarkPrepareWhereLabels 比较无条件、常见双条件、最大 32 条件和无匹配失败扫描。
func BenchmarkPrepareWhereLabels(b *testing.B) {
	const candidateCount = 1000
	ctx := context.Background()
	for _, labelCount := range []int{0, 2, 32} {
		b.Run(strconv.Itoa(labelCount), func(b *testing.B) {
			client := newRemotePrepareBenchmarkClient(b, candidateCount, labelCount)
			if _, err := client.PrepareNotify(ctx, 1); err != nil {
				b.Fatalf("warm PrepareNotify() error = %v", err)
			}
			b.ReportAllocs()
			b.ResetTimer()
			for b.Loop() {
				prepared, err := client.PrepareNotify(ctx, 1)
				if err != nil {
					b.Fatal(err)
				}
				benchmarkClientSink = prepared
			}
		})
	}

	b.Run("no-match", func(b *testing.B) {
		client := newRemotePrepareBenchmarkClient(b, candidateCount, 2).
			WhereLabels(map[string]string{"missing": "value"})
		if _, err := client.PrepareNotify(ctx, 1); !errors.Is(err, errs.ErrRPCNoRoute) {
			b.Fatalf("warm PrepareNotify() error = %v", err)
		}
		b.ReportAllocs()
		b.ResetTimer()
		for b.Loop() {
			_, err := client.PrepareNotify(ctx, 1)
			if !errors.Is(err, errs.ErrRPCNoRoute) {
				b.Fatal(err)
			}
		}
	})
}

func BenchmarkPrimitiveCodec(b *testing.B) {
	pool := bufferpool.NewPool(bufferpool.Options{})
	b.ReportAllocs()
	b.SetBytes(24)
	for b.Loop() {
		buffer := pool.Acquire(24)
		writer := NewWriter(buffer.Bytes())
		_ = writer.WriteInt64(1001)
		_ = writer.WriteFloat64(3.14)
		_ = writer.WriteString("player")
		_ = writer.Done()

		reader := NewRequestReader(buffer.Bytes())
		_, _ = reader.ReadInt64()
		_, _ = reader.ReadFloat64()
		_, _ = reader.ReadString()
		_ = reader.Done()
		buffer.Release()
	}
}

// BenchmarkBytePayloadCodec 保存小消息、普通消息和接近 32M 上限消息的编解码基线。
//
// ReadBytes 按已确认所有权规则复制业务结果，因此 B/op 会真实包含业务独立 Slice；该
// Benchmark 不是零复制 Transport 测试，不能用来推断 M13 网络帧的复制次数。
func BenchmarkBytePayloadCodec(b *testing.B) {
	cases := []int{
		16,
		1024,
		DefaultMaxPayloadSize - 4,
	}
	for _, payloadSize := range cases {
		b.Run(fmt.Sprintf("%dB", payloadSize), func(b *testing.B) {
			// 样本和 Pool 在计时前创建，循环只测量准确大小计算、最终写入、读取复制和归还。
			payload := make([]byte, payloadSize)
			pool := bufferpool.NewPool(bufferpool.Options{})
			b.ReportAllocs()
			b.SetBytes(int64(payloadSize))
			b.ResetTimer()
			for b.Loop() {
				sizer := NewSizer()
				if err := sizer.AddBytes(payload); err != nil {
					b.Fatal(err)
				}
				size, err := sizer.Size()
				if err != nil {
					b.Fatal(err)
				}
				buffer := pool.Acquire(size)
				writer := NewWriter(buffer.Bytes())
				if err := writer.WriteBytes(payload); err != nil {
					buffer.Release()
					b.Fatal(err)
				}
				if err := writer.Done(); err != nil {
					buffer.Release()
					b.Fatal(err)
				}

				reader := NewResponseReader(buffer.Bytes())
				decoded, err := reader.ReadBytes()
				if err != nil {
					buffer.Release()
					b.Fatal(err)
				}
				if err := reader.Done(); err != nil {
					buffer.Release()
					b.Fatal(err)
				}
				if len(decoded) != payloadSize {
					buffer.Release()
					b.Fatalf("decoded size = %d", len(decoded))
				}
				buffer.Release()
			}
		})
	}
}

// benchmarkBlobCodec 模拟遵守 M12 所有权规则的变长自定义 Codec。
//
// MarshalTo 直接写最终 Buffer；Unmarshal 必须复制为业务独立 Slice，因此 Benchmark
// 会真实记录大 payload 的必要业务分配。
type benchmarkBlob []byte

type benchmarkBlobCodec struct{}

func (benchmarkBlobCodec) Size(value *benchmarkBlob) (int, error) {
	return len(*value), nil
}

func (benchmarkBlobCodec) MarshalTo(
	dst []byte,
	value *benchmarkBlob,
) (int, error) {
	return copy(dst, *value), nil
}

func (benchmarkBlobCodec) Unmarshal(
	src []byte,
	value *benchmarkBlob,
) error {
	*value = append((*value)[:0], src...)
	return nil
}

// 编译期断言锁定 Benchmark 与公开 StaticCodec 形状一致。
var _ StaticCodec[benchmarkBlob] = benchmarkBlobCodec{}

// BenchmarkCustomPayloadCodec 保存 16B、1KB 和接近 32M 自定义 payload 的完整边界基线。
func BenchmarkCustomPayloadCodec(b *testing.B) {
	for _, payloadSize := range []int{
		16,
		1024,
		DefaultMaxPayloadSize - 4,
	} {
		b.Run(fmt.Sprintf("%dB", payloadSize), func(b *testing.B) {
			source := make(benchmarkBlob, payloadSize)
			pool := bufferpool.NewPool(bufferpool.Options{})
			codec := benchmarkBlobCodec{}
			b.ReportAllocs()
			b.SetBytes(int64(payloadSize))
			b.ResetTimer()
			for b.Loop() {
				// Size 和 MarshalTo 都是具体静态调用，payload 只写入一个最终 Buffer。
				customSize, err := codec.Size(&source)
				if err != nil {
					b.Fatal(err)
				}
				sizer := NewSizer()
				if err := sizer.AddCustom(customSize); err != nil {
					b.Fatal(err)
				}
				total, err := sizer.Size()
				if err != nil {
					b.Fatal(err)
				}
				buffer := pool.Acquire(total)
				writer := NewWriter(buffer.Bytes())
				target, err := writer.ReserveCustom(customSize)
				if err != nil {
					buffer.Release()
					b.Fatal(err)
				}
				written, err := codec.MarshalTo(target, &source)
				if err != nil || written != len(target) {
					buffer.Release()
					b.Fatalf("MarshalTo() = %d, %v", written, err)
				}
				if err := writer.Done(); err != nil {
					buffer.Release()
					b.Fatal(err)
				}

				// Unmarshal 建立业务独立所有权，不把 Buffer 借用扩散到循环之外。
				reader := NewResponseReader(buffer.Bytes())
				payload, err := reader.ReadCustomPayload()
				if err != nil {
					buffer.Release()
					b.Fatal(err)
				}
				var decoded benchmarkBlob
				if err := codec.Unmarshal(payload, &decoded); err != nil {
					buffer.Release()
					b.Fatal(err)
				}
				if err := reader.Done(); err != nil {
					buffer.Release()
					b.Fatal(err)
				}
				if len(decoded) != payloadSize {
					buffer.Release()
					b.Fatalf("decoded size = %d", len(decoded))
				}
				buffer.Release()
			}
		})
	}
}

func BenchmarkAwaitLocalCallBaselineAllocation(b *testing.B) {
	// Await localCall 的完成 Channel 会关闭，不能安全复用。该基线用于判断仅池化
	// 外层对象能否抵消代次、晚到响应和 ABA 状态机的维护成本。
	b.ReportAllocs()
	for b.Loop() {
		call := newAwaitCall()
		call.complete(nil, nil)
		_, _ = call.take()
	}
}

func BenchmarkAsyncLocalCallBaselineAllocation(b *testing.B) {
	// Async 还需要提交和中止门闩；三条 Channel 都是一次性终态。
	b.ReportAllocs()
	for b.Loop() {
		call := newAsyncCall()
		call.commit()
		call.complete(nil, nil)
		_, _ = call.take()
	}
}

func BenchmarkPendingMapValueLifecycle(b *testing.B) {
	// pendingCall 以值存入 Map，不为每次调用单独 new 对象；该基线用于决定是否需要对象池。
	session := &outboundSession{
		pending: make(map[uint64]pendingCall),
	}
	complete := func(*Buffer, error) {}
	var requestID uint64
	b.ReportAllocs()
	for b.Loop() {
		requestID++
		session.mu.Lock()
		session.pending[requestID] = pendingCall{complete: complete}
		call := session.pending[requestID]
		delete(session.pending, requestID)
		session.mu.Unlock()
		call.complete(nil, nil)
	}
}

func BenchmarkNATSPendingMapValueLifecycle(b *testing.B) {
	// NATS pending 同样以值保存；固定上限只参与准入判断，不预分配 65536 个槽位。
	table := newNATSPendingTable(DefaultPendingPerNode)
	complete := func(*Buffer, error) {}
	var requestID uint64
	b.ReportAllocs()
	for b.Loop() {
		requestID++
		if err := table.reserve(requestID, 1, complete); err != nil {
			b.Fatal(err)
		}
		call, exists := table.take(requestID, 1, 2, 2)
		if !exists {
			b.Fatal("pending missing")
		}
		call.complete(nil, nil)
	}
}
