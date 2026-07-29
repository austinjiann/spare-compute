package transfer

import (
	"bytes"
	"crypto/rand"
	"errors"
	"testing"

	"github.com/austinjiann/spare-compute/internal/snapshot"
)

func TestChunkEncodingUsesZstdOnlyWhenItSavesBytes(t *testing.T) {
	compressible := bytes.Repeat([]byte("computehop-output\n"), 8_192)
	encoded, err := EncodeChunk(compressible, SupportedChunkEncodings())
	if err != nil {
		t.Fatal(err)
	}
	if encoded.Encoding != EncodingZstd || len(encoded.Data) >= len(compressible) {
		t.Fatalf("compressed chunk = %#v", encoded)
	}
	decoded, err := DecodeChunk(encoded)
	if err != nil || !bytes.Equal(decoded, compressible) {
		t.Fatalf("DecodeChunk() = %d bytes, %v", len(decoded), err)
	}

	incompressible := make([]byte, 128<<10)
	if _, err := rand.Read(incompressible); err != nil {
		t.Fatal(err)
	}
	encoded, err = EncodeChunk(incompressible, SupportedChunkEncodings())
	if err != nil {
		t.Fatal(err)
	}
	if encoded.Encoding != EncodingIdentity || !bytes.Equal(encoded.Data, incompressible) {
		t.Fatalf("incompressible encoding = %d, %d bytes", encoded.Encoding, len(encoded.Data))
	}
}

func TestChunkDecodingRejectsCorruptionSizeMismatchAndUnknownEncoding(t *testing.T) {
	contents := bytes.Repeat([]byte("x"), 128<<10)
	encoded, err := EncodeChunk(contents, []ChunkEncoding{EncodingZstd})
	if err != nil {
		t.Fatal(err)
	}
	corrupt := encoded
	corrupt.Data = append([]byte(nil), corrupt.Data...)
	corrupt.Data[len(corrupt.Data)/2] ^= 0xff
	if _, err := DecodeChunk(corrupt); !errors.Is(err, ErrInvalidChunk) {
		t.Fatalf("corrupt DecodeChunk() error = %v", err)
	}
	wrongSize := encoded
	wrongSize.UncompressedSize--
	if _, err := DecodeChunk(wrongSize); !errors.Is(err, ErrInvalidChunk) {
		t.Fatalf("wrong-size DecodeChunk() error = %v", err)
	}
	if _, err := DecodeChunk(Chunk{
		Encoding: 99, Data: []byte("x"), UncompressedSize: 1,
	}); !errors.Is(err, ErrUnsupportedEncoding) {
		t.Fatalf("unknown DecodeChunk() error = %v", err)
	}
	if _, err := DecodeChunk(Chunk{
		Encoding: EncodingZstd, Data: []byte("x"), UncompressedSize: snapshot.MaximumChunkBytes + 1,
	}); !errors.Is(err, ErrInvalidChunk) {
		t.Fatalf("oversized DecodeChunk() error = %v", err)
	}
	encoder, _, err := codecs()
	if err != nil {
		t.Fatal(err)
	}
	oversizedFrame := encoder.EncodeAll(bytes.Repeat([]byte("x"), snapshot.MaximumChunkBytes+1), nil)
	if _, err := DecodeChunk(Chunk{
		Encoding: EncodingZstd, Data: oversizedFrame, UncompressedSize: snapshot.MaximumChunkBytes,
	}); !errors.Is(err, ErrInvalidChunk) {
		t.Fatalf("oversized frame DecodeChunk() error = %v", err)
	}
}

func TestChunkEncodingRejectsNoMutualEncoding(t *testing.T) {
	if _, err := EncodeChunk([]byte("payload"), nil); !errors.Is(err, ErrUnsupportedEncoding) {
		t.Fatalf("EncodeChunk() error = %v", err)
	}
	if _, err := EncodeChunk([]byte("payload"), []ChunkEncoding{99}); !errors.Is(err, ErrUnsupportedEncoding) {
		t.Fatalf("EncodeChunk() unknown error = %v", err)
	}
}
