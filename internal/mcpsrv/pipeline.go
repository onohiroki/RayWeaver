package mcpsrv

import (
	"context"
	"fmt"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// pipelineCommands are the document-to-document stages a pipeline may chain.
// escape/pso are excluded (they are jobs), and list/query/plot/import/export
// are excluded (they are not document producers or they write files).
var pipelineCommands = map[string]bool{
	"paraxial":  true,
	"chief":     true,
	"trace":     true,
	"zoom":      true,
	"cam":       true,
	"vignette":  true,
	"optimize":  true,
	"asphere":   true,
	"tmm":       true,
	"scale":     true,
	"psf":       true,
	"focus":     true,
	"wavefront": true,
	"clean":     true,
}

// pipelineForbiddenArgs are the flags a stage must not pass: they write files
// or serialise a result instead of producing the next pipeline document.
var pipelineForbiddenArgs = []string{
	"-o", "--output", "--save", "--log", "--cpuprofile", "--memprofile",
	"--gorprofile", "--yaml", "--csv", "--output-shifted-lens",
}

const maxPipelineStages = 8

type pipelineStage struct {
	Command string   `json:"command" jsonschema:"stage subcommand: chief, trace, paraxial, zoom, cam, vignette, optimize, asphere, tmm, scale, psf, focus, wavefront or clean"`
	Args    []string `json:"args,omitempty" jsonschema:"extra rayweave flags for this stage, e.g. [\"--clear-aperture\",\"--marginal-rays\"]"`
}

type pipelineIn struct {
	DocInput
	Stages []pipelineStage `json:"stages" jsonschema:"stages in order; each one reads the previous stage's document (at most 8)"`
}

type pipelineStageStat struct {
	Command    string   `json:"command"`
	DocumentID string   `json:"document_id"`
	ElapsedMS  int64    `json:"elapsed_ms"`
	Warnings   []string `json:"warnings,omitempty"`
	Failed     bool     `json:"failed,omitempty"`
}

type pipelineOut struct {
	DocumentID  string              `json:"document_id"`
	DocumentURI string              `json:"document_uri"`
	Summary     any                 `json:"summary,omitempty"`
	Warnings    []string            `json:"warnings,omitempty"`
	ElapsedMS   int64               `json:"elapsed_ms"`
	Truncated   bool                `json:"truncated,omitempty"`
	Stages      []pipelineStageStat `json:"stages"`
}

func (s *server) addPipeline() {
	mcp.AddTool(s.mcp, &mcp.Tool{
		Name:        "pipeline",
		Title:       "Chain pipeline stages",
		Description: "Run several stages in one call (the equivalent of `rayweave chief | rayweave trace | rayweave plot`, without plot). Each stage reads the previous stage's document, so the large intermediate documents never enter the conversation. Returns the final document plus per-stage ids and timings.",
		Annotations: toolAnnotations(false),
	}, func(ctx context.Context, req *mcp.CallToolRequest, in pipelineIn) (*mcp.CallToolResult, pipelineOut, error) {
		if len(in.Stages) == 0 {
			return nil, pipelineOut{}, fmt.Errorf("pipeline needs at least one stage")
		}
		if len(in.Stages) > maxPipelineStages {
			return nil, pipelineOut{}, fmt.Errorf("pipeline takes at most %d stages, got %d", maxPipelineStages, len(in.Stages))
		}
		doc, parent, err := s.store.Resolve(in.DocInput)
		if err != nil {
			return nil, pipelineOut{}, err
		}
		progress := progressReporter(req)
		out := pipelineOut{}
		var lastMeta DocMeta
		for i, st := range in.Stages {
			if !pipelineCommands[st.Command] {
				return nil, out, fmt.Errorf("stage %d: %q is not a pipeline stage (supported: %s)", i+1, st.Command, strings.Join(sortedKeys(boolMapToAny(pipelineCommands)), ", "))
			}
			if bad, ok := forbiddenArg(st.Args); ok {
				return nil, out, fmt.Errorf("stage %d (%s): flag %s is not allowed in a pipeline (it writes a file or a side result); use the dedicated tool", i+1, st.Command, bad)
			}
			args := append(s.commonArgs(), st.Args...)
			res, err := s.runner.Run(ctx, Call{
				Command: st.Command,
				Args:    args,
				Stdin:   doc,
				Progress: func(ev ProgressEvent) {
					if progress != nil {
						progress(ProgressEvent{Message: fmt.Sprintf("[%d/%d %s] %s", i+1, len(in.Stages), st.Command, ev.Message), Progress: ev.Progress})
					}
				},
			})
			if err != nil {
				return nil, out, fmt.Errorf("stage %d (%s): %w", i+1, st.Command, err)
			}
			if res.ExitCode != 0 {
				return nil, out, fmt.Errorf("stage %d (%s): %w", i+1, st.Command, commandError(st.Command, res))
			}
			meta, err := s.store.Put(st.Command, res.Stdout, parent)
			if err != nil {
				return nil, out, err
			}
			parent = meta.ID
			lastMeta = meta
			doc = res.Stdout
			out.ElapsedMS += res.Elapsed.Milliseconds()
			out.Warnings = append(out.Warnings, res.Warnings...)
			out.Stages = append(out.Stages, pipelineStageStat{
				Command:    st.Command,
				DocumentID: meta.ID,
				ElapsedMS:  res.Elapsed.Milliseconds(),
				Warnings:   res.Warnings,
			})
		}
		last := out.Stages[len(out.Stages)-1]
		out.DocumentID = last.DocumentID
		out.DocumentURI = s.store.URI(last.DocumentID)
		out.Summary, out.Truncated = s.summarize(ctx, last.Command, doc)
		res := s.docResult(ResultOut{
			DocumentID:  out.DocumentID,
			DocumentURI: out.DocumentURI,
			Summary:     out.Summary,
			Warnings:    out.Warnings,
			ElapsedMS:   out.ElapsedMS,
			Truncated:   out.Truncated,
		}, lastMeta)
		var b strings.Builder
		for _, st := range out.Stages {
			fmt.Fprintf(&b, "stage %-10s -> %s (%d ms)\n", st.Command, st.DocumentID, st.ElapsedMS)
		}
		res.Content = append([]mcp.Content{&mcp.TextContent{Text: b.String()}}, res.Content...)
		return res, out, nil
	})
}

func forbiddenArg(args []string) (string, bool) {
	for _, a := range args {
		name := a
		if i := strings.IndexByte(a, '='); i > 0 {
			name = a[:i]
		}
		for _, bad := range pipelineForbiddenArgs {
			if name == bad {
				return name, true
			}
		}
	}
	return "", false
}

func boolMapToAny(m map[string]bool) map[string]any {
	out := make(map[string]any, len(m))
	for k := range m {
		out[k] = true
	}
	return out
}
