// Package frame implements the binary wire protocol spoken with the locker
// hardware over a serial line.
//
// Frame layout (all multi-byte fields big-endian):
//
//	+--------+--------+---------+--------+----------+---------+--------+
//	| mark0  | mark1  | version | length | cmd code | sequence| payload |
//	| 0xA5   | 0x5A   |  1 byte | 2 byte |  1 byte  | 4 byte  | length  |
//	+--------+--------+---------+--------+----------+---------+--------+
//	+---------+
//	| CRC-16  |
//	| 2 byte  |
//	+---------+
//
// The start marker, fixed length fields and CRC together let a streaming
// decoder isolate corrupt frames and resynchronize without losing subsequent
// valid frames. CRC-16/CCITT-FALSE covers version..payload (everything between
// the marker and the CRC).
package frame

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"

	"library-locker-lease-controller/internal/domain"
)

// Wire constants.
const (
	Mark0       byte = 0xA5
	Mark1       byte = 0x5A
	Version1    byte = 0x01
	HeaderLen   int  = 10 // 2 marker + 1 version + 2 length + 1 code + 4 sequence
	CRCLen      int  = 2
	MaxPayload  int  = 4096
	MaxFrameLen int  = HeaderLen + MaxPayload + CRCLen
)

// Command codes mirrored from the domain; the frame layer keeps its own copy so
// it has no upward dependency, but the values are identical.
const (
	CmdOpenDoor  byte = 0x01
	CmdCloseDoor byte = 0x02
	CmdQueryDoor byte = 0x03
	// CmdReceipt is a device-to-controller frame reporting the outcome of a
	// previously sent command, identified by its echoed sequence.
	CmdReceipt byte = 0x81
)

// Receipt result codes carried in the payload of a CmdReceipt frame.
const (
	ReceiptSuccess  byte = 0x00
	ReceiptRejected byte = 0x01
)

// Frame is a decoded protocol frame.
type Frame struct {
	Version  byte
	Code     byte
	Sequence uint32
	Payload  []byte
}

// FrameError wraps a corrupt frame with enough classification for the
// transport layer to audit it distinctly (too large vs bad CRC vs truncated).
type FrameError struct {
	Kind    string
	Detail  string
	wrapped error
}

func (e *FrameError) Error() string {
	if e.wrapped != nil {
		return fmt.Sprintf("frame %s: %s: %v", e.Kind, e.Detail, e.wrapped)
	}
	return fmt.Sprintf("frame %s: %s", e.Kind, e.Detail)
}

func (e *FrameError) Unwrap() error { return e.wrapped }

// Is lets errors.Is match the domain sentinel even when wrapped in a
// FrameError.
func (e *FrameError) Is(target error) bool {
	switch e.Kind {
	case "corrupt", "truncated", "bad_crc":
		return errors.Is(target, domain.ErrFrameCorrupt)
	case "too_large":
		return errors.Is(target, domain.ErrFrameTooLarge)
	}
	return false
}

// crc16 computes CRC-16/CCITT-FALSE (poly 0x1021, init 0xFFFF, no
// reflection/final xor) over data.
func crc16(data []byte) uint16 {
	crc := uint16(0xFFFF)
	for _, b := range data {
		crc ^= uint16(b) << 8
		for i := 0; i < 8; i++ {
			if crc&0x8000 != 0 {
				crc = (crc << 1) ^ 0x1021
			} else {
				crc <<= 1
			}
		}
	}
	return crc
}

// Encode writes a single frame to w. It returns a wrapped
// domain.ErrFrameTooLarge if the payload exceeds MaxPayload.
func Encode(w io.Writer, f Frame) error {
	if len(f.Payload) > MaxPayload {
		return &FrameError{Kind: "too_large", Detail: fmt.Sprintf("payload %d > %d", len(f.Payload), MaxPayload)}
	}
	if f.Version == 0 {
		f.Version = Version1
	}
	buf := make([]byte, 0, HeaderLen+len(f.Payload)+CRCLen)
	buf = append(buf, Mark0, Mark1)
	buf = append(buf, f.Version)
	var lenbuf [2]byte
	binary.BigEndian.PutUint16(lenbuf[:], uint16(len(f.Payload)))
	buf = append(buf, lenbuf[:]...)
	buf = append(buf, f.Code)
	var seqbuf [4]byte
	binary.BigEndian.PutUint32(seqbuf[:], f.Sequence)
	buf = append(buf, seqbuf[:]...)
	buf = append(buf, f.Payload...)
	crc := crc16(buf[2:]) // over version..payload (skip the 2 marker bytes)
	var crcbuf [2]byte
	binary.BigEndian.PutUint16(crcbuf[:], crc)
	buf = append(buf, crcbuf[:]...)
	_, err := w.Write(buf)
	return err
}

