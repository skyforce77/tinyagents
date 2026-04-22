// Package proto defines the on-wire messages tinyagents exchanges
// between nodes. Despite the name, v1 serializes these via encoding/gob
// (see internal/wire/codec.go) to keep stdlib-only dependencies. The
// package is named "proto" so a future protobuf-backed codec lands as a
// drop-in replacement without renaming imports.
package proto

import "time"

// Kind discriminates message variants carried in an Envelope.
type Kind uint8

const (
	KindUnknown   Kind = iota
	KindMessage        // KindMessage carries a user-level actor message.
	KindAck            // KindAck acknowledges delivery of a KindMessage frame.
	KindHeartbeat      // KindHeartbeat is a liveness ping between nodes.
)

// Envelope is one framed unit on the wire. Every exchange between nodes
// is wrapped in an Envelope; the Codec is responsible for marshaling and
// unmarshaling it; the framing layer prefixes the result with a 4-byte
// big-endian length.
type Envelope struct {
	// Kind identifies the message variant; callers must switch on this
	// before interpreting Payload.
	Kind Kind

	// ID is a monotonic per-connection counter assigned by the sender.
	// Peers use it to acknowledge frames selectively via KindAck.
	ID uint64

	// From is the PID of the actor that emitted this envelope.
	From PID

	// To is the PID of the target actor on the receiving node.
	To PID

	// ReplyTo is non-zero when the sender expects a response (Ask pattern).
	ReplyTo PID

	// Meta carries free-form tags such as trace IDs and idempotency keys.
	Meta map[string]string

	// Deadline is the unix-nanosecond absolute deadline for processing.
	// Zero means no deadline is attached.
	Deadline int64

	// Payload is the codec-specific body for KindMessage envelopes. The
	// wire layer treats it as opaque bytes; higher layers decode it.
	Payload []byte

	// Sent is the wall-clock time at which the sender emitted the frame.
	Sent time.Time
}

// PID identifies an actor across the cluster. It is re-declared here so
// internal/proto has zero deps on pkg/actor — the dependency graph flows
// strictly one way (actor → internal, never the reverse).
type PID struct {
	Node string
	Path string
}

// Zero reports whether p is the zero PID (both Node and Path are empty).
func (p PID) Zero() bool { return p.Node == "" && p.Path == "" }

// String renders a PID as "node/path" for logs and diagnostics. A zero
// PID renders as "-" so it is never confused with a real path. The path
// component conventionally starts with "/" so the rendered form is
// "node/path" where the slash is part of Path, e.g. "n1/actors/foo".
func (p PID) String() string {
	if p.Zero() {
		return "-"
	}
	return p.Node + p.Path
}

// Heartbeat is the dedicated payload for KindHeartbeat frames. It is
// encoded by the Codec; callers need not construct Envelopes directly —
// the Heartbeater in internal/wire does so on their behalf.
type Heartbeat struct {
	// NodeID is the stable identifier of the node emitting the heartbeat.
	NodeID string

	// Timestamp is the unix-nanosecond wall clock when the frame was
	// emitted.
	Timestamp int64
}
