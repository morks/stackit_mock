// Package faults implements optional error injection.
package faults

import (
	"strings"
	"sync"
)

type Rule struct {
	ID         int    `json:"id"`
	Method     string `json:"method,omitempty"`      // empty = any
	PathPrefix string `json:"path_prefix,omitempty"` // empty = any
	Status     int    `json:"status"`
	Message    string `json:"message,omitempty"`
	Remaining  int    `json:"remaining"` // -1 = unlimited
}

type Set struct {
	mu    sync.Mutex
	next  int
	rules []Rule
}

func (s *Set) Add(r Rule) Rule {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.next++
	r.ID = s.next
	if r.Remaining == 0 {
		r.Remaining = -1
	}
	if r.Status == 0 {
		r.Status = 500
	}
	s.rules = append(s.rules, r)
	return r
}

func (s *Set) List() []Rule {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]Rule{}, s.rules...)
}

func (s *Set) Clear() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.rules = nil
}

func (s *Set) Remove(id int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i, r := range s.rules {
		if r.ID == id {
			s.rules = append(s.rules[:i], s.rules[i+1:]...)
			return
		}
	}
}

// Match returns the first matching rule and consumes one use.
func (s *Set) Match(method, path string) (Rule, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i, r := range s.rules {
		if (r.Method != "" && !strings.EqualFold(r.Method, method)) || !strings.HasPrefix(path, r.PathPrefix) {
			continue
		}
		if r.Remaining > 0 {
			s.rules[i].Remaining--
			if s.rules[i].Remaining == 0 {
				s.rules = append(s.rules[:i], s.rules[i+1:]...)
			}
		}
		return r, true
	}
	return Rule{}, false
}
