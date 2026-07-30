//go:build !darwin && !linux

package resources

func totalMemoryBytes() uint64 {
	return 0
}
