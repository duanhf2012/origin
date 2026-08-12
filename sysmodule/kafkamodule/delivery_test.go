package kafkamodule

import (
	"context"
	"errors"
	"sync"
	"testing"
)

func TestDeliveryCompletesExactlyOnce(t *testing.T) {
	delivery := newDelivery()
	var wait sync.WaitGroup
	for index := int64(1); index <= 32; index++ {
		wait.Add(1)
		go func(offset int64) {
			defer wait.Done()
			delivery.complete(DeliveryResult{Metadata: Metadata{Offset: offset}})
		}(index)
	}
	wait.Wait()
	result, ok := delivery.Result()
	if !ok || result.Err != nil || result.Metadata.Offset < 1 || result.Metadata.Offset > 32 {
		t.Fatalf("unexpected immutable result: %#v, %v", result, ok)
	}
	select {
	case <-delivery.Done():
	default:
		t.Fatal("Done was not closed")
	}
}

func TestDeliveryWaitContextDoesNotCancelDelivery(t *testing.T) {
	delivery := newDelivery()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := delivery.Wait(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("unexpected wait error: %v", err)
	}
	delivery.complete(DeliveryResult{Metadata: Metadata{Offset: 7}})
	metadata, err := delivery.Wait(context.Background())
	if err != nil || metadata.Offset != 7 {
		t.Fatalf("delivery was cancelled by waiter: %+v, %v", metadata, err)
	}
}

func TestDeliveryRejectsNilContextAndNilReceiver(t *testing.T) {
	delivery := newDelivery()
	if _, err := delivery.Wait(nil); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("nil context accepted: %v", err)
	}
	var missing *Delivery
	if _, err := missing.Wait(context.Background()); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("nil receiver accepted: %v", err)
	}
	if missing.Done() != nil {
		t.Fatal("nil receiver returned a non-nil Done channel")
	}
}
