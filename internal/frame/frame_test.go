package frame

import (
	"bytes"
	"encoding/binary"
	"errors"
	"io"
	"testing"

	"library-locker-lease-controller/internal/domain"
)

// encodeFrame is a small test helper that encodes f and returns its wire bytes.
func encodeFrame(t *testing.T, f Frame) []byte {
	t.Helper()
	var buf bytes.Buffer
	if err := Encode(&buf, f); err != nil {
		t.Fatalf("Encode: %v", err)
	}
	return buf.Bytes()
}

func TestDecodeCompleteFrame(t *testing.T) {
	original := Frame{
		Version:  Version1,
		Code:     CmdOpenDoor,
		Sequence: 0x11223344,
		Payload:  []byte("hello-locker"),
	}
	d := NewDecoder(bytes.NewReader(encodeFrame(t, original)))

	f, err := d.ReadFrame()
	if err != nil {
		t.Fatalf("ReadFrame: %v", err)
	}
	if f.Version != original.Version || f.Code != original.Code ||
		f.Sequence != original.Sequence || !bytes.Equal(f.Payload, original.Payload) {
		t.Fatalf("decoded frame mismatch: got %+v want %+v", f, original)
	}

	// A trailing clean EOF after a complete frame is the normal stream end.
	if _, err := d.ReadFrame(); !errors.Is(err, io.EOF) {
		t.Fatalf("want io.EOF after complete frame, got %v", err)
	}
	if errors.Is(err, domain.ErrFrameCorrupt) {
		t.Fatalf("clean EOF must not classify as ErrFrameCorrupt: %v", err)
	}
}

func TestCleanEOFEmptyStream(t *testing.T) {
	d := NewDecoder(bytes.NewReader(nil))
	_, err := d.ReadFrame()
	if !errors.Is(err, io.EOF) {
		t.Fatalf("want io.EOF on empty stream, got %v", err)
	}
	if errors.Is(err, domain.ErrFrameCorrupt) {
		t.Fatalf("clean EOF must not classify as ErrFrameCorrupt: %v", err)
	}
}

// TestTruncatedEOF covers the boundary the bug was about: the reader supplied
// part of a frame and then reached EOF. The caller must see a stable,
// distinguishable corrupt-frame error, not a bare io.EOF. Different truncation
// points are exercised to make sure the classifier fires regardless of where
// the stream was cut.
func TestTruncatedEOF(t *testing.T) {
	full := encodeFrame(t, Frame{
		Version:  Version1,
		Code:     CmdQueryDoor,
		Sequence: 7,
		Payload:  []byte("payload-bytes"),
	})
	payloadLen := len("payload-bytes")
	totalLen := HeaderLen + payloadLen + CRCLen
	if len(full) != totalLen {
		t.Fatalf("encoded frame length %d != expected %d", len(full), totalLen)
	}

	cases := []struct {
		name string
		n    int // number of leading bytes to keep before truncating
	}{
		{"only_mark0", 1},
		{"marker_pair", 2},
		{"partial_header", HeaderLen / 2},
		{"full_header_no_payload", HeaderLen},
		{"partial_payload", HeaderLen + payloadLen/2},
		{"full_payload_no_crc", HeaderLen + payloadLen},
		{"partial_crc", HeaderLen + payloadLen + 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if tc.n >= len(full) {
				t.Fatalf("truncation point %d >= frame length %d", tc.n, len(full))
			}
			d := NewDecoder(bytes.NewReader(full[:tc.n]))

			_, err := d.ReadFrame()
			if err == nil {
				t.Fatalf("want truncated error, got nil")
			}

			// Must classify as protocol corruption via the domain sentinel.
			if !errors.Is(err, domain.ErrFrameCorrupt) {
				t.Fatalf("want errors.Is(err, ErrFrameCorrupt), got %v", err)
			}
			// Must remain distinguishable from a clean stream end.
			if errors.Is(err, io.EOF) {
				t.Fatalf("truncated error must not match io.EOF, got %v", err)
			}

			var fe *FrameError
			if !errors.As(err, &fe) {
				t.Fatalf("want *FrameError, got %T: %v", err, err)
			}
			if fe.Kind != "truncated" {
				t.Fatalf("want Kind %q, got %q", "truncated", fe.Kind)
			}
		})
	}
}

