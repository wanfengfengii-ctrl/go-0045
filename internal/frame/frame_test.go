package frame

import (
	"bytes"
	"errors"
	"io"
	"testing"

	"library-locker-lease-controller/internal/domain"
)

type partialEOFReader struct {
	data []byte
	done bool
}

func (r *partialEOFReader) Read(p []byte) (int, error) {
	if r.done {
		return 0, io.EOF
	}
	r.done = true
	return copy(p, r.data), io.EOF
}

func TestDecoderTruncatedEOFClassification(t *testing.T) {
	var encoded bytes.Buffer
	if err := Encode(&encoded, Frame{Code: CmdOpenDoor, Sequence: 7, Payload: []byte{0x10, 0x20, 0x30}}); err != nil {
		t.Fatalf("Encode() error = %v", err)
	}

	cuts := []struct {
		name string
		n    int
	}{
		{name: "marker", n: 1},
		{name: "length", n: 4},
		{name: "header", n: HeaderLen - 1},
		{name: "payload", n: HeaderLen + 1},
		{name: "crc", n: encoded.Len() - 1},
	}
	for _, tc := range cuts {
		t.Run(tc.name, func(t *testing.T) {
			reader := &partialEOFReader{data: encoded.Bytes()[:tc.n]}
			_, err := NewDecoder(reader).ReadFrame()

			var frameErr *FrameError
			if !errors.As(err, &frameErr) {
				t.Fatalf("ReadFrame() error = %v, want *FrameError", err)
			}
			if frameErr.Kind != "truncated" {
				t.Fatalf("FrameError.Kind = %q, want truncated", frameErr.Kind)
			}
			if !errors.Is(err, domain.ErrFrameCorrupt) {
				t.Fatalf("errors.Is(%v, ErrFrameCorrupt) = false", err)
			}
		})
	}
}
