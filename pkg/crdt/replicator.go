package crdt

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"sync"
	"time"
)

// Broadcast is the user-supplied fan-out function. Implementations MUST
// deliver the payload to all peers except the sender (callers filter echoes
// if needed, though Deliver also silently drops payloads whose Origin matches
// the local node ID). Returning an error is logged; Replicator does not retry.
type Broadcast func(payload []byte) error

// message is the wire format for a single CRDT snapshot broadcast.
// It is marshalled as a JSON header followed by a newline and opaque body bytes.
type message struct {
	// Version is always 1 for v1 payloads. Allows forward evolution.
	Version uint8 `json:"version"`
	// Origin is the node ID that produced the payload.
	Origin string `json:"origin"`
	// Key is the CRDT registration key on both sender and receiver.
	Key string `json:"key"`
	// Type is the CRDT type discriminator ("gcounter", "pncounter",
	// "lww[string]", "orset[string]", …). Used to instantiate a scratch
	// replica on Deliver; also useful for diagnostics.
	Type string `json:"type"`
	// Body is the raw output of CRDT.Snapshot(). Treated as opaque bytes
	// by the replicator; each concrete CRDT type knows how to Restore it.
	Body []byte `json:"body"`
}

// marshalMessage serialises msg to wire format: JSON header + "\n" + body.
func marshalMessage(m *message) ([]byte, error) {
	hdr, err := json.Marshal(m)
	if err != nil {
		return nil, fmt.Errorf("crdt/replicator: marshal header: %w", err)
	}
	out := make([]byte, 0, len(hdr)+1)
	out = append(out, hdr...)
	out = append(out, '\n')
	return out, nil
}

// unmarshalMessage parses a wire payload back into a message.
func unmarshalMessage(payload []byte) (*message, error) {
	idx := bytes.IndexByte(payload, '\n')
	if idx < 0 {
		// No newline: treat the whole payload as the JSON header (body in JSON field).
		var m message
		if err := json.Unmarshal(payload, &m); err != nil {
			return nil, fmt.Errorf("crdt/replicator: unmarshal: %w", err)
		}
		return &m, nil
	}
	var m message
	if err := json.Unmarshal(payload[:idx], &m); err != nil {
		return nil, fmt.Errorf("crdt/replicator: unmarshal header: %w", err)
	}
	return &m, nil
}

// typeRegistry maps CRDT type names to constructor functions.  Each
// constructor returns a fresh zero-value CRDT of the matching concrete type.
// The nodeID argument is used only to satisfy constructors that embed one
// (GCounter, PNCounter) — for scratch replicas it is ephemeral.
//
// LWWRegister and ORSet are generic; callers must call RegisterType for their
// concrete instantiation (e.g. "lww[string]", "orset[string]") during init or
// before the first Replicate / Deliver call.
var (
	typeRegistryMu sync.RWMutex
	typeRegistry   = map[string]func(nodeID string) CRDT{
		"gcounter":  func(id string) CRDT { return NewGCounter(NodeID(id)) },
		"pncounter": func(id string) CRDT { return NewPNCounter(NodeID(id)) },
	}
)

// RegisterType registers a constructor for a CRDT Type string. Packages with
// custom CRDTs, or generic instantiations of LWWRegister / ORSet, call this at
// init time to make their type discoverable by the Replicator.
//
// Example:
//
//	func init() {
//	    crdt.RegisterType("lww[string]", func(id string) crdt.CRDT {
//	        return crdt.NewLWWRegister[string](crdt.NodeID(id), &crdt.HybridClock{}, "")
//	    })
//	    crdt.RegisterType("orset[string]", func(id string) crdt.CRDT {
//	        return crdt.NewORSet[string](crdt.NodeID(id))
//	    })
//	}
func RegisterType(name string, ctor func(nodeID string) CRDT) {
	typeRegistryMu.Lock()
	defer typeRegistryMu.Unlock()
	typeRegistry[name] = ctor
}

// lookupType returns a constructor for the given type name, or (nil, false).
func lookupType(name string) (func(nodeID string) CRDT, bool) {
	typeRegistryMu.RLock()
	defer typeRegistryMu.RUnlock()
	ctor, ok := typeRegistry[name]
	return ctor, ok
}

// entry holds a registered CRDT alongside its type name.
type entry struct {
	c       CRDT
	typName string
}

// config holds optional Replicator settings.
type config struct {
	log          *slog.Logger
	syncInterval time.Duration
}

// Option configures a Replicator at construction time.
type Option func(*config)

// WithLogger overrides slog.Default() as the logger for the Replicator.
func WithLogger(l *slog.Logger) Option {
	return func(c *config) { c.log = l }
}

// WithSyncInterval enables periodic anti-entropy: every interval the
// Replicator re-broadcasts every registered CRDT's snapshot. Zero disables
// the periodic sync. Useful for healing after partitions or for late joiners.
func WithSyncInterval(d time.Duration) Option {
	return func(c *config) { c.syncInterval = d }
}

// Replicator propagates CRDT snapshots between replicas. One Replicator is
// created per node; applications Register each CRDT they want replicated
// under a unique key. The Replicator hooks into a broadcast primitive (any
// func([]byte) error works — the cluster transport, memberlist Broadcast, or
// an in-process fanout for tests) and dispatches inbound payloads to the
// matching CRDT's Merge.
//
// Typical usage:
//
//	rep := crdt.New("node-1", myBroadcastFn, crdt.WithSyncInterval(5*time.Second))
//	gc  := crdt.NewGCounter("node-1")
//	rep.Register("hits", gc)
//	rep.Start(ctx)
//
//	// on local mutation:
//	gc.Inc(1)
//	rep.Replicate("hits")
//
//	// wired to transport inbound:
//	rep.Deliver(rawPayload)
type Replicator struct {
	local     string
	bcast     Broadcast
	cfg       config
	mu        sync.RWMutex
	crdts     map[string]*entry
	stopCh    chan struct{}
	once      sync.Once // guards Start
	closeOnce sync.Once
}

