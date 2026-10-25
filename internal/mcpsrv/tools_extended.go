package mcpsrv

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func (s *server) registerExtendedTools() {
	s.addZoom()
	s.addCam()
	s.addVignette()
	s.addAsphere()
	s.addTMM()
	s.addScale()
	s.addPSF()
	s.addFocus()
	s.addWavefront()
	s.addClean()
	s.addImport()
	s.addExport()
}

// ---- analysis stages ------------------------------------------------------

type zoomIn struct {
	DocInput
	settings
	Wavelength float64 `json:"wavelength,omitempty" jsonschema:"reference wavelength in mm"`
	NumRays    int     `json:"num_rays,omitempty" jsonschema:"pupil-grid rays per field"`
}

func (s *server) addZoom() {
	addStage(s, "zoom", "Zoom summary",
		"Per-config summary of a multi-config (zoom) system: first order, spot RMS and vignetting for every position.",
		"zoom", func(in zoomIn) []string {
			args := in.settings.args()
			args = floatFlag(args, in.Wavelength, "--wl")
			args = intFlag(args, in.NumRays, "--num-rays")
			return args
		})
}

type camIn struct {
	DocInput
	settings
	Wavelength float64 `json:"wavelength,omitempty" jsonschema:"reference wavelength in mm"`
	MaxIter    int     `json:"max_iter,omitempty" jsonschema:"maximum solver iterations"`
	Tolerance  float64 `json:"tolerance,omitempty" jsonschema:"convergence tolerance on the residuals in mm"`
	Verbose    bool    `json:"verbose,omitempty" jsonschema:"print one line per accepted iteration to stderr"`
}

func (s *server) addCam() {
	addStage(s, "cam", "Cam solve",
		"First-order cam solve: adjust the zoom group airspaces so every position hits its EFL with the focus on one fixed image plane.",
		"cam", func(in camIn) []string {
			args := in.settings.args()
			args = floatFlag(args, in.Wavelength, "--wl")
			args = intFlag(args, in.MaxIter, "--max-iter")
			args = floatFlag(args, in.Tolerance, "--tolerance")
			args = boolFlag(args, in.Verbose, "--verbose")
			return args
		})
}

type vignetteIn struct {
	DocInput
	settings
	Iterations   int     `json:"iterations,omitempty" jsonschema:"diameter/pupil passes (default 3)"`
	MinGlassPath float64 `json:"min_glass_path,omitempty" jsonschema:"minimum glass path (edge thickness) in mm"`
	MarginMM     float64 `json:"margin_mm,omitempty" jsonschema:"clearance added to each side of the beam footprint"`
	Wavelength   float64 `json:"wavelength,omitempty" jsonschema:"reference wavelength in mm"`
}

func (s *server) addVignette() {
	addStage(s, "vignette", "Settle vignetting",
		"Settle the per-field vignetting and auto_aperture diameters with the dynamic pupil.",
		"vignette", func(in vignetteIn) []string {
			args := in.settings.args()
			args = intFlag(args, in.Iterations, "--iterations")
			args = floatFlag(args, in.MinGlassPath, "--min-glass-path")
			args = floatFlag(args, in.MarginMM, "--margin-mm")
			args = floatFlag(args, in.Wavelength, "--wl")
			return args
		})
}

