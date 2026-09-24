// Package resource samples the optimizer process's own runtime state and, where
// the operating system exposes it, the system memory-pressure state. It backs
// the escape resource guard: a low-frequency self-monitor that can retire
// workers when memory pressure builds up.
//
// The process-side signals (heap, GC, goroutines) are pure Go and available on
// every platform. The system-side signals (memory pressure level, free-memory
// level, compaction/swap activity) are OS-specific and are provided by
// build-tagged files; unsupported platforms return zero values so the guard
// still works from the process-side signals alone.
package resource

import (
	"runtime"
)

// Sample is a point-in-time snapshot of the process and system memory state.
// Every field is best-effort: an unsupported signal is left at zero.
type Sample struct {
	// Process state (pure Go, every platform).
	HeapAllocMB float64
	HeapSysMB   float64
	HeapInuseMB float64
	Goroutines  int
	GCCount     uint32
	GCPauseMS   float64
	CPUSeconds  float64 // cumulative process CPU time (0 when unavailable)

	// System state (OS-specific, zero when unavailable).
	//
	// PressureLevel follows the macOS kern.memorystatus_vm_pressure_level
	// convention: 1 = normal, 2 = warning, 4 = critical. Linux derives an
	// equivalent level from the PSI some-avg10 stall percentage; Windows from
	// GlobalMemoryStatusEx's dwMemoryLoad.
	PressureLevel uint32
	// MemoryLevel is a free-ish memory percentage (macOS
	// kern.memorystatus_level; Linux MemAvailable/MemTotal; Windows
	// 100−dwMemoryLoad). 0 means unknown.
	MemoryLevel uint32
	// CompactionActivity is a cumulative counter of memory-pressure activity:
	// macOS vm.compressor.swapper.wakeups.total, Linux pswpin+pswpout, Windows
	// unavailable (0). Its delta over time is the activity rate.
	CompactionActivity uint64
}

// SampleNow reads the process runtime state and the OS system-memory signals.
func SampleNow() Sample {
	var ms runtime.MemStats
	runtime.ReadMemStats(&ms)
	s := Sample{
		HeapAllocMB: float64(ms.HeapAlloc) / (1 << 20),
		HeapSysMB:   float64(ms.HeapSys) / (1 << 20),
		HeapInuseMB: float64(ms.HeapInuse) / (1 << 20),
		Goroutines:  runtime.NumGoroutine(),
		GCCount:     ms.NumGC,
		GCPauseMS:   float64(ms.PauseTotalNs) / 1e6,
		CPUSeconds:  cpuSeconds(),
	}
	s.PressureLevel, s.MemoryLevel, s.CompactionActivity = systemSample()
	return s
}

// TotalRAMMB reports the physical RAM in MiB, or 0 when the platform does not
// expose it. It backs the resource guard's automatic heap ceiling (30 % of RAM).
func TotalRAMMB() float64 {
	b := totalRAMBytes()
	if b == 0 {
		return 0
	}
	return float64(b) / (1 << 20)
}

// decodeLEUint decodes a little-endian unsigned integer from raw sysctl bytes.
// syscall.Sysctl strips a trailing NUL byte, so an 8-byte value whose top byte
// is zero (e.g. hw.memsize = 24 GiB) arrives as 7 bytes; zero-extending the
// available bytes handles any length.
func decodeLEUint(b []byte) uint64 {
	var v uint64
	for i := 0; i < len(b) && i < 8; i++ {
		v |= uint64(b[i]) << (8 * uint(i))
	}
	return v
}
