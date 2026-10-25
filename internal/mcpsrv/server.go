// Package mcpsrv exposes RayWeaver to AI agents over the Model Context
// Protocol.
//
// The server is a protocol adapter, not a second implementation of the optics:
// every tool call builds a `rayweave` command line, feeds the pipeline document
// to its stdin, and turns its separate stdout/stderr into a document handle, a
// compact summary and a warnings list. That keeps the CLI the single source of
// truth for the YAML semantics and the CLI-wins/write-back rules, and it keeps
// `os.Exit`, package globals and long-running runs out of this process.
package mcpsrv

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// Config is the resolved server configuration.
type Config struct {
	// Binary is the rayweave executable (resolved with runctl.Resolve).
	Binary string
	// Workspace is the directory tool inputs and artefacts are confined to.
	Workspace string
	// GlassDir is the default AGF catalog directory (empty = the document's).
	GlassDir string
	// Tools selects the exposed tool set: "core" (default) or "all".
	Tools string
	// MaxInlineBytes caps an inline `document` argument; bigger documents must
	// be referenced by id or path.
	MaxInlineBytes int
	// MaxJobs caps concurrently running escape/pso jobs.
	MaxJobs int
	// CancelGrace is the delay between the two SIGTERMs when a synchronous
	// tool call is cancelled; CancelWait bounds the wait for the exit.
	CancelGrace time.Duration
	CancelWait  time.Duration
	// Version is reported to the client (build version).
	Version string
}

// DefaultConfig returns the documented defaults. The binary is resolved by New.
func DefaultConfig() Config {
	return Config{
		Tools:          "core",
		MaxInlineBytes: 2 << 20,
		MaxJobs:        2,
		CancelGrace:    5 * time.Second,
		CancelWait:     60 * time.Second,
		Version:        "dev",
	}
}

type server struct {
	cfg        Config
	mcp        *mcp.Server
	store      *Store
	runner     *Runner
	jobs       *Jobs
	docsDir    string
	samplesDir string
}

// New builds the MCP server: it resolves the binary, prepares the workspace and
// registers the tools and resources selected by cfg.Tools ("" means "core").
func New(cfg Config) (*mcp.Server, error) {
	cfg = withDefaults(cfg)
	if cfg.Binary == "" {
		return nil, errors.New("rayweave binary is required")
	}
	abs, err := filepath.Abs(cfg.Binary)
	if err != nil {
		return nil, err
	}
	if st, err := os.Stat(abs); err != nil || st.IsDir() {
		return nil, fmt.Errorf("rayweave binary %s: not an executable file", abs)
	}
	cfg.Binary = abs
	if cfg.GlassDir != "" {
		if g, err := filepath.Abs(cfg.GlassDir); err == nil {
			cfg.GlassDir = g
		}
	}
	ws, err := filepath.Abs(cfg.Workspace)
	if err != nil {
		return nil, err
	}
	cfg.Workspace = ws
	if err := os.MkdirAll(filepath.Join(ws, "documents"), 0o755); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Join(ws, "jobs"), 0o755); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Join(ws, "plots"), 0o755); err != nil {
		return nil, err
	}

	s := &server{
		cfg:    cfg,
		store:  &Store{root: ws, docs: filepath.Join(ws, "documents"), maxInline: cfg.MaxInlineBytes},
		runner: &Runner{binary: cfg.Binary, dir: ws, grace: cfg.CancelGrace, wait: cfg.CancelWait},
		jobs:   &Jobs{root: filepath.Join(ws, "jobs"), workdir: ws, max: cfg.MaxJobs, binary: cfg.Binary},
	}
	s.mcp = mcp.NewServer(&mcp.Implementation{
		Name:    "rayweave",
		Title:   "RayWeaver optics",
		Version: cfg.Version,
	}, &mcp.ServerOptions{Instructions: instructions})
	s.registerTools()
	s.registerResources()
	return s.mcp, nil
}

