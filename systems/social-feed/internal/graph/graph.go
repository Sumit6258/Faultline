// Package graph is the follow relationship between users: who follows whom.
// It is stored both ways, followers of a user and users a user follows,
// because the two timeline strategies in internal/timeline need opposite
// lookups: fan-out on write needs a post author's followers, fan-out on
// read needs a reader's followees.
package graph

import "sync"

type Graph struct {
	mu        sync.RWMutex
	followers map[string]map[string]struct{} // followee -> set of followers
	following map[string]map[string]struct{} // follower -> set of followees
}

func New() *Graph {
	return &Graph{
		followers: make(map[string]map[string]struct{}),
		following: make(map[string]map[string]struct{}),
	}
}

func (g *Graph) Follow(follower, followee string) {
	if follower == followee {
		return
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.followers[followee] == nil {
		g.followers[followee] = make(map[string]struct{})
	}
	if g.following[follower] == nil {
		g.following[follower] = make(map[string]struct{})
	}
	g.followers[followee][follower] = struct{}{}
	g.following[follower][followee] = struct{}{}
}

func (g *Graph) Unfollow(follower, followee string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	delete(g.followers[followee], follower)
	delete(g.following[follower], followee)
}

func (g *Graph) Followers(user string) []string {
	g.mu.RLock()
	defer g.mu.RUnlock()
	out := make([]string, 0, len(g.followers[user]))
	for f := range g.followers[user] {
		out = append(out, f)
	}
	return out
}

func (g *Graph) Following(user string) []string {
	g.mu.RLock()
	defer g.mu.RUnlock()
	out := make([]string, 0, len(g.following[user]))
	for f := range g.following[user] {
		out = append(out, f)
	}
	return out
}

func (g *Graph) FollowerCount(user string) int {
	g.mu.RLock()
	defer g.mu.RUnlock()
	return len(g.followers[user])
}
