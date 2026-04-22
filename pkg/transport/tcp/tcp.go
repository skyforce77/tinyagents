// Package tcp implements transport.Transport with length-prefixed
// framing over a plain TCP listener. Connections are bidirectional:
// once either side Dials or accepts, both directions use the same
// underlying net.Conn.
package tcp

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"sync"
	"time"

	"github.com/skyforce77/tinyagents/internal/proto"
	"github.com/skyforce77/tinyagents/internal/wire"
	"github.com/skyforce77/tinyagents/pkg/transport"
)

// Option configures a Transport.
type Option func(*Transport)

// WithCodec sets the Codec used to marshal and unmarshal Envelopes.
// Defaults to wire.GobCodec{}.
func WithCodec(c wire.Codec) Option {
	return func(t *Transport) { t.codec = c }
}

// WithHeartbeatInterval sets how often heartbeat frames are emitted.
// Defaults to 5 s.
func WithHeartbeatInterval(d time.Duration) Option {
	return func(t *Transport) { t.hbInterval = d }
}

// WithHeartbeatMiss sets how many consecutive missed intervals before
// a connection is declared dead. Defaults to 3.
func WithHeartbeatMiss(n int) Option {
	return func(t *Transport) { t.hbMiss = n }
}

// WithDialTimeout sets the TCP dial timeout. Defaults to 5 s.
func WithDialTimeout(d time.Duration) Option {
	return func(t *Transport) { t.dialTimeout = d }
}

// WithLogger sets the structured logger. Defaults to slog.Default().
func WithLogger(l *slog.Logger) Option {
	return func(t *Transport) { t.log = l }
}

// Transport is a TCP-backed implementation of transport.Transport.
// Create one with New; call Listen before accepting peers, and Dial
// to open outgoing connections.
type Transport struct {
	nodeID      string
	codec       wire.Codec
	hbInterval  time.Duration
	hbMiss      int
	dialTimeout time.Duration
	log         *slog.Logger

	mu       sync.Mutex
	handler  transport.Handler
	listener net.Listener
	conns    map[string]*conn // nodeID → conn
	closed   bool

	wg sync.WaitGroup
}

// New builds a Transport for nodeID. Options are applied before the
// transport becomes usable.
func New(nodeID string, opts ...Option) *Transport {
	t := &Transport{
		nodeID:      nodeID,
		codec:       wire.GobCodec{},
		hbInterval:  5 * time.Second,
		hbMiss:      3,
		dialTimeout: 5 * time.Second,
		log:         slog.Default(),
		conns:       make(map[string]*conn),
	}
	for _, o := range opts {
		o(t)
	}
	return t
}

// Listen binds a TCP listener on addr and starts an accept loop in a
// goroutine. Inbound Envelopes are dispatched to h. Listen returns once
// the listener is bound so callers can read LocalAddr immediately.
func (t *Transport) Listen(addr string, h transport.Handler) error {
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("transport/tcp: listen %s: %w", addr, err)
	}

	t.mu.Lock()
	t.listener = ln
	t.handler = h
	t.mu.Unlock()

	t.wg.Add(1)
	go func() {
		defer t.wg.Done()
		t.acceptLoop(ln)
	}()
	return nil
}

// LocalAddr returns the address the listener is bound to. Only valid
// after a successful Listen call.
func (t *Transport) LocalAddr() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.listener == nil {
		return ""
	}
	return t.listener.Addr().String()
}

// Dial opens (or reuses) an outgoing TCP connection to the node
// identified by nodeID at addr. It is idempotent — a second Dial for
// the same nodeID returns the existing Conn (first Dial wins).
func (t *Transport) Dial(ctx context.Context, nodeID, addr string) (transport.Conn, error) {
	t.mu.Lock()
	if t.closed {
		t.mu.Unlock()
		return nil, fmt.Errorf("transport/tcp: transport is closed")
	}
	if c, ok := t.conns[nodeID]; ok {
		t.mu.Unlock()
		return c, nil
	}
	t.mu.Unlock()

	dialCtx, cancel := context.WithTimeout(ctx, t.dialTimeout)
	defer cancel()

	var d net.Dialer
	nc, err := d.DialContext(dialCtx, "tcp", addr)
	if err != nil {
		return nil, fmt.Errorf("transport/tcp: dial %s: %w", addr, err)
	}

	c, err := t.handshake(nc, nodeID)
	if err != nil {
		nc.Close()
		return nil, err
	}

	t.mu.Lock()
	// Double-checked locking: another goroutine may have raced us.
	if existing, ok := t.conns[nodeID]; ok {
		t.mu.Unlock()
		c.closeInternal()
		return existing, nil
	}
	if t.closed {
		t.mu.Unlock()
		c.closeInternal()
		return nil, fmt.Errorf("transport/tcp: transport closed during dial")
	}
	t.conns[nodeID] = c
	t.mu.Unlock()

	t.startConn(c)
	return c, nil
}