type asphereIn struct {
	DocInput
	settings
	Rings              int      `json:"rings,omitempty" jsonschema:"polar cell radial rings"`
	Angles             int      `json:"angles,omitempty" jsonschema:"polar cell angular sectors"`
	TBins              int      `json:"t_bins,omitempty" jsonschema:"beam-frame tangential bins"`
	PupilSamples       int      `json:"pupil_samples,omitempty" jsonschema:"pupil grid radial samples"`
	SensitivitySamples int      `json:"sensitivity_samples,omitempty" jsonschema:"sensitivity trace radial samples (0 = analytic proxy)"`
	TopK               int      `json:"top_k,omitempty" jsonschema:"number of top-ranked surfaces to fit"`
	SagScale           float64  `json:"sag_scale,omitempty" jsonschema:"initial sag scale"`
	ScaleProbes        []string `json:"scale_probes,omitempty" jsonschema:"verify these scales instead of the quadratic estimate"`
	Validate           bool     `json:"validate,omitempty" jsonschema:"run a short DLS per fitted surface to verify the asphere improves the merit"`
	Apply              bool     `json:"apply,omitempty" jsonschema:"insert the top-ranked validated asphere onto its surface"`
	DLSIter            int      `json:"dls_iter,omitempty" jsonschema:"DLS iterations per validated surface"`
	NumRays            int      `json:"num_rays,omitempty" jsonschema:"pupil grid rays for the validation DLS"`
	Diagnostics        []string `json:"diagnostics,omitempty" jsonschema:"extra diagnostics: opd, focus"`
	FocusSamples       int      `json:"focus_samples,omitempty" jsonschema:"rays per T/S fan for the focus channel"`
	FocusFans          []string `json:"focus_fans,omitempty" jsonschema:"focus channel fans: tangential, sagittal"`
}

func (s *server) addAsphere() {
	addStage(s, "asphere", "Asphere candidates",
		"Rank surfaces for asphere introduction and estimate initial coefficients, optionally validating each candidate with a short DLS and applying the best one.",
		"asphere", func(in asphereIn) []string {
			args := in.settings.args()
			args = intFlag(args, in.Rings, "--rings")
			args = intFlag(args, in.Angles, "--angles")
			args = intFlag(args, in.TBins, "--t-bins")
			args = intFlag(args, in.PupilSamples, "--pupil-samples")
			args = intFlag(args, in.SensitivitySamples, "--sensitivity-samples")
			args = intFlag(args, in.TopK, "--top-k")
			args = floatFlag(args, in.SagScale, "--sag-scale")
			args = listFlag(args, in.ScaleProbes, "--scale-probes")
			args = boolFlag(args, in.Validate, "--validate")
			args = boolFlag(args, in.Apply, "--apply")
			args = intFlag(args, in.DLSIter, "--dls-iter")
			args = intFlag(args, in.NumRays, "--num-rays")
			args = listFlag(args, in.Diagnostics, "--diagnostics")
			args = intFlag(args, in.FocusSamples, "--focus-samples")
			args = listFlag(args, in.FocusFans, "--focus-fans")
			return args
		})
}

type tmmIn struct {
	DocInput
	settings
}

func (s *server) addTMM() {
	addStage(s, "tmm", "Thin-film coating analysis",
		"Transfer-matrix analysis of the document's coating stacks: reflectance/transmittance per wavelength and angle.",
		"tmm", func(in tmmIn) []string { return in.settings.args() })
}

type scaleIn struct {
	DocInput
	settings
	EFL float64 `json:"efl" jsonschema:"target effective focal length in mm (required)"`
}

func (s *server) addScale() {
	addStage(s, "scale", "Scale to a target EFL",
		"Scale every length in the system so its effective focal length matches the target.",
		"scale", func(in scaleIn) []string {
			args := in.settings.args()
			args = floatFlag(args, in.EFL, "--efl")
			return args
		})
}

