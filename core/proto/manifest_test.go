package proto_test

import (
	"testing"

	"github.com/cwlindean/filetransfer/core/proto"
)

// Empty files (size=0) are supported since G0 (2026-11-07): the sender emits no
// DATA frame for them and the receiver creates a zero-length file directly.
func TestManifestAllowsEmptyFile(t *testing.T) {
	m := &proto.Manifest{
		ChunkSize: proto.DefaultChunkSize,
		Files: []proto.FileEntry{
			{Name: "empty.bin", Size: 0, MtimeMS: 1762500000999},
			{Name: "a.bin", Size: 2100000, MtimeMS: 1762500000123},
		},
	}
	if err := m.Validate(); err != nil {
		t.Fatalf("Validate rejected empty file: %v", err)
	}

	raw, err := m.Encode()
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	got, err := proto.ParseManifest(raw)
	if err != nil {
		t.Fatalf("ParseManifest: %v", err)
	}
	if len(got.Files) != 2 || got.Files[0].Size != 0 || got.Files[1].Size != 2100000 {
		t.Fatalf("round-trip files = %+v", got.Files)
	}
}
