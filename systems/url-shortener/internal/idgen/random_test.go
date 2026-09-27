package idgen

import (
	"math/rand"
	"testing"
)

func TestRandom_GeneratesCodeOfConfiguredLength(t *testing.T) {
	r := &Random{Length: 6, MaxAttempts: 10, Rand: rand.New(rand.NewSource(1)), Exists: func(string) bool { return false }}
	code, err := r.Generate()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(code) != 6 {
		t.Fatalf("expected a 6 character code, got %q (%d chars)", code, len(code))
	}
}

func TestRandom_RetriesOnCollision(t *testing.T) {
	calls := 0
	r := &Random{
		Length:      4,
		MaxAttempts: 10,
		Rand:        rand.New(rand.NewSource(1)),
		Exists: func(string) bool {
			calls++
			return calls < 3 // first 2 calls collide, the 3rd is free
		},
	}
	_, attempts, err := r.GenerateCounting()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if attempts != 3 {
		t.Fatalf("expected exactly 3 attempts, 2 collisions then success, got %d", attempts)
	}
}

func TestRandom_ExhaustsAfterMaxAttempts(t *testing.T) {
	r := &Random{
		Length:      4,
		MaxAttempts: 5,
		Rand:        rand.New(rand.NewSource(1)),
		Exists:      func(string) bool { return true }, // everything always collides
	}
	_, err := r.Generate()
	if err != ErrExhausted {
		t.Fatalf("expected ErrExhausted, got %v", err)
	}
}

func TestRandom_CollisionRateRisesAsSpaceFillsUp(t *testing.T) {
	// A 2 character base62 code has only 62*62 = 3844 possible values.
	// Filling more than half of them should make collisions common, the
	// birthday paradox, not a rare edge case.
	existing := make(map[string]bool)
	r := &Random{
		Length:      2,
		MaxAttempts: 100000,
		Rand:        rand.New(rand.NewSource(1)),
		Exists:      func(c string) bool { return existing[c] },
	}

	totalAttempts := 0
	const n = 3000 // more than 3844/2, past the point collisions should be common
	for i := 0; i < n; i++ {
		code, attempts, err := r.GenerateCounting()
		if err != nil {
			t.Fatalf("generation %d: unexpected error: %v", i, err)
		}
		existing[code] = true
		totalAttempts += attempts
	}

	avgAttempts := float64(totalAttempts) / float64(n)
	if avgAttempts < 1.5 {
		t.Fatalf("expected the average attempts per code to rise well above 1 as the space fills up, got %.2f average over %d codes in a 3844 code space", avgAttempts, n)
	}
	t.Logf("average attempts per successful generation over %d codes in a 3844 code space: %.2f", n, avgAttempts)
}
