package mapper

import (
	"errors"
	"testing"

	"github.com/austinjiann/spare-compute/internal/snapshot"
)

func TestRemoteSnapshotManifestRoundTripAndIdentityValidation(t *testing.T) {
	contents := []byte("snapshot contents")
	manifest := snapshot.Manifest{
		Version: snapshot.ManifestVersion,
		Files: []snapshot.File{{
			Path: "src/main.go", Mode: 0o644, Size: int64(len(contents)),
			Chunks: []snapshot.Chunk{{Digest: snapshot.Sum(contents), Size: uint32(len(contents))}},
		}},
		TotalBytes: int64(len(contents)),
	}
	message, err := ManifestToRemoteProto(manifest)
	if err != nil {
		t.Fatal(err)
	}
	converted, err := ManifestFromRemoteProto(message)
	if err != nil {
		t.Fatal(err)
	}
	wantID, _ := manifest.ID()
	gotID, _ := converted.ID()
	if gotID != wantID || converted.Files[0].Path != "src/main.go" {
		t.Fatalf("converted = %#v", converted)
	}

	message.Files[0].Path = "src/changed.go"
	if _, err := ManifestFromRemoteProto(message); !errors.Is(err, snapshot.ErrInvalidManifest) {
		t.Fatalf("tampered manifest error = %v", err)
	}
	message.Files[0].Path = "../escape"
	if _, err := ManifestFromRemoteProto(message); !errors.Is(err, snapshot.ErrUnsafePath) {
		t.Fatalf("unsafe manifest error = %v", err)
	}
}

func TestRemoteSnapshotManifestRejectsOversizedWireEncoding(t *testing.T) {
	contents := []byte("x")
	chunks := make([]snapshot.Chunk, 14_000)
	for index := range chunks {
		chunks[index] = snapshot.Chunk{Digest: snapshot.Sum(contents), Size: 1}
	}
	manifest := snapshot.Manifest{
		Version: snapshot.ManifestVersion,
		Files: []snapshot.File{{
			Path: "large", Mode: 0o644, Size: int64(len(chunks)), Chunks: chunks,
		}},
		TotalBytes: int64(len(chunks)),
	}
	if _, err := manifest.CanonicalBytes(); err != nil {
		t.Fatalf("test manifest should fit canonical bound: %v", err)
	}
	if _, err := ManifestToRemoteProto(manifest); !errors.Is(err, snapshot.ErrInvalidManifest) {
		t.Fatalf("oversized wire manifest error = %v", err)
	}
}