// TestTruncatedEOFIsIdempotent ensures repeated calls keep reporting truncation
// rather than flipping to a bare io.EOF once the partial bytes are drained.
func TestTruncatedEOFIsIdempotent(t *testing.T) {
	full := encodeFrame(t, Frame{Version: Version1, Code: CmdOpenDoor, Sequence: 1, Payload: []byte("xyz")})
	d := NewDecoder(bytes.NewReader(full[:HeaderLen+1])) // mid-payload

	for i := 0; i < 3; i++ {
		_, err := d.ReadFrame()
		if !errors.Is(err, domain.ErrFrameCorrupt) {
			t.Fatalf("call %d: want ErrFrameCorrupt, got %v", i, err)
		}
		if errors.Is(err, io.EOF) {
			t.Fatalf("call %d: truncated must not be io.EOF, got %v", i, err)
		}
	}
}

func TestDecodeBadCRCAndResync(t *testing.T) {
	good := encodeFrame(t, Frame{Version: Version1, Code: CmdOpenDoor, Sequence: 99, Payload: []byte("ok")})

	// A complete frame whose CRC is corrupted.
	bad := encodeFrame(t, Frame{Version: Version1, Code: CmdCloseDoor, Sequence: 1, Payload: []byte("bad")})
	bad[len(bad)-1] ^= 0xFF // flip the low CRC byte

	// bad frame followed by a good frame: the decoder must isolate the bad one.
	stream := append(append([]byte{}, bad...), good...)
	d := NewDecoder(bytes.NewReader(stream))

	_, err := d.ReadFrame()
	if !errors.Is(err, domain.ErrFrameCorrupt) {
		t.Fatalf("bad-crc frame: want ErrFrameCorrupt, got %v", err)
	}
	var fe *FrameError
	if !errors.As(err, &fe) || fe.Kind != "bad_crc" {
		t.Fatalf("bad-crc frame: want Kind %q, got %+v", "bad_crc", err)
	}

	// Resync: the following good frame must still decode.
	f, err := d.ReadFrame()
	if err != nil {
		t.Fatalf("resync after bad crc: %v", err)
	}
	if f.Code != CmdOpenDoor || f.Sequence != 99 || !bytes.Equal(f.Payload, []byte("ok")) {
		t.Fatalf("resync decoded wrong frame: %+v", f)
	}
}

func TestDecodeTooLargeAndResync(t *testing.T) {
	good := encodeFrame(t, Frame{Version: Version1, Code: CmdQueryDoor, Sequence: 5, Payload: []byte("q")})

	// A frame whose declared length exceeds a small max payload.
	big := encodeFrame(t, Frame{Version: Version1, Code: CmdOpenDoor, Sequence: 1, Payload: []byte("too-big-payload")})

	stream := append(append([]byte{}, big...), good...)
	d := NewDecoderWithMax(bytes.NewReader(stream), 4) // 4 < len("too-big-payload")

	_, err := d.ReadFrame()
	if !errors.Is(err, domain.ErrFrameTooLarge) {
		t.Fatalf("too-large frame: want ErrFrameTooLarge, got %v", err)
	}
	if errors.Is(err, domain.ErrFrameCorrupt) {
		t.Fatalf("too-large must not also classify as ErrFrameCorrupt: %v", err)
	}

	// Resync: the following good frame must still decode.
	f, err := d.ReadFrame()
	if err != nil {
		t.Fatalf("resync after too large: %v", err)
	}
	if f.Code != CmdQueryDoor || f.Sequence != 5 || !bytes.Equal(f.Payload, []byte("q")) {
		t.Fatalf("resync decoded wrong frame: %+v", f)
	}
}

