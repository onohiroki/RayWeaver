//go:build !windows

package runctl

import (
	"fmt"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
)

// Unix keeps the signal mechanism: SIGTERM is how escape/pso/optimize advance
// their stop stages, and a detached session is what makes the run outlive the
// launching shell.
const (
	useStopFile = false
	stopName    = "SIGTERM"
	stopHint    = "SIGTERM twice"
	doneAdvice  = "never use kill -9: it always loses the assembled document (the --save minima survive)."
	killAdvice  = "do NOT send kill -9 (it always loses the assembled document) - re-run the stop, or accept the --save minima"
)

// sysProcAttr puts the child in its own session.
//
// Setsid alone: setsid(2) creates a new session with no controlling terminal,
// which is exactly the isolation needed. Do NOT add SysProcAttr.Noctty here -
// on darwin it issues ioctl(0, TIOCNOTTY), which fails with ENOTTY whenever fd 0
// is not a terminal (a pipe, /dev/null, a script), i.e. exactly how a long job
// is normally launched. It is also redundant: a session leader has no
// controlling tty to begin with.
func sysProcAttr() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{Setsid: true}
}

// requestStop sends the stage's SIGTERM. The optimisers advance one stage per
// signal, so stage is not part of the message.
func requestStop(pid, stage int, _ string) error {
	return syscall.Kill(pid, syscall.SIGTERM)
}

// pidAlive reports whether a signal can be delivered to pid. EPERM means it
// exists but belongs to someone else, which still counts as alive.
func pidAlive(pid int) bool {
	err := syscall.Kill(pid, 0)
	return err == nil || err == syscall.EPERM
}

// procInfo returns the elapsed time and the command name of pid, via ps.
func procInfo(pid int) (string, string, error) {
	out, err := exec.Command("ps", "-o", "etime=,comm=", "-p", strconv.Itoa(pid)).Output()
	if err != nil {
		return "", "", err
	}
	f := strings.Fields(string(out))
	if len(f) == 0 {
		return "", "", fmt.Errorf("no ps output for pid %d", pid)
	}
	comm := f[len(f)-1]
	elapsed := strings.Join(f[:len(f)-1], " ")
	return elapsed, comm, nil
}

// guardMatches checks the pid's command name against the expected executable.
// Unix paths are case-sensitive.
func guardMatches(comm, guard string) bool {
	return strings.Contains(comm, guard)
}
