package rpc

import (
	"errors"
	"testing"

	"github.com/duanhf2012/origin/v3/errs"
	"github.com/duanhf2012/origin/v3/internal/natsnet"
)

func TestRuntimeReportsTransportStateChanges(t *testing.T) {
	t.Parallel()

	runtime := &Runtime{}
	events := make([]TransportEvent, 0, 2)
	first := errors.New("first transport interruption")
	if err := runtime.BindTransportObserver(func(event TransportEvent) {
		events = append(events, event)
	}); err != nil {
		t.Fatalf("BindTransportObserver() error = %v", err)
	}

	runtime.reportTransportEvent(TransportEvent{
		Kind:      TransportKindTCP,
		State:     TransportStateRecovering,
		ErrorCode: errs.CodeTransportUnavailable,
		Cause:     first,
	})
	runtime.reportTransportEvent(TransportEvent{
		Kind:  TransportKindTCP,
		State: TransportStateReady,
	})
	if len(events) != 2 {
		t.Fatalf("transport observer events = %d, want 2", len(events))
	}
	if !errors.Is(events[0].Cause, first) ||
		events[0].State != TransportStateRecovering {
		t.Fatalf("first transport event = %+v", events[0])
	}
	if events[1].State != TransportStateReady || events[1].Cause != nil {
		t.Fatalf("second transport event = %+v", events[1])
	}
}

func TestNATSClosedEventReportsRecoveringOnlyWhenUnexpected(t *testing.T) {
	t.Parallel()

	events := make([]TransportEvent, 0, 2)
	owner := &Runtime{}
	if err := owner.BindTransportObserver(func(event TransportEvent) {
		events = append(events, event)
	}); err != nil {
		t.Fatalf("BindTransportObserver() error = %v", err)
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
	if len(events) != 1 ||
		events[0].State != TransportStateRecovering {
		t.Fatalf("unexpected close events = %+v", events)
	}

	normalOwner := &Runtime{}
	if err := normalOwner.BindTransportObserver(func(event TransportEvent) {
		events = append(events, event)
	}); err != nil {
		t.Fatalf("normal BindTransportObserver() error = %v", err)
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
	if len(events) != 1 {
		t.Fatalf("normal closure reported state change, events = %+v", events)
	}
}
