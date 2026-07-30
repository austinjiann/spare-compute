//go:build darwin

package resources

import "golang.org/x/sys/unix"

func totalMemoryBytes() uint64 {
	value, err := unix.SysctlUint64("hw.memsize")
	if err != nil {
		return 0
	}
	return value
}
