package idgen

import (
	"errors"
	"math/rand"
)

// ErrExhausted is returned once Random has tried MaxAttempts codes in a row
// and every one was already taken.
var ErrExhausted = errors.New("idgen: could not find a free code after max attempts")

// Random generates a fixed length random base62 code and checks it against
// Exists before returning it, retrying on a collision. Codes are not
// guessable in sequence, unlike Counter, at the cost of needing a check
// against existing storage on every single call, and a real, nonzero
// chance of collision that grows as the keyspace fills up, the birthday
// paradox. See the README for a measured collision rate at different
// occupancy levels.
type Random struct {
	Length      int
	MaxAttempts int
	Rand        *rand.Rand
	Exists      func(code string) bool
}

func (r *Random) Generate() (string, error) {
	for attempt := 0; attempt < r.MaxAttempts; attempt++ {
		code := r.randomCode()
		if !r.Exists(code) {
			return code, nil
		}
	}
	return "", ErrExhausted
}

// GenerateCounting is identical to Generate but also reports how many
// attempts it took, including the successful one. It exists purely so
// tests and the demo can measure collision behavior directly instead of
// inferring it.
func (r *Random) GenerateCounting() (code string, attempts int, err error) {
	for attempt := 1; attempt <= r.MaxAttempts; attempt++ {
		c := r.randomCode()
		if !r.Exists(c) {
			return c, attempt, nil
		}
	}
	return "", r.MaxAttempts, ErrExhausted
}

func (r *Random) randomCode() string {
	buf := make([]byte, r.Length)
	for i := range buf {
		buf[i] = base62Chars[r.Rand.Intn(62)]
	}
	return string(buf)
}
