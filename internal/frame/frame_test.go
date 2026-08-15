package frame

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"testing"

	"library-locker-lease-controller/internal/domain"
)

// This test file exercises the streaming decoder's resynchronization and
// read-boundary behavior. The central regression is the boundary fault where a
// complete valid frame is preceded by invalid leading bytes and the underlying
// reader is already at EOF: the decoder must skip the garbage and deliver the
// buffered frame rather than reporting a plain io.EOF.

// encodeFrame builds a single encoded frame for tests.
func encodeFrame(code byte, seq uint32, payload []byte) []byte {
	var buf bytes.Buffer
	if err := Encode(&buf, Frame{Version: Version1, Code: code, Sequence: seq, Payload: payload}); err != nil {
		panic(err)
	}
	return buf.Bytes()
}

// chunkReader is an io.Reader that exposes at most chunk bytes per Read call,
// modeling a slow/segmented transport. If eofWithLast is true the final Read
// returns io.EOF together with the remaining bytes (as some serial drivers do);
// otherwise it mirrors bytes.Reader and returns (n, nil) until exhausted, then
// (0, io.EOF).
type chunkReader struct {
	data        []byte
	chunk       int
	eofWithLast bool
	off         int
}

func newChunkReader(data []byte, chunk int, eofWithLast bool) *chunkReader {
	if chunk <= 0 {
		chunk = len(data)
	}
	return &chunkReader{data: data, chunk: chunk, eofWithLast: eofWithLast}
}

func (r *chunkReader) Read(p []byte) (int, error) {
	if r.off >= len(r.data) {
		return 0, io.EOF
	}
	n := len(r.data) - r.off
	if n > r.chunk {
		n = r.chunk
	}
	if n > len(p) {
		n = len(p)
	}
	copy(p, r.data[r.off:r.off+n])
	r.off += n
	if r.off >= len(r.data) && r.eofWithLast {
		return n, io.EOF
	}
	return n, nil
}

// deterministicGarbage returns n bytes that never contain the Mark0..Mark1
// pair, so they are unambiguously invalid leading data. It is deterministic so
// repeated runs are stable.
func deterministicGarbage(n int) []byte {
	out := make([]byte, n)
	for i := 0; i < n; i++ {
		b := byte((i*7 + 3) & 0xFF)
		if b == Mark0 {
			b = 0x00
		}
		out[i] = b
	}
	return out
}

// wantFrame is a small helper to assert a decoded frame's identity.
func wantFrame(t *testing.T, f *Frame, err error, code byte, seq uint32, payload []byte) {
	t.Helper()
	if err != nil {
		t.Fatalf("expected frame, got err=%v", err)
	}
	if f.Code != code || f.Sequence != seq || !bytes.Equal(f.Payload, payload) {
		t.Fatalf("decoded wrong frame: code=%02x seq=%d payload=%x (want code=%02x seq=%d payload=%x)",
			f.Code, f.Sequence, f.Payload, code, seq, payload)
	}
}

// TestLeadingGarbageOneShot is the core regression: a complete frame preceded
// by invalid leading bytes, delivered all at once via bytes.Reader (the reader
// is at EOF after the single fill).
func TestLeadingGarbageOneShot(t *testing.T) {
	frame := encodeFrame(CmdQueryDoor, 42, []byte{0xAA, 0xBB})
	for _, garbage := range [][]byte{
		{0xFF},
		{0xFF, 0xEE, 0xDD},
		deterministicGarbage(1),
		deterministicGarbage(17),
		deterministicGarbage(5000),
	} {
		stream := append(append([]byte(nil), garbage...), frame...)
		d := NewDecoder(bytes.NewReader(stream))
		f, err := d.ReadFrame()
		wantFrame(t, f, err, CmdQueryDoor, 42, []byte{0xAA, 0xBB})
	}
}

