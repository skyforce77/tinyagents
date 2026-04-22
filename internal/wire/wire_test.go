package wire

import (
	"bytes"
	"context"
	"errors"
	"io"
	"sync/atomic"
	"testing"
	"time"

	"github.com/skyforce77/tinyagents/internal/proto"
)

// ---- Codec tests -------------------------------------------------------

func TestGobCodecRoundTrip(t *testing.T) {
	t.Parallel()

	codec := GobCodec{}
	sent := time.Date(2024, 1, 2, 3, 4, 5, 0, time.UTC)

	original := &proto.Envelope{
		Kind:     proto.KindMessage,
		ID:       42,
		From:     proto.PID{Node: "n1", Path: "/actors/sender"},
		To:       proto.PID{Node: "n2", Path: "/actors/target"},
		ReplyTo:  proto.PID{}, // zero
		Meta:     map[string]string{"trace": "abc", "key": "val"},
		Deadline: 1234567890,
		Payload:  []byte("hello tinyagents"),
		Sent:     sent,
	}

	b, err := codec.Marshal(original)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}

	var got proto.Envelope
	if err := codec.Unmarshal(b, &got); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}

	if got.Kind != original.Kind {
		t.Errorf("Kind: got %v, want %v", got.Kind, original.Kind)
	}
	if got.ID != original.ID {
		t.Errorf("ID: got %d, want %d", got.ID, original.ID)
	}
	if got.From != original.From {
		t.Errorf("From: got %v, want %v", got.From, original.From)
	}
	if got.To != original.To {
		t.Errorf("To: got %v, want %v", got.To, original.To)
	}
	if !got.ReplyTo.Zero() {
		t.Errorf("ReplyTo: want zero PID, got %v", got.ReplyTo)
	}
	if got.Deadline != original.Deadline {
		t.Errorf("Deadline: got %d, want %d", got.Deadline, original.Deadline)
	}
	if !bytes.Equal(got.Payload, original.Payload) {
		t.Errorf("Payload: got %q, want %q", got.Payload, original.Payload)
	}
	if !got.Sent.Equal(original.Sent) {
		t.Errorf("Sent: got %v, want %v", got.Sent, original.Sent)
	}
	if got.Meta["trace"] != "abc" || got.Meta["key"] != "val" {
		t.Errorf("Meta: got %v, want %v", got.Meta, original.Meta)
	}
}

func TestGobCodecRoundTripZeroValues(t *testing.T) {
	t.Parallel()

	codec := GobCodec{}
	original := &proto.Envelope{
		Kind: proto.KindAck,
		// all other fields zero
	}

	b, err := codec.Marshal(original)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	var got proto.Envelope
	if err := codec.Unmarshal(b, &got); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if got.Kind != proto.KindAck {
		t.Errorf("Kind: got %v, want KindAck", got.Kind)
	}
	if !got.From.Zero() || !got.To.Zero() || !got.ReplyTo.Zero() {
		t.Error("zero PIDs should round-trip as zero")
	}
	if got.Deadline != 0 {
		t.Errorf("Deadline: got %d, want 0", got.Deadline)
	}
	if len(got.Meta) != 0 {
		t.Errorf("Meta: want empty, got %v", got.Meta)
	}
}

func TestGobCodecUnmarshalTruncated(t *testing.T) {
	t.Parallel()

	codec := GobCodec{}
	env := &proto.Envelope{Kind: proto.KindMessage, ID: 1}
	b, err := codec.Marshal(env)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}

	// Truncate the payload.
	truncated := b[:len(b)/2]
	var got proto.Envelope
	if err := codec.Unmarshal(truncated, &got); err == nil {
		t.Fatal("expected error on truncated input, got nil")
	}
}

// ---- Framing tests -----------------------------------------------------

func TestWriteReadFrameRoundTrip(t *testing.T) {
	t.Parallel()

	sizes := []int{0, 1, 4, 4096, MaxFrameSize}
	for _, sz := range sizes {
		sz := sz
		t.Run("", func(t *testing.T) {
			t.Parallel()
			payload := make([]byte, sz)
			for i := range payload {
				payload[i] = byte(i % 251)
			}

			var buf bytes.Buffer
			if err := WriteFrame(&buf, payload); err != nil {
				t.Fatalf("WriteFrame(size=%d): %v", sz, err)
			}

			got, err := ReadFrame(&buf)
			if err != nil {
				t.Fatalf("ReadFrame(size=%d): %v", sz, err)
			}
			if !bytes.Equal(got, payload) {
				t.Errorf("size=%d: payload mismatch (len %d vs %d)", sz, len(got), len(payload))
			}
		})
	}
}

func TestReadFrameRejectsOversize(t *testing.T) {
	t.Parallel()

	// Craft a header with length = MaxFrameSize + 1.
	oversize := uint32(MaxFrameSize + 1)
	var buf bytes.Buffer
	buf.WriteByte(byte(oversize >> 24))
	buf.WriteByte(byte(oversize >> 16))
	buf.WriteByte(byte(oversize >> 8))
	buf.WriteByte(byte(oversize))

	_, err := ReadFrame(&buf)
	if !errors.Is(err, ErrFrameTooLarge) {
		t.Fatalf("expected ErrFrameTooLarge, got %v", err)
	}
}

