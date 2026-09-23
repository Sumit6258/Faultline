package consistenthash

import (
	"hash/crc32"
	"sort"
)

// NaiveModN picks a node for a key with hash(key) % len(nodes). It's the
// obvious thing to reach for, and it distributes keys evenly as long as the
// node list never changes. The moment a node is added or removed, len(nodes)
// changes and almost every key maps to a different node than before, even
// though most nodes are still up and holding data nobody will ask them for
// anymore. Ring exists to avoid exactly this. See the README for measured
// numbers.
type NaiveModN struct {
	nodes []string
}

func NewNaiveModN(nodes ...string) *NaiveModN {
	sorted := append([]string(nil), nodes...)
	sort.Strings(sorted)
	return &NaiveModN{nodes: sorted}
}

func (n *NaiveModN) Get(key string) string {
	if len(n.nodes) == 0 {
		return ""
	}
	h := crc32.ChecksumIEEE([]byte(key))
	return n.nodes[int(h)%len(n.nodes)]
}

func (n *NaiveModN) AddNode(node string) {
	n.nodes = append(n.nodes, node)
	sort.Strings(n.nodes)
}

func (n *NaiveModN) RemoveNode(node string) {
	out := n.nodes[:0]
	for _, x := range n.nodes {
		if x != node {
			out = append(out, x)
		}
	}
	n.nodes = out
}