// TestLeadingGarbageSegmented covers the same regression when the stream is
// read in small chunks of varying sizes and EOF delivery styles.
func TestLeadingGarbageSegmented(t *testing.T) {
	frame := encodeFrame(CmdOpenDoor, 7, []byte{0x01, 0x02, 0x03})
	stream := append(deterministicGarbage(23), frame...)
	cases := []struct {
		name        string
		chunk       int
		eofWithLast bool
	}{
		{"chunk1-nilEOF", 1, false},
		{"chunk1-eofWithLast", 1, true},
		{"chunk3-nilEOF", 3, false},
		{"chunk7-eofWithLast", 7, true},
		{"chunk64-nilEOF", 64, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			d := NewDecoder(newChunkReader(stream, c.chunk, c.eofWithLast))
			f, err := d.ReadFrame()
			wantFrame(t, f, err, CmdOpenDoor, 7, []byte{0x01, 0x02, 0x03})
		})
	}
}

// TestGarbageArrivesBeforeFrame ensures the errNeedMore path (idx<0) still
// works: garbage with no marker arrives first, the decoder asks for more, the
// frame arrives in a later read.
func TestGarbageArrivesBeforeFrame(t *testing.T) {
	frame := encodeFrame(CmdCloseDoor, 100, nil)
	d := NewDecoder(newChunkReader(append(deterministicGarbage(11), frame...), 5, false))
	f, err := d.ReadFrame()
	wantFrame(t, f, err, CmdCloseDoor, 100, nil)
}

// TestStrayMarkerInGarbage: the garbage contains a stray Mark0 not followed by
// Mark1; the decoder must keep scanning to the real marker.
func TestStrayMarkerInGarbage(t *testing.T) {
	frame := encodeFrame(CmdQueryDoor, 1, []byte{0x09})
	// 0xA5 0xFF is a stray Mark0 (not a marker); 0x00 0xA5 0x5A would be the
	// real marker prefixed by a byte. Construct: garbage, stray 0xA5, garbage,
	// then the real frame.
	stream := []byte{0x01, 0xA5, 0xFF, 0x02, 0x03}
	stream = append(stream, frame...)
	d := NewDecoder(bytes.NewReader(stream))
	f, err := d.ReadFrame()
	wantFrame(t, f, err, CmdQueryDoor, 1, []byte{0x09})
}

// TestConsecutiveFramesAfterGarbage: after skipping garbage, multiple
// back-to-back frames must all be delivered, and the trailing exhausted stream
// yields io.EOF.
func TestConsecutiveFramesAfterGarbage(t *testing.T) {
	f1 := encodeFrame(CmdOpenDoor, 1, []byte{0xA1})
	f2 := encodeFrame(CmdCloseDoor, 2, []byte{0xA2})
	f3 := encodeFrame(CmdQueryDoor, 3, []byte{0xA3})
	stream := append(deterministicGarbage(8), f1...)
	stream = append(stream, f2...)
	stream = append(stream, f3...)
	d := NewDecoder(bytes.NewReader(stream))
	f, err := d.ReadFrame()
	wantFrame(t, f, err, CmdOpenDoor, 1, []byte{0xA1})
	f, err = d.ReadFrame()
	wantFrame(t, f, err, CmdCloseDoor, 2, []byte{0xA2})
	f, err = d.ReadFrame()
	wantFrame(t, f, err, CmdQueryDoor, 3, []byte{0xA3})
	// Stream exhausted with no more frames: a trailing EOF is expected.
	if _, err := d.ReadFrame(); err != io.EOF {
		t.Fatalf("expected io.EOF after consuming all frames, got %v", err)
	}
}

// TestBadCRCResyncThenGarbageThenFrame: a bad-CRC frame is reported as corrupt,
// then garbage, then a good frame is delivered on the next call.
func TestBadCRCResyncThenGarbageThenFrame(t *testing.T) {
	good := encodeFrame(CmdQueryDoor, 9, []byte{0x55})
	bad := encodeFrame(CmdOpenDoor, 1, []byte{0x01})
	// Corrupt the CRC so it mismatches.
	bad[len(bad)-1] ^= 0xFF
	stream := append([]byte{}, bad...)
	stream = append(stream, deterministicGarbage(5)...)
	stream = append(stream, good...)
	d := NewDecoder(bytes.NewReader(stream))
	f, err := d.ReadFrame()
	if err == nil {
		t.Fatalf("expected FrameError for bad CRC, got frame %+v", f)
	}
	if !errors.Is(err, domain.ErrFrameCorrupt) {
		t.Fatalf("expected errors.Is(ErrFrameCorrupt), got %v", err)
	}
	f, err = d.ReadFrame()
	wantFrame(t, f, err, CmdQueryDoor, 9, []byte{0x55})
}

