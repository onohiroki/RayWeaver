//go:build linux

package resource

import (
	"os"
	"strconv"
	"strings"
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

// systemSample derives the system memory state from /proc:
//   - /proc/pressure/memory (PSI): the some-avg10 stall percentage maps to the
//     macOS-like pressure level (>=5 % -> 4 critical, >=0.5 % -> 2 warning),
//   - /proc/meminfo: MemAvailable/MemTotal as the free-memory percentage,
//   - /proc/vmstat: pswpin+pswpout as the compaction/swap activity counter.
func systemSample() (pressure, memLevel uint32, compaction uint64) {
	if b, err := os.ReadFile("/proc/pressure/memory"); err == nil {
		for _, line := range strings.Split(string(b), "\n") {
			if !strings.HasPrefix(line, "some ") {
				continue
			}
			for _, f := range strings.Fields(line) {
				if strings.HasPrefix(f, "avg10=") {
					if v, err := strconv.ParseFloat(strings.TrimPrefix(f, "avg10="), 64); err == nil {
						switch {
						case v >= 5:
							pressure = 4
						case v >= 0.5:
							pressure = 2
						default:
							pressure = 1
						}
					}
				}
			}
			break
		}
	}
	if b, err := os.ReadFile("/proc/meminfo"); err == nil {
		var total, avail float64
		for _, line := range strings.Split(string(b), "\n") {
			f := strings.Fields(line)
			if len(f) < 2 {
				continue
			}
			v, err := strconv.ParseFloat(f[1], 64)
			if err != nil {
				continue
			}
			switch f[0] {
			case "MemTotal:":
				total = v
			case "MemAvailable:":
				avail = v
			}
		}
		if total > 0 {
			memLevel = uint32(100 * avail / total)
		}
	}
	if b, err := os.ReadFile("/proc/vmstat"); err == nil {
		for _, line := range strings.Split(string(b), "\n") {
			f := strings.Fields(line)
			if len(f) == 2 && (f[0] == "pswpin" || f[0] == "pswpout") {
				if v, err := strconv.ParseUint(f[1], 10, 64); err == nil {
					compaction += v
				}
			}
		}
	}
	return
}

// totalRAMBytes returns the physical RAM from /proc/meminfo MemTotal.
func totalRAMBytes() uint64 {
	b, err := os.ReadFile("/proc/meminfo")
	if err != nil {
		return 0
	}
	for _, line := range strings.Split(string(b), "\n") {
		f := strings.Fields(line)
		if len(f) >= 2 && f[0] == "MemTotal:" {
			if v, err := strconv.ParseUint(f[1], 10, 64); err == nil {
				return v * 1024 // kB -> bytes
			}
		}
	}
	return 0
}
