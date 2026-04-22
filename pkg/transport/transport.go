// Package transport abstracts the byte pipe tinyagents uses for
// cross-node Envelope delivery. Registry and CRDT replicator code
// work against these interfaces regardless of the underlying TCP,
// in-memory, or gRPC implementation.
package transport

import (
	"context"

	"github.com/skyforce77/tinyagents/internal/proto"
)

// Transport is the node-pair byte pipe.
type Transport interface {
	// Listen starts accepting inbound connections on addr (format
	// "host:port"; "0" as the port auto-picks). Inbound Envelopes
	// are dispatched to Handler. Listen returns once the listener
	// is bound; actual accept loop runs in a goroutine until Close.
	Listen(addr string, h Handler) error
	// LocalAddr returns the bound address. Only valid after Listen.
	LocalAddr() string
	// Dial opens (or reuses) an outgoing connection to nodeID at
	// addr. Idempotent — a second Dial for the same nodeID returns
	// the existing Conn regardless of addr (first Dial wins).
	Dial(ctx context.Context, nodeID, addr string) (Conn, error)
	// Close shuts the listener and every open Conn. Blocks until
	// all goroutines have exited.
	Close() error
}

// Conn is one end of a connection to a peer node.
type Conn interface {
	// Send enqueues env on the outbound write path. Returns once the
	// frame is handed to the kernel or on error; does not wait for
	// peer ack.
	Send(env *proto.Envelope) error
	// NodeID identifies the peer. Stable for the Conn's lifetime.
	NodeID() string
	// Close drops the connection and removes it from the transport
	// pool. Idempotent.
	Close() error
	// Done closes when the Conn has terminated (peer hangup, our
	// Close, heartbeat timeout). Use this to drive reconnection.
	Done() <-chan struct{}
}

// Handler is invoked for every incoming Envelope. It must return
// quickly; offload long work to a goroutine. The Conn argument is
// the receive-side — use it to send replies over the same pipe.
type Handler func(c Conn, env *proto.Envelope)