// New builds a Replicator. local is the node's own ID (used to tag outgoing
// payloads and to suppress echoes on Deliver). bcast is how outgoing deltas
// leave this node.
func New(local string, bcast Broadcast, opts ...Option) *Replicator {
	cfg := config{
		log: slog.Default(),
	}
	for _, o := range opts {
		o(&cfg)
	}
	return &Replicator{
		local:  local,
		bcast:  bcast,
		cfg:    cfg,
		crdts:  make(map[string]*entry),
		stopCh: make(chan struct{}),
	}
}

// Register adds a CRDT under key and begins replicating it. Panics if key is
// already registered. typName must match a type registered in the type
// registry (e.g. "gcounter", "pncounter", "lww[string]", "orset[string]").
// The CRDT must be concurrent-safe; all pkg/crdt types satisfy this.
func (r *Replicator) Register(key string, c CRDT, typName string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.crdts[key]; exists {
		panic(fmt.Sprintf("crdt/replicator: key %q already registered", key))
	}
	r.crdts[key] = &entry{c: c, typName: typName}
}

// Unregister removes key and stops replicating it. Safe to call concurrently
// with Replicate and Deliver.
func (r *Replicator) Unregister(key string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.crdts, key)
}

// Replicate broadcasts the current snapshot of the CRDT registered under key.
// Call this after a local mutation (Inc, Set, Add, Remove) to propagate the
// change. Returns an error if the key is unknown or the broadcast function
// fails.
func (r *Replicator) Replicate(key string) error {
	r.mu.RLock()
	e, ok := r.crdts[key]
	r.mu.RUnlock()
	if !ok {
		return fmt.Errorf("crdt/replicator: unknown key %q", key)
	}

	body, err := e.c.Snapshot()
	if err != nil {
		return fmt.Errorf("crdt/replicator: snapshot %q: %w", key, err)
	}

	payload, err := marshalMessage(&message{
		Version: 1,
		Origin:  r.local,
		Key:     key,
		Type:    e.typName,
		Body:    body,
	})
	if err != nil {
		return err
	}

	if err := r.bcast(payload); err != nil {
		r.cfg.log.Warn("crdt/replicator: broadcast error",
			"key", key,
			"err", err,
		)
		return fmt.Errorf("crdt/replicator: broadcast %q: %w", key, err)
	}
	return nil
}

// Deliver absorbs an incoming payload from a peer. Call this from the handler
// wired to your transport / gossip layer. Payloads whose Origin equals the
// local node ID are dropped silently so echo-safe transports work without
// special handling. Payloads for unknown keys are logged at warn and dropped.
func (r *Replicator) Deliver(payload []byte) error {
	m, err := unmarshalMessage(payload)
	if err != nil {
		return err
	}

	// Suppress echoes.
	if m.Origin == r.local {
		return nil
	}

	r.mu.RLock()
	e, ok := r.crdts[m.Key]
	r.mu.RUnlock()
	if !ok {
		r.cfg.log.Warn("crdt/replicator: deliver for unknown key, dropping",
			"key", m.Key,
			"origin", m.Origin,
			"type", m.Type,
		)
		return nil
	}

	// Build a scratch CRDT of the same concrete type, restore the body into
	// it, then merge into the registered instance.
	ctor, found := lookupType(m.Type)
	if !found {
		return fmt.Errorf("crdt/replicator: unknown type %q for key %q — register via RegisterType", m.Type, m.Key)
	}
	scratch := ctor(m.Origin)
	if err := scratch.Restore(m.Body); err != nil {
		return fmt.Errorf("crdt/replicator: restore %q (type %q): %w", m.Key, m.Type, err)
	}
	if err := e.c.Merge(scratch); err != nil {
		return fmt.Errorf("crdt/replicator: merge %q: %w", m.Key, err)
	}
	return nil
}

// Start launches the optional periodic anti-entropy goroutine. Idempotent. If
// WithSyncInterval was not set Start is a no-op.
func (r *Replicator) Start(ctx context.Context) {
	if r.cfg.syncInterval <= 0 {
		return
	}
	r.once.Do(func() {
		go r.syncLoop(ctx)
	})
}

// Close stops the sync goroutine and clears all registrations. Idempotent.
func (r *Replicator) Close() error {
	r.closeOnce.Do(func() {
		close(r.stopCh)
		r.mu.Lock()
		r.crdts = make(map[string]*entry)
		r.mu.Unlock()
	})
	return nil
}

// syncLoop is the anti-entropy goroutine. It re-broadcasts every registered
// CRDT's snapshot on every tick so late joiners and post-partition nodes
// converge without needing a dedicated re-sync protocol.
func (r *Replicator) syncLoop(ctx context.Context) {
	ticker := time.NewTicker(r.cfg.syncInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			r.broadcastAll()
		case <-ctx.Done():
			return
		case <-r.stopCh:
			return
		}
	}
}

// broadcastAll replicates every currently registered CRDT. Errors are logged
// but do not stop the loop.
func (r *Replicator) broadcastAll() {
	r.mu.RLock()
	keys := make([]string, 0, len(r.crdts))
	for k := range r.crdts {
		keys = append(keys, k)
	}
	r.mu.RUnlock()

	for _, k := range keys {
		if err := r.Replicate(k); err != nil {
			r.cfg.log.Warn("crdt/replicator: sync broadcast error",
				"key", k,
				"err", err,
			)
		}
	}
}
