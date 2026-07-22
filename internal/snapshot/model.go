// Package snapshot defines immutable, platform-neutral project snapshots.
package snapshot

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"path"
	"sort"
	"strings"
	"unicode/utf8"
)

const (
	ManifestVersion          uint32 = 1
	DigestBytes                     = sha256.Size
	DigestHexLength                 = DigestBytes * 2
	MinimumChunkBytes               = 64 << 10
	AverageChunkBytes               = 256 << 10
	MaximumChunkBytes               = 512 << 10
	MaximumFiles                    = 25_000
	MaximumChunks                   = 250_000
	MaximumPathBytes                = 4_096
	MaximumSnapshotBytes     int64  = 100 << 30
	MaximumManifestBytes            = 800 << 10
	MaximumWireManifestBytes        = 900 << 10
)

var (
	ErrInvalidDigest   = errors.New("invalid content digest")
	ErrInvalidManifest = errors.New("invalid snapshot manifest")
	ErrUnsafePath      = errors.New("unsafe snapshot path")
)

// Digest is a canonical lowercase SHA-256 content identifier.
type Digest string

// Sum returns the digest of contents.
func Sum(contents []byte) Digest {
	value := sha256.Sum256(contents)
	return Digest(hex.EncodeToString(value[:]))
}

// ParseDigest validates an untrusted digest.
func ParseDigest(value string) (Digest, error) {
	if len(value) != DigestHexLength || strings.ToLower(value) != value {
		return "", ErrInvalidDigest
	}
	decoded, err := hex.DecodeString(value)
	if err != nil || len(decoded) != DigestBytes {
		return "", ErrInvalidDigest
	}
	return Digest(value), nil
}

// Valid reports whether digest is canonical.
func (digest Digest) Valid() bool {
	_, err := ParseDigest(string(digest))
	return err == nil
}

// Chunk references one content-defined segment in the content store.
type Chunk struct {
	Digest Digest
	Size   uint32
}

// File is one regular file in a manifest. Paths always use forward slashes.
type File struct {
	Path   string
	Mode   uint32
	Size   int64
	Chunks []Chunk
}

// Manifest is a sorted, immutable description of one project tree.
type Manifest struct {
	Version    uint32
	Files      []File
	TotalBytes int64
}

// Validate enforces bounded, portable paths and internally consistent sizes.
func (manifest Manifest) Validate() error {
	if manifest.Version != ManifestVersion || len(manifest.Files) > MaximumFiles ||
		manifest.TotalBytes < 0 || manifest.TotalBytes > MaximumSnapshotBytes {
		return ErrInvalidManifest
	}
	var total int64
	chunkCount := 0
	previous := ""
	portablePaths := make(map[string]struct{}, len(manifest.Files))
	for index, file := range manifest.Files {
		if err := ValidatePath(file.Path); err != nil {
			return fmt.Errorf("%w: file %d: %w", ErrInvalidManifest, index, err)
		}
		if previous != "" && file.Path <= previous {
			return fmt.Errorf("%w: file paths are not strictly sorted", ErrInvalidManifest)
		}
		previous = file.Path
		portable := strings.ToLower(file.Path)
		if _, exists := portablePaths[portable]; exists {
			return fmt.Errorf("%w: path collision for %q", ErrInvalidManifest, file.Path)
		}
		for parent := path.Dir(portable); parent != "."; parent = path.Dir(parent) {
			if _, exists := portablePaths[parent]; exists {
				return fmt.Errorf("%w: file is also a parent directory for %q", ErrInvalidManifest, file.Path)
			}
		}
		portablePaths[portable] = struct{}{}
		if (file.Mode != 0o644 && file.Mode != 0o755) || file.Size < 0 {
			return fmt.Errorf("%w: invalid metadata for %q", ErrInvalidManifest, file.Path)
		}
		var fileBytes int64
		for _, chunk := range file.Chunks {
			chunkCount++
			if chunkCount > MaximumChunks || !chunk.Digest.Valid() || chunk.Size == 0 ||
				chunk.Size > MaximumChunkBytes {
				return fmt.Errorf("%w: invalid chunk for %q", ErrInvalidManifest, file.Path)
			}
			fileBytes += int64(chunk.Size)
		}
		if fileBytes != file.Size || (file.Size == 0 && len(file.Chunks) != 0) {
			return fmt.Errorf("%w: chunk sizes do not match %q", ErrInvalidManifest, file.Path)
		}
		total += file.Size
		if total > MaximumSnapshotBytes {
			return ErrInvalidManifest
		}
	}
	if total != manifest.TotalBytes {
		return fmt.Errorf("%w: total size does not match files", ErrInvalidManifest)
	}
	return nil
}