// Close shuts down the listener and all open connections. It blocks
// until every goroutine started by this Transport has exited.
func (t *Transport) Close() error {
	t.mu.Lock()
	if t.closed {
		t.mu.Unlock()
		return nil
	}
	t.closed = true
	ln := t.listener
	conns := make([]*conn, 0, len(t.conns))
	for _, c := range t.conns {
		conns = append(conns, c)
	}
	t.mu.Unlock()

	if ln != nil {
		ln.Close()
	}
	for _, c := range conns {
		c.Close()
	}
	t.wg.Wait()
	return nil
}

// acceptLoop runs until ln is closed (i.e., until Transport.Close).
func (t *Transport) acceptLoop(ln net.Listener) {
	for {
		nc, err := ln.Accept()
		if err != nil {
			// net.Listener.Accept returns a non-nil error after Close.
			// Check whether we were shut down intentionally.
			t.mu.Lock()
			closed := t.closed
			t.mu.Unlock()
			if closed || errors.Is(err, net.ErrClosed) {
				return
			}
			t.log.Warn("transport/tcp: accept error", "err", err)
			return
		}

		t.wg.Add(1)
		go func(nc net.Conn) {
			defer t.wg.Done()
			t.acceptConn(nc)
		}(nc)
	}
}

// acceptConn performs the server-side handshake and registers the conn.
func (t *Transport) acceptConn(nc net.Conn) {
	c, err := t.handshake(nc, "") // no expected nodeID on accept side
	if err != nil {
		t.log.Warn("transport/tcp: accept handshake failed", "remote", nc.RemoteAddr(), "err", err)
		nc.Close()
		return
	}

	t.mu.Lock()
	if t.closed {
		t.mu.Unlock()
		c.closeInternal()
		return
	}
	// If we already have a conn for this nodeID (e.g., simultaneous dial),
	// keep the existing one and drop the newly accepted one.
	if existing, ok := t.conns[c.nodeID]; ok {
		t.mu.Unlock()
		_ = existing // keep existing
		c.closeInternal()
		return
	}
	t.conns[c.nodeID] = c
	t.mu.Unlock()

	t.startConn(c)
}

// handshake exchanges the opening KindMessage frame carrying this node's
// identity. On the Dial side, expectedNodeID is the peer's nodeID and a
// mismatch returns an error. On the Accept side, pass "" to accept any peer.
func (t *Transport) handshake(nc net.Conn, expectedNodeID string) (*conn, error) {
	// Send our identity first.
	hello := &proto.Envelope{
		Kind: proto.KindMessage,
		From: proto.PID{Node: t.nodeID},
		Sent: time.Now(),
	}
	b, err := t.codec.Marshal(hello)
	if err != nil {
		return nil, fmt.Errorf("transport/tcp: marshal handshake: %w", err)
	}
	if err := wire.WriteFrame(nc, b); err != nil {
		return nil, fmt.Errorf("transport/tcp: write handshake: %w", err)
	}

	// Read peer's identity.
	frame, err := wire.ReadFrame(nc)
	if err != nil {
		return nil, fmt.Errorf("transport/tcp: read handshake: %w", err)
	}
	var peerEnv proto.Envelope
	if err := t.codec.Unmarshal(frame, &peerEnv); err != nil {
		return nil, fmt.Errorf("transport/tcp: unmarshal handshake: %w", err)
	}
	peerNodeID := peerEnv.From.Node
	if peerNodeID == "" {
		return nil, fmt.Errorf("transport/tcp: peer sent empty nodeID in handshake")
	}
	if expectedNodeID != "" && peerNodeID != expectedNodeID {
		return nil, fmt.Errorf("transport/tcp: handshake nodeID mismatch: want %q, got %q", expectedNodeID, peerNodeID)
	}

	c := newConn(nc, peerNodeID, t.codec, t.hbInterval, t.hbMiss, t.nodeID, t.log)
	return c, nil
}

// startConn launches the reader, writer, and heartbeater goroutines for c
// and wires up removal from the pool when Done fires.
func (t *Transport) startConn(c *conn) {
	t.mu.Lock()
	h := t.handler
	t.mu.Unlock()

	t.wg.Add(1)
	go func() {
		defer t.wg.Done()
		c.readLoop(h)
	}()

	t.wg.Add(1)
	go func() {
		defer t.wg.Done()
		c.writeLoop()
	}()

	t.wg.Add(1)
	go func() {
		defer t.wg.Done()
		c.heartbeatLoop()
	}()

	// Remove from pool when the conn terminates.
	t.wg.Add(1)
	go func() {
		defer t.wg.Done()
		<-c.Done()
		t.mu.Lock()
		delete(t.conns, c.nodeID)
		t.mu.Unlock()
	}()
}