type psfIn struct {
	DocInput
	settings
	RefSurface    int       `json:"ref_surface,omitempty" jsonschema:"reference surface id for wavefront sampling"`
	Grid          int       `json:"psf_grid,omitempty" jsonschema:"image-plane pixels per side"`
	Width         float64   `json:"psf_width,omitempty" jsonschema:"evaluation half-width in mm (0 = auto from the Airy radius)"`
	NumRays       int       `json:"num_rays,omitempty" jsonschema:"pupil grid rays"`
	Fields        []int     `json:"fields,omitempty" jsonschema:"field indices (default: all)"`
	Wavelengths   []float64 `json:"wavelengths,omitempty" jsonschema:"wavelengths in mm (default: the chief wavelengths)"`
	Polarization  string    `json:"polarization,omitempty" jsonschema:"RCP (default), LCP, X, Y or RCP+LCP"`
	Spectral      string    `json:"spectral,omitempty" jsonschema:"polychromatic PSF weighting: D65 or FLAT"`
	Workers       int       `json:"psf_workers,omitempty" jsonschema:"parallel Huygens workers"`
	MaxFrequency  float64   `json:"max_freq,omitempty" jsonschema:"MTF frequency cap in cycles/mm"`
	MTFSpectral   string    `json:"mtf_spectral,omitempty" jsonschema:"MTF polychromatic weighting: D65 or FLAT"`
	MTFCombine    string    `json:"mtf_combination,omitempty" jsonschema:"MTF combination: intensity or otf"`
	BestFocus     bool      `json:"best_focus,omitempty" jsonschema:"evaluate each field at its best-focus image plane"`
	FocusPlane    string    `json:"focus_plane,omitempty" jsonschema:"common image plane: \"\" (input plane) or all"`
	ConvergeCheck *bool     `json:"converge_check,omitempty" jsonschema:"label sampling convergence by re-evaluating each result"`
}

func (s *server) addPSF() {
	addStage(s, "psf", "PSF and MTF",
		"Point-spread function on the flat image plane via direct vector Huygens integration, with the MTF derived from it. The document carries the PSF results; large grids are summarised.",
		"psf", func(in psfIn) []string {
			args := in.settings.args()
			args = intFlag(args, in.RefSurface, "--ref-surface")
			args = intFlag(args, in.Grid, "--psf-grid")
			args = floatFlag(args, in.Width, "--psf-width")
			args = intFlag(args, in.NumRays, "--num-rays")
			args = listIntFlag(args, in.Fields, "--fields")
			args = listFloatFlag(args, in.Wavelengths, "--wavelengths")
			args = strFlag(args, in.Polarization, "--polarization")
			args = strFlag(args, in.Spectral, "--spectral")
			args = intFlag(args, in.Workers, "--psf-workers")
			args = floatFlag(args, in.MaxFrequency, "--max-freq")
			args = strFlag(args, in.MTFSpectral, "--mtf-spectral")
			args = strFlag(args, in.MTFCombine, "--mtf-combination")
			args = boolFlag(args, in.BestFocus, "--best-focus")
			args = strFlag(args, in.FocusPlane, "--focus-plane")
			args = boolPtrFlag(args, in.ConvergeCheck, "--converge-check")
			return args
		})
}

type focusIn struct {
	DocInput
	settings
	Wavelengths   []float64 `json:"wavelengths,omitempty" jsonschema:"wavelengths in mm"`
	Fields        []int     `json:"fields,omitempty" jsonschema:"field indices"`
	Planes        string    `json:"planes,omitempty" jsonschema:"file,all,best (default all three)"`
	ThroughFocus  string    `json:"through_focus,omitempty" jsonschema:"through-focus scan FROM,STEP,COUNT in mm"`
	NumRays       int       `json:"num_rays,omitempty" jsonschema:"pupil grid rays"`
	Grid          int       `json:"psf_grid,omitempty" jsonschema:"image-plane pixels per side"`
	RefSurface    int       `json:"ref_surface,omitempty" jsonschema:"reference surface id"`
	Polarization  string    `json:"polarization,omitempty" jsonschema:"RCP (default), LCP, X, Y or RCP+LCP"`
	ConvergeCheck *bool     `json:"converge_check,omitempty" jsonschema:"label sampling convergence"`
	Frequencies   []float64 `json:"frequencies,omitempty" jsonschema:"spatial frequencies in cycles/mm (default 50)"`
}

