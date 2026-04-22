package cluster

import (
	"context"
	"errors"
	"log/slog"
	"sync"
)

// errClosed is returned when an operation is attempted on a closed cluster.
var errClosed = errors.New("cluster: closed")

// Option configures a local Cluster.
type Option func(*localCluster)

// WithAddress sets the Member.Address field for the local node.
// Useful when tests need a non-empty address string.
func WithAddress(addr string) Option {
	return func(c *localCluster) {
		c.self.Address = addr
	}
}

// WithMeta sets the Member.Meta field. The map is cloned; the caller
// may mutate the original without affecting the cluster.
func WithMeta(meta map[string]string) Option {
	return func(c *localCluster) {
		cloned := make(map[string]string, len(meta))
		for k, v := range meta {
			cloned[k] = v
		}
		c.self.Meta = cloned
	}
}

// WithLogger replaces the default slog.Default() logger used when
// event delivery drops an event because the subscriber's buffer is full.
func WithLogger(l *slog.Logger) Option {
	return func(c *localCluster) {
		c.log = l
	}
}

// subscriber holds the per-subscriber delivery channel and its cancel func.
type subscriber struct {
	ch     chan Event
	cancel context.CancelFunc
}

// localCluster is a single-process Cluster implementation. The only member
// is the local node itself. It is safe for concurrent use.
type localCluster struct {
	mu          sync.Mutex
	self        Member
	joined      bool
	closed      bool
	subs        map[uint64]*subscriber
	nextID      uint64
	log         *slog.Logger
}

// New returns a local Cluster whose only member is the node identified by id.
// Options are applied before the cluster becomes usable; call Join to make
// the node appear in Members() and fire the first subscription event.
func New(id string, opts ...Option) Cluster {
	c := &localCluster{
		self: Member{ID: id},
		subs: make(map[uint64]*subscriber),
		log:  slog.Default(),
	}
	for _, o := range opts {
		o(c)
	}
	return c
}

// Join marks this node as a member and fires MemberJoined to all current
// subscribers. Subsequent calls while already joined are no-ops.
func (c *localCluster) Join(_ context.Context, _ ...string) error {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return errClosed
	}
	if c.joined {
		c.mu.Unlock()
		return nil
	}
	c.joined = true
	ev := Event{Kind: MemberJoined, Member: c.memberSnapshot()}
	subs := c.subsSnapshot()
	c.mu.Unlock()

	c.dispatch(subs, ev)
	return nil
}

// Leave marks this node as no longer a member and fires MemberLeft to all
// current subscribers. Leave is safe to call before Join (no-op) and multiple
// times; only the first call while joined fires the event.
func (c *localCluster) Leave(_ context.Context) error {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return errClosed
	}
	if !c.joined {
		c.mu.Unlock()
		return nil
	}
	c.joined = false
	ev := Event{Kind: MemberLeft, Member: c.memberSnapshot()}
	subs := c.subsSnapshot()
	c.mu.Unlock()

	c.dispatch(subs, ev)
	return nil
}

// Members returns a one-element slice containing the local node when joined,
// or an empty slice when not joined. The slice is owned by the caller.
func (c *localCluster) Members() []Member {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.joined {
		return []Member{}
	}
	return []Member{c.memberSnapshot()}
}

// LocalNode returns the Member that describes this node.
func (c *localCluster) LocalNode() Member {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.memberSnapshot()
}

// Subscribe registers h as an event receiver. Events are dispatched in a
// dedicated goroutine via a buffered channel (capacity 16). When the channel
// is full the event is dropped and a warning is logged. The returned function
// unsubscribes h; it is safe to call multiple times.
func (c *localCluster) Subscribe(h EventHandler) (unsubscribe func()) {
	ctx, cancel := context.WithCancel(context.Background())

	c.mu.Lock()
	id := c.nextID
	c.nextID++
	sub := &subscriber{ch: make(chan Event, 16), cancel: cancel}
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

// Close cancels all subscribers and marks the cluster as closed. After Close,
// Join and Leave return errClosed. Members returns the last known view.
func (c *localCluster) Close() error {
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
	c.mu.Unlock()
	return nil
}

// memberSnapshot returns a copy of self. Must be called with mu held.
func (c *localCluster) memberSnapshot() Member {
	m := Member{
		ID:      c.self.ID,
		Address: c.self.Address,
	}
	if c.self.Meta != nil {
		meta := make(map[string]string, len(c.self.Meta))
		for k, v := range c.self.Meta {
			meta[k] = v
		}
		m.Meta = meta
	}
	return m
}

// subsSnapshot returns a copy of the subscriber map. Must be called with mu held.
func (c *localCluster) subsSnapshot() []*subscriber {
	out := make([]*subscriber, 0, len(c.subs))
	for _, s := range c.subs {
		out = append(out, s)
	}
	return out
}

// dispatch sends ev to each subscriber's channel. If the channel is full
// the event is dropped and a warning is logged. Called without mu held.
func (c *localCluster) dispatch(subs []*subscriber, ev Event) {
	for _, sub := range subs {
		select {
		case sub.ch <- ev:
		default:
			c.log.Warn("cluster: subscriber channel full, event dropped",
				"kind", ev.Kind,
				"member_id", ev.Member.ID,
			)
		}
	}
}