// conn is one end of a peer-to-peer connection.
type conn struct {
	nc     net.Conn
	nodeID string
	codec  wire.Codec
	log    *slog.Logger

	// outbound is the write queue. Capacity 128 gives enough headroom
	// before Send blocks or errors.
	outbound chan *proto.Envelope

	// hb is the shared Heartbeater; both readLoop (Observed) and
	// heartbeatLoop (Run) operate on the same instance.
	hb *wire.Heartbeater

	hbCancel context.CancelFunc
	hbCtx    context.Context

	once sync.Once
	done chan struct{}
}

// newConn builds a conn over nc. It does not start any goroutines.
func newConn(
	nc net.Conn,
	nodeID string,
	codec wire.Codec,
	hbInterval time.Duration,
	hbMiss int,
	localNode string,
	log *slog.Logger,
) *conn {
	ctx, cancel := context.WithCancel(context.Background())
	c := &conn{
		nc:       nc,
		nodeID:   nodeID,
		codec:    codec,
		log:      log,
		outbound: make(chan *proto.Envelope, 128),
		hbCancel: cancel,
		hbCtx:    ctx,
		done:     make(chan struct{}),
	}
	// The heartbeater is created here so readLoop and heartbeatLoop share it.
	// Send and OnTimeout are set after c is constructed so they can close over c.
	c.hb = &wire.Heartbeater{
		Interval:  hbInterval,
		Miss:      hbMiss,
		NodeID:    localNode,
		Send:      c.sendHeartbeat,
		OnTimeout: c.closeInternal,
	}
	return c
}

// Send enqueues env on the outbound path. It returns an error if the
// connection is already closed or the outbound buffer is full.
func (c *conn) Send(env *proto.Envelope) error {
	select {
	case <-c.done:
		return fmt.Errorf("transport/tcp: conn to %s is closed", c.nodeID)
	case c.outbound <- env:
		return nil
	default:
		return fmt.Errorf("transport/tcp: outbound buffer full for %s", c.nodeID)
	}
}

// NodeID returns the peer's stable node identifier.
func (c *conn) NodeID() string { return c.nodeID }

// Close drops the connection and signals Done. Idempotent.
func (c *conn) Close() error {
	c.closeInternal()
	return nil
}

// Done returns a channel that is closed when this Conn has terminated.
func (c *conn) Done() <-chan struct{} { return c.done }

// closeInternal shuts down the underlying net.Conn and cancels the
// heartbeater context. Safe to call multiple times.
func (c *conn) closeInternal() {
	c.once.Do(func() {
		c.hbCancel()
		c.nc.Close()
		close(c.done)
	})
}

// sendHeartbeat is the Heartbeater.Send callback. It enqueues a
// KindHeartbeat Envelope on the write path.
func (c *conn) sendHeartbeat(env *proto.Envelope) error {
	select {
	case <-c.done:
		return fmt.Errorf("transport/tcp: conn closed")
	case c.outbound <- env:
		return nil
	default:
		return fmt.Errorf("transport/tcp: outbound buffer full (heartbeat)")
	}
}

// readLoop reads frames from the connection and dispatches them to h.
// It runs until the connection is closed.
func (c *conn) readLoop(h transport.Handler) {
	for {
		frame, err := wire.ReadFrame(c.nc)
		if err != nil {
			if errors.Is(err, io.EOF) || errors.Is(err, net.ErrClosed) {
				// Clean close from peer or us.
			} else {
				select {
				case <-c.done:
					// Already closing; suppress noise.
				default:
					c.log.Warn("transport/tcp: read error", "peer", c.nodeID, "err", err)
				}
			}
			c.closeInternal()
			return
		}

		// Notify the shared heartbeater that we heard from the peer.
		c.hb.Observed()

		var env proto.Envelope
		if err := c.codec.Unmarshal(frame, &env); err != nil {
			c.log.Warn("transport/tcp: unmarshal error", "peer", c.nodeID, "err", err)
			c.closeInternal()
			return
		}

		if env.Kind == proto.KindHeartbeat {
			// Heartbeat — liveness already recorded above; nothing else to do.
			continue
		}

		if h != nil {
			h(c, &env)
		}
	}
}

// writeLoop drains the outbound channel and writes frames to the conn.
func (c *conn) writeLoop() {
	for {
		select {
		case <-c.done:
			return
		case env, ok := <-c.outbound:
			if !ok {
				return
			}
			b, err := c.codec.Marshal(env)
			if err != nil {
				c.log.Warn("transport/tcp: marshal error", "peer", c.nodeID, "err", err)
				c.closeInternal()
				return
			}
			if err := wire.WriteFrame(c.nc, b); err != nil {
				select {
				case <-c.done:
				default:
					c.log.Warn("transport/tcp: write error", "peer", c.nodeID, "err", err)
				}
				c.closeInternal()
				return
			}
		}
	}
}

// heartbeatLoop runs the shared Heartbeater for this conn until the
// connection is closed or the heartbeat context is cancelled.
func (c *conn) heartbeatLoop() {
	c.hb.Run(c.hbCtx)
}
