package mcpsrv

import (
	"bufio"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/hiroki/rayweaver/internal/runctl"
)

// JobMeta is the on-disk record of a detached global search. The files are the
// truth: the server can be restarted (or die) and job_status/job_result still
// answer from <workspace>/jobs/<id>.
type JobMeta struct {
	ID         string    `json:"id"`
	Command    string    `json:"command"`
	Args       []string  `json:"args"`
	StartedAt  time.Time `json:"started_at"`
	PID        int       `json:"pid"`
	Dir        string    `json:"dir"`
	Input      string    `json:"input"`
	Output     string    `json:"output"`
	Stderr     string    `json:"stderr"`
	Progress   string    `json:"progress"`
	Minima     string    `json:"minima"`
	StopFile   string    `json:"stop_file,omitempty" jsonschema:"Windows control file for the staged stop"`
	WorkingDir string    `json:"working_dir"`
}

// JobStatus is the view of a job at one instant.
type JobStatus struct {
	ID         string         `json:"id"`
	Command    string         `json:"command"`
	State      string         `json:"state" jsonschema:"running, finished or failed"`
	PID        int            `json:"pid"`
	Running    bool           `json:"running"`
	ElapsedMS  int64          `json:"elapsed_ms"`
	Progress   map[string]any `json:"progress,omitempty" jsonschema:"the last progress event: cycle, best merit, minima, time out"`
	Warnings   []string       `json:"warnings,omitempty"`
	Artifacts  map[string]any `json:"artifacts,omitempty" jsonschema:"output, stderr, progress log and saved minima: path and size"`
	LastLog    []string       `json:"last_log,omitempty"`
	DocumentID string         `json:"document_id,omitempty" jsonschema:"set by job_result once the run has finished"`
}

// Jobs owns the job directory and the concurrency cap.
type Jobs struct {
	root    string
	workdir string
	max     int
	binary  string
	mu      sync.Mutex
}

func (j *Jobs) dir(id string) string { return filepath.Join(j.root, id) }

// Start launches a detached run. baseArgs are the command-specific flags; the
// log and save paths are added here so every job leaves the same artefacts.
func (j *Jobs) Start(command string, baseArgs []string, doc []byte) (JobMeta, error) {
	if err := j.checkCapacity(); err != nil {
		return JobMeta{}, err
	}
	id := newJobID()
	dir := j.dir(id)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return JobMeta{}, err
	}
	meta := JobMeta{
		ID:         id,
		Command:    command,
		StartedAt:  time.Now().UTC(),
		Dir:        dir,
		Input:      filepath.Join(dir, "input.yaml"),
		Output:     filepath.Join(dir, "out.yaml"),
		Stderr:     filepath.Join(dir, "stderr.log"),
		Progress:   filepath.Join(dir, "progress.jsonl"),
		Minima:     filepath.Join(dir, "minima"),
		StopFile:   filepath.Join(dir, "stop"),
		WorkingDir: j.workdir,
	}
	if err := os.WriteFile(meta.Input, doc, 0o644); err != nil {
		return JobMeta{}, err
	}
	args := append([]string{}, baseArgs...)
	args = append(args, "--log", meta.Progress, "--save", meta.Minima)
	meta.Args = args

	pid, err := runctl.Launch(runctl.LaunchOptions{
		Binary:   j.binary,
		Args:     append([]string{command}, args...),
		Dir:      meta.WorkingDir,
		Stdin:    meta.Input,
		Stdout:   meta.Output,
		Stderr:   meta.Stderr,
		StopFile: meta.StopFile,
	})
	if err != nil {
		return JobMeta{}, err
	}
	meta.PID = pid
	if err := runctl.WritePid(filepath.Join(dir, "job.pid"), pid); err != nil {
		return JobMeta{}, err
	}
	if err := writeJSON(filepath.Join(dir, "meta.json"), meta); err != nil {
		return JobMeta{}, err
	}
	return meta, nil
}

func (j *Jobs) checkCapacity() error {
	if j.max <= 0 {
		return nil
	}
	j.mu.Lock()
	defer j.mu.Unlock()
	metas, _ := j.list()
	alive := 0
	for _, m := range metas {
		if runctl.PidAlive(m.PID) {
			alive++
		}
	}
	if alive >= j.max {
		return fmt.Errorf("the workspace already runs %d global searches (max %d); wait for one with job_status or stop it with job_stop", alive, j.max)
	}
	return nil
}

// Status reports one job from its files.
func (j *Jobs) Status(id string) (JobStatus, error) {
	meta, err := j.read(id)
	if err != nil {
		return JobStatus{}, err
	}
	st := JobStatus{
		ID:        meta.ID,
		Command:   meta.Command,
		PID:       meta.PID,
		ElapsedMS: time.Since(meta.StartedAt).Milliseconds(),
		Artifacts: map[string]any{},
	}
	st.Running = runctl.PidAlive(meta.PID)
	if st.Running {
		// A recycled pid must not look like a live job just because some
		// unrelated process inherited the number.
		if _, comm, perr := runctl.ProcInfo(meta.PID); perr == nil && comm != "" && !strings.Contains(comm, filepath.Base(j.binary)) {
			st.Running = false
		}
	}
	switch {
	case st.Running:
		st.State = "running"
	case fileSize(meta.Output) > 0:
		st.State = "finished"
	default:
		st.State = "failed"
	}
	st.Progress = readProgressTail(meta.Progress)
	st.Warnings = readWarnings(meta.Stderr, 50)
	st.LastLog = readTailLines(meta.Stderr, 5)
	for name, p := range map[string]string{
		"output": meta.Output, "stderr": meta.Stderr, "progress": meta.Progress, "minima": meta.Minima,
	} {
		st.Artifacts[name] = map[string]any{"path": relTo(j.root, p), "bytes": fileSize(p)}
	}
	return st, nil
}

