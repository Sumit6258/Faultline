package senders

import "testing"

func TestLocalSender_RecordsSuccessfulSend(t *testing.T) {
	s := NewLocalSender("email", AlwaysSucceed)
	if err := s.Send("a@example.com", "hello"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	sent := s.Sent()
	if len(sent) != 1 || sent[0].Recipient != "a@example.com" {
		t.Fatalf("expected 1 recorded send, got %v", sent)
	}
}

func TestLocalSender_NeverSucceedAlwaysFails(t *testing.T) {
	s := NewLocalSender("sms", NeverSucceed)
	for i := 0; i < 5; i++ {
		if err := s.Send("x", "y"); err == nil {
			t.Fatalf("attempt %d: expected an error", i)
		}
	}
	if len(s.Sent()) != 0 {
		t.Fatal("expected nothing recorded as sent")
	}
}

func TestFailTimes_FailsExactlyNThenSucceeds(t *testing.T) {
	fail := FailTimes(3)
	s := NewLocalSender("push", fail)

	for i := 0; i < 3; i++ {
		if err := s.Send("x", "y"); err == nil {
			t.Fatalf("attempt %d should fail", i)
		}
	}
	if err := s.Send("x", "y"); err != nil {
		t.Fatalf("4th attempt should succeed, got %v", err)
	}
	if len(s.Sent()) != 1 {
		t.Fatalf("expected exactly 1 successful send recorded, got %d", len(s.Sent()))
	}
}