func (s *server) addFocus() {
	addStage(s, "focus", "Focus comparison",
		"Compare MTF/PSF/spot across focus conventions (file plane, all planes, best focus) and optionally a through-focus scan.",
		"focus", func(in focusIn) []string {
			args := in.settings.args()
			args = listFloatFlag(args, in.Wavelengths, "--wavelengths")
			args = listIntFlag(args, in.Fields, "--fields")
			args = strFlag(args, in.Planes, "--planes")
			args = strFlag(args, in.ThroughFocus, "--through-focus")
			args = intFlag(args, in.NumRays, "--num-rays")
			args = intFlag(args, in.Grid, "--psf-grid")
			args = intFlag(args, in.RefSurface, "--ref-surface")
			args = strFlag(args, in.Polarization, "--polarization")
			args = boolPtrFlag(args, in.ConvergeCheck, "--converge-check")
			args = listFloatFlag(args, in.Frequencies, "--frequencies")
			return args
		})
}

type wavefrontIn struct {
	DocInput
	settings
	RefSurface   int       `json:"ref_surface,omitempty" jsonschema:"reference surface id for wavefront sampling"`
	NumRays      int       `json:"num_rays,omitempty" jsonschema:"pupil grid rays"`
	Fields       []int     `json:"fields,omitempty" jsonschema:"field indices"`
	Wavelengths  []float64 `json:"wavelengths,omitempty" jsonschema:"wavelengths in mm"`
	Polarization string    `json:"polarization,omitempty" jsonschema:"RCP (default), LCP, X, Y or RCP+LCP"`
	ZernikeOrder int       `json:"zernike_order,omitempty" jsonschema:"highest Fringe Zernike index to fit"`
	Workers      int       `json:"wavefront_workers,omitempty" jsonschema:"per-field task parallelism"`
	MapGrid      int       `json:"map_grid,omitempty" jsonschema:"wavefront map resolution per side"`
	BestFocus    bool      `json:"best_focus,omitempty" jsonschema:"compute and apply the weighted best image-plane shift"`
	FocusWeight  string    `json:"focus_weight,omitempty" jsonschema:"best-focus weighting: uniform or custom"`
	FocusWeights []float64 `json:"focus_weights,omitempty" jsonschema:"per-field weights when focus_weight is custom"`
}

func (s *server) addWavefront() {
	addStage(s, "wavefront", "Wavefront analysis",
		"Wavefront analysis: paraboloid and best-focus sphere reference, Fringe-Zernike coefficients and the best image plane.",
		"wavefront", func(in wavefrontIn) []string {
			args := in.settings.args()
			args = intFlag(args, in.RefSurface, "--ref-surface")
			args = intFlag(args, in.NumRays, "--num-rays")
			args = listIntFlag(args, in.Fields, "--fields")
			args = listFloatFlag(args, in.Wavelengths, "--wavelengths")
			args = strFlag(args, in.Polarization, "--polarization")
			args = intFlag(args, in.ZernikeOrder, "--zernike-order")
			args = intFlag(args, in.Workers, "--wavefront-workers")
			args = intFlag(args, in.MapGrid, "--map-grid")
			args = boolFlag(args, in.BestFocus, "--best-focus")
			args = strFlag(args, in.FocusWeight, "--focus-weight")
			args = listFloatFlag(args, in.FocusWeights, "--focus-weights")
			return args
		})
}

type cleanIn struct {
	DocInput
	Verbose bool `json:"verbose,omitempty" jsonschema:"print the removed fields as JSONL to stderr"`
}

func (s *server) addClean() {
	addStage(s, "clean", "Strip results",
		"Strip every calculation result from a pipeline document, leaving a loadable input. Use it before re-running a long search on a document that already carries results.",
		"clean", func(in cleanIn) []string {
			return boolFlag(nil, in.Verbose, "--verbose")
		})
}

// ---- import / export ------------------------------------------------------

