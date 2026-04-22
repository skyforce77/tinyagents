// Package memberlist implements cluster.Cluster on top of
// hashicorp/memberlist: UDP-based SWIM for failure detection and
// eventually-consistent membership gossip.
//
// Use cluster/local for tests and single-node runs; use this package
// once more than one node is involved.
package memberlist

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net"
	"strconv"
	"sync"
	"time"

	ml "github.com/hashicorp/memberlist"

	"github.com/skyforce77/tinyagents/pkg/cluster"
)

// config holds the settings applied by Option functions.
type config struct {
	meta   map[string]string
	log    *slog.Logger
	useWAN bool
}

// Option configures a Cluster at construction time.
type Option func(*config)

// WithMeta gossips the given key/value pairs as Member.Meta.
func WithMeta(meta map[string]string) Option {
	return func(c *config) {
		cloned := make(map[string]string, len(meta))
		for k, v := range meta {
			cloned[k] = v
		}
		c.meta = cloned
	}
}

// WithLogger overrides slog.Default().
func WithLogger(l *slog.Logger) Option {
	return func(c *config) {
		c.log = l
	}
}

// WithWANProfile switches to DefaultWANConfig (larger timeouts, suitable
// for multi-datacenter deployments). Default is DefaultLANConfig.
func WithWANProfile() Option {
	return func(c *config) {
		c.useWAN = true
	}
}

// WithLANProfile selects DefaultLANConfig (default).
func WithLANProfile() Option {
	return func(c *config) {
		c.useWAN = false
	}
}

// subscriber holds the per-subscriber delivery channel and its cancel func.
type subscriber struct {
	ch     chan cluster.Event
	cancel context.CancelFunc
}

// eventDelegate implements ml.EventDelegate to translate memberlist events
// into cluster.Event values and fan them out to all subscribers.
type eventDelegate struct {
	c *Cluster
}

// NotifyJoin is called when a node joins the cluster.
func (d *eventDelegate) NotifyJoin(node *ml.Node) {
	d.c.dispatch(cluster.Event{
		Kind:   cluster.MemberJoined,
		Member: nodeToMember(node, d.c.log),
	})
}

// NotifyLeave is called when a node leaves or is suspected dead.
func (d *eventDelegate) NotifyLeave(node *ml.Node) {
	d.c.dispatch(cluster.Event{
		Kind:   cluster.MemberLeft,
		Member: nodeToMember(node, d.c.log),
	})
}

// NotifyUpdate is called when a node's metadata changes.
func (d *eventDelegate) NotifyUpdate(node *ml.Node) {
	d.c.dispatch(cluster.Event{
		Kind:   cluster.MemberUpdated,
		Member: nodeToMember(node, d.c.log),
	})
}

// delegate implements ml.Delegate to expose local node metadata.
type delegate struct {
	mu   sync.RWMutex
	meta map[string]string
}

// NodeMeta returns the JSON-serialized meta map, clipped to limit bytes.
func (d *delegate) NodeMeta(limit int) []byte {
	d.mu.RLock()
	m := d.meta
	d.mu.RUnlock()

	if len(m) == 0 {
		return []byte("{}")
	}
	b, err := json.Marshal(m)
	if err != nil {
		return []byte("{}")
	}
	if len(b) > limit {
		// Clip: return empty map rather than silently corrupt data.
		return []byte("{}")
	}
	return b
}

// NotifyMsg satisfies ml.Delegate but is unused for pure membership gossip.
func (d *delegate) NotifyMsg([]byte) {}

// GetBroadcasts satisfies ml.Delegate; no application-level broadcasts.
func (d *delegate) GetBroadcasts(overhead, limit int) [][]byte { return nil }

// LocalState satisfies ml.Delegate; no state merging.
func (d *delegate) LocalState(join bool) []byte { return nil }

// MergeRemoteState satisfies ml.Delegate; no state merging.
func (d *delegate) MergeRemoteState(buf []byte, join bool) {}

