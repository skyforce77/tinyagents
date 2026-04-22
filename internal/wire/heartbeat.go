package wire

import (
	"bytes"
	"context"
	"encoding/gob"
	"log/slog"
	"sync/atomic"
	"time"

	"github.com/skyforce77/tinyagents/internal/proto"
)

// Heartbeater emits proto.KindHeartbeat Envelopes at a fixed Interval
// and detects peer liveness by tracking how long it has been since the
// last observed incoming frame. If no frame of any Kind is observed for
// Miss × Interval, OnTimeout fires.
//
// Heartbeater does NOT own a connection — callers wire its Send function
// to their transport's write-side and notify it of incoming frames via
// Observed. A typical TCP connection goroutine starts a Heartbeater,
// calls Observed for every frame it reads, and wraps OnTimeout to tear
// down the connection.
type Heartbeater struct {
	// Interval is how often the Heartbeater emits a KindHeartbeat frame.
	// Defaults to 5 s if zero.
	Interval time.Duration

	// Miss is the number of consecutive Interval windows without an
	// observed frame before OnTimeout fires. Defaults to 3 if zero.
	Miss int

	// NodeID is included in the Heartbeat payload so the peer can
	// identify the sender.
	NodeID string

	// Send is called each tick with a freshly constructed KindHeartbeat
	// Envelope. If Send returns an error the run loop logs and continues;
	// transient write failures do not count as timeouts.
	Send func(env *proto.Envelope) error

	// OnTimeout is called at most once when the peer is declared dead. If
	// nil, Run simply returns.
	OnTimeout func()

	// lastObserved stores the unix-nanosecond timestamp of the most
	// recent Observed call. Accessed atomically.
	lastObserved atomic.Int64
}

// Run drives the heartbeat until ctx is cancelled or OnTimeout fires.
// It blocks; callers run it in a goroutine.
//
// Observed may be called from other goroutines while Run is active; it
// is safe for concurrent use.
func (h *Heartbeater) Run(ctx context.Context) {
	interval := h.Interval
	if interval <= 0 {
		interval = 5 * time.Second
	}
	miss := h.Miss
	if miss <= 0 {
		miss = 3
	}

	// Seed lastObserved with "now" so we don't immediately time out.
	h.lastObserved.Store(time.Now().UnixNano())

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	missCount := 0

	for {
		select {
		case <-ctx.Done():
			return
		case now := <-ticker.C:
			// Emit a heartbeat; log but do not count as a timeout on failure.
			if h.Send != nil {
				env := buildHeartbeat(h.NodeID, now)
				if err := h.Send(env); err != nil {
					slog.Default().Warn("wire: heartbeat send failed", "err", err)
				}
			}

			// Check liveness: has the peer been heard from recently?
			elapsed := now.UnixNano() - h.lastObserved.Load()
			if elapsed >= int64(interval) {
				missCount++
			} else {
				missCount = 0
			}

			if missCount >= miss {
				if h.OnTimeout != nil {
					h.OnTimeout()
				}
				return
			}
		}
	}
}

// Observed marks the peer as alive as of now, resetting the miss
// counter. Call this for every incoming frame, regardless of Kind.
func (h *Heartbeater) Observed() {
	h.lastObserved.Store(time.Now().UnixNano())
}

// buildHeartbeat constructs a KindHeartbeat Envelope whose Payload is a
// gob-encoded proto.Heartbeat. This keeps the payload self-describing for
// the receiving side.
func buildHeartbeat(nodeID string, now time.Time) *proto.Envelope {
	hb := proto.Heartbeat{
		NodeID:    nodeID,
		Timestamp: now.UnixNano(),
	}
	var buf bytes.Buffer
	_ = gob.NewEncoder(&buf).Encode(hb) // Heartbeat is a simple struct; encode never fails.
	return &proto.Envelope{
		Kind:    proto.KindHeartbeat,
		Payload: buf.Bytes(),
		Sent:    now,
	}
}
