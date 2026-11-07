package proto_test

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/cwlindean/filetransfer/core/proto"
)

// The golden vectors in docs/spec/vectors are produced by the independent
// encoder tools/gen_vectors.py. They are the cross-check between the protocol
// document and the Go codec: an expectation mismatch here means the document,
// the generator, or the codec disagree.

const vectorsDir = "../../docs/spec/vectors"

type frameExpect struct {
	Version    uint8           `json:"version"`
	Type       string          `json:"type"`
	TypeID     uint8           `json:"type_id"`
	Flags      uint16          `json:"flags"`
	SessionID  string          `json:"session_id"`
	Seq        uint64          `json:"seq"`
	PayloadLen uint32          `json:"payload_len"`
	Payload    json.RawMessage `json:"payload"`
}

type validVector struct {
	Name     string      `json:"name"`
	FrameHex string      `json:"frame_hex"`
	Expect   frameExpect `json:"expect"`
}

type invalidVector struct {
	Name        string `json:"name"`
	FrameHex    string `json:"frame_hex"`
	ExpectError string `json:"expect_error"`
}

func loadJSON(t *testing.T, name string, out any) {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(vectorsDir, name))
	if err != nil {
		t.Fatalf("read %s: %v", name, err)
	}
	if err := json.Unmarshal(b, out); err != nil {
		t.Fatalf("parse %s: %v", name, err)
	}
}

func TestValidVectors(t *testing.T) {
	var doc struct {
		Vectors []validVector `json:"vectors"`
	}
	loadJSON(t, "frames.json", &doc)
	if len(doc.Vectors) == 0 {
		t.Fatal("frames.json contains no vectors")
	}

	for _, vec := range doc.Vectors {
		t.Run(vec.Name, func(t *testing.T) {
			raw, err := hex.DecodeString(vec.FrameHex)
			if err != nil {
				t.Fatalf("bad frame_hex: %v", err)
			}
			frame, err := proto.ReadFrame(bytes.NewReader(raw))
			if err != nil {
				t.Fatalf("ReadFrame: %v", err)
			}
			e := vec.Expect
			if frame.Version != e.Version {
				t.Errorf("version = %d, want %d", frame.Version, e.Version)
			}
			if uint8(frame.Type) != e.TypeID {
				t.Errorf("type = 0x%02X, want 0x%02X", uint8(frame.Type), e.TypeID)
			}
			if parsed, ok := proto.ParseType(e.Type); !ok || uint8(parsed) != e.TypeID {
				t.Errorf("ParseType(%q) = 0x%02X, ok=%v", e.Type, uint8(parsed), ok)
			}
			if frame.Flags != e.Flags {
				t.Errorf("flags = 0x%04X, want 0x%04X", frame.Flags, e.Flags)
			}
			if got := hex.EncodeToString(frame.SessionID[:]); got != e.SessionID {
				t.Errorf("session_id = %s, want %s", got, e.SessionID)
			}
			if frame.Seq != e.Seq {
				t.Errorf("seq = %d, want %d", frame.Seq, e.Seq)
			}
			if uint32(len(frame.Payload)) != e.PayloadLen {
				t.Errorf("payload_len = %d, want %d", len(frame.Payload), e.PayloadLen)
			}

			// Re-encoding the parsed frame must reproduce the golden bytes.
			again, err := proto.MarshalFrame(frame)
			if err != nil {
				t.Fatalf("MarshalFrame: %v", err)
			}
			if !bytes.Equal(again, raw) {
				t.Errorf("MarshalFrame output differs from vector:\n got %x\nwant %x", again, raw)
			}

			// Decoding and re-encoding the payload must be byte-identical.
			if len(e.Payload) > 0 {
				msg, err := proto.DecodePayload(frame.Type, frame.Payload)
				if err != nil {
					t.Fatalf("DecodePayload: %v", err)
				}
				if got := encodePayload(t, msg); !bytes.Equal(got, frame.Payload) {
					t.Errorf("payload round-trip differs:\n got %x\nwant %x", got, frame.Payload)
				}
			}
		})
	}
}