// Cluster wraps *memberlist.Memberlist to satisfy cluster.Cluster.
type Cluster struct {
	mu     sync.Mutex
	list   *ml.Memberlist
	delg   *delegate
	log    *slog.Logger
	subs   map[uint64]*subscriber
	nextID uint64
	closed bool
	left   bool
}

// New builds a Cluster. bind controls the UDP+TCP gossip address (host:port).
// id is this node's stable identifier; if empty, memberlist picks one from
// the local hostname. Pass opts for logger, meta, and LAN vs WAN tuning.
func New(id, bind string, opts ...Option) (*Cluster, error) {
	cfg := &config{
		log: slog.Default(),
	}
	for _, o := range opts {
		o(cfg)
	}

	var mlCfg *ml.Config
	if cfg.useWAN {
		mlCfg = ml.DefaultWANConfig()
	} else {
		mlCfg = ml.DefaultLANConfig()
	}

	if id != "" {
		mlCfg.Name = id
	}

	if bind != "" {
		host, port, err := parseBindAddr(bind)
		if err != nil {
			return nil, fmt.Errorf("cluster/memberlist: invalid bind address %q: %w", bind, err)
		}
		mlCfg.BindAddr = host
		mlCfg.BindPort = port
	}

	// Silence memberlist's own logger — tinyagents uses slog.
	mlCfg.Logger = nil
	mlCfg.LogOutput = noopWriter{}

	delg := &delegate{meta: cfg.meta}

	c := &Cluster{
		delg: delg,
		log:  cfg.log,
		subs: make(map[uint64]*subscriber),
	}

	evDelg := &eventDelegate{c: c}
	mlCfg.Events = evDelg
	mlCfg.Delegate = delg

	list, err := ml.Create(mlCfg)
	if err != nil {
		return nil, fmt.Errorf("cluster/memberlist: create: %w", err)
	}

	c.list = list
	return c, nil
}

// parseBindAddr splits a "host:port" string into host string and int port.
func parseBindAddr(addr string) (string, int, error) {
	host, portStr, err := net.SplitHostPort(addr)
	if err != nil {
		return "", 0, err
	}
	port, err := strconv.Atoi(portStr)
	if err != nil {
		return "", 0, fmt.Errorf("invalid port %q: %w", portStr, err)
	}
	return host, port, nil
}

// noopWriter discards all memberlist internal log output.
type noopWriter struct{}

func (noopWriter) Write(p []byte) (int, error) { return len(p), nil }

// Join adds this node to the cluster by contacting the given seed addresses.
// If no seeds are provided, this bootstraps a single-node cluster (no error).
// Returns error if seeds were provided but zero could be contacted.
func (c *Cluster) Join(_ context.Context, seeds ...string) error {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return fmt.Errorf("cluster/memberlist: closed")
	}
	c.mu.Unlock()

	if len(seeds) == 0 {
		return nil
	}

	n, err := c.list.Join(seeds)
	if err != nil {
		return fmt.Errorf("cluster/memberlist: join: %w", err)
	}
	if n == 0 {
		return fmt.Errorf("cluster/memberlist: join: no seeds reachable (tried %d)", len(seeds))
	}
	return nil
}

// Leave announces this node's departure and blocks until peers acknowledge
// (best-effort) using the context deadline, or 5 seconds as fallback. Then
// shuts down memberlist. Leave is idempotent.
func (c *Cluster) Leave(ctx context.Context) error {
	c.mu.Lock()
	if c.closed || c.left {
		c.mu.Unlock()
		return nil
	}
	c.left = true
	c.mu.Unlock()

	timeout := 5 * time.Second
	if dl, ok := ctx.Deadline(); ok {
		if remaining := time.Until(dl); remaining > 0 && remaining < timeout {
			timeout = remaining
		}
	}

	if err := c.list.Leave(timeout); err != nil {
		c.log.Warn("cluster/memberlist: leave error", "err", err)
	}
	if err := c.list.Shutdown(); err != nil {
		return fmt.Errorf("cluster/memberlist: shutdown: %w", err)
	}
	return nil
}

