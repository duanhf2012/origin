package application

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"runtime"
	"strings"
	"sync"
	"testing"

	"github.com/duanhf2012/origin/v3/admin"
	"github.com/duanhf2012/origin/v3/node"
	"github.com/duanhf2012/origin/v3/service"
)

// adminHTTPService 把非并发安全计数器同时暴露给普通 Dispatch 和 Admin Endpoint；测试在
// Race 下依靠真实 ServiceScheduler 证明两类任务只在同一个串行执行槽访问这些字段。
type adminHTTPService struct {
	service.Service
	value              int
	ordinaryDispatches int
}

// AdminEndpoints 返回每个实例自己的冻结 GET/POST 描述符。
func (target *adminHTTPService) AdminEndpoints() []admin.Endpoint {
	return []admin.Endpoint{
		admin.Get("state", func(_ context.Context, request admin.Request) (admin.Response, error) {
			return admin.JSON(http.StatusOK, struct {
				Value              int    `json:"value"`
				OrdinaryDispatches int    `json:"ordinary_dispatches"`
				View               string `json:"view"`
			}{
				Value:              target.value,
				OrdinaryDispatches: target.ordinaryDispatches,
				View:               request.Query().Get("view"),
			})
		}),
		admin.Post("increment", func(ctx context.Context, _ admin.Request) (admin.Response, error) {
			// Await 能成功返回证明 Handler 当前持有真实 Service Task 身份；恢复后仍由同一
			// Scheduler 串行提交非原子读改写，错误桥接会在 Race 或最终值上暴露。
			if err := target.Await(ctx, func(context.Context) error { return nil }); err != nil {
				return admin.Response{}, err
			}
			current := target.value
			runtime.Gosched()
			target.value = current + 1
			return admin.Empty(http.StatusNoContent), nil
		}),
	}
}

