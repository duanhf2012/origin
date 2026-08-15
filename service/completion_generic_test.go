package service

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/duanhf2012/origin/v3/errs"
)

func TestServiceDispatchAsyncCompletionPassesTypedResult(t *testing.T) {
	fixture := newSchedulerFixture(t, DefaultSchedulerConfig())
	started := make(chan struct{})
	release := make(chan struct{})
	completed := make(chan struct{})

	err := fixture.service.DispatchAsyncCompletion(
		context.Background(),
		func(context.Context) (int, error) {
			close(started)
			<-release
			return 42, nil
		},
		func(callbackCtx context.Context, value int, callbackErr error) {
			if callbackErr != nil {
				t.Errorf("callback error = %v", callbackErr)
			}
			if value != 42 {
				t.Errorf("callback value = %d, want 42", value)
			}
			if _, ok := callbackCtx.Value(taskContextKey{}).(*taskContext); !ok {
				t.Error("callback did not resume in the Service task context")
			}
			close(completed)
		},
	)
	if err != nil {
		t.Fatalf("DispatchAsyncCompletion() error = %v", err)
	}

	waitSignal(t, started)
	close(release)
	waitSignal(t, completed)
}

func TestServiceDispatchAsyncCompletionPassesWaitErrorAndResult(t *testing.T) {
	fixture := newSchedulerFixture(t, DefaultSchedulerConfig())
	wantErr := errors.New("remote call failed")
	type completion struct {
		value int
		err   error
	}
	completed := make(chan completion, 1)

	err := fixture.service.DispatchAsyncCompletion(
		context.Background(),
		func(context.Context) (int, error) { return 7, wantErr },
		func(_ context.Context, value int, callbackErr error) {
			completed <- completion{value: value, err: callbackErr}
		},
	)
	if err != nil {
		t.Fatalf("DispatchAsyncCompletion() error = %v", err)
	}
	result := receive(t, completed)
	if result.value != 7 || !errors.Is(result.err, wantErr) {
		t.Fatalf("callback result = %+v", result)
	}
}

func TestServiceDispatchAsyncCompletionSkipsWaitForFinishedContext(t *testing.T) {
	tests := []struct {
		name string
		ctx  func() context.Context
		want error
	}{
		{
			name: "canceled",
			ctx: func() context.Context {
				ctx, cancel := context.WithCancel(context.Background())
				cancel()
				return ctx
			},
			want: errs.ErrCanceled,
		},
		{
			name: "deadline exceeded",
			ctx: func() context.Context {
				ctx, cancel := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
				cancel()
				return ctx
			},
			want: errs.ErrDeadlineExceeded,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture := newSchedulerFixture(t, DefaultSchedulerConfig())
			var waitCalled atomic.Bool
			type completion struct {
				value int
				err   error
			}
			completed := make(chan completion, 1)

			if err := fixture.service.DispatchAsyncCompletion(
				test.ctx(),
				func(context.Context) (int, error) {
					waitCalled.Store(true)
					return 99, nil
				},
				func(_ context.Context, value int, callbackErr error) {
					completed <- completion{value: value, err: callbackErr}
				},
			); err != nil {
				t.Fatalf("DispatchAsyncCompletion() error = %v", err)
			}
			result := receive(t, completed)
			if waitCalled.Load() || result.value != 0 || !errors.Is(result.err, test.want) {
				t.Fatalf("waitCalled=%v callback=%+v", waitCalled.Load(), result)
			}
		})
	}
}

func TestServiceDispatchAsyncCompletionDelegatesAdmissionAndPanic(t *testing.T) {
	t.Run("queue full", func(t *testing.T) {
		config := DefaultSchedulerConfig()
		config.MaxTasks = 1
		config.MaxAwaitTasks = 1
		fixture := newSchedulerFixture(t, config)
		started := make(chan struct{})
		release := make(chan struct{})
		done := make(chan struct{})
		if err := fixture.service.DispatchAsync(func(context.Context) {
			close(started)
			<-release
			close(done)
		}); err != nil {
			t.Fatal(err)
		}
		waitSignal(t, started)
		if err := fixture.service.DispatchAsyncCompletion(
			context.Background(),
			func(context.Context) (int, error) { return 1, nil },
			func(context.Context, int, error) {},
		); !errors.Is(err, errs.ErrServiceQueueFull) {
			t.Fatalf("DispatchAsyncCompletion() error = %v", err)
		}
		close(release)
		waitSignal(t, done)
	})

	t.Run("stopped", func(t *testing.T) {
		fixture := newSchedulerFixture(t, DefaultSchedulerConfig())
		fixture.stop(t)
		if err := fixture.service.DispatchAsyncCompletion(
			context.Background(),
			func(context.Context) (int, error) { return 1, nil },
			func(context.Context, int, error) {},
		); !errors.Is(err, errs.ErrServiceStopped) {
			t.Fatalf("DispatchAsyncCompletion() error = %v", err)
		}
	})

	t.Run("panic", func(t *testing.T) {
		fixture := newSchedulerFixture(t, DefaultSchedulerConfig())
		var callbackCalled atomic.Bool
		if err := fixture.service.DispatchAsyncCompletion(
			context.Background(),
			func(context.Context) (int, error) { panic("completion panic") },
			func(context.Context, int, error) { callbackCalled.Store(true) },
		); err != nil {
			t.Fatal(err)
		}
		waitForStats(t, fixture.service, func(stats ExecutionStats) bool {
			return stats.CompletedTotal == 1
		})
		stats := fixture.service.ExecutionStats()
		if callbackCalled.Load() || stats.PanicTotal != 1 {
			t.Fatalf("callbackCalled=%v stats=%+v", callbackCalled.Load(), stats)
		}
	})
}

func TestServiceDispatchAsyncCompletionRejectsInvalidArguments(t *testing.T) {
	fixture := newSchedulerFixture(t, DefaultSchedulerConfig())
	validWait := func(context.Context) (int, error) { return 0, nil }
	validCallback := func(context.Context, int, error) {}

	var nilService *Service
	if err := nilService.DispatchAsyncCompletion(context.Background(), validWait, validCallback); !errors.Is(err, errs.ErrInvalidArgument) {
		t.Fatalf("nil Service error = %v", err)
	}
	if err := fixture.service.DispatchAsyncCompletion(nil, validWait, validCallback); !errors.Is(err, errs.ErrInvalidArgument) {
		t.Fatalf("nil context error = %v", err)
	}
	if err := fixture.service.DispatchAsyncCompletion(context.Background(), nil, validCallback); !errors.Is(err, errs.ErrInvalidArgument) {
		t.Fatalf("nil wait error = %v", err)
	}
	if err := fixture.service.DispatchAsyncCompletion(context.Background(), validWait, nil); !errors.Is(err, errs.ErrInvalidArgument) {
		t.Fatalf("nil callback error = %v", err)
	}
}
