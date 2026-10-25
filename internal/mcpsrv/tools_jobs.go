package mcpsrv

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// globalToolArgs are the flags shared by the two global-search commands.
type globalToolArgs struct {
	settings
	Stream             *bool    `json:"stream,omitempty" jsonschema:"stream the document to stdout as the run progresses (default on)"`
	KeepInfeasible     bool     `json:"keep_infeasible,omitempty" jsonschema:"keep infeasible basins in the document and the saved minima"`
	PowerSolve         bool     `json:"power_solve,omitempty" jsonschema:"insert the power-preserving glass phase between cycles"`
	PowerSolveSurfaces []string `json:"power_solve_surfaces,omitempty" jsonschema:"surface ids whose curvature is recomputed to hold the element power"`
	GlassVariables     bool     `json:"glass_variables,omitempty" jsonschema:"auto-generate nd/vd variables for every refractive element"`
	MemLimit           string   `json:"mem_limit,omitempty" jsonschema:"Go soft memory limit (e.g. 8GiB, -1 = unlimited)"`
	GOGC               int      `json:"gogc,omitempty" jsonschema:"GC target percentage (0 = the compute default)"`
}

func (g globalToolArgs) args() []string {
	args := g.settings.args()
	args = boolPtrFlag(args, g.Stream, "--stream")
	args = boolFlag(args, g.KeepInfeasible, "--keep-infeasible")
	args = boolFlag(args, g.PowerSolve, "--power-solve")
	args = listFlag(args, g.PowerSolveSurfaces, "--power-solve-surfaces")
	args = boolFlag(args, g.GlassVariables, "--glass-variables")
	args = strFlag(args, g.MemLimit, "--mem-limit")
	args = intFlag(args, g.GOGC, "--gogc")
	return args
}

type escapeIn struct {
	DocInput
	globalToolArgs
}

type psoIn struct {
	DocInput
	globalToolArgs
	SwarmSize         int     `json:"swarm_size,omitempty" jsonschema:"particles per worker (overrides optimization.pso.swarm_size)"`
	Iterations        int     `json:"iterations,omitempty" jsonschema:"PSO iterations per cycle"`
	ConstraintPenalty float64 `json:"constraint_penalty,omitempty" jsonschema:"constraint penalty weight"`
	Gbest             string  `json:"gbest,omitempty" jsonschema:"scale the global best is ranked on: merit (default) or another scale"`
}

func (s *server) addEscape() {
	mcp.AddTool(s.mcp, &mcp.Tool{
		Name:        "escape",
		Title:       "Escape-function global search (job)",
		Description: "Start an escape-function global optimisation as a detached job and return immediately with a job id. Poll job_status, fetch job_result when finished, stop with job_stop (two graceful stages - never a force-kill, which loses the assembled document). The search runs for hours; the run survives an MCP server restart because it is started in its own session.",
		Annotations: toolAnnotations(false),
	}, func(ctx context.Context, req *mcp.CallToolRequest, in escapeIn) (*mcp.CallToolResult, JobStatus, error) {
		doc, _, err := s.store.Resolve(in.DocInput)
		if err != nil {
			return nil, JobStatus{}, err
		}
		args := in.globalToolArgs.args()
		meta, err := s.jobs.Start("escape", args, doc)
		if err != nil {
			return nil, JobStatus{}, err
		}
		st, _ := s.jobs.Status(meta.ID)
		return jobContent(st), st, nil
	})
}

func (s *server) addPSO() {
	mcp.AddTool(s.mcp, &mcp.Tool{
		Name:        "pso",
		Title:       "Particle-swarm global search (job)",
		Description: "Start a particle-swarm global optimisation as a detached job and return immediately with a job id. Same job model as escape: poll job_status, fetch job_result, stop with job_stop.",
		Annotations: toolAnnotations(false),
	}, func(ctx context.Context, req *mcp.CallToolRequest, in psoIn) (*mcp.CallToolResult, JobStatus, error) {
		doc, _, err := s.store.Resolve(in.DocInput)
		if err != nil {
			return nil, JobStatus{}, err
		}
		args := in.globalToolArgs.args()
		args = intFlag(args, in.SwarmSize, "--swarm-size")
		args = intFlag(args, in.Iterations, "--pso-iterations")
		args = floatFlag(args, in.ConstraintPenalty, "--constraint-penalty")
		args = strFlag(args, in.Gbest, "--gbest")
		meta, err := s.jobs.Start("pso", args, doc)
		if err != nil {
			return nil, JobStatus{}, err
		}
		st, _ := s.jobs.Status(meta.ID)
		return jobContent(st), st, nil
	})
}

// ---- job management -------------------------------------------------------

type jobIDIn struct {
	ID string `json:"id" jsonschema:"job id returned by escape/pso"`
}

type jobStopIn struct {
	ID           string `json:"id" jsonschema:"job id returned by escape/pso"`
	GraceSeconds int    `json:"grace_seconds,omitempty" jsonschema:"seconds between the two stop stages (default 5)"`
	WaitSeconds  int    `json:"wait_seconds,omitempty" jsonschema:"seconds to wait for the exit (default 60)"`
}

type jobListOut struct {
	Jobs []JobStatus `json:"jobs"`
}