func TestResyncAfterLeadingGarbage(t *testing.T) {
	good := encodeFrame(t, Frame{Version: Version1, Code: CmdOpenDoor, Sequence: 3, Payload: []byte("gg")})

	// Random garbage before a valid marker must be skipped, not fatal.
	stream := append([]byte{0x00, 0x11, 0x22, 0x33}, good...)
	d := NewDecoder(bytes.NewReader(stream))

	f, err := d.ReadFrame()
	if err != nil {
		t.Fatalf("ReadFrame after garbage: %v", err)
	}
	if f.Code != CmdOpenDoor || f.Sequence != 3 || !bytes.Equal(f.Payload, []byte("gg")) {
		t.Fatalf("decoded wrong frame after garbage: %+v", f)
	}
}

// TestRoundTripMultipleFrames ensures streaming decode of several back-to-back
// frames is unaffected by the truncation fix.
func TestRoundTripMultipleFrames(t *testing.T) {
	frames := []Frame{
		{Version: Version1, Code: CmdOpenDoor, Sequence: 1, Payload: []byte("a")},
		{Version: Version1, Code: CmdCloseDoor, Sequence: 2, Payload: []byte("bb")},
		{Version: Version1, Code: CmdQueryDoor, Sequence: 3, Payload: []byte("ccc")},
	}
	var buf bytes.Buffer
	for _, f := range frames {
		if err := Encode(&buf, f); err != nil {
			t.Fatalf("Encode: %v", err)
		}
	}
	d := NewDecoder(&buf)
	for i, want := range frames {
		got, err := d.ReadFrame()
		if err != nil {
			t.Fatalf("frame %d: %v", i, err)
		}
		if got.Code != want.Code || got.Sequence != want.Sequence || !bytes.Equal(got.Payload, want.Payload) {
			t.Fatalf("frame %d mismatch: got %+v want %+v", i, got, want)
		}
	}
	if _, err := d.ReadFrame(); !errors.Is(err, io.EOF) {
		t.Fatalf("want clean io.EOF after last frame, got %v", err)
	}
}

// TestCompleteFrameFollowedByTruncation verifies the two boundaries coexist in
// one stream: a complete frame decodes, then a trailing partial frame is
// classified as truncation rather than a clean EOF.
func TestCompleteFrameFollowedByTruncation(t *testing.T) {
	complete := encodeFrame(t, Frame{Version: Version1, Code: CmdOpenDoor, Sequence: 8, Payload: []byte("whole")})
	partial := encodeFrame(t, Frame{Version: Version1, Code: CmdQueryDoor, Sequence: 9, Payload: []byte("cut-off")})
	// Keep the second frame's header plus a couple payload bytes, drop the rest.
	partial = partial[:HeaderLen+2]

	d := NewDecoder(bytes.NewReader(append(append([]byte{}, complete...), partial...)))

	f, err := d.ReadFrame()
	if err != nil {
		t.Fatalf("first frame: %v", err)
	}
	if f.Code != CmdOpenDoor || f.Sequence != 8 || !bytes.Equal(f.Payload, []byte("whole")) {
		t.Fatalf("first frame mismatch: %+v", f)
	}

	_, err = d.ReadFrame()
	if !errors.Is(err, domain.ErrFrameCorrupt) {
		t.Fatalf("trailing partial: want ErrFrameCorrupt, got %v", err)
	}
	if errors.Is(err, io.EOF) {
		t.Fatalf("trailing partial must not be io.EOF: %v", err)
	}
}

// chunkReader delivers data in fixed-size chunks so the decoder exercises its
// incremental fill path rather than receiving a whole frame at once.
type chunkReader struct {
	data  []byte
	chunk int
	off   int
}