// Members returns a snapshot of the current membership view, including this
// node. The slice is owned by the caller.
func (c *Cluster) Members() []cluster.Member {
	nodes := c.list.Members()
	out := make([]cluster.Member, 0, len(nodes))
	for _, n := range nodes {
		out = append(out, nodeToMember(n, c.log))
	}
	return out
}

// LocalNode returns the Member describing this node.
func (c *Cluster) LocalNode() cluster.Member {
	return nodeToMember(c.list.LocalNode(), c.log)
}

// Subscribe registers an EventHandler that receives cluster events. Returns
// an unsubscribe function. Multiple subscribers are allowed; events are
// delivered sequentially per subscriber via a buffered channel (capacity 32).
// When the channel is full, the event is dropped and a warning is logged.
func (c *Cluster) Subscribe(h cluster.EventHandler) (unsubscribe func()) {
	ctx, cancel := context.WithCancel(context.Background())

	c.mu.Lock()
	id := c.nextID
	c.nextID++
	sub := &subscriber{ch: make(chan cluster.Event, 32), cancel: cancel}
	c.subs[id] = sub
	c.mu.Unlock()

	go func() {
		for {
			select {
			case ev, ok := <-sub.ch:
				if !ok {
					return
				}
				h(ev)
			case <-ctx.Done():
				return
			}
		}
	}()

	var once sync.Once
	return func() {
		once.Do(func() {
			c.mu.Lock()
			delete(c.subs, id)
			c.mu.Unlock()
			cancel()
		})
	}
}

// Close shuts down the cluster and all subscribers. Subsequent Members()
// returns the last known view. Close is idempotent.
func (c *Cluster) Close() error {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return nil
	}
	c.closed = true
	subs := c.subsSnapshot()
	for _, sub := range subs {
		sub.cancel()
	}
	c.subs = make(map[uint64]*subscriber)
	alreadyLeft := c.left
	c.left = true
	c.mu.Unlock()

	if !alreadyLeft {
		if err := c.list.Leave(500 * time.Millisecond); err != nil {
			c.log.Warn("cluster/memberlist: leave on close error", "err", err)
		}
	}
	if err := c.list.Shutdown(); err != nil {
		return fmt.Errorf("cluster/memberlist: shutdown on close: %w", err)
	}
	return nil
}

// dispatch sends ev to each subscriber's channel. If the channel is full
// the event is dropped and a warning is logged. Called without mu held.
func (c *Cluster) dispatch(ev cluster.Event) {
	c.mu.Lock()
	subs := c.subsSnapshot()
	c.mu.Unlock()

	for _, sub := range subs {
		select {
		case sub.ch <- ev:
		default:
			c.log.Warn("cluster/memberlist: subscriber channel full, event dropped",
				"kind", ev.Kind,
				"member_id", ev.Member.ID,
			)
		}
	}
}

// subsSnapshot returns a copy of the subscriber slice. Must be called with mu held.
func (c *Cluster) subsSnapshot() []*subscriber {
	out := make([]*subscriber, 0, len(c.subs))
	for _, s := range c.subs {
		out = append(out, s)
	}
	return out
}

// nodeToMember converts a *ml.Node into a cluster.Member. Meta bytes are
// decoded as JSON into a map[string]string. If decoding fails, warn and
// return an empty map — the member is never dropped.
func nodeToMember(n *ml.Node, log *slog.Logger) cluster.Member {
	addr := n.FullAddress().Addr
	meta := make(map[string]string)
	if len(n.Meta) > 0 {
		if err := json.Unmarshal(n.Meta, &meta); err != nil {
			log.Warn("cluster/memberlist: failed to decode node meta",
				"node", n.Name,
				"err", err,
			)
			meta = make(map[string]string)
		}
	}
	return cluster.Member{
		ID:      n.Name,
		Address: addr,
		Meta:    meta,
	}
}
