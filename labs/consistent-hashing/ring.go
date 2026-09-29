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
	mu       sync.RWMutex
	replicas int
	hashFn   func(string) uint32
	points   []point // kept sorted by hash, then by node name
	nodes    map[string]bool
}

// point is one virtual node: a position on the ring and the real node that
// owns it. Two different nodes can land on the same position, a 32 bit hash
// collides eventually, so points are ordered by (hash, node) and are always
// removed by their owner, never looked up through the position alone. An
// earlier version kept a map from position to node, which silently lost
// track of the second owner on a collision and returned an empty node
// after a removal, see TestRing_HashCollisionsBetweenNodesDoNotCorruptTheRing.
type point struct {
	hash uint32
	node string
}

func NewRing(replicas int) *Ring {
	return &Ring{
		replicas: replicas,
		nodes:    make(map[string]bool),
	}
}

// hash uses the injected function when a test provides one, and calls crc32
// directly otherwise. Going through a stored function value on every call
// stops the compiler keeping the key's byte conversion on the stack, which
// cost an extra allocation per Get.
func (r *Ring) hash(s string) uint32 {
	if r.hashFn != nil {
		return r.hashFn(s)
	}
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
		r.points = append(r.points, point{hash: r.hash(node + "#" + strconv.Itoa(i)), node: node})
	}
	sort.Slice(r.points, func(i, j int) bool {
		if r.points[i].hash != r.points[j].hash {
			return r.points[i].hash < r.points[j].hash
		}
		return r.points[i].node < r.points[j].node
	})
}

func (r *Ring) RemoveNode(node string) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if !r.nodes[node] {
		return
	}
	delete(r.nodes, node)

	kept := make([]point, 0, len(r.points))
	for _, p := range r.points {
		if p.node != node {
			kept = append(kept, p)
		}
	}
	r.points = kept
}

// Get returns the node responsible for key: the first node clockwise from
// key's position on the ring.
func (r *Ring) Get(key string) string {
	r.mu.RLock()
	defer r.mu.RUnlock()

	if len(r.points) == 0 {
		return ""
	}

	h := r.hash(key)
	idx := sort.Search(len(r.points), func(i int) bool { return r.points[i].hash >= h })
	if idx == len(r.points) {
		idx = 0 // wrap around the ring
	}
	return r.points[idx].node
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
