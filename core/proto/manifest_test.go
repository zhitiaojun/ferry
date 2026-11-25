package proto_test

import (
	"strings"
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

func TestManifestRejectsTooManyFiles(t *testing.T) {
	files := make([]proto.FileEntry, proto.MaxFiles+1)
	for i := range files {
		files[i] = proto.FileEntry{Name: "f.bin", Size: 1}
	}
	m := proto.Manifest{TransferID: [16]byte{1}, ChunkSize: proto.DefaultChunkSize, Files: files}
	if err := m.Validate(); err == nil {
		t.Fatal("manifest with too many files accepted")
	}
}

func TestManifestRejectsUnsafeNames(t *testing.T) {
	bad := []string{
		"../evil",
		"a/../b",
		"/abs/path",
		"a//b",
		"a/",
		"C:/win.ini",
		`a\b`,
		"a\x00b",
		strings.Repeat("x", proto.MaxNameLen+1),
		strings.Repeat("a/", 520) + "file.bin",
		"",
		".",
		"..",
	}
	for _, name := range bad {
		m := proto.Manifest{TransferID: [16]byte{1}, ChunkSize: proto.DefaultChunkSize,
			Files: []proto.FileEntry{{Name: name, Size: 1}}}
		if err := m.Validate(); err == nil {
			t.Fatalf("name %q accepted", name)
		}
	}
}

func TestManifestAllowsSafeRelativePaths(t *testing.T) {
	good := []string{"a.txt", "a/b.txt", "photos/2026/img.png"}
	files := make([]proto.FileEntry, 0, len(good))
	for _, name := range good {
		files = append(files, proto.FileEntry{Name: name, Size: 1})
	}
	m := &proto.Manifest{
		TransferID: [16]byte{1},
		ChunkSize:  proto.DefaultChunkSize,
		Files:      files,
	}
	if err := m.Validate(); err != nil {
		t.Fatalf("Validate rejected safe relative paths: %v", err)
	}
	raw, err := m.Encode()
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	got, err := proto.ParseManifest(raw)
	if err != nil {
		t.Fatalf("ParseManifest: %v", err)
	}
	for i, want := range good {
		if got.Files[i].Name != want {
			t.Fatalf("Files[%d].Name = %q, want %q", i, got.Files[i].Name, want)
		}
	}
}
