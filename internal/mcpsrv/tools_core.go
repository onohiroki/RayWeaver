package mcpsrv

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// ViewOut is the structured result of a read-only view (validate/list/query)
// that does not produce a pipeline document.
type ViewOut struct {
	Summary   any      `json:"summary,omitempty" jsonschema:"the parsed view or a short diagnostic record"`
	Warnings  []string `json:"warnings,omitempty" jsonschema:"non-fatal warnings reported by the command"`
	ElapsedMS int64    `json:"elapsed_ms"`
}

// ArtifactOut is the structured result of a tool whose product is a file in the
// workspace rather than a pipeline document (plot).
type ArtifactOut struct {
	URI       string   `json:"uri" jsonschema:"MCP resource URI of the artefact"`
	Path      string   `json:"path" jsonschema:"workspace-relative path"`
	Format    string   `json:"format"`
	Bytes     int      `json:"bytes"`
	Warnings  []string `json:"warnings,omitempty"`
	ElapsedMS int64    `json:"elapsed_ms"`
}

func (s *server) registerCoreTools() {
	s.addValidate()
	s.addList()
	s.addQuery()
	s.addParaxial()
	s.addChief()
	s.addTrace()
	s.addOptimize()
	s.addPlot()
	s.addPipeline()
	s.addEscape()
	s.addPSO()
	s.addJobTools()
}

// ---- validate -------------------------------------------------------------

type validateIn struct {
	DocInput
	settings
}

func (s *server) addValidate() {
	mcp.AddTool(s.mcp, &mcp.Tool{
		Name:        "validate",
		Title:       "Validate a RayWeaver document",
		Description: "Parse a pipeline document and load its glass catalogs without tracing rays. Returns the surface list and config ids, or the parse error. Use this before a long optimisation to catch a malformed document early.",
		Annotations: toolAnnotations(true),
	}, func(ctx context.Context, req *mcp.CallToolRequest, in validateIn) (*mcp.CallToolResult, ViewOut, error) {
		doc, _, err := s.store.Resolve(in.DocInput)
		if err != nil {
			return nil, ViewOut{}, err
		}
		args := append([]string{"surfaces", "--format", "json"}, in.settings.args()...)
		res, err := s.runner.Run(ctx, Call{Command: "list", Args: args, Stdin: doc})
		if err != nil {
			return nil, ViewOut{}, err
		}
		if res.ExitCode != 0 {
			return nil, ViewOut{}, commandError("validate", res)
		}
		view, ok := decodeJSON(res.Stdout)
		out := ViewOut{
			Summary: map[string]any{
				"valid":    true,
				"surfaces": view,
			},
			Warnings:  res.Warnings,
			ElapsedMS: res.Elapsed.Milliseconds(),
		}
		if !ok {
			out.Summary = map[string]any{"valid": true}
		}
		return &mcp.CallToolResult{Content: []mcp.Content{
			&mcp.TextContent{Text: "document is valid\n" + renderResult(ResultOut{Summary: out.Summary, Warnings: out.Warnings})},
		}}, out, nil
	})
}

// ---- list -----------------------------------------------------------------

type listIn struct {
	DocInput
	settings
	Targets      []string `json:"targets,omitempty" jsonschema:"what to list: surfaces, glasses, paraxial, fields, rays, merit, optimization, global, focus, default or all (default: default)"`
	Format       string   `json:"format,omitempty" jsonschema:"output format: json (default), yaml, csv or table"`
	Curvature    bool     `json:"curvature,omitempty" jsonschema:"show curvature instead of radius (surfaces)"`
	AutoAperture bool     `json:"auto_aperture,omitempty" jsonschema:"surfaces: show the auto_aperture state column"`
	AllGlasses   bool     `json:"all_glasses,omitempty" jsonschema:"glasses: include catalog entries no surface uses"`
	Roles        bool     `json:"roles,omitempty" jsonschema:"paraxial: also show the element roles table"`
	SummaryOnly  bool     `json:"summary_only,omitempty" jsonschema:"rays: omit the per-surface detail"`
}