func TestReadFrameCleanEOF(t *testing.T) {
	t.Parallel()

	// Empty reader → clean EOF before any bytes are read.
	_, err := ReadFrame(bytes.NewReader(nil))
	if !errors.Is(err, io.EOF) {
		t.Fatalf("expected io.EOF on empty reader, got %v", err)
	}
}

func TestReadFrameMidFrameEOF(t *testing.T) {
	t.Parallel()

	// Write only a 4-byte header declaring 100 bytes of payload, but
	// provide no payload — the reader closes mid-frame.
	var buf bytes.Buffer
	buf.WriteByte(0)
	buf.WriteByte(0)
	buf.WriteByte(0)
	buf.WriteByte(100) // declares 100 bytes

	_, err := ReadFrame(&buf)
	if !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Fatalf("expected io.ErrUnexpectedEOF on mid-frame EOF, got %v", err)
	}
}

// ---- Heartbeater tests -------------------------------------------------

func TestHeartbeaterEmitsOnInterval(t *testing.T) {
	t.Parallel()

	var count atomic.Int32
	h := &Heartbeater{
		Interval: 5 * time.Millisecond,
		Miss:     100, // high enough that timeout never fires
		NodeID:   "test-node",
		Send: func(env *proto.Envelope) error {
			if env.Kind != proto.KindHeartbeat {
				t.Errorf("expected KindHeartbeat, got %v", env.Kind)
			}
			count.Add(1)
			return nil
		},
	}

	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Millisecond)
	defer cancel()

	done := make(chan struct{})
	go func() {
		h.Run(ctx)
		close(done)
	}()
	<-done

	got := int(count.Load())
	if got < 3 {
		t.Errorf("expected ≥3 heartbeats in 25ms at 5ms interval, got %d", got)
	}
}

func TestHeartbeaterTimesOutAfterMiss(t *testing.T) {
	t.Parallel()

	var timeoutCount atomic.Int32
	h := &Heartbeater{
		Interval: 5 * time.Millisecond,
		Miss:     3,
		NodeID:   "node-x",
		Send:     func(*proto.Envelope) error { return nil },
		OnTimeout: func() {
			timeoutCount.Add(1)
		},
	}

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	// Do NOT call Observed — peer is silent from the start.
	done := make(chan struct{})
	go func() {
		h.Run(ctx)
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(200 * time.Millisecond):
		t.Fatal("Run did not return after timeout")
	}

	if n := timeoutCount.Load(); n != 1 {
		t.Errorf("OnTimeout called %d times; want exactly 1", n)
	}
}

func TestHeartbeaterObservedPreventsTimeout(t *testing.T) {
	t.Parallel()

	var timedOut atomic.Bool
	interval := 5 * time.Millisecond
	miss := 3
	h := &Heartbeater{
		Interval: interval,
		Miss:     miss,
		NodeID:   "node-alive",
		Send:     func(*proto.Envelope) error { return nil },
		OnTimeout: func() {
			timedOut.Store(true)
		},
	}

	ctx, cancel := context.WithCancel(context.Background())

	// Keep calling Observed every Interval/2 in the background.
	stopObserver := make(chan struct{})
	go func() {
		ticker := time.NewTicker(interval / 2)
		defer ticker.Stop()
		for {
			select {
			case <-stopObserver:
				return
			case <-ticker.C:
				h.Observed()
			}
		}
	}()

	// Let the heartbeater run for well past Miss × Interval.
	runFor := time.Duration(miss*5) * interval
	time.AfterFunc(runFor, cancel)

	done := make(chan struct{})
	go func() {
		h.Run(ctx)
		close(done)
	}()

	<-done
	close(stopObserver)

	if timedOut.Load() {
		t.Error("OnTimeout fired despite regular Observed() calls")
	}
}

func TestHeartbeaterRunReturnsOnCtxCancel(t *testing.T) {
	t.Parallel()

	interval := 10 * time.Millisecond
	h := &Heartbeater{
		Interval: interval,
		Miss:     1000, // ensure timeout never fires
		NodeID:   "node-cancel",
		Send:     func(*proto.Envelope) error { return nil },
	}

	ctx, cancel := context.WithCancel(context.Background())

	done := make(chan struct{})
	go func() {
		h.Run(ctx)
		close(done)
	}()

	cancel()

	select {
	case <-done:
		// good — Run returned promptly
	case <-time.After(2 * interval):
		t.Fatal("Run did not return within 2×Interval after ctx cancel")
	}
}

// TestHeartbeaterSendErrorContinues ensures that a Send failure is logged
// but does not cause Run to exit prematurely.
func TestHeartbeaterSendErrorContinues(t *testing.T) {
	t.Parallel()

	var count atomic.Int32
	h := &Heartbeater{
		Interval: 5 * time.Millisecond,
		Miss:     100,
		NodeID:   "node-err",
		Send: func(*proto.Envelope) error {
			count.Add(1)
			return errors.New("transient write error")
		},
	}

	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Millisecond)
	defer cancel()

	done := make(chan struct{})
	go func() {
		h.Run(ctx)
		close(done)
	}()
	<-done

	// Run should have continued despite errors and emitted multiple frames.
	if count.Load() < 2 {
		t.Errorf("expected Send called multiple times despite errors, got %d", count.Load())
	}
}
