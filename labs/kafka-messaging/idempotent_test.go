package kafkasim

import (
	"errors"
	"testing"
)

func TestIdempotentConsumer_ProcessesOnce(t *testing.T) {
	calls := 0
	c := NewIdempotentConsumer(func(Message) error {
		calls++
		return nil
	})

	msg := Message{Offset: 0, Key: "order-123", Value: "charge"}
	ran, err := c.Process(msg)
	if !ran || err != nil {
		t.Fatalf("expected the first delivery to run and succeed, ran=%v err=%v", ran, err)
	}
	if calls != 1 {
		t.Fatalf("expected 1 call, got %d", calls)
	}
}

func TestIdempotentConsumer_SkipsRedeliveredKey(t *testing.T) {
	calls := 0
	c := NewIdempotentConsumer(func(Message) error {
		calls++
		return nil
	})

	msg := Message{Offset: 0, Key: "order-123", Value: "charge"}
	c.Process(msg)
	ran, err := c.Process(msg) // redelivered, same key, simulating a consumer crash before commit

	if ran {
		t.Fatal("expected the redelivered message to be skipped, not reprocessed")
	}
	if err != nil {
		t.Fatalf("expected a skip to report no error, got %v", err)
	}
	if calls != 1 {
		t.Fatalf("expected the underlying process function to have run exactly once despite 2 deliveries, got %d", calls)
	}
}

func TestIdempotentConsumer_DifferentKeysBothProcess(t *testing.T) {
	calls := 0
	c := NewIdempotentConsumer(func(Message) error {
		calls++
		return nil
	})

	c.Process(Message{Key: "order-1"})
	c.Process(Message{Key: "order-2"})

	if calls != 2 {
		t.Fatalf("expected 2 distinct keys to both be processed, got %d calls", calls)
	}
}

func TestIdempotentConsumer_FailureIsRetryable(t *testing.T) {
	attempt := 0
	c := NewIdempotentConsumer(func(Message) error {
		attempt++
		if attempt == 1 {
			return errors.New("transient failure")
		}
		return nil
	})

	msg := Message{Key: "order-123"}
	_, err1 := c.Process(msg)
	if err1 == nil {
		t.Fatal("expected the first attempt to fail")
	}

	ran, err2 := c.Process(msg) // retry after failure, should actually run again
	if !ran || err2 != nil {
		t.Fatalf("expected a retry after failure to actually run and succeed, ran=%v err=%v", ran, err2)
	}
	if attempt != 2 {
		t.Fatalf("expected 2 attempts total, got %d", attempt)
	}
}
