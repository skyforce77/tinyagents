// Package wire provides length-prefixed framing, a Codec interface, and a
// Heartbeater for the tinyagents TCP transport layer.
package wire

import (
	"bytes"
	"encoding/gob"
	"fmt"

	"github.com/skyforce77/tinyagents/internal/proto"
)

// Codec marshals and unmarshals Envelopes. Implementations must be safe
// for concurrent use or the caller must serialize access.
type Codec interface {
	// Marshal serializes env to a byte slice. The returned slice is owned
	// by the caller; the implementation must not retain a reference to it.
	Marshal(env *proto.Envelope) ([]byte, error)

	// Unmarshal deserializes b into env. It returns an error on truncated
	// or malformed input rather than panicking.
	Unmarshal(b []byte, env *proto.Envelope) error
}

// GobCodec is the default v1 codec. It serializes Envelopes with
// encoding/gob, which is stdlib-only and binary-compact. A future
// protobuf-backed codec can replace it without changing callers by
// swapping the Codec passed to the transport.
type GobCodec struct{}

// Marshal serializes env using encoding/gob and returns the result.
func (GobCodec) Marshal(env *proto.Envelope) ([]byte, error) {
	var buf bytes.Buffer
	if err := gob.NewEncoder(&buf).Encode(env); err != nil {
		return nil, fmt.Errorf("wire: gob marshal: %w", err)
	}
	return buf.Bytes(), nil
}

// Unmarshal deserializes b into env using encoding/gob. It returns an
// error on truncated input rather than panicking.
func (GobCodec) Unmarshal(b []byte, env *proto.Envelope) error {
	if err := gob.NewDecoder(bytes.NewReader(b)).Decode(env); err != nil {
		return fmt.Errorf("wire: gob unmarshal: %w", err)
	}
	return nil
}
