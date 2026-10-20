// Package post stores posts, and looks them up by author, newest first.
package post

import (
	"strconv"
	"sync"
	"time"
)

type Post struct {
	ID        string
	Seq       int64 // creation order, breaks ties between posts with equal timestamps
	Author    string
	Body      string
	CreatedAt time.Time
}

type Store struct {
	mu       sync.RWMutex
	byID     map[string]*Post
	byAuthor map[string][]*Post // each author's posts, newest first
	seq      int64
	clock    func() time.Time
}

func NewStore() *Store {
	return &Store{
		byID:     make(map[string]*Post),
		byAuthor: make(map[string][]*Post),
		clock:    time.Now,
	}
}

// SetClock overrides the store's time source, so tests and demos can give
// posts distinct, controlled timestamps instead of racing the real clock.
func (s *Store) SetClock(c func() time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.clock = c
}

func (s *Store) Create(author, body string) *Post {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.seq++
	p := &Post{ID: strconv.FormatInt(s.seq, 10), Seq: s.seq, Author: author, Body: body, CreatedAt: s.clock()}
	s.byID[p.ID] = p
	s.byAuthor[author] = append([]*Post{p}, s.byAuthor[author]...)
	return p
}

func (s *Store) Get(id string) (*Post, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	p, ok := s.byID[id]
	return p, ok
}

// ByAuthor returns up to limit of author's most recent posts, newest first.
func (s *Store) ByAuthor(author string, limit int) []*Post {
	s.mu.RLock()
	defer s.mu.RUnlock()
	posts := s.byAuthor[author]
	if len(posts) > limit {
		posts = posts[:limit]
	}
	out := make([]*Post, len(posts))
	copy(out, posts)
	return out
}
