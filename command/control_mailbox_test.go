package command

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/duanhf2012/origin/v3/errs"
)

func TestControlMailboxRoundTrip(t *testing.T) {
	pidDir := t.TempDir()
	lease, err := acquirePIDLease(pidDir, "game")
	if err != nil {
		t.Fatal(err)
	}
	defer lease.close()
	mailbox, err := startControlMailbox(t.Context(), pidDir, "game")
	if err != nil {
		t.Fatal(err)
	}
	defer mailbox.close()

	result := make(chan error, 1)
	go func() {
		result <- requestApplicationControl(t.Context(), pidDir, "game", ControlActionRetire)
	}()
	request := receiveControlRequest(t, mailbox.requests)
	if request.Action() != ControlActionRetire {
		t.Fatalf("Action = %v", request.Action())
	}
	request.Complete(nil)
	request.Complete(errors.New("second completion must be ignored"))
	if err := receiveControlResult(t, result); err != nil {
		t.Fatalf("requestApplicationControl() error = %v", err)
	}
}

func TestControlMailboxPreservesBoundedOriginError(t *testing.T) {
	pidDir := t.TempDir()
	lease, err := acquirePIDLease(pidDir, "game")
	if err != nil {
		t.Fatal(err)
	}
	defer lease.close()
	mailbox, err := startControlMailbox(t.Context(), pidDir, "game")
	if err != nil {
		t.Fatal(err)
	}
	defer mailbox.close()

	result := make(chan error, 1)
	go func() {
		result <- requestApplicationControl(t.Context(), pidDir, "game", ControlActionResume)
	}()
	request := receiveControlRequest(t, mailbox.requests)
	request.Complete(errs.NewMessage(
		errs.CodeDiscoveryUnavailable,
		strings.Repeat("不可用", maxControlRecordSize),
	))
	controlErr := receiveControlResult(t, result)
	if !errs.IsCode(controlErr, errs.CodeDiscoveryUnavailable) {
		t.Fatalf("control error = %v, want discovery unavailable", controlErr)
	}
	if len(controlErr.Error()) > maxControlRecordSize {
		t.Fatalf("control error length = %d, want bounded", len(controlErr.Error()))
	}
}

func TestControlMailboxTimeoutDoesNotDeleteProcessingRequest(t *testing.T) {
	pidDir := t.TempDir()
	lease, err := acquirePIDLease(pidDir, "game")
	if err != nil {
		t.Fatal(err)
	}
	defer lease.close()
	processing := controlProcessingPath(pidDir, "game")
	if err := os.WriteFile(processing, []byte("owned by target"), 0o600); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(t.Context(), 80*time.Millisecond)
	defer cancel()
	err = requestApplicationControl(ctx, pidDir, "game", ControlActionRetire)
	if !errs.IsCode(err, errs.CodeDeadlineExceeded) {
		t.Fatalf("requestApplicationControl() error = %v, want deadline exceeded", err)
	}
	if _, err := os.Stat(processing); err != nil {
		t.Fatalf("processing request was removed: %v", err)
	}
}

func TestControlMailboxSerializesConcurrentCommands(t *testing.T) {
	pidDir := t.TempDir()
	lease, err := acquirePIDLease(pidDir, "game")
	if err != nil {
		t.Fatal(err)
	}
	defer lease.close()
	mailbox, err := startControlMailbox(t.Context(), pidDir, "game")
	if err != nil {
		t.Fatal(err)
	}
	defer mailbox.close()

	firstResult := make(chan error, 1)
	go func() {
		firstResult <- requestApplicationControl(t.Context(), pidDir, "game", ControlActionRetire)
	}()
	first := receiveControlRequest(t, mailbox.requests)

	secondResult := make(chan error, 1)
	go func() {
		secondResult <- requestApplicationControl(t.Context(), pidDir, "game", ControlActionResume)
	}()
	select {
	case request := <-mailbox.requests:
		t.Fatalf("second request arrived before first completion: %v", request.Action())
	case <-time.After(100 * time.Millisecond):
	}
	first.Complete(nil)
	if err := receiveControlResult(t, firstResult); err != nil {
		t.Fatal(err)
	}
	second := receiveControlRequest(t, mailbox.requests)
	if second.Action() != ControlActionResume {
		t.Fatalf("second Action = %v, want Resume", second.Action())
	}
	second.Complete(nil)
	if err := receiveControlResult(t, secondResult); err != nil {
		t.Fatal(err)
	}
}

func TestControlMailboxStartCleansStaleFiles(t *testing.T) {
	pidDir := t.TempDir()
	lease, err := acquirePIDLease(pidDir, "game")
	if err != nil {
		t.Fatal(err)
	}
	defer lease.close()
	paths := []string{
		controlRequestPath(pidDir, "game"),
		controlProcessingPath(pidDir, "game"),
		controlResponsePath(pidDir, "game"),
	}
	for _, path := range paths {
		if err := os.WriteFile(path, []byte("stale"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	mailbox, err := startControlMailbox(t.Context(), pidDir, "game")
	if err != nil {
		t.Fatal(err)
	}
	defer mailbox.close()
	for _, path := range paths {
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Fatalf("stale control path %q still exists: %v", path, err)
		}
	}
}

func TestControlMailboxRejectsDirectoryAsControlFile(t *testing.T) {
	pidDir := t.TempDir()
	lease, err := acquirePIDLease(pidDir, "game")
	if err != nil {
		t.Fatal(err)
	}
	defer lease.close()
	path := controlRequestPath(pidDir, "game")
	if err := os.Mkdir(path, 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := startControlMailbox(t.Context(), pidDir, "game"); err == nil {
		t.Fatal("startControlMailbox() accepted directory as request file")
	}
}

func TestControlMailboxCloseCompletesPendingRequest(t *testing.T) {
	pidDir := t.TempDir()
	lease, err := acquirePIDLease(pidDir, "game")
	if err != nil {
		t.Fatal(err)
	}
	defer lease.close()
	mailbox, err := startControlMailbox(t.Context(), pidDir, "game")
	if err != nil {
		t.Fatal(err)
	}

	result := make(chan error, 1)
	go func() {
		result <- requestApplicationControl(t.Context(), pidDir, "game", ControlActionRetire)
	}()
	request := receiveControlRequest(t, mailbox.requests)
	closeResult := make(chan error, 1)
	go func() { closeResult <- mailbox.close() }()
	select {
	case <-request.Context().Done():
	case <-time.After(3 * time.Second):
		t.Fatal("pending request Context was not canceled")
	}
	if err := receiveControlResult(t, result); !errs.IsCode(err, errs.CodeCanceled) {
		t.Fatalf("pending command error = %v, want canceled", err)
	}
	if err := receiveControlResult(t, closeResult); err != nil {
		t.Fatalf("mailbox.close() error = %v", err)
	}
}

func receiveControlRequest(t *testing.T, requests <-chan ControlRequest) ControlRequest {
	t.Helper()
	select {
	case request := <-requests:
		return request
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for control request")
		return nil
	}
}

func receiveControlResult(t *testing.T, result <-chan error) error {
	t.Helper()
	select {
	case err := <-result:
		return err
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for control result")
		return nil
	}
}
