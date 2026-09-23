package cache

import (
	"container/list"
	"sync"
	"time"
)

// LRU is a fixed capacity cache with a time to live per entry. Getting an
// entry moves it to the front of the recency list. Setting an entry once
// the cache is full evicts the least recently used one.
type LRU struct {
	mu       sync.Mutex
	capacity int
	ttl      time.Duration
	items    map[string]*list.Element
	order    *list.List
	clock    Clock
}

type lruEntry struct {
	key       string
	value     string
	expiresAt time.Time
}

func NewLRU(capacity int, ttl time.Duration) *LRU {
	return &LRU{
		capacity: capacity,
		ttl:      ttl,
		items:    make(map[string]*list.Element),
		order:    list.New(),
		clock:    realClock,
	}
}

func (c *LRU) Get(key string) (string, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()

	el, ok := c.items[key]
	if !ok {
		return "", false
	}
	entry := el.Value.(*lruEntry)
	if c.clock().After(entry.expiresAt) {
		c.removeElement(el)
		return "", false
	}
	c.order.MoveToFront(el)
	return entry.value, true
}

func (c *LRU) Set(key, value string) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if el, ok := c.items[key]; ok {
		entry := el.Value.(*lruEntry)
		entry.value = value
		entry.expiresAt = c.clock().Add(c.ttl)
		c.order.MoveToFront(el)
		return
	}

	entry := &lruEntry{key: key, value: value, expiresAt: c.clock().Add(c.ttl)}
	el := c.order.PushFront(entry)
	c.items[key] = el

	if c.order.Len() > c.capacity {
		if oldest := c.order.Back(); oldest != nil {
			c.removeElement(oldest)
		}
	}
}

func (c *LRU) removeElement(el *list.Element) {
	c.order.Remove(el)
	entry := el.Value.(*lruEntry)
	delete(c.items, entry.key)
}

func (c *LRU) Len() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.order.Len()
}