func (s *server) addList() {
	mcp.AddTool(s.mcp, &mcp.Tool{
		Name:        "list",
		Title:       "List system data",
		Description: "Read-only listing of the document's definition and results: surfaces, glasses, paraxial properties, fields, traced rays, merit, optimisation and global-search results. JSON is the default so the answer is structured; use targets=[\"all\"] for the full picture.",
		Annotations: toolAnnotations(true),
	}, func(ctx context.Context, req *mcp.CallToolRequest, in listIn) (*mcp.CallToolResult, ViewOut, error) {
		doc, _, err := s.store.Resolve(in.DocInput)
		if err != nil {
			return nil, ViewOut{}, err
		}
		format := in.Format
		if format == "" {
			format = "json"
		}
		args := append([]string{}, in.Targets...)
		args = append(args, "--format", format)
		args = append(args, in.settings.args()...)
		args = boolFlag(args, in.Curvature, "--curvature")
		args = boolFlag(args, in.AutoAperture, "--auto-aperture")
		args = boolFlag(args, in.AllGlasses, "--all-glasses")
		args = boolFlag(args, in.Roles, "--roles")
		args = boolFlag(args, in.SummaryOnly, "--summary")
		res, err := s.runner.Run(ctx, Call{Command: "list", Args: args, Stdin: doc})
		if err != nil {
			return nil, ViewOut{}, err
		}
		if res.ExitCode != 0 {
			return nil, ViewOut{}, commandError("list", res)
		}
		out := ViewOut{Warnings: res.Warnings, ElapsedMS: res.Elapsed.Milliseconds()}
		if v, ok := decodeJSON(res.Stdout); ok {
			out.Summary, _ = fit(v)
		}
		return &mcp.CallToolResult{Content: []mcp.Content{
			&mcp.TextContent{Text: capText(res.Stdout, 32<<10)},
		}}, out, nil
	})
}

// ---- query ----------------------------------------------------------------

type queryIn struct {
	DocInput
	Selector   string   `json:"selector,omitempty" jsonschema:"path expression, e.g. chief_rays[0].spot_stats.rms_r or paraxial_result.focal_length"`
	Expr       string   `json:"expr,omitempty" jsonschema:"same as selector, as a flag"`
	JSON       bool     `json:"json,omitempty" jsonschema:"serialize the result as JSON"`
	YAML       bool     `json:"yaml,omitempty" jsonschema:"serialize the result as YAML"`
	Count      string   `json:"count,omitempty" jsonschema:"count non-null values resolved by PATH"`
	Len        string   `json:"len,omitempty" jsonschema:"length of the array/mapping resolved by PATH"`
	Sum        string   `json:"sum,omitempty" jsonschema:"sum of the numeric values resolved by PATH"`
	Stdev      string   `json:"stdev,omitempty" jsonschema:"population standard deviation of the values resolved by PATH"`
	Each       string   `json:"each,omitempty" jsonschema:"PATH:col1,col2,... one row per element"`
	CSV        string   `json:"csv,omitempty" jsonschema:"PATH:col1,col2,... CSV rows"`
	CSVHeader  bool     `json:"csv_header,omitempty" jsonschema:"with csv: emit a header row"`
	Printf     string   `json:"printf,omitempty" jsonschema:"Go fmt format string applied to the value"`
	Default    string   `json:"default,omitempty" jsonschema:"value printed when a scalar result is missing"`
	DefaultNum string   `json:"default_num,omitempty" jsonschema:"numeric default for missing --each column values"`
	JSONL      bool     `json:"jsonl,omitempty" jsonschema:"read JSON Lines instead of YAML (optimize/escape --log)"`
	Where      string   `json:"where,omitempty" jsonschema:"with jsonl: keep only lines where EXPR is truthy"`
	First      bool     `json:"first,omitempty" jsonschema:"with jsonl: use the first matching line instead of the last"`
	Gate       string   `json:"gate,omitempty" jsonschema:"evaluate EXPR and report pass/fail by truthiness"`
	Edit       []string `json:"edit,omitempty" jsonschema:"mutation expressions applied in order, e.g. [\"surfaces[id=2].thickness = 4\"]"`
	Set        []string `json:"set,omitempty" jsonschema:"variable bindings, evaluated in order"`
	Raw        bool     `json:"raw,omitempty" jsonschema:"raw text output (the default for scalars)"`
}