// Stop sends the two-stage stop to a job: SIGTERM twice on Unix, stage numbers
// in the job's control file on Windows. It never force-kills, so the run still
// writes its document; the --save minima survive regardless.
func (j *Jobs) Stop(id string, grace, wait time.Duration) (JobStatus, error) {
	meta, err := j.read(id)
	if err != nil {
		return JobStatus{}, err
	}
	stopFile := meta.StopFile
	if stopFile == "" {
		stopFile = runctl.StopFilePath(filepath.Join(meta.Dir, "job.pid"))
	}
	if _, err := runctl.Stop(runctl.StopOptions{
		PidFile:  filepath.Join(meta.Dir, "job.pid"),
		StopFile: stopFile,
		Grace:    grace,
		Wait:     wait,
		Guard:    filepath.Base(j.binary),
	}); err != nil {
		// Still running after the wait (or a stale pid): report the status, but
		// keep the error so the caller knows the stop did not complete.
		st, _ := j.Status(id)
		return st, err
	}
	return j.Status(id)
}

// Result ingests the finished run's stdout as a document. It refuses while the
// process is still alive: the document is written at the very end.
func (j *Jobs) Result(id string) (JobMeta, []byte, JobStatus, error) {
	meta, err := j.read(id)
	if err != nil {
		return meta, nil, JobStatus{}, err
	}
	st, err := j.Status(id)
	if err != nil {
		return meta, nil, st, err
	}
	if st.Running {
		return meta, nil, st, fmt.Errorf("job %s is still running (pid %d); poll job_status, or stop it with job_stop", id, meta.PID)
	}
	data, err := os.ReadFile(meta.Output)
	if err != nil || len(data) == 0 {
		return meta, nil, st, fmt.Errorf("job %s produced no document (see its stderr log: %s)", id, relTo(j.root, meta.Stderr))
	}
	return meta, data, st, nil
}

// List returns every job in the workspace, newest first.
func (j *Jobs) List() ([]JobStatus, error) {
	metas, err := j.list()
	if err != nil {
		return nil, err
	}
	out := make([]JobStatus, 0, len(metas))
	for _, m := range metas {
		st, err := j.Status(m.ID)
		if err != nil {
			continue
		}
		out = append(out, st)
	}
	return out, nil
}

func (j *Jobs) list() ([]JobMeta, error) {
	entries, err := os.ReadDir(j.root)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var metas []JobMeta
	for _, e := range entries {
		if !e.IsDir() || !strings.HasPrefix(e.Name(), "job-") {
			continue
		}
		m, err := j.read(e.Name())
		if err != nil {
			continue
		}
		metas = append(metas, m)
	}
	sort.Slice(metas, func(a, b int) bool { return metas[a].StartedAt.After(metas[b].StartedAt) })
	return metas, nil
}

func (j *Jobs) read(id string) (JobMeta, error) {
	if !strings.HasPrefix(id, "job-") || strings.ContainsAny(id, `/\`) {
		return JobMeta{}, fmt.Errorf("invalid job id %q (expected job-...)", id)
	}
	b, err := os.ReadFile(filepath.Join(j.dir(id), "meta.json"))
	if err != nil {
		if os.IsNotExist(err) {
			return JobMeta{}, fmt.Errorf("unknown job %s (see job_list)", id)
		}
		return JobMeta{}, err
	}
	var m JobMeta
	if err := json.Unmarshal(b, &m); err != nil {
		return JobMeta{}, err
	}
	return m, nil
}

func newJobID() string {
	var b [2]byte
	_, _ = rand.Read(b[:])
	return "job-" + time.Now().UTC().Format("20060102-150405") + "-" + hex.EncodeToString(b[:])
}

func writeJSON(path string, v any) error {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, append(b, '\n'), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func fileSize(p string) int64 {
	st, err := os.Stat(p)
	if err != nil {
		return 0
	}
	return st.Size()
}

func relTo(root, p string) string {
	if r, err := filepath.Rel(root, p); err == nil {
		return filepath.ToSlash(r)
	}
	return p
}

// readProgressTail returns the last progress event of a live JSONL stream,
// reduced to the numbers a caller polls for. The full log stays on disk.
func readProgressTail(path string) map[string]any {
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()
	var last map[string]any
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64<<10), 4<<20)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		var ev map[string]any
		if json.Unmarshal([]byte(line), &ev) != nil {
			continue
		}
		last = ev
	}
	if last == nil {
		return nil
	}
	out := map[string]any{}
	for _, k := range []string{"cycle", "event", "merit", "best_merit", "explored", "recorded", "minima", "e", "status", "timed_out", "interrupted", "workers"} {
		if v, ok := last[k]; ok {
			out[k] = v
		}
	}
	return out
}

func readWarnings(path string, limit int) []string {
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()
	var out []string
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64<<10), 4<<20)
	for sc.Scan() {
		body := sc.Text()
		if m := taggedLine.FindStringSubmatch(body); m != nil {
			body = m[1]
		}
		if strings.HasPrefix(body, "Warning:") {
			out = append(out, strings.TrimSpace(strings.TrimPrefix(body, "Warning:")))
			if len(out) >= limit {
				break
			}
		}
	}
	return out
}

func readTailLines(path string, n int) []string {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	lines := strings.Split(strings.TrimRight(string(data), "\n"), "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return lines
}
