package kafkamodule

import (
	"errors"
	"testing"

	"github.com/duanhf2012/origin/v3/errs"
)

func TestSubmitQueueHoldsBudgetUntilDelivery(t *testing.T) {
	queue := newSubmitQueue(1, 128)
	first := testEnvelope(96)
	if err := queue.trySubmit(first); err != nil {
		t.Fatal(err)
	}
	if err := queue.trySubmit(testEnvelope(1)); !errors.Is(err, errs.ErrTransportOverloaded) {
		t.Fatalf("message budget was released before delivery: %v", err)
	}
	// Delivery 只能发生在 submit goroutine 已经取走消息之后，模拟该所有权转移。
	<-queue.items
	queue.release(first)
	if err := queue.trySubmit(testEnvelope(1)); err != nil {
		t.Fatalf("released budget was not reusable: %v", err)
	}
}

func TestSubmitQueueEnforcesByteBudgetAndClose(t *testing.T) {
	queue := newSubmitQueue(4, 100)
	if err := queue.trySubmit(testEnvelope(101)); !errors.Is(err, errs.ErrTransportOverloaded) {
		t.Fatalf("oversized message accepted: %v", err)
	}
	queue.close()
	queue.close()
	if err := queue.trySubmit(testEnvelope(1)); !errors.Is(err, ErrNotRunning) {
		t.Fatalf("closed queue accepted a message: %v", err)
	}
}

func testEnvelope(size int64) *producerEnvelope {
	return &producerEnvelope{encoded: &encodedMessage{topic: "events", value: make([]byte, size), payloadBytes: size}, delivery: newDelivery()}
}
