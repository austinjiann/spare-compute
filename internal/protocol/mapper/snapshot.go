package mapper

import (
	"fmt"

	computehopv1 "github.com/austinjiann/spare-compute/gen/go/computehop/v1"
	"github.com/austinjiann/spare-compute/internal/snapshot"
	"google.golang.org/protobuf/proto"
)

// ManifestToRemoteProto maps one canonical manifest to the paired-worker wire format.
func ManifestToRemoteProto(manifest snapshot.Manifest) (*computehopv1.SnapshotManifest, error) {
	id, err := manifest.ID()
	if err != nil {
		return nil, err
	}
	message := &computehopv1.SnapshotManifest{
		Version: manifest.Version, ManifestId: string(id), TotalBytes: manifest.TotalBytes,
		Files: make([]*computehopv1.SnapshotFile, len(manifest.Files)),
	}
	for fileIndex, file := range manifest.Files {
		converted := &computehopv1.SnapshotFile{
			Path: file.Path, Mode: file.Mode, Size: file.Size,
			Chunks: make([]*computehopv1.SnapshotChunk, len(file.Chunks)),
		}
		for chunkIndex, chunk := range file.Chunks {
			converted.Chunks[chunkIndex] = &computehopv1.SnapshotChunk{
				Digest: string(chunk.Digest), Size: chunk.Size,
			}
		}
		message.Files[fileIndex] = converted
	}
	if proto.Size(message) > snapshot.MaximumWireManifestBytes {
		return nil, fmt.Errorf(
			"%w: wire encoding exceeds %d bytes",
			snapshot.ErrInvalidManifest,
			snapshot.MaximumWireManifestBytes,
		)
	}
	return message, nil
}

// ManifestFromRemoteProto validates an untrusted manifest and its claimed identity.
func ManifestFromRemoteProto(message *computehopv1.SnapshotManifest) (snapshot.Manifest, error) {
	if message == nil {
		return snapshot.Manifest{}, snapshot.ErrInvalidManifest
	}
	if proto.Size(message) > snapshot.MaximumWireManifestBytes {
		return snapshot.Manifest{}, snapshot.ErrInvalidManifest
	}
	claimedID, err := snapshot.ParseDigest(message.GetManifestId())
	if err != nil {
		return snapshot.Manifest{}, err
	}
	manifest := snapshot.Manifest{
		Version: message.GetVersion(), TotalBytes: message.GetTotalBytes(),
		Files: make([]snapshot.File, len(message.GetFiles())),
	}
	for fileIndex, fileMessage := range message.GetFiles() {
		if fileMessage == nil {
			return snapshot.Manifest{}, snapshot.ErrInvalidManifest
		}
		file := snapshot.File{
			Path: fileMessage.GetPath(), Mode: fileMessage.GetMode(), Size: fileMessage.GetSize(),
			Chunks: make([]snapshot.Chunk, len(fileMessage.GetChunks())),
		}
		for chunkIndex, chunkMessage := range fileMessage.GetChunks() {
			if chunkMessage == nil {
				return snapshot.Manifest{}, snapshot.ErrInvalidManifest
			}
			digest, err := snapshot.ParseDigest(chunkMessage.GetDigest())
			if err != nil {
				return snapshot.Manifest{}, err
			}
			file.Chunks[chunkIndex] = snapshot.Chunk{Digest: digest, Size: chunkMessage.GetSize()}
		}
		manifest.Files[fileIndex] = file
	}
	if _, err := manifest.CanonicalBytes(); err != nil {
		return snapshot.Manifest{}, err
	}
	actualID, err := manifest.ID()
	if err != nil {
		return snapshot.Manifest{}, err
	}
	if actualID != claimedID {
		return snapshot.Manifest{}, fmt.Errorf("%w: manifest identity does not match contents", snapshot.ErrInvalidManifest)
	}
	return manifest, nil
}
