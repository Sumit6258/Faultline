package store

import (
	"errors"
	"sync"
)

// ErrCodeTaken is returned by Save when code already maps to a URL.
var ErrCodeTaken = errors.New("store: code already taken")

// Memory is an in-process, in-memory Store. Nothing here survives a
// restart, and nothing here is shared across more than one process. It is
// the V0, naive implementation this system starts from. See the README's
// Scaling section for what a persistent, shared store would need to add.
type Memory struct {
	mu   sync.RWMutex
	data map[string]string
}

func NewMemory() *Memory {
	return &Memory{data: make(map[string]string)}
}

func (m *Memory) Save(code, longURL string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, exists := m.data[code]; exists {
		return ErrCodeTaken
	}
	m.data[code] = longURL
	return nil
}

func (m *Memory) Load(code string) (string, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	v, ok := m.data[code]
	return v, ok
}

func (m *Memory) Exists(code string) bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	_, ok := m.data[code]
	return ok
}