func encodePayload(t *testing.T, msg any) []byte {
	t.Helper()
	var (
		b   []byte
		err error
	)
	switch m := msg.(type) {
	case *proto.Hello:
		b, err = m.Encode()
	case *proto.HelloAck:
		b, err = m.Encode()
	case *proto.Manifest:
		b, err = m.Encode()
	case *proto.Data:
		b, err = m.Encode()
	case *proto.Ack:
		b, err = m.Encode()
	case *proto.Resume:
		b, err = m.Encode()
	case *proto.Fin:
		b, err = m.Encode()
	case *proto.ErrorMsg:
		b, err = m.Encode()
	case *proto.Ping:
		b, err = m.Encode()
	default:
		t.Fatalf("no Encode for %T", msg)
	}
	if err != nil {
		t.Fatalf("Encode %T: %v", msg, err)
	}
	return b
}

func TestInvalidVectors(t *testing.T) {
	var doc struct {
		Vectors []invalidVector `json:"vectors"`
	}
	loadJSON(t, "invalid.json", &doc)
	if len(doc.Vectors) == 0 {
		t.Fatal("invalid.json contains no vectors")
	}

	for _, vec := range doc.Vectors {
		t.Run(vec.Name, func(t *testing.T) {
			raw, err := hex.DecodeString(vec.FrameHex)
			if err != nil {
				t.Fatalf("bad frame_hex: %v", err)
			}
			frame, err := proto.ReadFrame(bytes.NewReader(raw))
			if err == nil {
				// Frame-level checks passed; the payload must still be rejected.
				_, err = proto.DecodePayload(frame.Type, frame.Payload)
			}
			if err == nil {
				t.Fatalf("expected %s, got no error", vec.ExpectError)
			}
			assertErrorName(t, vec.ExpectError, err)
		})
	}
}

func assertErrorName(t *testing.T, name string, err error) {
	t.Helper()
	if name == "short_header" {
		if !errors.Is(err, io.ErrUnexpectedEOF) {
			t.Fatalf("short header error = %v, want io.ErrUnexpectedEOF", err)
		}
		return
	}
	var pe *proto.Error
	if !errors.As(err, &pe) {
		t.Fatalf("error %v is not a *proto.Error", err)
	}
	if pe.Name != name {
		t.Fatalf("error name = %q, want %q (%v)", pe.Name, name, err)
	}
}

func TestReadFrameTruncated(t *testing.T) {
	raw, err := hex.DecodeString("465452580101000000112233445566778899aabbccddeeff00000000000000000000004100010001")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := proto.ReadFrame(bytes.NewReader(raw)); !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Fatalf("truncated payload error = %v, want io.ErrUnexpectedEOF", err)
	}
	if _, err := proto.ReadFrame(bytes.NewReader(raw[:20])); !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Fatalf("truncated header error = %v, want io.ErrUnexpectedEOF", err)
	}
}

func TestMarshalFrameRejectsInvalid(t *testing.T) {
	valid := proto.Frame{
		Header: proto.Header{Version: proto.Version, Type: proto.TypePing, SessionID: [16]byte{1}},
	}
	cases := []struct {
		name   string
		mutate func(*proto.Frame)
		want   proto.ErrorCode
	}{
		{"bad_version", func(f *proto.Frame) { f.Version = 2 }, proto.CodeVersionUnsupported},
		{"zero_session", func(f *proto.Frame) { f.SessionID = [16]byte{} }, proto.CodeProtocolViolation},
		{"unknown_type", func(f *proto.Frame) { f.Type = 0x7F }, proto.CodeUnknownType},
		{"oversized", func(f *proto.Frame) { f.Payload = make([]byte, proto.MaxPayload+1) }, proto.CodeFrameTooLarge},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := valid
			tc.mutate(&f)
			_, err := proto.MarshalFrame(f)
			var pe *proto.Error
			if !errors.As(err, &pe) || pe.Code != tc.want {
				t.Fatalf("error = %v, want code 0x%04X", err, tc.want)
			}
		})
	}
}
