// Package store provides a generic, thread-safe in-memory resource store
// with an event log and optional JSON snapshots.
package store

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"
)

var ErrExists = errors.New("resource already exists")

// Resource is one mocked cloud object. Data holds the service specific JSON body.
type Resource struct {
	Type    string         `json:"type"`    // e.g. "iaas/network"
	Service string         `json:"service"` // e.g. "iaas"
	ID      string         `json:"id"`
	Project string         `json:"project"`
	Region  string         `json:"region,omitempty"`
	Parent  string         `json:"parent,omitempty"`
	Name    string         `json:"name"`
	Status  string         `json:"status"`
	Data    map[string]any `json:"data,omitempty"`
	Created time.Time      `json:"created"`
	Updated time.Time      `json:"updated"`

	// Pending async transition (persisted so snapshots can resume it).
	Next       string `json:"next,omitempty"`        // target status
	NextDelete bool   `json:"next_delete,omitempty"` // remove after delay instead
}

type Event struct {
	Seq     uint64    `json:"seq"`
	Time    time.Time `json:"time"`
	Action  string    `json:"action"` // created | updated | deleted
	Type    string    `json:"type"`
	ID      string    `json:"id"`
	Project string    `json:"project"`
	Name    string    `json:"name"`
	Status  string    `json:"status"`
}

type Filter struct {
	Type, Service, Project, Parent string
}

type Store struct {
	mu     sync.RWMutex
	items  map[string]Resource
	events []Event
	seq    uint64
	dirty  bool
}

const maxEvents = 2000

func New() *Store { return &Store{items: map[string]Resource{}} }

func key(typ, project, id string) string { return typ + "\x00" + project + "\x00" + id }

func (s *Store) emit(action string, r Resource) {
	s.seq++
	s.events = append(s.events, Event{Seq: s.seq, Time: time.Now(), Action: action,
		Type: r.Type, ID: r.ID, Project: r.Project, Name: r.Name, Status: r.Status})
	if len(s.events) > maxEvents {
		s.events = append([]Event(nil), s.events[len(s.events)-maxEvents:]...)
	}
	s.dirty = true
}

func (s *Store) Create(r Resource) (Resource, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	k := key(r.Type, r.Project, r.ID)
	if _, ok := s.items[k]; ok {
		return Resource{}, ErrExists
	}
	now := time.Now().UTC()
	r.Created, r.Updated = now, now
	if r.Data == nil {
		r.Data = map[string]any{}
	}
	s.items[k] = r
	s.emit("created", r)
	return r, nil
}

func (s *Store) Get(typ, project, id string) (Resource, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	r, ok := s.items[key(typ, project, id)]
	return r, ok
}

// Update applies fn to a copy and stores it. Returns false if missing.
func (s *Store) Update(typ, project, id string, fn func(*Resource)) (Resource, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	k := key(typ, project, id)
	r, ok := s.items[k]
	if !ok {
		return Resource{}, false
	}
	fn(&r)
	r.Updated = time.Now().UTC()
	s.items[k] = r
	s.emit("updated", r)
	return r, true
}

func (s *Store) Delete(typ, project, id string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	k := key(typ, project, id)
	r, ok := s.items[k]
	if !ok {
		return false
	}
	delete(s.items, k)
	s.emit("deleted", r)
	return true
}

func (s *Store) List(f Filter) []Resource {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := []Resource{}
	for _, r := range s.items {
		if (f.Type != "" && r.Type != f.Type) || (f.Service != "" && r.Service != f.Service) ||
			(f.Project != "" && r.Project != f.Project) || (f.Parent != "" && r.Parent != f.Parent) {
			continue
		}
		out = append(out, r)
	}
	sort.Slice(out, func(i, j int) bool {
		if !out[i].Created.Equal(out[j].Created) {
			return out[i].Created.Before(out[j].Created)
		}
		return out[i].ID < out[j].ID
	})
	return out
}

// Events returns events with Seq > since, plus the latest sequence number.
func (s *Store) Events(since uint64) ([]Event, uint64) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := []Event{}
	for _, e := range s.events {
		if e.Seq > since {
			out = append(out, e)
		}
	}
	return out, s.seq
}

func (s *Store) Reset() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.items = map[string]Resource{}
	s.seq++
	s.events = append(s.events, Event{Seq: s.seq, Time: time.Now(), Action: "reset"})
	s.dirty = true
}

type snapshot struct {
	Resources []Resource `json:"resources"`
}

// Save writes a snapshot if state changed since the last save.
func (s *Store) Save(path string) error {
	s.mu.Lock()
	if !s.dirty {
		s.mu.Unlock()
		return nil
	}
	snap := snapshot{Resources: make([]Resource, 0, len(s.items))}
	for _, r := range s.items {
		snap.Resources = append(snap.Resources, r)
	}
	s.dirty = false
	s.mu.Unlock()

	b, err := json.MarshalIndent(snap, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// Load restores a snapshot; a missing file is not an error.
func (s *Store) Load(path string) error {
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	var snap snapshot
	if err := json.Unmarshal(b, &snap); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, r := range snap.Resources {
		s.items[key(r.Type, r.Project, r.ID)] = r
	}
	return nil
}
