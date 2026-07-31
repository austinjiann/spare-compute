//go:build linux

package resources

import "golang.org/x/sys/unix"

func totalMemoryBytes() uint64 {
	var info unix.Sysinfo_t
	if err := unix.Sysinfo(&info); err != nil {
		return 0
	}
	unit := uint64(info.Unit)
	if unit == 0 {
		unit = 1
	}
	return uint64(info.Totalram) * unit
}