func (c *chunkReader) Read(p []byte) (int, error) {
	if c.off >= len(c.data) {
		return 0, io.EOF
	}
	n := c.chunk
	if remaining := len(c.data) - c.off; n > remaining {
		n = remaining
	}
	copy(p, c.data[c.off:c.off+n])
	c.off += n
	return n, nil
}

// TestDecodeIncrementalDelivery ensures frames arriving byte-by-byte (the worst
// case for the need-more loop) still decode correctly and end in a clean EOF.
func TestDecodeIncrementalDelivery(t *testing.T) {
	frames := []Frame{
		{Version: Version1, Code: CmdOpenDoor, Sequence: 1, Payload: []byte("alpha")},
		{Version: Version1, Code: CmdReceipt, Sequence: 1, Payload: []byte{ReceiptSuccess}},
	}
	var buf bytes.Buffer
	for _, f := range frames {
		if err := Encode(&buf, f); err != nil {
			t.Fatalf("Encode: %v", err)
		}
	}
	d := NewDecoder(&chunkReader{data: buf.Bytes(), chunk: 1})

	for i, want := range frames {
		got, err := d.ReadFrame()
		if err != nil {
			t.Fatalf("frame %d (byte-by-byte): %v", i, err)
		}
		if got.Code != want.Code || got.Sequence != want.Sequence || !bytes.Equal(got.Payload, want.Payload) {
			t.Fatalf("frame %d mismatch: got %+v want %+v", i, got, want)
		}
	}
	if _, err := d.ReadFrame(); !errors.Is(err, io.EOF) {
		t.Fatalf("want clean io.EOF, got %v", err)
	}
}

// TestTruncatedAfterGarbage verifies that leading garbage before a partial
// frame still yields a truncation error (garbage is dropped, then the partial
// frame is recognized as cut off).
func TestTruncatedAfterGarbage(t *testing.T) {
	partial := encodeFrame(t, Frame{Version: Version1, Code: CmdOpenDoor, Sequence: 2, Payload: []byte("incomplete")})
	partial = partial[:HeaderLen+1] // header + 1 payload byte
	stream := append([]byte{0x00, 0x01, 0x02}, partial...)

	d := NewDecoder(bytes.NewReader(stream))
	_, err := d.ReadFrame()
	if !errors.Is(err, domain.ErrFrameCorrupt) {
		t.Fatalf("want ErrFrameCorrupt for partial after garbage, got %v", err)
	}
	if errors.Is(err, io.EOF) {
		t.Fatalf("partial after garbage must not be io.EOF: %v", err)
	}
}

// TestEncodeTooLarge guards the encode-side guard.
func TestEncodeTooLarge(t *testing.T) {
	var buf bytes.Buffer
	err := Encode(&buf, Frame{Version: Version1, Code: CmdOpenDoor, Payload: make([]byte, MaxPayload+1)})
	if !errors.Is(err, domain.ErrFrameTooLarge) {
		t.Fatalf("want ErrFrameTooLarge, got %v", err)
	}
}

// TestCRCCoverage pins the CRC-16/CCITT-FALSE value for a known input so a
// regression in crc16 is caught.
func TestCRCCoverage(t *testing.T) {
	// CRC-16/CCITT-FALSE of "123456789" is 0x29B1.
	if got := crc16([]byte("123456789")); got != 0x29B1 {
		t.Fatalf("crc16(\"123456789\") = %04x, want 29B1", got)
	}
}

// TestFrameLengthFieldIsBigEndian is a small structural guard: the length field
// is a 2-byte big-endian value at offset 3, so a 256-byte payload encodes as
// 0x0100, not 0x0001.
func TestFrameLengthFieldIsBigEndian(t *testing.T) {
	data := encodeFrame(t, Frame{Version: Version1, Code: CmdOpenDoor, Payload: make([]byte, 256)})
	if got := binary.BigEndian.Uint16(data[3:5]); got != 256 {
		t.Fatalf("length field = %d, want 256", got)
	}
}
