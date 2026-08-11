package mongodbmodule

import (
	"context"
	"errors"
	"testing"

	"github.com/duanhf2012/origin/v3/errs"
	mongooptions "go.mongodb.org/mongo-driver/v2/mongo/options"
)

func TestWithSessionPassesDriverContextAndError(t *testing.T) {
	t.Parallel()
	runtime := newFakeRuntime()
	module := startTestModule(t, runtime)
	callbackErr := errors.New("callback failed")
	err := module.WithSession(
		context.Background(),
		func(ctx context.Context) error {
			if got := ctx.Value(contextMarker{}); got != "session" {
				t.Fatalf("session marker = %v", got)
			}
			return callbackErr
		},
		mongooptions.Session().SetCausalConsistency(true),
	)
	if !errors.Is(err, callbackErr) || runtime.sessionCalls != 1 {
		t.Fatalf("WithSession() error=%v calls=%d", err, runtime.sessionCalls)
	}
}

func TestWithTransactionAllowsDriverRetry(t *testing.T) {
	t.Parallel()
	runtime := newFakeRuntime()
	module := startTestModule(t, runtime)
	callbackRuns := 0
	err := module.WithTransaction(
		context.Background(),
		func(ctx context.Context) error {
			if got := ctx.Value(contextMarker{}); got != "transaction" {
				t.Fatalf("transaction marker = %v", got)
			}
			callbackRuns++
			return nil
		},
		mongooptions.Transaction(),
	)
	if err != nil || callbackRuns != 2 || runtime.transactionRuns != 2 {
		t.Fatalf("WithTransaction() error=%v callback=%d runtime=%d", err, callbackRuns, runtime.transactionRuns)
	}
}

func TestSessionAndTransactionValidateInputsAndRuntime(t *testing.T) {
	t.Parallel()
	module := configuredTestModule(newFakeRuntime())
	if err := module.WithSession(context.Background(), func(context.Context) error { return nil }); !errs.IsCode(err, errs.CodeServiceNotReady) {
		t.Fatalf("not-running WithSession error = %v", err)
	}
	typedNilSession := (*mongooptions.SessionOptionsBuilder)(nil)
	typedNilTransaction := (*mongooptions.TransactionOptionsBuilder)(nil)
	running := startTestModule(t, newFakeRuntime())
	tests := []struct {
		name string
		err  error
	}{
		{name: "nil session context", err: running.WithSession(nil, func(context.Context) error { return nil })},
		{name: "nil session callback", err: running.WithSession(context.Background(), nil)},
		{name: "nil session option", err: running.WithSession(context.Background(), func(context.Context) error { return nil }, typedNilSession)},
		{name: "nil transaction context", err: running.WithTransaction(nil, func(context.Context) error { return nil })},
		{name: "nil transaction callback", err: running.WithTransaction(context.Background(), nil)},
		{name: "nil transaction option", err: running.WithTransaction(context.Background(), func(context.Context) error { return nil }, typedNilTransaction)},
	}
	for _, test := range tests {
		if !errs.IsCode(test.err, errs.CodeInvalidArgument) {
			t.Errorf("%s error = %v", test.name, test.err)
		}
	}
}

func TestSessionAndTransactionReturnDriverErrors(t *testing.T) {
	t.Parallel()
	runtime := newFakeRuntime()
	runtime.sessionErr = errFake
	module := startTestModule(t, runtime)
	if err := module.WithSession(context.Background(), func(context.Context) error { return nil }); !errors.Is(err, errFake) {
		t.Fatalf("session driver error = %v", err)
	}
	runtime.sessionErr = nil
	runtime.transactionErr = errFake
	if err := module.WithTransaction(context.Background(), func(context.Context) error { return nil }); !errors.Is(err, errFake) {
		t.Fatalf("transaction driver error = %v", err)
	}
}
