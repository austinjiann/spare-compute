package mapper

import (
	computehopv1 "github.com/austinjiann/spare-compute/gen/go/computehop/v1"
	"github.com/austinjiann/spare-compute/internal/transfer"
)

func ChunkEncodingToRemoteProto(
	encoding transfer.ChunkEncoding,
) (computehopv1.ChunkEncoding, error) {
	switch encoding {
	case transfer.EncodingIdentity:
		return computehopv1.ChunkEncoding_CHUNK_ENCODING_IDENTITY, nil
	case transfer.EncodingZstd:
		return computehopv1.ChunkEncoding_CHUNK_ENCODING_ZSTD, nil
	default:
		return computehopv1.ChunkEncoding_CHUNK_ENCODING_UNSPECIFIED, transfer.ErrUnsupportedEncoding
	}
}

func ChunkEncodingFromRemoteProto(
	encoding computehopv1.ChunkEncoding,
) (transfer.ChunkEncoding, error) {
	switch encoding {
	case computehopv1.ChunkEncoding_CHUNK_ENCODING_IDENTITY:
		return transfer.EncodingIdentity, nil
	case computehopv1.ChunkEncoding_CHUNK_ENCODING_ZSTD:
		return transfer.EncodingZstd, nil
	default:
		return 0, transfer.ErrUnsupportedEncoding
	}
}

func ChunkEncodingsToRemoteProto(
	encodings []transfer.ChunkEncoding,
) ([]computehopv1.ChunkEncoding, error) {
	if len(encodings) == 0 || len(encodings) > 2 {
		return nil, transfer.ErrUnsupportedEncoding
	}
	result := make([]computehopv1.ChunkEncoding, len(encodings))
	seen := make(map[computehopv1.ChunkEncoding]struct{}, len(encodings))
	for index, encoding := range encodings {
		converted, err := ChunkEncodingToRemoteProto(encoding)
		if err != nil {
			return nil, err
		}
		if _, exists := seen[converted]; exists {
			return nil, transfer.ErrUnsupportedEncoding
		}
		seen[converted] = struct{}{}
		result[index] = converted
	}
	return result, nil
}

func ChunkEncodingsFromRemoteProto(
	encodings []computehopv1.ChunkEncoding,
) ([]transfer.ChunkEncoding, error) {
	if len(encodings) == 0 || len(encodings) > 2 {
		return nil, transfer.ErrUnsupportedEncoding
	}
	result := make([]transfer.ChunkEncoding, len(encodings))
	seen := make(map[transfer.ChunkEncoding]struct{}, len(encodings))
	for index, encoding := range encodings {
		converted, err := ChunkEncodingFromRemoteProto(encoding)
		if err != nil {
			return nil, err
		}
		if _, exists := seen[converted]; exists {
			return nil, transfer.ErrUnsupportedEncoding
		}
		seen[converted] = struct{}{}
		result[index] = converted
	}
	return result, nil
}