// TestTooLargeResync: an oversized frame is reported as too large, then a good
// frame is delivered on the next call.
func TestTooLargeResync(t *testing.T) {
	// Build an oversized length header (max+1) with a valid marker so the
	// decoder classifies it as too_large rather than garbage.
	hdr := make([]byte, HeaderLen)
	hdr[0] = Mark0
	hdr[1] = Mark1
	hdr[2] = Version1
	hdr[3] = 0x00
	hdr[4] = 0x09 // length 9 > max 8
	hdr[5] = CmdOpenDoor
	good := encodeFrame(CmdQueryDoor, 3, []byte{0x77})
	stream := append(hdr, good...)
	dec := NewDecoderWithMax(bytes.NewReader(stream), 8)
	f, err := dec.ReadFrame()
	if err == nil {
		t.Fatalf("expected FrameError for too large, got frame %+v", f)
	}
	if !errors.Is(err, domain.ErrFrameTooLarge) {
		t.Fatalf("expected errors.Is(ErrFrameTooLarge), got %v", err)
	}
	if errors.Is(err, domain.ErrFrameCorrupt) {
		t.Fatalf("too_large must not match ErrFrameCorrupt, got %v", err)
	}
	f, err = dec.ReadFrame()
	wantFrame(t, f, err, CmdQueryDoor, 3, []byte{0x77})
}

// TestErrorClassification checks the FrameError sentinel mapping in isolation.
func TestErrorClassification(t *testing.T) {
	bad := &FrameError{Kind: "bad_crc", Detail: "x"}
	if !errors.Is(bad, domain.ErrFrameCorrupt) {
		t.Fatal("bad_crc should match ErrFrameCorrupt")
	}
	if errors.Is(bad, domain.ErrFrameTooLarge) {
		t.Fatal("bad_crc should not match ErrFrameTooLarge")
	}
	corrupt := &FrameError{Kind: "corrupt", Detail: "x"}
	if !errors.Is(corrupt, domain.ErrFrameCorrupt) {
		t.Fatal("corrupt should match ErrFrameCorrupt")
	}
	trunc := &FrameError{Kind: "truncated", Detail: "x"}
	if !errors.Is(trunc, domain.ErrFrameCorrupt) {
		t.Fatal("truncated should match ErrFrameCorrupt")
	}
	big := &FrameError{Kind: "too_large", Detail: "x"}
	if !errors.Is(big, domain.ErrFrameTooLarge) {
		t.Fatal("too_large should match ErrFrameTooLarge")
	}
	if errors.Is(big, domain.ErrFrameCorrupt) {
		t.Fatal("too_large should not match ErrFrameCorrupt")
	}
}

// TestTruncatedFrameEOF documents the existing semantics: a frame whose
// declared length exceeds the available bytes, followed by EOF, yields io.EOF.
func TestTruncatedFrameEOF(t *testing.T) {
	hdr := make([]byte, HeaderLen)
	hdr[0] = Mark0
	hdr[1] = Mark1
	hdr[2] = Version1
	hdr[3] = 0x00
	hdr[4] = 0x64 // length 100, but no payload/CRC follows
	hdr[5] = CmdQueryDoor
	d := NewDecoder(bytes.NewReader(hdr))
	if _, err := d.ReadFrame(); err != io.EOF {
		t.Fatalf("expected io.EOF for truncated frame, got %v", err)
	}
}

// TestTruncatedHeaderEOF: fewer than HeaderLen bytes then EOF yields io.EOF.
func TestTruncatedHeaderEOF(t *testing.T) {
	d := NewDecoder(bytes.NewReader([]byte{Mark0, Mark1, Version1}))
	if _, err := d.ReadFrame(); err != io.EOF {
		t.Fatalf("expected io.EOF for truncated header, got %v", err)
	}
}