// Decode reads exactly one frame from r, blocking until a complete, valid frame
// is available or an error occurs. Corrupt frames are isolated: the decoder
// drops the leading marker byte of a bad frame and rescans, so a single
// corruption never desynchronizes the stream permanently.
//
// On a corrupt frame Decode returns a FrameError wrapping
// domain.ErrFrameCorrupt (or domain.ErrFrameTooLarge). The caller may continue
// calling Decode; the decoder preserves unconsumed valid bytes across the
// resync.
type Decoder struct {
	r          io.Reader
	buf        []byte
	maxPayload int
}

// NewDecoder returns a Decoder reading from r with the default max payload.
func NewDecoder(r io.Reader) *Decoder {
	return &Decoder{r: r, maxPayload: MaxPayload}
}

// NewDecoderWithMax returns a Decoder with a custom maximum payload, useful for
// fuzz/property tests.
func NewDecoderWithMax(r io.Reader, max int) *Decoder {
	if max <= 0 {
		max = MaxPayload
	}
	return &Decoder{r: r, maxPayload: max}
}

// fill reads more bytes into the buffer.
func (d *Decoder) fill() error {
	tmp := make([]byte, 1024)
	n, err := d.r.Read(tmp)
	if n > 0 {
		d.buf = append(d.buf, tmp[:n]...)
	}
	if err != nil && err != io.EOF {
		return err
	}
	// io.EOF with zero new bytes is reported as io.EOF; with bytes it is
	// swallowed so the caller can consume the remainder first.
	if n == 0 && err == io.EOF {
		return io.EOF
	}
	return nil
}

// ReadFrame decodes the next valid frame. It is the exported entry point.
func (d *Decoder) ReadFrame() (*Frame, error) {
	for {
		f, consumed, err := d.tryDecode()
		if consumed > 0 {
			d.buf = d.buf[consumed:]
		}
		if err == nil {
			return f, nil
		}
		if err == errNeedMore {
			if ferr := d.fill(); ferr != nil {
				return nil, ferr
			}
			continue
		}
		// Corrupt/too-large: resync by dropping one marker byte so the next
		// iteration scans for the marker starting one byte later.
		return nil, err
	}
}

// errNeedMore is internal; it signals the caller to read more bytes.
var errNeedMore = errors.New("need more data")

// tryDecode attempts to decode a frame from the buffer. It returns the decoded
// frame, the number of leading bytes consumed, and an error. errNeedMore means
// more bytes must be read. On corrupt data it returns a FrameError and consumed
// == 1 so the caller drops the leading marker byte and rescans.
func (d *Decoder) tryDecode() (*Frame, int, error) {
	// Scan for the start marker.
	idx := indexMarker(d.buf)
	if idx < 0 {
		// Keep the last byte in case it is Mark0 and the partner arrives next.
		if len(d.buf) > 0 && d.buf[len(d.buf)-1] == Mark0 {
			return nil, len(d.buf) - 1, errNeedMore
		}
		return nil, len(d.buf), errNeedMore
	}
	if idx > 0 {
		// Drop leading garbage up to the marker.
		return nil, idx, errNeedMore
	}
	// idx == 0: buffer starts with a marker.
	if len(d.buf) < HeaderLen {
		return nil, 0, errNeedMore
	}
	length := int(binary.BigEndian.Uint16(d.buf[3:5]))
	if length > d.maxPayload {
		// Too large: drop the marker byte and resync.
		return nil, 1, &FrameError{Kind: "too_large", Detail: fmt.Sprintf("length %d > max %d", length, d.maxPayload)}
	}
	total := HeaderLen + length + CRCLen
	if len(d.buf) < total {
		return nil, 0, errNeedMore
	}
	want := crc16(d.buf[2 : HeaderLen+length])
	got := binary.BigEndian.Uint16(d.buf[HeaderLen+length : total])
	if want != got {
		return nil, 1, &FrameError{Kind: "bad_crc", Detail: fmt.Sprintf("want %04x got %04x", want, got)}
	}
	f := &Frame{
		Version:  d.buf[2],
		Code:     d.buf[5],
		Sequence: binary.BigEndian.Uint32(d.buf[6:HeaderLen]),
		Payload:  append([]byte(nil), d.buf[HeaderLen:HeaderLen+length]...),
	}
	return f, total, nil
}

// indexMarker returns the index of the first Mark0..Mark1 pair, or -1.
func indexMarker(b []byte) int {
	for i := 0; i+1 < len(b); i++ {
		if b[i] == Mark0 && b[i+1] == Mark1 {
			return i
		}
	}
	return -1
}
