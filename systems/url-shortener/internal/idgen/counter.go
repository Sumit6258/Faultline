package idgen

import "sync/atomic"

// Counter generates codes by incrementing a shared counter and encoding it
// in base62. Two calls never produce the same code, there is no collision
// to handle, at the cost of codes being sequential and therefore
// guessable: anyone can enumerate every code ever issued just by counting
// up from 1. See Random for the alternative and the README for the
// measured trade-off.
type Counter struct {
	n atomic.Int64
}

func (c *Counter) Generate() (string, error) {
	n := c.n.Add(1)
	return encodeBase62(n), nil
}