func (s *server) addQuery() {
	mcp.AddTool(s.mcp, &mcp.Tool{
		Name:        "query",
		Title:       "Query a document",
		Description: "Read-only selector over a pipeline document: extract one value or a table without reading the whole YAML. Supports paths, arithmetic, --count/--len/--sum/--stdev, --each/--csv tables and JSON Lines run logs. Cheaper than reading the document resource.",
		Annotations: toolAnnotations(true),
	}, func(ctx context.Context, req *mcp.CallToolRequest, in queryIn) (*mcp.CallToolResult, ViewOut, error) {
		// query is the one command that also works without a document (literal
		// expressions), so an empty selector set is not an error.
		var doc []byte
		if in.Document != "" || in.DocumentID != "" || in.Path != "" {
			data, _, err := s.store.Resolve(in.DocInput)
			if err != nil {
				return nil, ViewOut{}, err
			}
			doc = data
		}
		args := []string{}
		args = boolFlag(args, in.JSON, "--json")
		args = boolFlag(args, in.YAML, "--yaml")
		args = boolFlag(args, in.Raw, "--raw")
		args = boolFlag(args, in.JSONL, "--jsonl")
		args = boolFlag(args, in.First, "--first")
		args = boolFlag(args, in.CSVHeader, "--csv-header")
		args = strFlag(args, in.Count, "--count")
		args = strFlag(args, in.Len, "--len")
		args = strFlag(args, in.Sum, "--sum")
		args = strFlag(args, in.Stdev, "--stdev")
		args = strFlag(args, in.Each, "--each")
		args = strFlag(args, in.CSV, "--csv")
		args = strFlag(args, in.Printf, "--printf")
		args = strFlag(args, in.Default, "--default")
		args = strFlag(args, in.DefaultNum, "--default-num")
		args = strFlag(args, in.Where, "--where")
		args = strFlag(args, in.Gate, "--gate")
		args = strFlag(args, in.Expr, "--expr")
		for _, e := range in.Edit {
			args = append(args, "--edit", e)
		}
		for _, e := range in.Set {
			args = append(args, "--set", e)
		}
		if in.Selector != "" {
			args = append(args, in.Selector)
		}
		res, err := s.runner.Run(ctx, Call{Command: "query", Args: args, Stdin: doc})
		if err != nil {
			return nil, ViewOut{}, err
		}
		if res.ExitCode != 0 {
			return nil, ViewOut{}, commandError("query", res)
		}
		out := ViewOut{Warnings: res.Warnings, ElapsedMS: res.Elapsed.Milliseconds()}
		if in.JSON {
			if v, ok := decodeJSON(res.Stdout); ok {
				out.Summary, _ = fit(v)
			}
		}
		return &mcp.CallToolResult{Content: []mcp.Content{
			&mcp.TextContent{Text: capText(res.Stdout, 32<<10)},
		}}, out, nil
	})
}

// ---- analysis stages ------------------------------------------------------

type paraxialIn struct {
	DocInput
	settings
	Wavelength float64 `json:"wavelength,omitempty" jsonschema:"reference wavelength in mm (overrides the document's)"`
}

func (s *server) addParaxial() {
	addStage(s, "paraxial", "Paraxial analysis",
		"First-order analysis: EFL, BFL, F-number, NA, pupils, cardinal points and element glass roles. Writes paraxial_result into the returned document.",
		"paraxial", func(in paraxialIn) []string {
			args := in.settings.args()
			return floatFlag(args, in.Wavelength, "--wl")
		})
}

