package natsnet

import (
	"context"
	"errors"
	"testing"

	"github.com/duanhf2012/origin/v3/errs"
	"github.com/nats-io/nats.go"
)

// TestMapError 验证官方客户端错误到稳定 Origin Code 的全部主要分类。
func TestMapError(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		input error
		code  errs.Code
	}{
		{name: "nil", input: nil, code: errs.CodeOK},
		{name: "canceled", input: context.Canceled, code: errs.CodeCanceled},
		{name: "deadline", input: context.DeadlineExceeded, code: errs.CodeDeadlineExceeded},
		{name: "nats timeout", input: nats.ErrTimeout, code: errs.CodeDeadlineExceeded},
		{name: "closed", input: nats.ErrConnectionClosed, code: errs.CodeTransportClosed},
		{name: "bad subscription", input: nats.ErrBadSubscription, code: errs.CodeTransportClosed},
		{name: "reconnect buffer", input: nats.ErrReconnectBufExceeded, code: errs.CodeTransportOverloaded},
		{name: "slow consumer", input: nats.ErrSlowConsumer, code: errs.CodeTransportOverloaded},
		{name: "max payload", input: nats.ErrMaxPayload, code: errs.CodeTransportMessageTooLarge},
		{name: "bad subject", input: nats.ErrBadSubject, code: errs.CodeInvalidArgument},
		{name: "protocol", input: nats.ErrNoInfoReceived, code: errs.CodeTransportProtocol},
		{name: "no servers", input: nats.ErrNoServers, code: errs.CodeTransportUnavailable},
		{name: "auth", input: nats.ErrAuthorization, code: errs.CodeTransportUnavailable},
	}

	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			mapped := mapError(test.input)
			if got := errs.CodeOf(mapped); got != test.code {
				t.Fatalf("CodeOf(mapError(%v)) = %d, want %d", test.input, got, test.code)
			}
			if test.input != nil && !errors.Is(mapped, test.input) {
				t.Fatalf("mapError(%v) 没有保留错误链：%v", test.input, mapped)
			}
		})
	}
}

// TestPanicError 验证 panic 会形成内部错误并保留现场说明。
func TestPanicError(t *testing.T) {
	t.Parallel()

	// panicError 必须携带稳定内部错误码，文本中保留 scope 便于定位。
	err := panicError("test handler", "boom")
	if !errors.Is(err, errs.ErrInternal) {
		t.Fatalf("panicError() = %v", err)
	}
	if !containsAny(err.Error(), "test handler", "boom") {
		t.Fatalf("panicError() 缺少现场信息：%v", err)
	}
}
