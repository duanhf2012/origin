package application

import (
	"context"
	"net/http"
	"testing"
	"time"
)

// newHTTPTestApplication 只建立 HTTP Runtime 依赖的真实生命周期状态。
func newHTTPTestApplication(t *testing.T) *Application {
	t.Helper()
	app := New()
	app.mu.Lock()
	app.appName = "http-test"
	app.startedAt = time.Now()
	app.resourcesReady = true
	app.state.Store(uint32(StateRunning))
	app.mu.Unlock()
	if err := app.freezeAdminRoutes(nil); err != nil {
		t.Fatalf("freezeAdminRoutes() error = %v", err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		if err := app.StopAdminServer(ctx); err != nil {
			t.Errorf("StopAdminServer() cleanup error = %v", err)
		}
		if err := app.StopPprof(ctx); err != nil {
			t.Errorf("StopPprof() cleanup error = %v", err)
		}
	})
	return app
}

func mustRequest(t *testing.T, method string, target string) *http.Request {
	t.Helper()
	request, err := http.NewRequest(method, target, nil)
	if err != nil {
		t.Fatal(err)
	}
	return request
}