type chiefIn struct {
	DocInput
	settings
	Wavelength          float64   `json:"wavelength,omitempty" jsonschema:"reference wavelength in mm"`
	PassThrough         int       `json:"pass_through,omitempty" jsonschema:"constrain the chief ray to the centre of surface N"`
	ClearAperture       bool      `json:"clear_aperture,omitempty" jsonschema:"trace the grid through every surface and size auto_aperture surfaces from the beam footprint"`
	ClearApertureMargin float64   `json:"clear_aperture_margin_mm,omitempty" jsonschema:"clearance added to each side of the beam footprint in mm"`
	MarginalRays        bool      `json:"marginal_rays,omitempty" jsonschema:"extract the marginal rays for trace/plot"`
	RayFan              bool      `json:"ray_fan,omitempty" jsonschema:"compute the ray fan (transverse aberration) of every field"`
	FanPlane            string    `json:"fan_plane,omitempty" jsonschema:"fan plane: yz or xz (implies ray_fan)"`
	FanRotation         []float64 `json:"fan_rotation,omitempty" jsonschema:"fan planes rotated by DEG around Z (implies ray_fan)"`
	EPD                 float64   `json:"epd,omitempty" jsonschema:"virtual entrance pupil diameter in mm"`
	FNum                float64   `json:"fnum,omitempty" jsonschema:"virtual entrance pupil diameter = paraxial EFL / N"`
	EPZ                 float64   `json:"epz,omitempty" jsonschema:"virtual entrance pupil axial position in mm"`
	PupilAxial          float64   `json:"pupil_model_axial_position,omitempty" jsonschema:"virtual entrance pupil Z in mm (activates the pupil model)"`
	PupilDiameter       float64   `json:"pupil_model_diameter,omitempty" jsonschema:"virtual entrance pupil diameter in mm"`
	PupilMode           string    `json:"pupil_model_mode,omitempty" jsonschema:"pupil model mode"`
}

func (s *server) addChief() {
	addStage(s, "chief", "Chief rays",
		"Determine the chief ray of each field and trace the pupil grid: fields, chief_rays[] with per-field spot statistics, and optionally the ray fan and clear apertures. This is the usual first stage before trace or plot.",
		"chief", func(in chiefIn) []string {
			args := in.settings.args()
			args = floatFlag(args, in.Wavelength, "--wl")
			args = intFlag(args, in.PassThrough, "--pass-through")
			args = boolFlag(args, in.ClearAperture, "--clear-aperture")
			args = floatFlag(args, in.ClearApertureMargin, "--clear-aperture-margin-mm")
			args = boolFlag(args, in.MarginalRays, "--marginal-rays")
			args = boolFlag(args, in.RayFan, "--ray-fan")
			args = strFlag(args, in.FanPlane, "--fan-plane")
			if len(in.FanRotation) > 0 {
				for _, r := range in.FanRotation {
					args = append(args, "--fan-rotation", fmt.Sprintf("%g", r))
				}
			}
			args = floatFlag(args, in.EPD, "--epd")
			args = floatFlag(args, in.FNum, "--fnum")
			args = floatFlag(args, in.EPZ, "--epz")
			args = floatFlag(args, in.PupilAxial, "--pupil-model-axial-position")
			args = floatFlag(args, in.PupilDiameter, "--pupil-model-diameter")
			args = strFlag(args, in.PupilMode, "--pupil-model-mode")
			return args
		})
}

type traceIn struct {
	DocInput
	settings
	Details bool `json:"details,omitempty" jsonschema:"add per-surface detail (AOI, n1/n2, Fresnel) to the output"`
	Lenient bool `json:"lenient,omitempty" jsonschema:"skip aperture and glass-path checks"`
	Verbose bool `json:"verbose,omitempty" jsonschema:"print per-surface info to stderr (returned as logs)"`
}

func (s *server) addTrace() {
	addStage(s, "trace", "Trace rays",
		"Trace the rays and chief_rays the document already carries through every surface. Needs a preceding chief (or a document with a rays section); results[] is appended. Use chief first in the same pipeline.",
		"trace", func(in traceIn) []string {
			args := in.settings.args()
			args = boolFlag(args, in.Details, "--details")
			args = boolFlag(args, in.Lenient, "--lenient")
			args = boolFlag(args, in.Verbose, "--verbose")
			return args
		})
}

