package frame

import (
	"bytes"
	"testing"
)

func TestDecoderReadFrameWithPrefixGarbageAtEOF(t *testing.T) {
	var encoded bytes.Buffer
	want := Frame{Code: CmdOpenDoor, Sequence: 42, Payload: []byte("ok")}
	if err := Encode(&encoded, want); err != nil {
		t.Fatalf("Encode() error = %v", err)
	}

	input := append([]byte{0x00, 0x7f}, encoded.Bytes()...)
	got, err := NewDecoder(bytes.NewReader(input)).ReadFrame()
	if err != nil {
		t.Fatalf("ReadFrame() error = %v", err)
	}
	if got == nil {
		t.Fatal("ReadFrame() returned nil frame")
	}
	if got.Code != want.Code || got.Sequence != want.Sequence || !bytes.Equal(got.Payload, want.Payload) {
		t.Fatalf("ReadFrame() = %#v, want %#v", got, &want)
	}
}
