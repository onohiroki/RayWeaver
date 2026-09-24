//go:build windows

package resource

import (
	"syscall"
	"unsafe"
)

var procGlobalMemoryStatusEx = syscall.NewLazyDLL("kernel32.dll").NewProc("GlobalMemoryStatusEx")

type memoryStatusEx struct {
	Length               uint32
	MemoryLoad           uint32
	TotalPhys            uint64
	AvailPhys            uint64
	TotalPageFile        uint64
	AvailPageFile        uint64
	TotalVirtual         uint64
	AvailVirtual         uint64
	AvailExtendedVirtual uint64
}

// cpuSeconds returns the cumulative process CPU time via GetProcessTimes.
func cpuSeconds() float64 {
	h, err := syscall.GetCurrentProcess()
	if err != nil {
		return 0
	}
	var creation, exit, kernel, user syscall.Filetime
	if err := syscall.GetProcessTimes(h, &creation, &exit, &kernel, &user); err != nil {
		return 0
	}
	secs := func(ft syscall.Filetime) float64 {
		return float64(uint64(ft.HighDateTime)<<32|uint64(ft.LowDateTime)) / 1e7
	}
	return secs(kernel) + secs(user)
}

// systemSample reads GlobalMemoryStatusEx: dwMemoryLoad maps to the macOS-like
// pressure level (>=90 critical, >=70 warning) and 100−dwMemoryLoad to the
// free-memory percentage. Windows exposes no public compaction counter.
func systemSample() (pressure, memLevel uint32, compaction uint64) {
	var m memoryStatusEx
	m.Length = uint32(unsafe.Sizeof(m))
	r, _, _ := procGlobalMemoryStatusEx.Call(uintptr(unsafe.Pointer(&m)))
	if r == 0 {
		return 0, 0, 0
	}
	memLevel = 100 - m.MemoryLoad
	switch {
	case m.MemoryLoad >= 90:
		pressure = 4
	case m.MemoryLoad >= 70:
		pressure = 2
	default:
		pressure = 1
	}
	return
}

// totalRAMBytes returns the physical RAM from GlobalMemoryStatusEx.
func totalRAMBytes() uint64 {
	var m memoryStatusEx
	m.Length = uint32(unsafe.Sizeof(m))
	if r, _, _ := procGlobalMemoryStatusEx.Call(uintptr(unsafe.Pointer(&m))); r != 0 {
		return m.TotalPhys
	}
	return 0
}