func withDefaults(cfg Config) Config {
	d := DefaultConfig()
	if cfg.Tools == "" {
		cfg.Tools = d.Tools
	}
	if cfg.MaxInlineBytes == 0 {
		cfg.MaxInlineBytes = d.MaxInlineBytes
	}
	if cfg.MaxJobs == 0 {
		cfg.MaxJobs = d.MaxJobs
	}
	if cfg.CancelGrace == 0 {
		cfg.CancelGrace = d.CancelGrace
	}
	if cfg.CancelWait == 0 {
		cfg.CancelWait = d.CancelWait
	}
	if cfg.Version == "" {
		cfg.Version = d.Version
	}
	if cfg.Workspace == "" {
		cfg.Workspace = ".rw-mcp"
	}
	return cfg
}

// instructions is delivered at initialize time. It states the rules that make
// the tools work: settings are flags (never YAML edits), documents are chained
// by id, and long searches are jobs.
const instructions = `RayWeaver is an optical-design engine. Every analysis reads a pipeline YAML
document and returns it with results appended.

Working rules:
- Input: pass exactly one of ` + "`document`" + ` (inline YAML), ` + "`document_id`" + ` (returned by an
  earlier call) or ` + "`path`" + ` (workspace-relative YAML file). Large documents should be
  chained by id: every tool result carries ` + "`document_id`" + `.
- Settings are CLI flags. Do not edit the YAML to change a setting; pass the
  tool argument instead (e.g. ` + "`config`" + `, ` + "`wavelength`" + `, ` + "`clear_aperture`" + `). The
  corresponding value is written back into the result document.
- Results are compact summaries; the full document is a resource
  (` + "`rayweave://documents/<id>`" + `). Read it only when the summary is not enough, and
  prefer ` + "`query`" + ` to extract one value instead of reading everything.
- ` + "`escape`" + ` and ` + "`pso`" + ` return a job immediately. Poll ` + "`job_status`" + `, fetch
  ` + "`job_result`" + ` when finished, and stop with ` + "`job_stop`" + ` (never kill -9: it
  always loses the assembled document).
- Use ` + "`pipeline`" + ` for multi-stage runs (chief → trace → plot): it avoids passing
  the large intermediate documents back and forth.
- ` + "`list`" + ` and ` + "`query`" + ` are read-only views; ` + "`validate`" + ` checks a document
  parses before a longer run.
- Manuals are resources under ` + "`rayweave://docs/`" + ` and demo inputs under
  ` + "`rayweave://samples/`" + `.`

// ResultOut is the structured result of every document-producing tool.
type ResultOut struct {
	DocumentID  string   `json:"document_id" jsonschema:"id of the produced document; pass it as document_id to chain another call"`
	DocumentURI string   `json:"document_uri" jsonschema:"MCP resource URI of the full YAML document"`
	Summary     any      `json:"summary,omitempty" jsonschema:"compact result summary; large arrays are folded to counts"`
	Warnings    []string `json:"warnings,omitempty" jsonschema:"non-fatal warnings reported by the command"`
	Diagnostics []string `json:"diagnostics,omitempty" jsonschema:"the last plain stderr lines of the run (progress JSON is omitted)"`
	ElapsedMS   int64    `json:"elapsed_ms"`
	Truncated   bool     `json:"truncated,omitempty" jsonschema:"the summary was compacted; read the document resource for the rest"`
}

// DocInput is the document selector shared by every document tool.
type DocInput struct {
	Document   string `json:"document,omitempty" jsonschema:"inline RayWeaver pipeline YAML. Give exactly one of document, document_id or path."`
	DocumentID string `json:"document_id,omitempty" jsonschema:"id of a document returned by an earlier tool call"`
	Path       string `json:"path,omitempty" jsonschema:"workspace-relative path of a YAML document"`
}

// base lets the generic stage tools reach the document selector of their
// concrete input struct. The receiver is a value: tools only read it.
func (d DocInput) base() *DocInput { return &d }

// settings are the flags almost every subcommand accepts.
type settings struct {
	Config   string `json:"config,omitempty" jsonschema:"config id for a multi-config (zoom) document; a selector, not a setting"`
	GlassDir string `json:"glass_dir,omitempty" jsonschema:"AGF glass catalog directory (defaults to the server setting, then the document's glass_catalog.directory)"`
}

func (s settings) args() []string {
	var a []string
	if s.Config != "" {
		a = append(a, "--config", s.Config)
	}
	if s.GlassDir != "" {
		a = append(a, "--glass-dir", s.GlassDir)
	}
	return a
}

