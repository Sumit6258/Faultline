package kafkasim

import "testing"

func TestDLQ_SendAndRetrieve(t *testing.T) {
	d := &DLQ{}
	d.Send(Message{Key: "poison", Value: "always fails"})

	msgs := d.Messages()
	if len(msgs) != 1 || msgs[0].Key != "poison" {
		t.Fatalf("expected 1 message with key poison, got %v", msgs)
	}
}

func TestDLQ_Len(t *testing.T) {
	d := &DLQ{}
	if d.Len() != 0 {
		t.Fatal("expected an empty DLQ to have length 0")
	}
	d.Send(Message{Key: "a"})
	d.Send(Message{Key: "b"})
	if d.Len() != 2 {
		t.Fatalf("expected length 2, got %d", d.Len())
	}
}
