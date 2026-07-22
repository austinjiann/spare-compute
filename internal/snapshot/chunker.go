package snapshot

import (
	"context"
	"errors"
	"io"
)

var gearTable = func() [256]uint64 {
	var table [256]uint64
	value := uint64(0x9e3779b97f4a7c15)
	for index := range table {
		value += 0x9e3779b97f4a7c15
		mixed := value
		mixed = (mixed ^ (mixed >> 30)) * 0xbf58476d1ce4e5b9
		mixed = (mixed ^ (mixed >> 27)) * 0x94d049bb133111eb
		table[index] = mixed ^ (mixed >> 31)
	}
	return table
}()

// ChunkReader splits contents on deterministic content-defined boundaries.
// The callback owns no bytes after it returns.
func ChunkReader(
	ctx context.Context,
	reader io.Reader,
	consume func([]byte) error,
) error {
	if reader == nil || consume == nil {
		return ErrInvalidManifest
	}
	chunk := make([]byte, 0, AverageChunkBytes)
	buffer := make([]byte, 128<<10)
	var rolling uint64
	emptyReads := 0
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		read, readErr := reader.Read(buffer)
		if read == 0 && readErr == nil {
			emptyReads++
			if emptyReads >= 100 {
				return io.ErrNoProgress
			}
			continue
		}
		emptyReads = 0
		for _, value := range buffer[:read] {
			chunk = append(chunk, value)
			rolling = (rolling << 1) + gearTable[value]
			if len(chunk) < MinimumChunkBytes ||
				rolling&uint64(AverageChunkBytes-1) != 0 && len(chunk) < MaximumChunkBytes {
				continue
			}
			if err := consume(chunk); err != nil {
				return err
			}
			chunk = make([]byte, 0, AverageChunkBytes)
			rolling = 0
		}
		if readErr != nil {
			if !errors.Is(readErr, io.EOF) {
				return readErr
			}
			if len(chunk) > 0 {
				return consume(chunk)
			}
			return nil
		}
	}
}
