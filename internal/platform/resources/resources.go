// Package resources reports conservative static local-machine resource hints.
package resources

import "runtime"

// Snapshot contains stable resource hints safe to advertise as untrusted
// discovery metadata. Dynamic load, free memory, and GPU telemetry belong in a
// separate authenticated metrics path.
type Snapshot struct {
	LogicalCPUCount  uint32
	TotalMemoryBytes uint64
}

// Static returns best-effort static local-machine resources.
func Static() Snapshot {
	count := runtime.NumCPU()
	if count < 0 {
		count = 0
	}
	if count > 4096 {
		count = 4096
	}
	return Snapshot{
		LogicalCPUCount:  uint32(count),
		TotalMemoryBytes: totalMemoryBytes(),
	}
}
