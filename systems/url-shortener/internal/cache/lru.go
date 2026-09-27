package cache

import (
	"container/list"
	"sync"
)

// LRU is a fixed capacity, in-memory cache in front of the real store. It
// exists because most redirect traffic in a URL shortener concentrates on
// a small number of hot codes, see the README's Benchmark section for a
// measured hit ratio under that kind of access pattern, and there is no
// reason every one of those redirects should pay for a store lookup.
type LRU struct {
	mu       sync.Mutex
	capacity int
	items    map[string]*list.Element
	order    *list.List
}

type entry struct {
	key   string
	value string
}

func NewLRU(capacity int) *LRU {
	return &LRU{
		capacity: capacity,
		items:    make(map[string]*list.Element),
		order:    list.New(),
	}
}

func (c *LRU) Get(key string) (string, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()

	el, ok := c.items[key]
	if !ok {
		return "", false
	}
	c.order.MoveToFront(el)
	return el.Value.(*entry).value, true
}

func (c *LRU) Set(key, value string) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if el, ok := c.items[key]; ok {
		el.Value.(*entry).value = value
		c.order.MoveToFront(el)
		return
	}

	el := c.order.PushFront(&entry{key: key, value: value})
	c.items[key] = el

	if c.order.Len() > c.capacity {
		oldest := c.order.Back()
		if oldest != nil {
			c.order.Remove(oldest)
			delete(c.items, oldest.Value.(*entry).key)
		}
	}
}

func (c *LRU) Len() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.order.Len()
}