func (s *server) addJobTools() {
	mcp.AddTool(s.mcp, &mcp.Tool{
		Name:        "job_status",
		Title:       "Global-search job status",
		Description: "Report a detached escape/pso job: state, pid, elapsed time, the last progress event (cycle, best merit, minima) and the artefact sizes. Cheap to poll; does not disturb the run.",
		Annotations: toolAnnotations(true),
	}, func(ctx context.Context, req *mcp.CallToolRequest, in jobIDIn) (*mcp.CallToolResult, JobStatus, error) {
		st, err := s.jobs.Status(in.ID)
		if err != nil {
			return nil, JobStatus{}, err
		}
		return jobContent(st), st, nil
	})

	mcp.AddTool(s.mcp, &mcp.Tool{
		Name:        "job_stop",
		Title:       "Stop a global-search job gracefully",
		Description: "Stop a running escape/pso job with the two-stage stop the optimisers expect: the first lets the current DLS finish (it can take minutes), the second aborts it within one iteration. The run still writes its document and exits 0. Unix sends SIGTERM twice; Windows writes the stages to the job's control file. Never force-kills.",
		Annotations: toolAnnotations(false),
	}, func(ctx context.Context, req *mcp.CallToolRequest, in jobStopIn) (*mcp.CallToolResult, JobStatus, error) {
		grace := time.Duration(in.GraceSeconds) * time.Second
		wait := time.Duration(in.WaitSeconds) * time.Second
		st, err := s.jobs.Stop(in.ID, grace, wait)
		if err != nil {
			if st.ID != "" {
				return jobContent(st), st, err
			}
			return nil, JobStatus{}, err
		}
		return jobContent(st), st, nil
	})

	mcp.AddTool(s.mcp, &mcp.Tool{
		Name:        "job_result",
		Title:       "Finished job result",
		Description: "Ingest the document a finished escape/pso job wrote: returns a document id, the compact result summary (best minimum, basins) and the saved minima files. Fails while the job is still running - the document is written at the very end.",
		Annotations: toolAnnotations(false),
	}, func(ctx context.Context, req *mcp.CallToolRequest, in jobIDIn) (*mcp.CallToolResult, ResultOut, error) {
		meta, data, st, err := s.jobs.Result(in.ID)
		if err != nil {
			return nil, ResultOut{}, err
		}
		stored, err := s.store.Put(meta.Command, data, "")
		if err != nil {
			return nil, ResultOut{}, err
		}
		summary, truncated := s.summarize(ctx, meta.Command, data)
		st.DocumentID = stored.ID
		out := ResultOut{
			DocumentID:  stored.ID,
			DocumentURI: s.store.URI(stored.ID),
			Summary:     summary,
			Warnings:    st.Warnings,
			ElapsedMS:   st.ElapsedMS,
			Truncated:   truncated,
		}
		res := s.docResult(out, stored)
		text := fmt.Sprintf("job %s %s; document %s\n", st.ID, st.State, stored.ID)
		res.Content = append([]mcp.Content{&mcp.TextContent{Text: text}}, res.Content...)
		return res, out, nil
	})

	mcp.AddTool(s.mcp, &mcp.Tool{
		Name:        "job_list",
		Title:       "List global-search jobs",
		Description: "List every escape/pso job in the workspace with its state and elapsed time (newest first). Survives a server restart: jobs live on disk.",
		Annotations: toolAnnotations(true),
	}, func(ctx context.Context, req *mcp.CallToolRequest, _ struct{}) (*mcp.CallToolResult, jobListOut, error) {
		jobs, err := s.jobs.List()
		if err != nil {
			return nil, jobListOut{}, err
		}
		var b strings.Builder
		if len(jobs) == 0 {
			b.WriteString("no jobs in this workspace\n")
		}
		for _, j := range jobs {
			fmt.Fprintf(&b, "%s  %-6s  %-8s  %s\n", j.ID, j.Command, j.State, formatElapsed(j.ElapsedMS))
		}
		return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: b.String()}}}, jobListOut{Jobs: jobs}, nil
	})
}

// jobContent renders a job status as text.
func jobContent(st JobStatus) *mcp.CallToolResult {
	var b strings.Builder
	fmt.Fprintf(&b, "job %s (%s): %s\n", st.ID, st.Command, st.State)
	fmt.Fprintf(&b, "pid %d, elapsed %s\n", st.PID, formatElapsed(st.ElapsedMS))
	if st.Progress != nil {
		fmt.Fprintf(&b, "progress: %v\n", st.Progress)
	}
	if out, ok := st.Artifacts["output"].(map[string]any); ok {
		fmt.Fprintf(&b, "output: %v (%v bytes)\n", out["path"], out["bytes"])
	}
	if len(st.Warnings) > 0 {
		b.WriteString("warnings:\n")
		for _, w := range st.Warnings {
			fmt.Fprintf(&b, "- %s\n", w)
		}
	}
	if st.State == "finished" {
		b.WriteString("finished: fetch the document with job_result\n")
	}
	return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: b.String()}}}
}

func formatElapsed(ms int64) string {
	d := time.Duration(ms) * time.Millisecond
	if d < time.Minute {
		return fmt.Sprintf("%.0fs", d.Seconds())
	}
	if d < time.Hour {
		return fmt.Sprintf("%dm%02ds", int(d.Minutes()), int(d.Seconds())%60)
	}
	return fmt.Sprintf("%dh%02dm", int(d.Hours()), int(d.Minutes())%60)
}
