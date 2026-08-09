package main

import (
	"context"
	"encoding/json"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/duanhf2012/origin/v3/admin"
	"github.com/duanhf2012/origin/v3/errs"
	originlog "github.com/duanhf2012/origin/v3/log"
	"github.com/duanhf2012/origin/v3/node"
)

// TestLogicServiceAdminEndpoints 直接通过真实 Service 调度槽验证三类 Endpoint 的完整行为。
func TestLogicServiceAdminEndpoints(t *testing.T) {
	target := NewLogicService()
	stopNode := startLogicServiceNode(t, target)
	defer stopNode()

	summary := findEndpoint(t, target, http.MethodGet, "summary")
	reload := findEndpoint(t, target, http.MethodPost, "reload-logic")
	refresh := findEndpoint(t, target, http.MethodPost, "refresh-player")

	// 初始查询必须返回冻结在 Service 槽内的版本和计数。
	initial := invokeSummary(t, target, summary)
	if initial.Version != "v1" || initial.Reloads != 0 || initial.Refreshes != 0 {
		t.Fatalf("initial summary = %+v", initial)
	}

	// DecodeJSON 必须拒绝未知字段，失败请求不能提交任何业务状态。
	_, err := admin.InvokeService(
		t.Context(),
		target,
		reload,
		admin.NewRequest("invalid", admin.Principal{}, nil, nil, []byte(`{"version":"v2","extra":true}`)),
	)
	if !errs.IsCode(err, errs.CodeInvalidArgument) {
		t.Fatalf("reload unknown field error = %v", err)
	}
	if current := invokeSummary(t, target, summary); current.Version != "v1" || current.Reloads != 0 {
		t.Fatalf("summary after invalid reload = %+v", current)
	}

	// Await 期间只写局部变量，并释放 Service 执行权；同一槽内的查询应看到提交前旧状态。
	loadEntered := make(chan struct{})
	releaseLoad := make(chan struct{})
	target.loadLogic = func(ctx context.Context, version string) (string, error) {
		close(loadEntered)
		select {
		case <-releaseLoad:
			return version, nil
		case <-ctx.Done():
			return "", ctx.Err()
		}
	}
	reloadDone := make(chan error, 1)
	go func() {
		response, invokeErr := admin.InvokeService(
			t.Context(),
			target,
			reload,
			admin.NewRequest("reload", admin.Principal{}, nil, nil, []byte(`{"version":"v2"}`)),
		)
		if invokeErr == nil && response.Status() != http.StatusNoContent {
			t.Errorf("reload status = %d", response.Status())
		}
		reloadDone <- invokeErr
	}()
	<-loadEntered
	if during := invokeSummary(t, target, summary); during.Version != "v1" || during.Reloads != 0 {
		t.Fatalf("summary during Await = %+v", during)
	}
	close(releaseLoad)
	if err := <-reloadDone; err != nil {
		t.Fatalf("reload error = %v", err)
	}
	if current := invokeSummary(t, target, summary); current.Version != "v2" || current.Reloads != 1 {
		t.Fatalf("summary after reload = %+v", current)
	}

	// 202 只表示通知已被有界队列接受；真正刷新在当前 Handler 返回后串行执行。
	refreshed := make(chan string, 1)
	target.onPlayerRefreshed = func(playerID string) { refreshed <- playerID }
	response, err := admin.InvokeService(
		t.Context(),
		target,
		refresh,
		admin.NewRequest("refresh", admin.Principal{}, nil, nil, []byte(`{"player_id":"player-7"}`)),
	)
	if err != nil || response.Status() != http.StatusAccepted {
		t.Fatalf("refresh response = %d, %v", response.Status(), err)
	}
	select {
	case playerID := <-refreshed:
		if playerID != "player-7" {
			t.Fatalf("refreshed player = %q", playerID)
		}
	case <-t.Context().Done():
		t.Fatal("refresh notification was not executed")
	}

	// 所有并发请求使用同一目标版本，串行提交计数必须精确且最终版本不依赖调度顺序。
	target.loadLogic = func(_ context.Context, version string) (string, error) { return version, nil }
	const calls = 32
	start := make(chan struct{})
	errorsChannel := make(chan error, calls)
	var requests sync.WaitGroup
	requests.Add(calls)
	for index := 0; index < calls; index++ {
		go func() {
			defer requests.Done()
			<-start
			_, invokeErr := admin.InvokeService(
				t.Context(),
				target,
				reload,
				admin.NewRequest("concurrent", admin.Principal{}, nil, nil, []byte(`{"version":"v3"}`)),
			)
			errorsChannel <- invokeErr
		}()
	}
	close(start)
	requests.Wait()
	for index := 0; index < calls; index++ {
		if err := <-errorsChannel; err != nil {
			t.Fatalf("concurrent reload error = %v", err)
		}
	}
	final := invokeSummary(t, target, summary)
	if final.Version != "v3" || final.Reloads != calls+1 || final.Refreshes != 1 {
		t.Fatalf("final summary = %+v", final)
	}
}

// startLogicServiceNode 为示例测试创建真实 Scheduler，并返回确定性的同步清理函数。
func startLogicServiceNode(t *testing.T, target *LogicService) func() {
	t.Helper()
	current, err := node.New(
		node.Config{ID: "game-1", Services: []string{"LogicService"}},
		[]node.ServiceBinding{{Name: "LogicService", Template: "LogicService", Service: target}},
		originlog.NewNop(),
		node.Options{MaxTimersPerNode: 128, TimerLocation: time.UTC},
	)
	if err != nil {
		t.Fatalf("node.New() error = %v", err)
	}
	if err := current.Start(t.Context()); err != nil {
		t.Fatalf("Node.Start() error = %v", err)
	}
	return func() {
		if err := current.Stop(context.Background()); err != nil {
			t.Errorf("Node.Stop() error = %v", err)
		}
	}
}

// findEndpoint 按方法和名称定位 Provider 返回的冻结描述符。
func findEndpoint(t *testing.T, target *LogicService, method, name string) admin.Endpoint {
	t.Helper()
	for _, endpoint := range target.AdminEndpoints() {
		if endpoint.Method() == method && endpoint.Name() == name {
			return endpoint
		}
	}
	t.Fatalf("endpoint %s %s not found", method, name)
	return admin.Endpoint{}
}

// invokeSummary 解码 summary 的稳定 JSON 外观，避免测试读取业务对象内部字段。
func invokeSummary(t *testing.T, target *LogicService, endpoint admin.Endpoint) logicSummary {
	t.Helper()
	response, err := admin.InvokeService(
		t.Context(),
		target,
		endpoint,
		admin.NewRequest("summary", admin.Principal{}, nil, nil, nil),
	)
	if err != nil || response.Status() != http.StatusOK {
		t.Fatalf("summary response = %d, %v", response.Status(), err)
	}
	var result logicSummary
	if err := json.Unmarshal(response.Body(), &result); err != nil {
		t.Fatalf("decode summary error = %v", err)
	}
	return result
}
