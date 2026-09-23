package consistenthash

import (
	"hash/crc32"
	"sort"
	"strconv"
	"sync"
)

// Ring is a consistent hash ring. Each real node is represented by
// replicas virtual points on the ring, so that removing one node only
// remaps roughly 1/N of the keys instead of reshuffling everything, and so
// that load spreads evenly across nodes even when there aren't many of
// them. See the README for measured numbers.
type Ring struct {
	mu         sync.RWMutex
	replicas   int
	hashes     []uint32 // kept sorted
	hashToNode map[uint32]string
	nodes      map[string]bool
}

func NewRing(replicas int) *Ring {
	return &Ring{
		replicas:   replicas,
		hashToNode: make(map[uint32]string),
		nodes:      make(map[string]bool),
	}
}

func (r *Ring) hashKey(s string) uint32 {
	return crc32.ChecksumIEEE([]byte(s))
}

func (r *Ring) AddNode(node string) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.nodes[node] {
		return
	}
	r.nodes[node] = true

	for i := 0; i < r.replicas; i++ {
		h := r.hashKey(node + "#" + strconv.Itoa(i))
		r.hashToNode[h] = node
		r.hashes = append(r.hashes, h)
	}
	sort.Slice(r.hashes, func(i, j int) bool { return r.hashes[i] < r.hashes[j] })
}

func (r *Ring) RemoveNode(node string) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if !r.nodes[node] {
		return
	}
	delete(r.nodes, node)

	kept := make([]uint32, 0, len(r.hashes))
	for _, h := range r.hashes {
		if r.hashToNode[h] == node {
			delete(r.hashToNode, h)
			continue
		}
		kept = append(kept, h)
	}
	r.hashes = kept
}

// Get returns the node responsible for key: the first node clockwise from
// key's position on the ring.
func (r *Ring) Get(key string) string {
	r.mu.RLock()
	defer r.mu.RUnlock()

	if len(r.hashes) == 0 {
		return ""
	}

	h := r.hashKey(key)
	idx := sort.Search(len(r.hashes), func(i int) bool { return r.hashes[i] >= h })
	if idx == len(r.hashes) {
		idx = 0 // wrap around the ring
	}
	return r.hashToNode[r.hashes[idx]]
}

func (r *Ring) Nodes() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]string, 0, len(r.nodes))
	for n := range r.nodes {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}