type importIn struct {
	Content    string `json:"content,omitempty" jsonschema:"foreign lens file contents (ZEMAX .zmx, OSLO .len, CODE V .seq)"`
	Path       string `json:"path,omitempty" jsonschema:"workspace-relative path of the foreign lens file (alternative to content)"`
	Format     string `json:"format" jsonschema:"input format: zemax, oslo or codev"`
	ConfigID   string `json:"config_id,omitempty" jsonschema:"config id to create (default config1)"`
	ConfigName string `json:"config_name,omitempty" jsonschema:"config display name"`
	NoChief    bool   `json:"no_chief,omitempty" jsonschema:"skip the automatic chief-ray computation"`
	GlassDir   string `json:"glass_dir,omitempty" jsonschema:"AGF glass catalog directory"`
}

func (s *server) addImport() {
	mcp.AddTool(s.mcp, &mcp.Tool{
		Name:        "import",
		Title:       "Import a foreign lens file",
		Description: "Convert a ZEMAX (.zmx), OSLO (.len) or CODE V (.seq) lens file into a RayWeaver document. Chief and marginal rays are computed automatically, so the result can go straight into trace or plot.",
		Annotations: toolAnnotations(false),
	}, func(ctx context.Context, req *mcp.CallToolRequest, in importIn) (*mcp.CallToolResult, ResultOut, error) {
		data := []byte(in.Content)
		if in.Content == "" && in.Path != "" {
			p, err := s.store.resolvePath(in.Path)
			if err != nil {
				return nil, ResultOut{}, err
			}
			b, err := os.ReadFile(p)
			if err != nil {
				return nil, ResultOut{}, err
			}
			data = b
		}
		if len(data) == 0 {
			return nil, ResultOut{}, fmt.Errorf("give the lens file as content or path")
		}
		if in.Format == "" {
			return nil, ResultOut{}, fmt.Errorf("format is required (zemax, oslo or codev)")
		}
		args := append(s.commonArgs(), "--format", in.Format)
		args = strFlag(args, in.ConfigID, "--config-id")
		args = strFlag(args, in.ConfigName, "--config-name")
		args = boolFlag(args, in.NoChief, "--no-chief")
		args = strFlag(args, in.GlassDir, "--glass-dir")
		res, err := s.runner.Run(ctx, Call{Command: "import", Args: args, Stdin: data})
		if err != nil {
			return nil, ResultOut{}, err
		}
		if res.ExitCode != 0 {
			return nil, ResultOut{}, commandError("import", res)
		}
		meta, err := s.store.Put("import", res.Stdout, "")
		if err != nil {
			return nil, ResultOut{}, err
		}
		summary, truncated := s.summarize(ctx, "import", res.Stdout)
		out := ResultOut{
			DocumentID:  meta.ID,
			DocumentURI: s.store.URI(meta.ID),
			Summary:     summary,
			Warnings:    res.Warnings,
			ElapsedMS:   res.Elapsed.Milliseconds(),
			Truncated:   truncated,
		}
		return s.docResult(out, meta), out, nil
	})
}

type exportIn struct {
	DocInput
	settings
	Format string `json:"format,omitempty" jsonschema:"output format: zemax, codev or oslo (inferred from output when omitted)"`
	NDVD   bool   `json:"nd_vd,omitempty" jsonschema:"CODE V: write every glass as its inline nd:vd model form"`
	Output string `json:"output,omitempty" jsonschema:"workspace-relative file to write (e.g. out.zmx); without it the text is returned inline"`
}