type optimizeIn struct {
	DocInput
	settings
	Snap               bool     `json:"snap,omitempty" jsonschema:"run the glass-snapping post-pass instead of a DLS solve"`
	ExcludeParam       []string `json:"exclude_param,omitempty" jsonschema:"target parameter names to drop from the variables (e.g. [\"conic\",\"a4\"])"`
	PowerSolve         bool     `json:"power_solve,omitempty" jsonschema:"hold each lens element's thin-lens power while only the glass dispersions are free"`
	PowerSolveSurfaces []string `json:"power_solve_surfaces,omitempty" jsonschema:"surface ids whose curvature is recomputed to hold the element power"`
	GlassVariables     bool     `json:"glass_variables,omitempty" jsonschema:"auto-generate nd/vd variables for every refractive element"`
	MemLimit           string   `json:"mem_limit,omitempty" jsonschema:"Go soft memory limit (e.g. 8GiB, -1 = unlimited)"`
	GOGC               int      `json:"gogc,omitempty" jsonschema:"GC target percentage (0 = the compute default)"`
}

func (s *server) addOptimize() {
	mcp.AddTool(s.mcp, &mcp.Tool{
		Name:        "optimize",
		Title:       "DLS optimisation",
		Description: "Damped-least-squares optimisation of the document's declared variables against its merit function. Runs to completion (progress notifications while it works); for an hours-long global search use escape or pso instead. With snap=true it instead replaces declared nd/vd glass variables with the nearest real catalog glasses.",
		Annotations: toolAnnotations(false),
	}, func(ctx context.Context, req *mcp.CallToolRequest, in optimizeIn) (*mcp.CallToolResult, ResultOut, error) {
		args, err := buildOptimize(in)
		if err != nil {
			return nil, ResultOut{}, err
		}
		if !in.Snap && req.Params.GetProgressToken() != nil {
			// The JSONL stream is what makes progress notifications useful; the
			// Runner parses iter/merit out of it and keeps only a bounded tail.
			args = append(args, "--verbose")
		}
		return s.runDocTool(ctx, req, "optimize", args, *in.base())
	})
}

func buildOptimize(in optimizeIn) ([]string, error) {
	if in.Snap {
		if in.Config != "" {
			return nil, fmt.Errorf("config is not supported by optimize snap (it operates on the document's own nd/vd variables)")
		}
		var args []string
		if in.GlassDir != "" {
			args = append(args, "--glass-dir", in.GlassDir)
		}
		return args, nil
	}
	args := in.settings.args()
	args = listFlag(args, in.ExcludeParam, "--exclude-param")
	args = boolFlag(args, in.PowerSolve, "--power-solve")
	args = listFlag(args, in.PowerSolveSurfaces, "--power-solve-surfaces")
	args = boolFlag(args, in.GlassVariables, "--glass-variables")
	args = strFlag(args, in.MemLimit, "--mem-limit")
	args = intFlag(args, in.GOGC, "--gogc")
	return args, nil
}

// ---- plot -----------------------------------------------------------------

type plotIn struct {
	DocInput
	settings
	Format            string   `json:"format,omitempty" jsonschema:"png (default, returned as an image) or svg"`
	Output            string   `json:"output,omitempty" jsonschema:"workspace-relative output path (default: plots/<document>.png)"`
	LensWidth         float64  `json:"lens_width,omitempty" jsonschema:"lens body stroke width in pixels"`
	RayWidth          float64  `json:"ray_width,omitempty" jsonschema:"ray path stroke width in pixels"`
	Scale             float64  `json:"scale,omitempty" jsonschema:"render scale factor (0 = auto)"`
	RightMargin       float64  `json:"right_margin,omitempty" jsonschema:"right-side margin beyond the image plane (% of lens length)"`
	FanRays           int      `json:"fan_rays,omitempty" jsonschema:"max fan rays drawn per field (0 = hide)"`
	ShowInvalidRayFan bool     `json:"show_invalid_ray_fan,omitempty" jsonschema:"draw fan rays with error codes in full"`
	ClipInvalidRayFan bool     `json:"clip_invalid_ray_fan,omitempty" jsonschema:"draw error-coded fan rays up to the first error"`
	ElementColor      []string `json:"element_color,omitempty" jsonschema:"element fill colours as 1=#ff0000 entries"`
}

