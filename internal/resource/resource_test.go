package resource

import (
	"runtime"
	"testing"
)

// TestSampleNowSanity verifies the cross-platform signals are populated and the
// OS-level signals are read where the platform exposes them.
func TestSampleNowSanity(t *testing.T) {
	s := SampleNow()
	if s.HeapSysMB <= 0 {
		t.Errorf("HeapSysMB = %v, want > 0", s.HeapSysMB)
	}
	if s.Goroutines < 1 {
		t.Errorf("Goroutines = %d, want >= 1", s.Goroutines)
	}
	if runtime.GOOS == "darwin" || runtime.GOOS == "linux" {
		if s.PressureLevel == 0 {
			t.Errorf("PressureLevel = 0 on %s, want the OS signal populated", runtime.GOOS)
		}
		if s.MemoryLevel == 0 {
			t.Errorf("MemoryLevel = 0 on %s, want the OS signal populated", runtime.GOOS)
		}
	}
	t.Logf("heap_sys=%.1fMB heap_alloc=%.1fMB goroutines=%d gc=%d gc_pause=%.1fms cpu=%.3fs pressure=%d memlevel=%d compaction=%d",
		s.HeapSysMB, s.HeapAllocMB, s.Goroutines, s.GCCount, s.GCPauseMS,
		s.CPUSeconds, s.PressureLevel, s.MemoryLevel, s.CompactionActivity)
}