// TestAdminServiceEndpointQueryAndMutation 防止 Service Endpoint 绕过 InvokeService、只按
// 名称不按真实 Node/Service 实例路由，或与普通 Dispatch 并发访问业务状态。
func TestAdminServiceEndpointQueryAndMutation(t *testing.T) {
	app := New()
	target := &adminHTTPService{}
	current := newAdminRegistryNode(t, app, "game-1", "counter", target)
	if err := app.freezeAdminRoutes([]*node.Node{current}); err != nil {
		t.Fatalf("freezeAdminRoutes() error = %v", err)
	}
	if err := current.Start(t.Context()); err != nil {
		t.Fatalf("Node.Start() error = %v", err)
	}
	app.mu.Lock()
	app.nodes = []*node.Node{current}
	app.mu.Unlock()
	baseURL := startAdminRouteTestServer(t, app)

	// 128 个调用方同时发起 POST；Transport 把真实服务端活动请求限制在 32，保留既有
	// 64 请求外层配额的语义，避免把本测试误写成配额拒绝测试。
	transport := &http.Transport{
		DialContext:         (&net.Dialer{}).DialContext,
		MaxConnsPerHost:     32,
		MaxIdleConnsPerHost: 32,
	}
	t.Cleanup(transport.CloseIdleConnections)
	client := &http.Client{Transport: transport}
	const calls = 128
	start := make(chan struct{})
	errorsChannel := make(chan error, calls)
	var requests sync.WaitGroup
	requests.Add(calls)
	for range calls {
		go func() {
			defer requests.Done()
			<-start
			request, err := http.NewRequest(
				http.MethodPost,
				baseURL+"/admin/v1/nodes/game-1/services/counter/endpoints/increment",
				strings.NewReader(`{}`),
			)
			if err != nil {
				errorsChannel <- err
				return
			}
			request.Header.Set("Content-Type", "application/json")
			response, err := client.Do(request)
			if err != nil {
				errorsChannel <- err
				return
			}
			bodyBytes, readErr := io.ReadAll(response.Body)
			closeErr := response.Body.Close()
			if readErr != nil {
				errorsChannel <- readErr
				return
			}
			if closeErr != nil {
				errorsChannel <- closeErr
				return
			}
			body := string(bodyBytes)
			if response.StatusCode != http.StatusNoContent || body != "" {
				errorsChannel <- fmt.Errorf("POST increment status=%d Body=%q", response.StatusCode, body)
				return
			}
			errorsChannel <- nil
		}()
	}

	// 普通业务 Dispatch 与 HTTP 请求从同一屏障出发并修改另一个非原子字段；两类任务
	// 只有确实共用 Scheduler 才能在 Race 下保持安全。
	ordinaryResults := make(chan error, calls)
	for range calls {
		go func() {
			<-start
			err := target.DispatchAsync(func(context.Context) {
				currentValue := target.ordinaryDispatches
				runtime.Gosched()
				target.ordinaryDispatches = currentValue + 1
				ordinaryResults <- nil
			})
			if err != nil {
				ordinaryResults <- err
			}
		}()
	}
	close(start)
	requests.Wait()
	for range calls {
		if err := <-errorsChannel; err != nil {
			t.Fatal(err)
		}
	}
	for range calls {
		if err := <-ordinaryResults; err != nil {
			t.Fatal(err)
		}
	}

	// 最终 GET 也必须进入同一 Service 槽，因此它观察到此前全部 FIFO 提交的精确终态。
	response, err := client.Get(
		baseURL + "/admin/v1/nodes/game-1/services/counter/endpoints/state?view=final",
	)
	if err != nil {
		t.Fatalf("GET state error = %v", err)
	}
	defer response.Body.Close()
	var state struct {
		Value              int    `json:"value"`
		OrdinaryDispatches int    `json:"ordinary_dispatches"`
		View               string `json:"view"`
	}
	if err := json.NewDecoder(response.Body).Decode(&state); err != nil {
		t.Fatalf("decode GET state error = %v", err)
	}
	if response.StatusCode != http.StatusOK || state.Value != calls ||
		state.OrdinaryDispatches != calls || state.View != "final" {
		t.Fatalf("GET state status=%d state=%+v, want value/dispatches=%d", response.StatusCode, state, calls)
	}

	// Service 的 NodeID、ServiceName、EndpointName 与 Method 必须同时精确命中冻结键。
	for _, probe := range []struct {
		method string
		path   string
	}{
		{method: http.MethodGet, path: "/admin/v1/nodes/missing/services/counter/endpoints/state"},
		{method: http.MethodGet, path: "/admin/v1/nodes/game-1/services/missing/endpoints/state"},
		{method: http.MethodGet, path: "/admin/v1/nodes/game-1/services/counter/endpoints/missing"},
		{method: http.MethodGet, path: "/admin/v1/nodes/game-1/services/counter/endpoints/increment"},
		{method: http.MethodPost, path: "/admin/v1/nodes/game-1/services/counter/endpoints/state"},
	} {
		request, requestErr := http.NewRequest(
			probe.method,
			baseURL+probe.path,
			strings.NewReader(`{}`),
		)
		if requestErr != nil {
			t.Fatal(requestErr)
		}
		if probe.method == http.MethodPost {
			request.Header.Set("Content-Type", "application/json")
		}
		probeResponse, requestErr := client.Do(request)
		if requestErr != nil {
			t.Fatalf("%s %s error = %v", probe.method, probe.path, requestErr)
		}
		bodyBytes, readErr := io.ReadAll(probeResponse.Body)
		_ = probeResponse.Body.Close()
		if readErr != nil {
			t.Fatal(readErr)
		}
		if probeResponse.StatusCode != http.StatusNotFound ||
			string(bodyBytes) != http.StatusText(http.StatusNotFound)+"\n" {
			t.Fatalf("%s %s status=%d Body=%q", probe.method, probe.path, probeResponse.StatusCode, bodyBytes)
		}
	}
}
