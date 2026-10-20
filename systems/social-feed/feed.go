// Package socialfeed wires the follow graph, the post store, and one
// timeline strategy into the four operations a feed needs: follow, post,
// read a timeline, and report how much fan-out work has been done.
package socialfeed

import (
	"fmt"

	"github.com/Sumit6258/Faultline/systems/social-feed/internal/graph"
	"github.com/Sumit6258/Faultline/systems/social-feed/internal/post"
	"github.com/Sumit6258/Faultline/systems/social-feed/internal/timeline"
)

type StrategyKind string

const (
	FanoutOnWrite StrategyKind = "write"
	FanoutOnRead  StrategyKind = "read"
	Hybrid        StrategyKind = "hybrid"
)

type Config struct {
	Strategy           StrategyKind
	MaxInbox           int // bound on each precomputed inbox
	CelebrityThreshold int // hybrid only: followers at which an author stops being fanned out
}

type Service struct {
	graph    *graph.Graph
	posts    *post.Store
	strategy timeline.Strategy
	kind     StrategyKind
}

func New(cfg Config) (*Service, error) {
	g := graph.New()
	posts := post.NewStore()
	var s timeline.Strategy
	switch cfg.Strategy {
	case FanoutOnWrite:
		s = timeline.NewFanoutOnWrite(g, cfg.MaxInbox)
	case FanoutOnRead:
		s = timeline.NewFanoutOnRead(g, posts)
	case Hybrid:
		s = timeline.NewHybrid(g, posts, cfg.MaxInbox, cfg.CelebrityThreshold)
	default:
		return nil, fmt.Errorf("socialfeed: unknown strategy %q, want write, read, or hybrid", cfg.Strategy)
	}
	return &Service{graph: g, posts: posts, strategy: s, kind: cfg.Strategy}, nil
}

func (s *Service) Follow(follower, followee string) { s.graph.Follow(follower, followee) }

func (s *Service) Unfollow(follower, followee string) { s.graph.Unfollow(follower, followee) }

// Post stores a post and then lets the strategy deliver it. The order
// matters: the post must be in the store before OnPost runs, because
// fan-out on read and the hybrid's celebrity path read it back from there.
func (s *Service) Post(author, body string) *post.Post {
	p := s.posts.Create(author, body)
	s.strategy.OnPost(p)
	return p
}

func (s *Service) Timeline(user string, limit int) []*post.Post {
	return s.strategy.GetTimeline(user, limit)
}

type Stats struct {
	Strategy    StrategyKind   `json:"strategy"`
	InboxWrites int64          `json:"inbox_writes"`
	FollowersOf map[string]int `json:"followers_of,omitempty"`
}

func (s *Service) Stats() Stats {
	return Stats{Strategy: s.kind, InboxWrites: s.strategy.InboxWrites()}
}
