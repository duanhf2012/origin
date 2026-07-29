package rpc

import (
	"errors"
	"sync/atomic"
	"testing"

	"github.com/duanhf2012/origin/v3/errs"
	"github.com/duanhf2012/origin/v3/internal/natsnet"
)

func TestRuntimeReportsOnlyFirstTransportFailure(t *testing.T) {
	t.Parallel()

	runtime := &Runtime{}
	var calls atomic.Int32
	first := errors.New("first transport failure")
	if err := runtime.BindFailureHandler(func(cause error) {
		if !errors.Is(cause, first) {
			t.Errorf("failure cause = %v", cause)
		}
		calls.Add(1)
	}); err != nil {
		t.Fatalf("BindFailureHandler() error = %v", err)
	}

	runtime.reportTransportFailure(first)
	runtime.reportTransportFailure(errors.New("second transport failure"))
	if got := calls.Load(); got != 1 {
		t.Fatalf("failure handler calls = %d, want 1", got)
	}
}

func TestNATSTerminalEventReportsOnlyUnexpectedClosure(t *testing.T) {
	t.Parallel()

	var calls atomic.Int32
	owner := &Runtime{}
	if err := owner.BindFailureHandler(func(error) {
		calls.Add(1)
	}); err != nil {
		t.Fatalf("BindFailureHandler() error = %v", err)
	}
	nats := &natsRuntime{
		owner:   owner,
		started: true,
		pending: newNATSPendingTable(1),
	}

	nats.handleEvent(natsnet.Event{
		Type: natsnet.EventClosed,
		Err:  errs.ErrTransportUnavailable,
	})
	nats.handleEvent(natsnet.Event{
		Type: natsnet.EventClosed,
		Err:  errs.ErrTransportUnavailable,
	})
	if got := calls.Load(); got != 1 {
		t.Fatalf("unexpected terminal calls = %d, want 1", got)
	}

	normalOwner := &Runtime{}
	if err := normalOwner.BindFailureHandler(func(error) {
		calls.Add(1)
	}); err != nil {
		t.Fatalf("normal BindFailureHandler() error = %v", err)
	}
	normal := &natsRuntime{
		owner:    normalOwner,
		started:  true,
		stopping: true,
		pending:  newNATSPendingTable(1),
	}
	normal.handleEvent(natsnet.Event{
		Type: natsnet.EventClosed,
		Err:  errs.ErrTransportUnavailable,
	})
	if got := calls.Load(); got != 1 {
		t.Fatalf("normal closure reported failure, calls = %d", got)
	}
}
