package network

import (
	"testing"
	"time"

	"github.com/duanhf2012/origin/v3/errs"
)

func TestDefaultEndpointOptions(t *testing.T) {
	t.Parallel()

	// 逐项锁定正式设计确认的首轮默认值。
	handler := HandlerFuncs{}
	options := DefaultEndpointOptions(handler)
	if options.Handler == nil || options.MaxSessions != 4096 ||
		options.MaxMessageSize != 64*1024 ||
		options.ReceivePendingMessages != 64 ||
		options.ReceivePendingSize != 256*1024 ||
		options.ReceivePendingTotalSize != 64*1024*1024 ||
		options.SendQueueMessages != 256 ||
		options.SendQueueSize != 256*1024 ||
		options.SendQueueTotalSize != 128*1024*1024 ||
		options.ReadIdleTimeout != 0 ||
		options.WriteTimeout != 15*time.Second ||
		options.SlowClientTimeout != 10*time.Second {
		t.Fatalf("DefaultEndpointOptions=%+v", options)
	}
	if err := options.Validate(); err != nil {
		t.Fatalf("默认配置无效：%v", err)
	}
}

func TestEndpointOptionsValidation(t *testing.T) {
	t.Parallel()

	// 使用有效基线逐字段破坏，保证每个容量和时间边界都在创建资源前失败。
	valid := DefaultEndpointOptions(HandlerFuncs{})
	tests := []struct {
		name   string
		mutate func(*EndpointOptions)
	}{
		{name: "nil handler", mutate: func(value *EndpointOptions) { value.Handler = nil }},
		{name: "zero sessions", mutate: func(value *EndpointOptions) { value.MaxSessions = 0 }},
		{name: "too many sessions", mutate: func(value *EndpointOptions) { value.MaxSessions = MaxEndpointSessions + 1 }},
		{name: "zero message", mutate: func(value *EndpointOptions) { value.MaxMessageSize = 0 }},
		{name: "zero receive messages", mutate: func(value *EndpointOptions) { value.ReceivePendingMessages = 0 }},
		{name: "small receive size", mutate: func(value *EndpointOptions) { value.ReceivePendingSize = int64(value.MaxMessageSize - 1) }},
		{name: "small receive total", mutate: func(value *EndpointOptions) { value.ReceivePendingTotalSize = value.ReceivePendingSize - 1 }},
		{name: "zero send messages", mutate: func(value *EndpointOptions) { value.SendQueueMessages = 0 }},
		{name: "small send size", mutate: func(value *EndpointOptions) { value.SendQueueSize = int64(value.MaxMessageSize - 1) }},
		{name: "small send total", mutate: func(value *EndpointOptions) { value.SendQueueTotalSize = value.SendQueueSize - 1 }},
		{name: "negative read idle", mutate: func(value *EndpointOptions) { value.ReadIdleTimeout = -time.Nanosecond }},
		{name: "zero write timeout", mutate: func(value *EndpointOptions) { value.WriteTimeout = 0 }},
		{name: "zero slow timeout", mutate: func(value *EndpointOptions) { value.SlowClientTimeout = 0 }},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			options := valid
			test.mutate(&options)
			if err := options.Validate(); !errs.IsCode(err, errs.CodeInvalidConfig) {
				t.Fatalf("Validate error=%v", err)
			}
		})
	}
}

func TestEndpointOptionsRejectsTypedNilHandler(t *testing.T) {
	t.Parallel()

	// 接口非 nil 但内部指针为 nil 的 Handler 必须在初始化期拒绝。
	type pointerHandler struct{ HandlerFuncs }
	var handler *pointerHandler
	options := DefaultEndpointOptions(handler)
	if err := options.Validate(); !errs.IsCode(err, errs.CodeInvalidConfig) {
		t.Fatalf("typed nil Validate error=%v", err)
	}
}

func TestEndpointOptionsUsesRetainedBufferCapacity(t *testing.T) {
	t.Parallel()

	options := DefaultEndpointOptions(HandlerFuncs{})
	options.MaxMessageSize = 17
	options.ReceivePendingSize = 17
	options.SendQueueSize = 17
	if err := options.Validate(); !errs.IsCode(err, errs.CodeInvalidConfig) {
		t.Fatalf("未拒绝小于 32B 池档位的预算：%v", err)
	}
	options.ReceivePendingSize = 32
	options.SendQueueSize = 32
	if err := options.Validate(); err != nil {
		t.Fatalf("精确池档位预算无效：%v", err)
	}
}
