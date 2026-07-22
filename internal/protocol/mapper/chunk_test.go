package mapper

import (
	"errors"
	"testing"

	computehopv1 "github.com/austinjiann/spare-compute/gen/go/computehop/v1"
	"github.com/austinjiann/spare-compute/internal/transfer"
)

func TestChunkEncodingMappingRoundTripsAndRejectsUnknownValues(t *testing.T) {
	want := transfer.SupportedChunkEncodings()
	message, err := ChunkEncodingsToRemoteProto(want)
	if err != nil {
		t.Fatal(err)
	}
	got, err := ChunkEncodingsFromRemoteProto(message)
	if err != nil || len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("round trip = %#v, %v", got, err)
	}
	if _, err := ChunkEncodingsFromRemoteProto([]computehopv1.ChunkEncoding{99}); !errors.Is(err, transfer.ErrUnsupportedEncoding) {
		t.Fatalf("unknown encoding error = %v", err)
	}
	if _, err := ChunkEncodingsFromRemoteProto([]computehopv1.ChunkEncoding{
		computehopv1.ChunkEncoding_CHUNK_ENCODING_ZSTD,
		computehopv1.ChunkEncoding_CHUNK_ENCODING_ZSTD,
	}); !errors.Is(err, transfer.ErrUnsupportedEncoding) {
		t.Fatalf("duplicate encoding error = %v", err)
	}
}
