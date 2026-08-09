package application

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/duanhf2012/origin/v3/command"
	"github.com/duanhf2012/origin/v3/errs"
	originlog "github.com/duanhf2012/origin/v3/log"
	"github.com/duanhf2012/origin/v3/service"
)

const adminAddressSecretMarker = "unique-secret-marker"

type adminAddressRedactionService struct{ service.Service }

// adminAddressRedactionHandler 保存日志消息及字符串类字段，验证 report 不会把地址带入异步日志。
type adminAddressRedactionHandler struct {
	mu     sync.Mutex
	output strings.Builder
}

func (*adminAddressRedactionHandler) Enabled(originlog.Level) bool { return true }

func (handler *adminAddressRedactionHandler) Write(
	record originlog.Record,
	fields []originlog.Field,
) error {
	handler.mu.Lock()
	defer handler.mu.Unlock()
	handler.output.WriteString(record.Message)
	for _, field := range fields {
		handler.output.WriteString(field.StringValue())
		handler.output.Write(field.BytesValue())
	}
	return nil
}

func (*adminAddressRedactionHandler) Sync() error  { return nil }
func (*adminAddressRedactionHandler) Close() error { return nil }

func (handler *adminAddressRedactionHandler) text() string {
	handler.mu.Lock()
	defer handler.mu.Unlock()
	return handler.output.String()
}

// TestInitialAdminAddressErrorIsRedactedFromResultAndReportLog 防止启动事务把原始 Admin
// 地址写进返回错误或 application lifecycle failed 的 error 字段。
func TestInitialAdminAddressErrorIsRedactedFromResultAndReportLog(t *testing.T) {
	directory := writeApplicationConfig(t, `
nodes:
  - id: gateway-1
    services: [adminAddressRedactionService]
`)
	handler := &adminAddressRedactionHandler{}
	app := New(Options{
		LogHandlerFactory: func(originlog.Config) (originlog.Handler, error) {
			return handler, nil
		},
	})
	app.Setup(&adminAddressRedactionService{})
	err := app.run(context.Background(), command.StartRequest{
		AppName:      "admin-redaction",
		ConfigDir:    directory,
		AdminAddress: adminAddressSecretMarker,
	})
	if !errors.Is(err, errs.ErrInvalidArgument) {
		t.Fatalf("run() error = %v", err)
	}
	if strings.Contains(err.Error(), adminAddressSecretMarker) {
		t.Fatalf("run() error leaked Admin address marker: %v", err)
	}
	if output := handler.text(); strings.Contains(output, adminAddressSecretMarker) {
		t.Fatalf("report log leaked Admin address marker: %q", output)
	}
}

// TestAdminResolveErrorDoesNotExposeAddress 固定带 Guard 的解析失败仍只返回稳定错误，
// 不依赖无 Guard 的非环回拒绝路径实现脱敏。
func TestAdminResolveErrorDoesNotExposeAddress(t *testing.T) {
	app := New()
	if err := app.SetAdminGuard(adminRegistryAllowGuard{}); err != nil {
		t.Fatal(err)
	}
	if err := app.freezeAdminRoutes(nil); err != nil {
		t.Fatal(err)
	}
	app.mu.Lock()
	app.resourcesReady = true
	app.state.Store(uint32(StateRunning))
	app.mu.Unlock()

	address := adminAddressSecretMarker + "^:6061"
	err := app.StartAdminServer(address)
	if !errors.Is(err, errs.ErrInvalidArgument) {
		t.Fatalf("StartAdminServer(resolve) error = %v", err)
	}
	if strings.Contains(err.Error(), adminAddressSecretMarker) {
		t.Fatalf("StartAdminServer(resolve) leaked address marker: %v", err)
	}
}
