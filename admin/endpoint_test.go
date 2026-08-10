package admin

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/duanhf2012/origin/v3/errs"
)

func TestEndpointGetPostAndValidation(t *testing.T) {
	handler := func(context.Context, Request) (Response, error) {
		return Empty(http.StatusNoContent), nil
	}
	get := Get("summary", handler)
	post := Post("reload-logic", handler,
		WithTimeout(3*time.Second),
		WithMaxBodyBytes(1024),
		WithMaxResponseBytes(2048),
		WithSuccessStatus(http.StatusAccepted),
	)
	if err := get.Validate(); err != nil || get.Method() != http.MethodGet {
		t.Fatalf("GET endpoint = %+v, %v", get, err)
	}
	if err := post.Validate(); err != nil || post.Method() != http.MethodPost {
		t.Fatalf("POST endpoint = %+v, %v", post, err)
	}
	for _, endpoint := range []Endpoint{
		Get("", handler),
		Get("Bad_Name", handler),
		Get(strings.Repeat("a", maxEndpointNameBytes+1), handler),
		Post("missing", nil),
		Post("bad-timeout", handler, WithTimeout(-time.Second)),
		Post("long-timeout", handler, WithTimeout(DefaultTimeout+time.Nanosecond)),
	} {
		if endpoint.Validate() == nil {
			t.Fatalf("Endpoint %+v unexpectedly valid", endpoint)
		}
	}
}

func TestEndpointDefaultsRejectInvalidLimitsAndRecoverHandlerPanics(t *testing.T) {
	called := false
	endpoint := Post("reload", func(_ context.Context, _ Request) (Response, error) {
		called = true
		return Response{}, nil
	})
	if endpoint.Timeout() != 15*time.Second || endpoint.MaxBodyBytes() != 1<<20 ||
		endpoint.MaxResponseBytes() != 4<<20 || endpoint.SuccessStatus() != http.StatusNoContent {
		t.Fatalf("POST defaults = timeout %s, body %d, response %d, status %d",
			endpoint.Timeout(), endpoint.MaxBodyBytes(), endpoint.MaxResponseBytes(), endpoint.SuccessStatus())
	}
	if _, err := endpoint.Invoke(context.Background(), Request{}); err != nil || !called {
		t.Fatalf("Invoke() = %v, called = %t", err, called)
	}
	if err := Get("summary", func(context.Context, Request) (Response, error) {
		return Response{}, nil
	}, WithMaxBodyBytes(1)).Validate(); err == nil {
		t.Fatal("GET accepted a body limit")
	}
	for _, endpoint := range []Endpoint{
		Post("reload", func(context.Context, Request) (Response, error) { return Response{}, nil }, WithMaxBodyBytes(0)),
		Post("reload", func(context.Context, Request) (Response, error) { return Response{}, nil }, WithMaxBodyBytes(DefaultMaxBodyBytes+1)),
		Post("reload", func(context.Context, Request) (Response, error) { return Response{}, nil }, WithMaxResponseBytes(0)),
		Post("reload", func(context.Context, Request) (Response, error) { return Response{}, nil }, WithMaxResponseBytes(DefaultMaxResponseBytes+1)),
		Post("reload", func(context.Context, Request) (Response, error) { return Response{}, nil }, WithSuccessStatus(http.StatusBadRequest)),
	} {
		if endpoint.Validate() == nil {
			t.Fatalf("invalid endpoint %+v unexpectedly validated", endpoint)
		}
	}
	panicking := Get("panic", func(context.Context, Request) (Response, error) {
		panic("handler failure")
	})
	if _, err := panicking.Invoke(context.Background(), Request{}); !errs.IsCode(err, errs.CodeInternal) {
		t.Fatalf("Invoke panic error = %v, want CodeInternal", err)
	}
}

func TestEndpointPreservesOptionErrorsUntilValidation(t *testing.T) {
	want := errors.New("option failed")
	endpoint := Get("summary", func(context.Context, Request) (Response, error) {
		return Response{}, nil
	},
		func(*endpointOptions) error { return want },
		nil,
		func(*endpointOptions) error { return errors.New("later option failed") },
	)
	if err := endpoint.Validate(); !errors.Is(err, want) {
		t.Fatalf("Validate() = %v, want option error", err)
	}
	if endpoint.Name() != "summary" {
		t.Fatalf("Name() = %q, want summary", endpoint.Name())
	}
}

func TestGuardSecurityValueModel(t *testing.T) {
	operation := Operation{
		Method:      http.MethodPost,
		Endpoint:    "reload-logic",
		NodeID:      "node-1",
		ServiceName: "logic",
	}
	var guard Guard = guardFunc(func(_ context.Context, _ *http.Request, got Operation) (Principal, error) {
		if got != operation {
			t.Fatalf("Operation = %+v, want %+v", got, operation)
		}
		return Principal{Subject: "operator", Roles: []string{"admin"}}, nil
	})
	request, err := http.NewRequest(http.MethodPost, "http://admin.example/reload-logic", nil)
	if err != nil {
		t.Fatal(err)
	}
	principal, err := guard.Authorize(context.Background(), request, operation)
	if err != nil || principal.Subject != "operator" || principal.Roles[0] != "admin" {
		t.Fatalf("Authorize() = %+v, %v", principal, err)
	}
	if errors.Is(ErrUnauthenticated, ErrForbidden) {
		t.Fatalf("guard errors = %v, %v", ErrUnauthenticated, ErrForbidden)
	}
}

type guardFunc func(context.Context, *http.Request, Operation) (Principal, error)

func (function guardFunc) Authorize(ctx context.Context, request *http.Request, operation Operation) (Principal, error) {
	return function(ctx, request, operation)
}
