package wire

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
)

// MaxFrameSize is the hard ceiling for a single frame. Frames larger than
// this limit are rejected to prevent allocation attacks from a hostile or
// buggy peer. 16 MiB is generous for agent messages.
const MaxFrameSize = 16 * 1024 * 1024

// ErrFrameTooLarge is returned by ReadFrame when the length prefix
// declares a payload larger than MaxFrameSize.
var ErrFrameTooLarge = errors.New("wire: frame exceeds MaxFrameSize")

// WriteFrame writes a 4-byte big-endian length prefix followed by b.
// A short write at either stage returns an error — the connection is
// likely corrupt and the caller should close it.
func WriteFrame(w io.Writer, b []byte) error {
	if len(b) > MaxFrameSize {
		return fmt.Errorf("wire: payload (%d bytes) exceeds MaxFrameSize", len(b))
	}
	var hdr [4]byte
	binary.BigEndian.PutUint32(hdr[:], uint32(len(b)))
	if _, err := w.Write(hdr[:]); err != nil {
		return fmt.Errorf("wire: write length prefix: %w", err)
	}
	if len(b) == 0 {
		return nil
	}
	if _, err := w.Write(b); err != nil {
		return fmt.Errorf("wire: write payload: %w", err)
	}
	return nil
}

// ReadFrame reads a 4-byte big-endian length prefix and then exactly
// that many payload bytes.
//
// It returns io.EOF on a clean peer close — that is, if no bytes at all
// have been read before EOF. This lets a transport loop distinguish a
// normal connection close from a mid-frame truncation, which returns
// io.ErrUnexpectedEOF.
//
// It returns ErrFrameTooLarge if the declared payload length exceeds
// MaxFrameSize.
func ReadFrame(r io.Reader) ([]byte, error) {
	var hdr [4]byte
	if _, err := io.ReadFull(r, hdr[:]); err != nil {
		// io.ReadFull converts a clean EOF on the very first byte to
		// io.ErrUnexpectedEOF; map it back to io.EOF so callers can
		// detect a normal peer close.
		if errors.Is(err, io.ErrUnexpectedEOF) {
			return nil, io.EOF
		}
		return nil, err
	}

	size := binary.BigEndian.Uint32(hdr[:])
	if size > MaxFrameSize {
		return nil, ErrFrameTooLarge
	}
	if size == 0 {
		return []byte{}, nil
	}

	payload := make([]byte, size)
	if _, err := io.ReadFull(r, payload); err != nil {
		// Any EOF after we have already read the header is unexpected —
		// the peer closed the connection mid-frame. io.ReadFull returns
		// io.EOF only when zero bytes are read (empty reader), which here
		// means the declared payload is entirely absent; that is still a
		// mid-frame truncation, so normalise it to io.ErrUnexpectedEOF.
		if errors.Is(err, io.EOF) {
			return nil, io.ErrUnexpectedEOF
		}
		return nil, err
	}
	return payload, nil
}
