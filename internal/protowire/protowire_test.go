package protowire

import (
	"bytes"
	"encoding/hex"
	"testing"
)

func TestEncoderGolden(t *testing.T) {
	enc := &Encoder{}
	enc.String(1, "abc")
	enc.Varint(2, 150)
	got := hex.EncodeToString(enc.BytesValue())
	want := "0a03616263109601"
	if got != want {
		t.Fatalf("golden mismatch: got %s want %s", got, want)
	}
}

func TestConnectFrameRoundTrip(t *testing.T) {
	payload := []byte("hello protobuf")
	frame, err := EncodeConnectFrame(payload, true)
	if err != nil {
		t.Fatal(err)
	}
	frames, err := DecodeConnectFrames(frame)
	if err != nil {
		t.Fatal(err)
	}
	if len(frames) != 1 {
		t.Fatalf("got %d frames", len(frames))
	}
	if !bytes.Equal(frames[0], payload) {
		t.Fatalf("payload mismatch: %q", frames[0])
	}
}

func TestExtractStrings(t *testing.T) {
	enc := &Encoder{}
	enc.String(1, "short")
	enc.String(2, "long-enough")
	got := ExtractStrings(enc.BytesValue())
	if len(got) != 1 || got[0] != "long-enough" {
		t.Fatalf("unexpected strings: %#v", got)
	}
}