// progressReporter adapts the CLI's stderr lines into MCP progress
// notifications. It returns nil when the client sent no progress token.
func progressReporter(req *mcp.CallToolRequest) func(ProgressEvent) {
	if req == nil || req.Params == nil || req.Params.GetProgressToken() == nil {
		return nil
	}
	token := req.Params.GetProgressToken()
	session := req.Session
	return func(ev ProgressEvent) {
		if session == nil {
			return
		}
		_ = session.NotifyProgress(context.Background(), &mcp.ProgressNotificationParams{
			Message:       ev.Message,
			ProgressToken: token,
			Progress:      ev.Progress,
		})
	}
}

// runDocTool is the shared implementation of every document-producing tool.
func (s *server) runDocTool(ctx context.Context, req *mcp.CallToolRequest, command string, args []string, in DocInput) (*mcp.CallToolResult, ResultOut, error) {
	doc, parent, err := s.store.Resolve(in)
	if err != nil {
		return nil, ResultOut{}, err
	}
	// Server defaults go first: a per-call flag must win, and Go's flag
	// package keeps the last occurrence.
	args = append(s.commonArgs(), args...)
	res, err := s.runner.Run(ctx, Call{
		Command:  command,
		Args:     args,
		Stdin:    doc,
		Progress: progressReporter(req),
	})
	if err != nil {
		return nil, ResultOut{}, fmt.Errorf("%s: %w", command, err)
	}
	if res.ExitCode != 0 {
		return nil, ResultOut{}, commandError(command, res)
	}
	if len(res.Stdout) == 0 {
		return nil, ResultOut{}, fmt.Errorf("rayweave %s produced no output document", command)
	}
	meta, err := s.store.Put(command, res.Stdout, parent)
	if err != nil {
		return nil, ResultOut{}, err
	}
	summary, truncated := s.summarize(ctx, command, res.Stdout)
	out := ResultOut{
		DocumentID:  meta.ID,
		DocumentURI: s.store.URI(meta.ID),
		Summary:     summary,
		Warnings:    res.Warnings,
		Diagnostics: diagnosticsOf(res.Logs, 10),
		ElapsedMS:   res.Elapsed.Milliseconds(),
		Truncated:   truncated,
	}
	return s.docResult(out, meta), out, nil
}

// commonArgs appends the server-wide defaults to every command line.
func (s *server) commonArgs() []string {
	var a []string
	if s.cfg.GlassDir != "" {
		a = append(a, "--glass-dir", s.cfg.GlassDir)
	}
	return a
}

// docResult renders the content blocks of a document-producing tool call: a
// compact text summary plus a link to the full document resource.
func (s *server) docResult(out ResultOut, meta DocMeta) *mcp.CallToolResult {
	size := int64(meta.Bytes)
	return &mcp.CallToolResult{Content: []mcp.Content{
		&mcp.TextContent{Text: renderResult(out)},
		&mcp.ResourceLink{
			URI:         out.DocumentURI,
			Name:        meta.ID + ".yaml",
			Title:       "RayWeaver document " + meta.ID,
			Description: "full pipeline document produced by " + meta.Command,
			MIMEType:    "application/yaml",
			Size:        &size,
		},
	}}
}

// renderResult is the human/model-facing text of a result: the summary JSON
// capped at a readable size, then the warnings and the document URI.
func renderResult(out ResultOut) string {
	var b strings.Builder
	if out.DocumentID != "" {
		fmt.Fprintf(&b, "document_id: %s\n", out.DocumentID)
	}
	if out.Truncated {
		b.WriteString("summary: compacted (read the document resource for the full data)\n")
	}
	if out.Summary != nil {
		data, err := json.Marshal(out.Summary)
		if err == nil {
			const max = 12 << 10
			if len(data) > max {
				data = append(data[:max], []byte(" ... (compact summary truncated)")...)
				out.Truncated = true
			}
			b.WriteString("summary:\n")
			b.Write(data)
			b.WriteString("\n")
		}
	}
	if len(out.Warnings) > 0 {
		b.WriteString("warnings:\n")
		for _, w := range out.Warnings {
			fmt.Fprintf(&b, "- %s\n", w)
		}
	}
	if len(out.Diagnostics) > 0 {
		b.WriteString("diagnostics (last stderr lines):\n")
		for _, d := range out.Diagnostics {
			fmt.Fprintf(&b, "  %s\n", d)
		}
	}
	if out.ElapsedMS > 0 {
		fmt.Fprintf(&b, "elapsed: %d ms\n", out.ElapsedMS)
	}
	return b.String()
}

