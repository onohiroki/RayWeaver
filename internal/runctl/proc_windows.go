//go:build windows

package runctl

import (
	"fmt"
	"os"
	"strings"
	"syscall"
	"time"
	"unsafe"
)

// Windows has no setsid(2) and no SIGTERM. A detached process is created with
// its own (console-less) process group, and the staged stop travels through a
// small file the optimisers poll: stage 1 lets the running DLS finish, stage 2
// aborts it within one iteration. The stop file is created by Launch before the
// child starts.
const (
	useStopFile = true
	stopName    = "stop request"
	stopHint    = "two-stage stop file"
	doneAdvice  = "the run was not force-terminated; the document and the --save minima are on disk."
	killAdvice  = "the run was not force-terminated; re-run the stop or inspect it (the --save minima survive)"

	// detachedProcess is DETACHED_PROCESS: the child has no console. Combined
	// with CREATE_NEW_PROCESS_GROUP it is also out of reach of a Ctrl+C sent to
	// the launcher's console.
	detachedProcess = 0x00000008

	errorInvalidParameter = syscall.Errno(87)
)

func sysProcAttr() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{
		CreationFlags: syscall.CREATE_NEW_PROCESS_GROUP | detachedProcess,
	}
}

// requestStop appends the stage number to the file the child watches. Appending
// (rather than rewriting) means a stage is never lost if the watcher reads in
// the middle of a write; the reader takes the highest number it has seen.
func requestStop(pid, stage int, stopFile string) error {
	if stopFile == "" {
		return fmt.Errorf("no stop file for pid %d: the run was not launched with %s", pid, StopFileEnv)
	}
	f, err := os.OpenFile(stopFile, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = fmt.Fprintf(f, "%d\n", stage)
	return err
}

// pidAlive opens the process and checks whether it has signalled. ACCESS_DENIED
// means it exists but belongs to another user, which still counts as alive (the
// Unix EPERM case).
func pidAlive(pid int) bool {
	h, err := syscall.OpenProcess(syscall.SYNCHRONIZE|syscall.PROCESS_QUERY_INFORMATION, false, uint32(pid))
	if err != nil {
		if err == errorInvalidParameter {
			return false
		}
		return true
	}
	defer syscall.CloseHandle(h)
	event, err := syscall.WaitForSingleObject(h, 0)
	if err != nil || event == syscall.WAIT_FAILED {
		return true
	}
	return event == syscall.WAIT_TIMEOUT
}

// procInfo reports the elapsed time since the process was created and its image
// path. QueryFullProcessImageName is not in package syscall, so it is resolved
// lazily; keeping runctl on the standard library avoids making x/sys a direct
// dependency for one call.
func procInfo(pid int) (string, string, error) {
	h, err := syscall.OpenProcess(syscall.PROCESS_QUERY_INFORMATION, false, uint32(pid))
	if err != nil {
		return "", "", err
	}
	defer syscall.CloseHandle(h)

	var creation, exit, kernel, user syscall.Filetime
	if err := syscall.GetProcessTimes(h, &creation, &exit, &kernel, &user); err != nil {
		return "", "", err
	}
	elapsed := formatElapsed(time.Since(time.Unix(0, creation.Nanoseconds())))
	comm, err := processImage(h)
	if err != nil {
		return elapsed, "", err
	}
	return elapsed, comm, nil
}

var (
	modkernel32                    = syscall.NewLazyDLL("kernel32.dll")
	procQueryFullProcessImageNameW = modkernel32.NewProc("QueryFullProcessImageNameW")
)

func processImage(h syscall.Handle) (string, error) {
	buf := make([]uint16, 1024)
	size := uint32(len(buf))
	r1, _, callErr := procQueryFullProcessImageNameW.Call(
		uintptr(h), 0, uintptr(unsafe.Pointer(&buf[0])), uintptr(unsafe.Pointer(&size)))
	if r1 == 0 {
		return "", callErr
	}
	return syscall.UTF16ToString(buf[:size]), nil
}

// formatElapsed renders a duration the way -status displays elapsed time. It is
// not ps's "etime" format, but the information is the same.
func formatElapsed(d time.Duration) string {
	if d < 0 {
		d = 0
	}
	d = d.Round(time.Second)
	h := d / time.Hour
	d -= h * time.Hour
	m := d / time.Minute
	s := (d - m*time.Minute) / time.Second
	switch {
	case h > 0:
		return fmt.Sprintf("%dh%02dm%02ds", h, m, s)
	case m > 0:
		return fmt.Sprintf("%dm%02ds", m, s)
	default:
		return fmt.Sprintf("%ds", s)
	}
}

// guardMatches compares the image path case-insensitively: Windows paths are
// not case-sensitive, and callers pass either the full path or its basename.
func guardMatches(comm, guard string) bool {
	return strings.Contains(strings.ToLower(comm), strings.ToLower(guard))
}
