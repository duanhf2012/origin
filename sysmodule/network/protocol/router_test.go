package protocol

import (
	"context"
	"errors"
	"testing"

	"github.com/duanhf2012/origin/v3/errs"
	"github.com/duanhf2012/origin/v3/sysmodule/network"
)

type testMessage struct {
	Text string
}

type testCodec struct {
	id    MessageID
	panic bool
}

func (codec testCodec) Decode(_ []byte, resolver Resolver) (Message, error) {
	if codec.panic {
		panic("decode")
	}
	value, _ := resolver.New(codec.id)
	if message, ok := value.(*testMessage); ok {
		message.Text = "decoded"
	}
	return Message{ID: codec.id, Value: value}, nil
}

func (testCodec) Encode(*Encoder, MessageID, any) error { return nil }

func TestRouterRegisterDispatchAndFreeze(t *testing.T) {
	router, err := NewRouter(RouterOptions{Codec: testCodec{id: 7}})
	if err != nil {
		t.Fatal(err)
	}
	called := false
	if err := Register(router, 7, func(_ context.Context, _ network.Session, value *testMessage) error {
		called = value.Text == "decoded"
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := router.OnMessage(t.Context(), nil, []byte("raw")); err != nil {
		t.Fatalf("OnMessage error=%v", err)
	}
	if !called {
		t.Fatal("注册 Handler 未收到解码后的具体类型")
	}
	if err := router.Freeze(); err != nil {
		t.Fatal(err)
	}
	if err := Register(router, 8, func(context.Context, network.Session, *testMessage) error {
		return nil
	}); !errs.IsCode(err, errs.CodeInvalidConfig) {
		t.Fatalf("冻结后 Register error=%v", err)
	}
}

func TestRouterUnknownAndDecodePanic(t *testing.T) {
	unknown := errors.New("unknown")
	router, err := NewRouter(RouterOptions{
		Codec: testCodec{id: 9},
		Unknown: func(_ context.Context, _ network.Session, id MessageID, raw []byte) error {
			if id != 9 || string(raw) != "raw" {
				t.Fatalf("Unknown=(%d,%q)", id, raw)
			}
			return unknown
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := router.OnMessage(t.Context(), nil, []byte("raw")); !errors.Is(err, unknown) {
		t.Fatalf("unknown error=%v", err)
	}

	panicRouter, err := NewRouter(RouterOptions{Codec: testCodec{panic: true}})
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if recover() == nil {
			t.Fatal("Codec panic 应交给上层 Handler panic 边界")
		}
	}()
	_ = panicRouter.OnMessage(t.Context(), nil, nil)
}

func TestRouterRejectsInvalidAndDuplicateRegistration(t *testing.T) {
	router, err := NewRouter(RouterOptions{Codec: testCodec{id: 1}})
	if err != nil {
		t.Fatal(err)
	}
	handler := func(context.Context, network.Session, *testMessage) error { return nil }
	if err := Register(router, 0, handler); !errors.Is(err, errs.ErrInvalidArgument) {
		t.Fatalf("zero ID error=%v", err)
	}
	if err := Register(router, 1, handler); err != nil {
		t.Fatal(err)
	}
	if err := Register(router, 1, handler); !errors.Is(err, errs.ErrInvalidConfig) {
		t.Fatalf("duplicate error=%v", err)
	}
}
