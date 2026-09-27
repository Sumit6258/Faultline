package store

import "testing"

func TestMemory_SaveAndLoad(t *testing.T) {
	m := NewMemory()
	if err := m.Save("abc", "https://example.com"); err != nil {
		t.Fatalf("unexpected error saving: %v", err)
	}
	v, ok := m.Load("abc")
	if !ok || v != "https://example.com" {
		t.Fatalf("expected to load back the saved URL, got %q ok=%v", v, ok)
	}
}

func TestMemory_SaveRejectsTakenCode(t *testing.T) {
	m := NewMemory()
	m.Save("abc", "https://first.example.com")
	err := m.Save("abc", "https://second.example.com")
	if err != ErrCodeTaken {
		t.Fatalf("expected ErrCodeTaken, got %v", err)
	}
	v, _ := m.Load("abc")
	if v != "https://first.example.com" {
		t.Fatalf("expected the original value to be preserved, got %q", v)
	}
}

func TestMemory_LoadMissingCode(t *testing.T) {
	m := NewMemory()
	_, ok := m.Load("missing")
	if ok {
		t.Fatal("expected a miss for a code that was never saved")
	}
}

func TestMemory_Exists(t *testing.T) {
	m := NewMemory()
	if m.Exists("abc") {
		t.Fatal("expected Exists to be false before saving")
	}
	m.Save("abc", "https://example.com")
	if !m.Exists("abc") {
		t.Fatal("expected Exists to be true after saving")
	}
}