// TestEmptyStreamEOF: a truly empty stream yields io.EOF on the first call.
func TestEmptyStreamEOF(t *testing.T) {
	d := NewDecoder(bytes.NewReader(nil))
	if _, err := d.ReadFrame(); err != io.EOF {
		t.Fatalf("expected io.EOF for empty stream, got %v", err)
	}
}

// TestOneShotEqualsSegmented asserts the one-shot (bytes.Reader) and segmented
// (1-byte chunks) decoders produce identical frame/error sequences for a mixed
// stream of garbage + good frame + bad-crc frame + good frame.
func TestOneShotEqualsSegmented(t *testing.T) {
	good1 := encodeFrame(CmdOpenDoor, 1, []byte{0x10})
	bad := encodeFrame(CmdCloseDoor, 2, []byte{0x20})
	bad[len(bad)-1] ^= 0x0F
	good2 := encodeFrame(CmdQueryDoor, 3, []byte{0x30, 0x31})

	stream := deterministicGarbage(13)
	stream = append(stream, good1...)
	stream = append(stream, bad...)
	stream = append(stream, good2...)

	decode := func(r io.Reader) []string {
		d := NewDecoder(r)
		var out []string
		for i := 0; i < 4; i++ {
			f, err := d.ReadFrame()
			if err != nil {
				out = append(out, fmt.Sprintf("err:%T(%v)", err, err))
			} else {
				out = append(out, fmt.Sprintf("frame:code=%02x,seq=%d,payload=%x", f.Code, f.Sequence, f.Payload))
			}
		}
		return out
	}

	oneShot := decode(bytes.NewReader(stream))
	seg := decode(newChunkReader(stream, 1, false))
	segEOF := decode(newChunkReader(stream, 3, true))

	if len(oneShot) != 4 || len(seg) != 4 || len(segEOF) != 4 {
		t.Fatalf("result length mismatch: %d %d %d", len(oneShot), len(seg), len(segEOF))
	}
	for i := 0; i < 4; i++ {
		if oneShot[i] != seg[i] || oneShot[i] != segEOF[i] {
			t.Fatalf("divergence at step %d:\n  oneShot=%s\n  seg1   =%s\n  seg3eof=%s",
				i, oneShot[i], seg[i], segEOF[i])
		}
	}
}

// TestRepeatedRunStability decodes a deterministic mixed stream and repeats the
// whole decode multiple times, asserting identical results every run. This
// guards against any internal state leaking between ReadFrame calls.
func TestRepeatedRunStability(t *testing.T) {
	good1 := encodeFrame(CmdOpenDoor, 1, []byte{0x10})
	bad := encodeFrame(CmdCloseDoor, 2, []byte{0x20})
	bad[len(bad)-1] ^= 0x01
	good2 := encodeFrame(CmdQueryDoor, 3, []byte{0x30})

	buildStream := func() []byte {
		s := deterministicGarbage(31)
		s = append(s, good1...)
		s = append(s, deterministicGarbage(5)...)
		s = append(s, good2...)
		s = append(s, bad...)
		s = append(s, good1...)
		return s
	}

	decodeOnce := func() []string {
		d := NewDecoder(newChunkReader(buildStream(), 4, false))
		var out []string
		for i := 0; i < 10; i++ {
			f, err := d.ReadFrame()
			if err != nil {
				if errors.Is(err, io.EOF) {
					out = append(out, "EOF")
					break
				}
				out = append(out, fmt.Sprintf("err:%s", err))
				continue
			}
			out = append(out, fmt.Sprintf("frame:%02x/%d/%x", f.Code, f.Sequence, f.Payload))
		}
		return out
	}

	first := decodeOnce()
	for run := 0; run < 10; run++ {
		got := decodeOnce()
		if len(got) != len(first) {
			t.Fatalf("run %d: length changed: %v vs %v", run, got, first)
		}
		for i := range got {
			if got[i] != first[i] {
				t.Fatalf("run %d: divergence at step %d: %q vs %q", run, i, got[i], first[i])
			}
		}
	}
}
