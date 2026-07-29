// Package transfer defines bounded wire encodings for content-addressed chunks.
package transfer

import (
	"errors"
	"fmt"
	"sync"

	"github.com/klauspost/compress/zstd"

	"github.com/austinjiann/spare-compute/internal/snapshot"
)

type ChunkEncoding uint8

const (
	EncodingIdentity ChunkEncoding = iota + 1
	EncodingZstd

	MaximumEncodedChunkBytes  = snapshot.MaximumChunkBytes + 64<<10
	minimumCompressionSavings = 64
)

var (
	ErrInvalidChunk        = errors.New("invalid encoded transfer chunk")
	ErrUnsupportedEncoding = errors.New("unsupported transfer chunk encoding")

	codecOnce    sync.Once
	codecEncoder *zstd.Encoder
	codecDecoder *zstd.Decoder
	codecErr     error
)

// Chunk is one bounded payload whose digest remains defined over the decoded data.
type Chunk struct {
	Encoding         ChunkEncoding
	Data             []byte
	UncompressedSize uint32
}

// SupportedChunkEncodings returns preference order for this build.
func SupportedChunkEncodings() []ChunkEncoding {
	return []ChunkEncoding{EncodingZstd, EncodingIdentity}
}

// EncodeChunk chooses the smallest mutually supported representation, while
// retaining identity encoding when compression does not save meaningful bytes.
func EncodeChunk(contents []byte, accepted []ChunkEncoding) (Chunk, error) {
	if len(contents) == 0 || len(contents) > snapshot.MaximumChunkBytes {
		return Chunk{}, ErrInvalidChunk
	}
	identity, zstandard, err := acceptedEncodings(accepted)
	if err != nil {
		return Chunk{}, err
	}
	if zstandard {
		encoder, _, err := codecs()
		if err != nil {
			return Chunk{}, err
		}
		compressed := encoder.EncodeAll(contents, nil)
		if len(compressed) > 0 && len(compressed) <= MaximumEncodedChunkBytes &&
			(!identity || len(compressed)+minimumCompressionSavings <= len(contents)) {
			return Chunk{
				Encoding: EncodingZstd, Data: compressed, UncompressedSize: uint32(len(contents)),
			}, nil
		}
	}
	if identity {
		return Chunk{
			Encoding: EncodingIdentity, Data: append([]byte(nil), contents...),
			UncompressedSize: uint32(len(contents)),
		}, nil
	}
	return Chunk{}, ErrUnsupportedEncoding
}

// DecodeChunk rejects unknown encodings, oversized declarations, malformed
// frames, decompression bombs, and size mismatches before returning content.
func DecodeChunk(chunk Chunk) ([]byte, error) {
	if chunk.UncompressedSize == 0 || chunk.UncompressedSize > snapshot.MaximumChunkBytes ||
		len(chunk.Data) == 0 || len(chunk.Data) > MaximumEncodedChunkBytes {
		return nil, ErrInvalidChunk
	}
	switch chunk.Encoding {
	case EncodingIdentity:
		if len(chunk.Data) != int(chunk.UncompressedSize) {
			return nil, ErrInvalidChunk
		}
		return append([]byte(nil), chunk.Data...), nil
	case EncodingZstd:
		_, decoder, err := codecs()
		if err != nil {
			return nil, err
		}
		decoded, err := decoder.DecodeAll(chunk.Data, make([]byte, 0, int(chunk.UncompressedSize)))
		if err != nil || len(decoded) != int(chunk.UncompressedSize) {
			return nil, fmt.Errorf("%w: zstd decode failed", ErrInvalidChunk)
		}
		return decoded, nil
	default:
		return nil, ErrUnsupportedEncoding
	}
}

func acceptedEncodings(values []ChunkEncoding) (identity bool, zstandard bool, err error) {
	if len(values) == 0 || len(values) > 2 {
		return false, false, ErrUnsupportedEncoding
	}
	for _, value := range values {
		switch value {
		case EncodingIdentity:
			if identity {
				return false, false, ErrUnsupportedEncoding
			}
			identity = true
		case EncodingZstd:
			if zstandard {
				return false, false, ErrUnsupportedEncoding
			}
			zstandard = true
		default:
			return false, false, ErrUnsupportedEncoding
		}
	}
	return identity, zstandard, nil
}

func codecs() (*zstd.Encoder, *zstd.Decoder, error) {
	codecOnce.Do(func() {
		codecEncoder, codecErr = zstd.NewWriter(
			nil,
			zstd.WithEncoderConcurrency(1),
			zstd.WithEncoderLevel(zstd.SpeedFastest),
			zstd.WithWindowSize(snapshot.MaximumChunkBytes),
			zstd.WithEncoderCRC(false),
		)
		if codecErr != nil {
			return
		}
		codecDecoder, codecErr = zstd.NewReader(
			nil,
			zstd.WithDecoderConcurrency(1),
			zstd.WithDecoderMaxMemory(uint64(snapshot.MaximumChunkBytes)),
			zstd.WithDecoderMaxWindow(uint64(snapshot.MaximumChunkBytes)),
		)
		if codecErr != nil {
			_ = codecEncoder.Close()
			codecEncoder = nil
		}
	})
	return codecEncoder, codecDecoder, codecErr
}
