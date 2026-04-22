package tcp_test

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/skyforce77/tinyagents/internal/proto"
	"github.com/skyforce77/tinyagents/pkg/transport"
	"github.com/skyforce77/tinyagents/pkg/transport/tcp"
)

// deadline returns a context that times out after d — used to keep tests from
// hanging forever on broken code.
func deadline(t *testing.T, d time.Duration) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), d)
	t.Cleanup(cancel)
	return ctx
}

// waitChan waits for ch to be closed before deadline; fails the test otherwise.
func waitChan(t *testing.T, ch <-chan struct{}, d time.Duration, msg string) {
	t.Helper()
	select {
	case <-ch:
	case <-time.After(d):
		t.Fatalf("timeout after %v waiting for: %s", d, msg)
	}
}

// TestTCPSendReceive: B dials A; B sends an Envelope; A's Handler receives it.
func TestTCPSendReceive(t *testing.T) {
	t.Parallel()

	received := make(chan *proto.Envelope, 1)

	a := tcp.New("a")
	if err := a.Listen("127.0.0.1:0", func(_ transport.Conn, env *proto.Envelope) {
		received <- env
	}); err != nil {
		t.Fatalf("a.Listen: %v", err)
	}
	t.Cleanup(func() { a.Close() })

	b := tcp.New("b")
	t.Cleanup(func() { b.Close() })

	ctx := deadline(t, 5*time.Second)
	conn, err := b.Dial(ctx, "a", a.LocalAddr())
	if err != nil {
		t.Fatalf("b.Dial: %v", err)
	}

	want := &proto.Envelope{
		Kind:    proto.KindMessage,
		From:    proto.PID{Node: "b", Path: "/test"},
		To:      proto.PID{Node: "a", Path: "/actor"},
		Payload: []byte("hello"),
	}
	if err := conn.Send(want); err != nil {
		t.Fatalf("conn.Send: %v", err)
	}

	select {
	case got := <-received:
		if got.From.Node != want.From.Node {
			t.Errorf("From.Node: got %q, want %q", got.From.Node, want.From.Node)
		}
		if string(got.Payload) != string(want.Payload) {
			t.Errorf("Payload: got %q, want %q", got.Payload, want.Payload)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("timeout waiting for Envelope delivery")
	}
}

// TestTCPHandshakeFlagsMismatch: Dial with wrong nodeID returns an error; no handler call.
func TestTCPHandshakeFlagsMismatch(t *testing.T) {
	t.Parallel()

	var handlerCalled atomic.Bool

	a := tcp.New("a1")
	if err := a.Listen("127.0.0.1:0", func(_ transport.Conn, _ *proto.Envelope) {
		handlerCalled.Store(true)
	}); err != nil {
		t.Fatalf("a.Listen: %v", err)
	}
	t.Cleanup(func() { a.Close() })

	b := tcp.New("b1")
	t.Cleanup(func() { b.Close() })

	ctx := deadline(t, 5*time.Second)
	_, err := b.Dial(ctx, "wrong", a.LocalAddr())
	if err == nil {
		t.Fatal("expected Dial error for nodeID mismatch, got nil")
	}

	// Give A a moment to process any stray frame, then confirm no handler call.
	time.Sleep(100 * time.Millisecond)
	if handlerCalled.Load() {
		t.Error("handler was called despite handshake mismatch")
	}
}

// TestTCPDialReusesConn: two Dials to the same nodeID return the same Conn.
func TestTCPDialReusesConn(t *testing.T) {
	t.Parallel()

	a := tcp.New("a")
	if err := a.Listen("127.0.0.1:0", func(_ transport.Conn, _ *proto.Envelope) {}); err != nil {
		t.Fatalf("a.Listen: %v", err)
	}
	t.Cleanup(func() { a.Close() })

	b := tcp.New("b")
	t.Cleanup(func() { b.Close() })

	ctx := deadline(t, 5*time.Second)
	c1, err := b.Dial(ctx, "a", a.LocalAddr())
	if err != nil {
		t.Fatalf("first Dial: %v", err)
	}
	c2, err := b.Dial(ctx, "a", a.LocalAddr())
	if err != nil {
		t.Fatalf("second Dial: %v", err)
	}

	if c1 != c2 {
		t.Error("second Dial returned a different Conn (want same pointer)")
	}
}

// TestTCPConnCloseSignalsDone: closing one side causes the other side's Done to close.
func TestTCPConnCloseSignalsDone(t *testing.T) {
	t.Parallel()

	aConnCh := make(chan transport.Conn, 1)

	a := tcp.New("a")
	if err := a.Listen("127.0.0.1:0", func(c transport.Conn, _ *proto.Envelope) {
		select {
		case aConnCh <- c:
		default:
		}
	}); err != nil {
		t.Fatalf("a.Listen: %v", err)
	}
	t.Cleanup(func() { a.Close() })

	b := tcp.New("b")
	t.Cleanup(func() { b.Close() })

	ctx := deadline(t, 5*time.Second)
	bConn, err := b.Dial(ctx, "a", a.LocalAddr())
	if err != nil {
		t.Fatalf("b.Dial: %v", err)
	}

	// Send a message so A's handler fires and we get A's Conn.
	_ = bConn.Send(&proto.Envelope{Kind: proto.KindMessage})

	// Close B's side of the connection.
	if err := bConn.Close(); err != nil {
		t.Fatalf("bConn.Close: %v", err)
	}

	// B's Done should close immediately.
	waitChan(t, bConn.Done(), 500*time.Millisecond, "bConn.Done()")
}

// TestTCPHeartbeatKeepsAlive: with a short interval and high miss threshold,
// an idle connection should remain alive for at least 2 s.
func TestTCPHeartbeatKeepsAlive(t *testing.T) {
	t.Parallel()

	a := tcp.New("a",
		tcp.WithHeartbeatInterval(50*time.Millisecond),
		tcp.WithHeartbeatMiss(10),
	)
	if err := a.Listen("127.0.0.1:0", func(_ transport.Conn, _ *proto.Envelope) {}); err != nil {
		t.Fatalf("a.Listen: %v", err)
	}
	t.Cleanup(func() { a.Close() })

	b := tcp.New("b",
		tcp.WithHeartbeatInterval(50*time.Millisecond),
		tcp.WithHeartbeatMiss(10),
	)
	t.Cleanup(func() { b.Close() })

	ctx := deadline(t, 10*time.Second)
	c, err := b.Dial(ctx, "a", a.LocalAddr())
	if err != nil {
		t.Fatalf("b.Dial: %v", err)
	}

	// Wait 2 s; Done must NOT fire.
	select {
	case <-c.Done():
		t.Error("connection closed unexpectedly after 2 s idle")
	case <-time.After(2 * time.Second):
		// Good — connection survived.
	}
}

// TestTCPBidirectional: A.Dial gives a Conn; A.Send goes to B's Handler; B
// replies via the Conn it received in its Handler; A receives the reply.
func TestTCPBidirectional(t *testing.T) {
	t.Parallel()

	aReceived := make(chan *proto.Envelope, 1)
	bReceived := make(chan *proto.Envelope, 1)

	a := tcp.New("a")
	if err := a.Listen("127.0.0.1:0", func(c transport.Conn, env *proto.Envelope) {
		aReceived <- env
	}); err != nil {
		t.Fatalf("a.Listen: %v", err)
	}
	t.Cleanup(func() { a.Close() })

	b := tcp.New("b")
	if err := b.Listen("127.0.0.1:0", func(c transport.Conn, env *proto.Envelope) {
		bReceived <- env
		// Reply back over the same pipe.
		_ = c.Send(&proto.Envelope{
			Kind:    proto.KindMessage,
			From:    proto.PID{Node: "b"},
			Payload: []byte("pong"),
		})
	}); err != nil {
		t.Fatalf("b.Listen: %v", err)
	}
	t.Cleanup(func() { b.Close() })

	ctx := deadline(t, 5*time.Second)
	aConn, err := a.Dial(ctx, "b", b.LocalAddr())
	if err != nil {
		t.Fatalf("a.Dial: %v", err)
	}

	if err := aConn.Send(&proto.Envelope{
		Kind:    proto.KindMessage,
		From:    proto.PID{Node: "a"},
		Payload: []byte("ping"),
	}); err != nil {
		t.Fatalf("aConn.Send: %v", err)
	}

	// B should receive the ping.
	select {
	case env := <-bReceived:
		if string(env.Payload) != "ping" {
			t.Errorf("b received %q, want %q", env.Payload, "ping")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("timeout waiting for B to receive ping")
	}

	// A should receive B's pong reply.
	select {
	case env := <-aReceived:
		if string(env.Payload) != "pong" {
			t.Errorf("a received %q, want %q", env.Payload, "pong")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("timeout waiting for A to receive pong")
	}
}

// TestTCPCloseAllConns: open 3 outgoing conns, call Transport.Close, every Done closes.
func TestTCPCloseAllConns(t *testing.T) {
	t.Parallel()

	peerIDs := []string{"peer0", "peer1", "peer2"}
	peers := make([]*tcp.Transport, len(peerIDs))
	for i, id := range peerIDs {
		p := tcp.New(id)
		if err := p.Listen("127.0.0.1:0", func(_ transport.Conn, _ *proto.Envelope) {}); err != nil {
			t.Fatalf("peer[%d].Listen: %v", i, err)
		}
		t.Cleanup(func() { p.Close() })
		peers[i] = p
	}

	dialer := tcp.New("dialer")
	t.Cleanup(func() { dialer.Close() })

	ctx := deadline(t, 5*time.Second)
	conns := make([]transport.Conn, len(peerIDs))
	for i, p := range peers {
		c, err := dialer.Dial(ctx, peerIDs[i], p.LocalAddr())
		if err != nil {
			t.Fatalf("Dial peer[%d]: %v", i, err)
		}
		conns[i] = c
	}

	// Close the dialer — all Conns must terminate.
	if err := dialer.Close(); err != nil {
		t.Fatalf("dialer.Close: %v", err)
	}

	var wg sync.WaitGroup
	for i, c := range conns {
		wg.Add(1)
		go func(idx int, c transport.Conn) {
			defer wg.Done()
			waitChan(t, c.Done(), 500*time.Millisecond,
				"conn["+string(rune('0'+idx))+"].Done()")
		}(i, c)
	}
	wg.Wait()
}
