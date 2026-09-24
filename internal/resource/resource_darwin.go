//go:build darwin

package resource

import (
	"syscall"
)

// cpuSeconds returns the cumulative process CPU time via getrusage.
func cpuSeconds() float64 {
	var ru syscall.Rusage
	if err := syscall.Getrusage(syscall.RUSAGE_SELF, &ru); err != nil {
		return 0
	}
	return float64(ru.Utime.Sec+ru.Stime.Sec) +
		float64(int64(ru.Utime.Usec)+int64(ru.Stime.Usec))/1e6
}

// systemSample reads the macOS memory-pressure sysctls:
//   - kern.memorystatus_vm_pressure_level: 1 normal, 2 warning, 4 critical
//   - kern.memorystatus_level: free-ish memory percentage
//   - vm.compressor.swapper.wakeups.total: cumulative compression activity
func systemSample() (pressure, memLevel uint32, compaction uint64) {
	if v, err := syscall.SysctlUint32("kern.memorystatus_vm_pressure_level"); err == nil {
		pressure = v
	}
	if v, err := syscall.SysctlUint32("kern.memorystatus_level"); err == nil {
		memLevel = v
	}
	if s, err := syscall.Sysctl("vm.compressor.swapper.wakeups.total"); err == nil {
		compaction = decodeLEUint([]byte(s))
	}
	return
}

// totalRAMBytes returns the physical RAM from sysctl hw.memsize.
func totalRAMBytes() uint64 {
	s, err := syscall.Sysctl("hw.memsize")
	if err != nil {
		return 0
	}
	return decodeLEUint([]byte(s))
}