// diagnosticsOf keeps the last plain stderr lines. Progress lines are JSONL and
// are already represented by the summary, so only prose is kept.
func diagnosticsOf(logs []string, limit int) []string {
	var out []string
	for _, l := range logs {
		l = strings.TrimSpace(l)
		if l == "" || strings.HasPrefix(l, "{") {
			continue
		}
		out = append(out, l)
	}
	if len(out) > limit {
		out = out[len(out)-limit:]
	}
	return out
}

// commandError turns a non-zero exit into a tool error that tells the model
// what the command said. The shell context (exit code, message, warnings) is
// part of the text so the model can correct itself.
func commandError(command string, res RunResult) error {
	msg := fmt.Sprintf("rayweave %s failed (exit %d)", command, res.ExitCode)
	if tail := stderrTail(res.Stderr); tail != "" {
		msg += ":\n" + tail
	}
	if len(res.Warnings) > 0 {
		msg += "\nwarnings:\n- " + strings.Join(res.Warnings, "\n- ")
	}
	return errors.New(msg)
}

func stderrTail(stderr []byte) string {
	const maxLines = 20
	lines := strings.Split(strings.TrimRight(string(stderr), "\n"), "\n")
	if len(lines) > maxLines {
		lines = lines[len(lines)-maxLines:]
		return "...\n" + strings.Join(lines, "\n")
	}
	return strings.Join(lines, "\n")
}

// toolAnnotations is the annotation set shared by the analysis tools.
func toolAnnotations(readOnly bool) *mcp.ToolAnnotations {
	no := false
	return &mcp.ToolAnnotations{
		ReadOnlyHint:   readOnly,
		IdempotentHint: true,
		OpenWorldHint:  &no,
	}
}

// registerTools registers the tool set selected by cfg.Tools.
func (s *server) registerTools() {
	s.registerCoreTools()
	if s.cfg.Tools == "all" {
		s.registerExtendedTools()
	}
}

// addStage registers a document-to-document tool whose settings map to a
// rayweave subcommand's flags.
func addStage[In interface{ base() *DocInput }](s *server, name, title, desc, command string, build func(In) []string) {
	mcp.AddTool(s.mcp, &mcp.Tool{
		Name:        name,
		Title:       title,
		Description: desc,
		Annotations: toolAnnotations(false),
	}, func(ctx context.Context, req *mcp.CallToolRequest, in In) (*mcp.CallToolResult, ResultOut, error) {
		return s.runDocTool(ctx, req, command, build(in), *in.base())
	})
}

// sortStrings is used by the doc/resource listings.
func sortStrings(v []string) []string { sort.Strings(v); return v }

// boolFlag renders a flag that takes no value.
func boolFlag(args []string, set bool, flag string) []string {
	if set {
		return append(args, flag)
	}
	return args
}

// strFlag renders a flag with a value when the value was given.
func strFlag(args []string, value, flag string) []string {
	if value != "" {
		return append(args, flag, value)
	}
	return args
}

// floatFlag renders a numeric flag when the value is non-zero (an unset MCP
// argument must not override the document, mirroring the CLI's flagWasSet rule).
func floatFlag(args []string, value float64, flag string) []string {
	if value != 0 {
		return append(args, flag, fmt.Sprintf("%g", value))
	}
	return args
}

// intFlag renders an integer flag when the value is non-zero.
func intFlag(args []string, value int, flag string) []string {
	if value != 0 {
		return append(args, flag, fmt.Sprintf("%d", value))
	}
	return args
}

// boolPtrFlag renders a two-valued flag (`--stream true|false`) only when the
// caller set it explicitly.
func boolPtrFlag(args []string, v *bool, flag string) []string {
	if v == nil {
		return args
	}
	return append(args, flag, fmt.Sprintf("%t", *v))
}

// listFlag renders a repeatable or comma-joined flag. RayWeaver accepts the
// comma-separated form for most list options, so the values are joined.
func listFlag(args []string, values []string, flag string) []string {
	if len(values) == 0 {
		return args
	}
	return append(args, flag, strings.Join(values, ","))
}
