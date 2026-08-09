package mapper

import (
	computehopv1 "github.com/austinjiann/spare-compute/gen/go/computehop/v1"
	"github.com/austinjiann/spare-compute/internal/transfer"
)

// ChunkEncodingToRemoteProto maps one supported content encoding to its wire value.
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

// ChunkEncodingFromRemoteProto validates and maps one wire content encoding.
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

// ChunkEncodingsToRemoteProto validates and maps a unique encoding preference list.
func ChunkEncodingsToRemoteProto(
	encodings []transfer.ChunkEncoding,
) ([]computehopv1.ChunkEncoding, error) {
	return mapChunkEncodings(encodings, ChunkEncodingToRemoteProto)
}

// ChunkEncodingsFromRemoteProto validates and maps a unique wire encoding list.
func ChunkEncodingsFromRemoteProto(
	encodings []computehopv1.ChunkEncoding,
) ([]transfer.ChunkEncoding, error) {
	return mapChunkEncodings(encodings, ChunkEncodingFromRemoteProto)
}

func mapChunkEncodings[source, target comparable](
	encodings []source,
	convert func(source) (target, error),
) ([]target, error) {
	if len(encodings) == 0 || len(encodings) > 2 {
		return nil, transfer.ErrUnsupportedEncoding
	}
	result := make([]target, len(encodings))
	seen := make(map[target]struct{}, len(encodings))
	for index, encoding := range encodings {
		converted, err := convert(encoding)
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
