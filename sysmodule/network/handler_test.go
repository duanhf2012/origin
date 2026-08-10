package network

import (
	"context"
	"errors"
	"testing"
)

func TestHandlerFuncsDefaultsAndDelegation(t *testing.T) {
	t.Parallel()

	// 零值必须可以直接作为安全 Handler，避免简单使用者实现四个空方法。
	var empty HandlerFuncs
	if err := empty.OnOpen(t.Context(), nil); err != nil {
		t.Fatalf("zero OnOpen error=%v", err)
	}
	if err := empty.OnMessage(t.Context(), nil, nil); err != nil {
		t.Fatalf("zero OnMessage error=%v", err)
	}
	empty.OnWritableChanged(t.Context(), nil, true)
	empty.OnClose(t.Context(), nil, nil)

	// 非空字段应只调用各自函数并原样传播 error/参数。
	wantOpen := errors.New("open")
	wantMessage := errors.New("message")
	calledWritable := false
	calledClose := false
	handler := HandlerFuncs{
		Open: func(context.Context, Session) error { return wantOpen },
		Message: func(_ context.Context, _ Session, payload []byte) error {
			if string(payload) != "payload" {
				t.Fatalf("payload=%q", payload)
			}
			return wantMessage
		},
		WritableChanged: func(_ context.Context, _ Session, writable bool) {
			calledWritable = writable
		},
		Close: func(_ context.Context, _ Session, cause error) {
			calledClose = errors.Is(cause, wantOpen)
		},
	}
	if err := handler.OnOpen(t.Context(), nil); !errors.Is(err, wantOpen) {
		t.Fatalf("OnOpen error=%v", err)
	}
	if err := handler.OnMessage(t.Context(), nil, []byte("payload")); !errors.Is(err, wantMessage) {
		t.Fatalf("OnMessage error=%v", err)
	}
	handler.OnWritableChanged(t.Context(), nil, true)
	handler.OnClose(t.Context(), nil, wantOpen)
	if !calledWritable || !calledClose {
		t.Fatalf("callbacks=(writable=%v close=%v)", calledWritable, calledClose)
	}
}