func (s *server) addPlot() {
	mcp.AddTool(s.mcp, &mcp.Tool{
		Name:        "plot",
		Title:       "Render the lens diagram",
		Description: "Render a lens cross-section with ray paths. PNG is returned inline as an image (so the model can look at it); SVG is written to the workspace and returned as a resource link. Usually chained after chief/trace in a pipeline.",
		Annotations: toolAnnotations(false),
	}, func(ctx context.Context, req *mcp.CallToolRequest, in plotIn) (*mcp.CallToolResult, ArtifactOut, error) {
		doc, parent, err := s.store.Resolve(in.DocInput)
		if err != nil {
			return nil, ArtifactOut{}, err
		}
		format := strings.ToLower(in.Format)
		if format == "" {
			format = "png"
		}
		if format != "png" && format != "svg" {
			return nil, ArtifactOut{}, fmt.Errorf("format must be png or svg, got %q", in.Format)
		}
		name := in.Output
		if name == "" {
			stem := "plot"
			if parent != "" {
				stem = parent
			}
			name = filepath.Join("plots", stem+"."+format)
		}
		if filepath.Ext(name) == "" {
			name += "." + format
		}
		outPath, err := s.store.resolvePath(name)
		if err != nil {
			return nil, ArtifactOut{}, err
		}
		if err := os.MkdirAll(filepath.Dir(outPath), 0o755); err != nil {
			return nil, ArtifactOut{}, err
		}
		args := append(s.commonArgs(), "-o", outPath)
		args = append(args, in.settings.args()...)
		args = floatFlag(args, in.LensWidth, "--lens-width")
		args = floatFlag(args, in.RayWidth, "--ray-width")
		args = floatFlag(args, in.Scale, "--scale")
		args = floatFlag(args, in.RightMargin, "--right-margin")
		args = intFlag(args, in.FanRays, "--fan-rays")
		args = boolFlag(args, in.ShowInvalidRayFan, "--show-invalid-ray-fan")
		args = boolFlag(args, in.ClipInvalidRayFan, "--clip-invalid-ray-fan")
		for _, c := range in.ElementColor {
			args = append(args, "--element-color", c)
		}
		res, err := s.runner.Run(ctx, Call{Command: "plot", Args: args, Stdin: doc})
		if err != nil {
			return nil, ArtifactOut{}, err
		}
		if res.ExitCode != 0 {
			return nil, ArtifactOut{}, commandError("plot", res)
		}
		data, err := os.ReadFile(outPath)
		if err != nil {
			return nil, ArtifactOut{}, fmt.Errorf("plot wrote no file: %w", err)
		}
		rel, err := filepath.Rel(s.cfg.Workspace, outPath)
		if err != nil {
			rel = outPath
		}
		uri := "rayweave://plots/" + filepath.ToSlash(rel)
		size := int64(len(data))
		out := ArtifactOut{
			URI:       uri,
			Path:      rel,
			Format:    format,
			Bytes:     len(data),
			Warnings:  res.Warnings,
			ElapsedMS: res.Elapsed.Milliseconds(),
		}
		content := []mcp.Content{&mcp.ResourceLink{URI: uri, Name: filepath.Base(rel), MIMEType: mimeFor(format), Size: &size}}
		if format == "png" {
			// The SDK base64-encodes the []byte on the wire; pass the raw PNG.
			content = append(content, &mcp.ImageContent{Data: data, MIMEType: "image/png"})
		} else {
			content = append(content, &mcp.TextContent{Text: fmt.Sprintf("wrote %s (%d bytes)", rel, len(data))})
		}
		return &mcp.CallToolResult{Content: content}, out, nil
	})
}

func mimeFor(format string) string {
	if format == "png" {
		return "image/png"
	}
	return "image/svg+xml"
}

// capText bounds a free-form text result, pointing at the resource when cut.
func capText(b []byte, limit int) string {
	if len(b) <= limit {
		return string(b)
	}
	cut := limit
	for cut > 0 && !utf8.RuneStart(b[cut]) {
		cut--
	}
	return string(b[:cut]) + fmt.Sprintf("\n... (%d bytes total; the structured summary carries the parsed view)", len(b))
}
