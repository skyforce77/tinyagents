package crdt

import (
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"sync"
)

// ORSet is an observed-remove set CRDT. Each element is associated with
// a set of unique tags generated at Add time. Remove tombstones all tags
// that were observed at the time of the call. An element is "live" if it
// has at least one tag in adds that is absent from tombs.
//
// Tag format: "<NodeID>:<counter>" — globally unique without coordination.
//
// Merge rule: union the adds and tombs maps per element. An element is
// present in the merged set iff it has at least one surviving (non-
// tombstoned) tag, implementing add-wins semantics: a concurrent Add on
// one node and Remove on another results in the element being present
// after merge.
type ORSet[T comparable] struct {
	mu      sync.Mutex
	Node    NodeID
	adds    map[T]map[string]struct{} // elem → set of live tags
	tombs   map[T]map[string]struct{} // elem → set of removed tags
	nextTag uint64
}

// NewORSet creates an empty ORSet owned by node.
func NewORSet[T comparable](node NodeID) *ORSet[T] {
	return &ORSet[T]{
		Node:  node,
		adds:  make(map[T]map[string]struct{}),
		tombs: make(map[T]map[string]struct{}),
	}
}

// Add inserts v into the set with a fresh tag. If v is already present
// a new tag is added alongside the existing ones; this ensures that a
// concurrent Remove (which only tombstones observed tags) does not win
// over a concurrent Add.
func (s *ORSet[T]) Add(v T) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.nextTag++
	tag := fmt.Sprintf("%s:%d", s.Node, s.nextTag)
	if s.adds[v] == nil {
		s.adds[v] = make(map[string]struct{})
	}
	s.adds[v][tag] = struct{}{}
}

// Remove tombstones all currently observed tags for v. If v is not
// present the call is a no-op. Any tag added by a concurrent Add on a
// remote node that has not yet been merged is unaffected.
func (s *ORSet[T]) Remove(v T) {
	s.mu.Lock()
	defer s.mu.Unlock()
	live := s.adds[v]
	if len(live) == 0 {
		return
	}
	if s.tombs[v] == nil {
		s.tombs[v] = make(map[string]struct{})
	}
	for tag := range live {
		s.tombs[v][tag] = struct{}{}
	}
}

// Contains reports whether v has at least one live (non-tombstoned) tag.
func (s *ORSet[T]) Contains(v T) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.isLive(v)
}

// isLive must be called with s.mu held.
func (s *ORSet[T]) isLive(v T) bool {
	for tag := range s.adds[v] {
		if _, removed := s.tombs[v][tag]; !removed {
			return true
		}
	}
	return false
}

// Values returns a sorted-by-string-representation slice of all live
// elements. The sort is performed on the JSON encoding of each element so
// that the output is deterministic for equality checks in tests.
func (s *ORSet[T]) Values() []T {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []T
	for v := range s.adds {
		if s.isLive(v) {
			out = append(out, v)
		}
	}
	// Stable sort by JSON representation for deterministic output.
	sort.Slice(out, func(i, j int) bool {
		bi, _ := json.Marshal(out[i])
		bj, _ := json.Marshal(out[j])
		return string(bi) < string(bj)
	})
	return out
}

// Merge absorbs the state of another ORSet[T] by taking the union of
// adds and tombs. Merge returns an error if other is nil or not an
// *ORSet[T].
func (s *ORSet[T]) Merge(other CRDT) error {
	if other == nil {
		return errors.New("orset: cannot merge nil CRDT")
	}
	o, ok := other.(*ORSet[T])
	if !ok {
		return errors.New("orset: type mismatch on Merge")
	}

	// Snapshot the incoming state under its lock.
	o.mu.Lock()
	addSnap := make(map[T]map[string]struct{}, len(o.adds))
	tombSnap := make(map[T]map[string]struct{}, len(o.tombs))
	for elem, tags := range o.adds {
		cp := make(map[string]struct{}, len(tags))
		for t := range tags {
			cp[t] = struct{}{}
		}
		addSnap[elem] = cp
	}
	for elem, tags := range o.tombs {
		cp := make(map[string]struct{}, len(tags))
		for t := range tags {
			cp[t] = struct{}{}
		}
		tombSnap[elem] = cp
	}
	o.mu.Unlock()

	s.mu.Lock()
	defer s.mu.Unlock()

	for elem, tags := range addSnap {
		if s.adds[elem] == nil {
			s.adds[elem] = make(map[string]struct{})
		}
		for t := range tags {
			s.adds[elem][t] = struct{}{}
		}
	}
	for elem, tags := range tombSnap {
		if s.tombs[elem] == nil {
			s.tombs[elem] = make(map[string]struct{})
		}
		for t := range tags {
			s.tombs[elem][t] = struct{}{}
		}
	}
	return nil
}

// orsetWire is the JSON envelope for Snapshot/Restore.
// Keys in the outer maps are JSON-encoded element values so that any
// comparable T (including structs) round-trips correctly.
type orsetWire struct {
	Node  NodeID              `json:"node"`
	Adds  map[string][]string `json:"adds"`
	Tombs map[string][]string `json:"tombs"`
	Next  uint64              `json:"next"`
}

// Snapshot serializes the full set state to JSON.
func (s *ORSet[T]) Snapshot() ([]byte, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	adds := make(map[string][]string, len(s.adds))
	for elem, tags := range s.adds {
		key, err := json.Marshal(elem)
		if err != nil {
			return nil, err
		}
		tagSlice := make([]string, 0, len(tags))
		for t := range tags {
			tagSlice = append(tagSlice, t)
		}
		sort.Strings(tagSlice)
		adds[string(key)] = tagSlice
	}

	tombs := make(map[string][]string, len(s.tombs))
	for elem, tags := range s.tombs {
		key, err := json.Marshal(elem)
		if err != nil {
			return nil, err
		}
		tagSlice := make([]string, 0, len(tags))
		for t := range tags {
			tagSlice = append(tagSlice, t)
		}
		sort.Strings(tagSlice)
		tombs[string(key)] = tagSlice
	}

	return json.Marshal(orsetWire{
		Node:  s.Node,
		Adds:  adds,
		Tombs: tombs,
		Next:  s.nextTag,
	})
}

// Restore replaces the set state with the output of a prior Snapshot.
func (s *ORSet[T]) Restore(data []byte) error {
	var w orsetWire
	if err := json.Unmarshal(data, &w); err != nil {
		return err
	}

	adds := make(map[T]map[string]struct{}, len(w.Adds))
	for keyStr, tags := range w.Adds {
		var elem T
		if err := json.Unmarshal([]byte(keyStr), &elem); err != nil {
			return err
		}
		m := make(map[string]struct{}, len(tags))
		for _, t := range tags {
			m[t] = struct{}{}
		}
		adds[elem] = m
	}

	tombs := make(map[T]map[string]struct{}, len(w.Tombs))
	for keyStr, tags := range w.Tombs {
		var elem T
		if err := json.Unmarshal([]byte(keyStr), &elem); err != nil {
			return err
		}
		m := make(map[string]struct{}, len(tags))
		for _, t := range tags {
			m[t] = struct{}{}
		}
		tombs[elem] = m
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	s.Node = w.Node
	s.adds = adds
	s.tombs = tombs
	s.nextTag = w.Next
	return nil
}