// ID returns the SHA-256 digest of the canonical manifest encoding.
func (manifest Manifest) ID() (Digest, error) {
	canonical, err := manifest.CanonicalBytes()
	if err != nil {
		return "", err
	}
	return Sum(canonical), nil
}

// CanonicalBytes returns the stable encoding used for manifest identity.
func (manifest Manifest) CanonicalBytes() ([]byte, error) {
	if err := manifest.Validate(); err != nil {
		return nil, err
	}
	var buffer bytes.Buffer
	if err := manifest.writeCanonical(&buffer); err != nil {
		return nil, err
	}
	if buffer.Len() > MaximumManifestBytes {
		return nil, fmt.Errorf("%w: canonical encoding exceeds %d bytes", ErrInvalidManifest, MaximumManifestBytes)
	}
	return buffer.Bytes(), nil
}

func (manifest Manifest) writeCanonical(writer io.Writer) error {
	if err := binary.Write(writer, binary.BigEndian, manifest.Version); err != nil {
		return err
	}
	if err := binary.Write(writer, binary.BigEndian, uint32(len(manifest.Files))); err != nil {
		return err
	}
	for _, file := range manifest.Files {
		if err := binary.Write(writer, binary.BigEndian, uint32(len(file.Path))); err != nil {
			return err
		}
		if _, err := io.WriteString(writer, file.Path); err != nil {
			return err
		}
		for _, value := range []any{file.Mode, uint64(file.Size), uint32(len(file.Chunks))} {
			if err := binary.Write(writer, binary.BigEndian, value); err != nil {
				return err
			}
		}
		for _, chunk := range file.Chunks {
			decoded, _ := hex.DecodeString(string(chunk.Digest))
			if _, err := writer.Write(decoded); err != nil {
				return err
			}
			if err := binary.Write(writer, binary.BigEndian, chunk.Size); err != nil {
				return err
			}
		}
	}
	return binary.Write(writer, binary.BigEndian, uint64(manifest.TotalBytes))
}

// Digests returns the unique referenced chunks in lexical order.
func (manifest Manifest) Digests() []Digest {
	values := make(map[Digest]struct{})
	for _, file := range manifest.Files {
		for _, chunk := range file.Chunks {
			values[chunk.Digest] = struct{}{}
		}
	}
	result := make([]Digest, 0, len(values))
	for digest := range values {
		result = append(result, digest)
	}
	sort.Slice(result, func(left, right int) bool { return result[left] < result[right] })
	return result
}

// Clone returns a manifest with independent slices.
func (manifest Manifest) Clone() Manifest {
	clone := manifest
	clone.Files = make([]File, len(manifest.Files))
	for index, file := range manifest.Files {
		clone.Files[index] = file
		clone.Files[index].Chunks = append([]Chunk(nil), file.Chunks...)
	}
	return clone
}

// ValidatePath accepts only normalized portable relative file paths.
func ValidatePath(value string) error {
	if value == "" || len(value) > MaximumPathBytes || !utf8.ValidString(value) ||
		strings.ContainsAny(value, "\\\x00<>:\"|?*") ||
		strings.HasPrefix(value, "/") || path.Clean(value) != value || value == "." || value == ".." ||
		strings.HasPrefix(value, "../") {
		return ErrUnsafePath
	}
	for _, segment := range strings.Split(value, "/") {
		if segment == "" || segment == "." || segment == ".." ||
			strings.HasSuffix(segment, ".") || strings.HasSuffix(segment, " ") ||
			windowsReservedName(segment) {
			return ErrUnsafePath
		}
		for _, character := range segment {
			if character < 0x20 {
				return ErrUnsafePath
			}
		}
	}
	return nil
}

func windowsReservedName(segment string) bool {
	base := strings.ToUpper(strings.SplitN(segment, ".", 2)[0])
	if base == "CON" || base == "PRN" || base == "AUX" || base == "NUL" {
		return true
	}
	if len(base) == 4 && (strings.HasPrefix(base, "COM") || strings.HasPrefix(base, "LPT")) {
		return base[3] >= '1' && base[3] <= '9'
	}
	return false
}
