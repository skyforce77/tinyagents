package actor

import "sync"

// Store is a typed string-keyed map exposed to actors through Context.
// Implementations vary: the per-actor store is un-synchronized (only the
// owning goroutine touches it) while the system-wide store is concurrent-safe.
type Store interface {
	Get(key string) (any, bool)
	Set(key string, v any)
	Delete(key string)
	Snapshot() map[string]any
}

// localStore is backed by a plain map; callers must only touch it from the
// owning actor's goroutine. Zero overhead, no locks.
type localStore struct {
	data map[string]any
}

func newLocalStore() *localStore { return &localStore{data: map[string]any{}} }

func (s *localStore) Get(k string) (any, bool) { v, ok := s.data[k]; return v, ok }
func (s *localStore) Set(k string, v any)      { s.data[k] = v }
func (s *localStore) Delete(k string)          { delete(s.data, k) }
func (s *localStore) Snapshot() map[string]any {
	out := make(map[string]any, len(s.data))
	for k, v := range s.data {
		out[k] = v
	}
	return out
}

// globalStore is backed by sync.Map for concurrent reads and writes. Intended
// for small shared facts; for anything requiring consistency across nodes use
// the CRDT package instead.
type globalStore struct{ m sync.Map }

func newGlobalStore() *globalStore { return &globalStore{} }

func (s *globalStore) Get(k string) (any, bool) { return s.m.Load(k) }
func (s *globalStore) Set(k string, v any)      { s.m.Store(k, v) }
func (s *globalStore) Delete(k string)          { s.m.Delete(k) }
func (s *globalStore) Snapshot() map[string]any {
	out := map[string]any{}
	s.m.Range(func(k, v any) bool {
		if ks, ok := k.(string); ok {
			out[ks] = v
		}
		return true
	})
	return out
}
