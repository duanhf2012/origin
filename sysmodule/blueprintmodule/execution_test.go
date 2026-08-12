package blueprintmodule

import (
	"context"
	"errors"
	"testing"
)

func TestExecutionSynchronousStateAndResult(t *testing.T) {
	fixture := startInstanceTestFixture(t)
	err := dispatchInstanceTest(t, fixture, func(ctx context.Context) error {
		instance, err := fixture.module.Create("lifecycle")
		if err != nil {
			return err
		}
		defer instance.Close()
		execution, err := instance.Start(ctx, 1)
		if err != nil {
			return err
		}
		if execution.ID() == 0 || !execution.IsDone() || execution.State() != ExecutionCompleted {
			t.Fatalf("unexpected synchronous execution: id=%d state=%v done=%v", execution.ID(), execution.State(), execution.IsDone())
		}
		if _, err = execution.Result(); err != nil {
			return err
		}
		if execution.Cancel() {
			t.Fatal("Cancel succeeded after completion")
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestInstanceRejectsInvalidArguments(t *testing.T) {
	fixture := startInstanceTestFixture(t)
	err := dispatchInstanceTest(t, fixture, func(ctx context.Context) error {
		instance, err := fixture.module.Create("lifecycle")
		if err != nil {
			return err
		}
		defer instance.Close()
		if _, err = instance.Start(nil, 1); !errors.Is(err, ErrInvalidArgument) {
			t.Fatalf("Start(nil) error = %v", err)
		}
		if _, err = fixture.module.Run(nil, "lifecycle", 1); !errors.Is(err, ErrInvalidArgument) {
			t.Fatalf("Run(nil) error = %v", err)
		}
		if _, err = instance.Start(ctx, 99); !errors.Is(err, ErrEntranceNotFound) {
			t.Fatalf("invalid entrance error = %v", err)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
