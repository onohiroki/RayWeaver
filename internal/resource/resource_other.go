//go:build !darwin && !linux && !windows

package resource

// cpuSeconds is unavailable on this platform.
func cpuSeconds() float64 { return 0 }

// systemSample is unavailable on this platform.
func systemSample() (pressure, memLevel uint32, compaction uint64) { return 0, 0, 0 }

// totalRAMBytes is unavailable on this platform.
func totalRAMBytes() uint64 { return 0 }