func (s *server) addExport() {
	mcp.AddTool(s.mcp, &mcp.Tool{
		Name:        "export",
		Title:       "Export to a foreign lens format",
		Description: "Write the document out as ZEMAX, CODE V or OSLO text. Returns the text inline, or writes it to a workspace file and returns a resource link. See the export manual for the documented limitations.",
		Annotations: toolAnnotations(false),
	}, func(ctx context.Context, req *mcp.CallToolRequest, in exportIn) (*mcp.CallToolResult, ViewOut, error) {
		doc, _, err := s.store.Resolve(in.DocInput)
		if err != nil {
			return nil, ViewOut{}, err
		}
		// The CLI infers the format from `-o`'s extension; this tool writes the
		// file itself, so it must do the same inference and pass --format.
		format := in.Format
		if format == "" {
			format = exportFormatName("", in.Output)
		}
		if format == "" {
			return nil, ViewOut{}, fmt.Errorf("give format (zemax, codev, oslo) or an output path whose extension names the format")
		}
		args := append(s.commonArgs(), in.settings.args()...)
		args = append(args, "--format", format)
		args = boolFlag(args, in.NDVD, "--nd-vd")
		res, err := s.runner.Run(ctx, Call{Command: "export", Args: args, Stdin: doc})
		if err != nil {
			return nil, ViewOut{}, err
		}
		if res.ExitCode != 0 {
			return nil, ViewOut{}, commandError("export", res)
		}
		out := ViewOut{Warnings: res.Warnings, ElapsedMS: res.Elapsed.Milliseconds()}
		if in.Output != "" {
			path, err := s.store.WriteFile(in.Output, res.Stdout)
			if err != nil {
				return nil, out, err
			}
			rel, _ := filepath.Rel(s.cfg.Workspace, path)
			rel = filepath.ToSlash(rel)
			out.Summary = map[string]any{"path": rel, "bytes": len(res.Stdout), "format": format}
			size := int64(len(res.Stdout))
			return &mcp.CallToolResult{Content: []mcp.Content{
				&mcp.TextContent{Text: fmt.Sprintf("wrote %s (%d bytes)\n", rel, len(res.Stdout))},
				&mcp.ResourceLink{URI: "rayweave://files/" + rel, Name: filepath.Base(rel), Size: &size},
			}}, out, nil
		}
		// A big foreign file is more useful as a workspace artefact than as a
		// truncated text block, so spill it and link it.
		if len(res.Stdout) > 32<<10 {
			rel := "exports/export-" + shortHash(res.Stdout) + exportExt(format)
			path, err := s.store.WriteFile(rel, res.Stdout)
			if err != nil {
				return nil, out, err
			}
			rel, _ = filepath.Rel(s.cfg.Workspace, path)
			rel = filepath.ToSlash(rel)
			out.Summary = map[string]any{"path": rel, "bytes": len(res.Stdout), "format": format}
			size := int64(len(res.Stdout))
			return &mcp.CallToolResult{Content: []mcp.Content{
				&mcp.TextContent{Text: fmt.Sprintf("the export is %d bytes; wrote %s\n", len(res.Stdout), rel)},
				&mcp.ResourceLink{URI: "rayweave://files/" + rel, Name: filepath.Base(rel), Size: &size},
			}}, out, nil
		}
		out.Summary = map[string]any{"format": format, "bytes": len(res.Stdout)}
		return &mcp.CallToolResult{Content: []mcp.Content{
			&mcp.TextContent{Text: capText(res.Stdout, 32<<10)},
		}}, out, nil
	})
}

func shortHash(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:6])
}

func exportExt(format string) string {
	switch format {
	case "zemax":
		return ".zmx"
	case "codev":
		return ".seq"
	case "oslo":
		return ".len"
	}
	return ".txt"
}

func exportFormatName(format, output string) string {
	if format != "" {
		return format
	}
	switch strings.ToLower(filepath.Ext(output)) {
	case ".zmx":
		return "zemax"
	case ".seq":
		return "codev"
	case ".len":
		return "oslo"
	}
	return ""
}

// ---- list helpers ---------------------------------------------------------

func listIntFlag(args []string, values []int, flag string) []string {
	if len(values) == 0 {
		return args
	}
	parts := make([]string, 0, len(values))
	for _, v := range values {
		parts = append(parts, fmt.Sprintf("%d", v))
	}
	return append(args, flag, strings.Join(parts, ","))
}

func listFloatFlag(args []string, values []float64, flag string) []string {
	if len(values) == 0 {
		return args
	}
	parts := make([]string, 0, len(values))
	for _, v := range values {
		parts = append(parts, fmt.Sprintf("%g", v))
	}
	return append(args, flag, strings.Join(parts, ","))
}
