// Command importsweep drives every lens file under a data directory through
// the same pipeline as "rayweave import | rayweave trace | rayweave paraxial"
// and reports per-file status as CSV.
//
// It calls the exact internal functions the CLI wires up in
// cmd/rayweave/import.go, main.go (runTrace/runParaxial): importer.ParseZemax
// / ParseOslo, AGF glass enhancement, surface.Precompute, the FNO->stop
// diameter fill, chief.DetermineChiefRaysGrid, marginal-ray extraction, ray
// tracing and paraxial.Compute.
package main

import (
	"encoding/csv"
	"flag"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"

	"github.com/hiroki/rayweaver/internal/chief"
	"github.com/hiroki/rayweaver/internal/dls"
	"github.com/hiroki/rayweaver/internal/glass"
	"github.com/hiroki/rayweaver/internal/importer"
	"github.com/hiroki/rayweaver/internal/paraxial"
	"github.com/hiroki/rayweaver/internal/ray"
	"github.com/hiroki/rayweaver/internal/surface"
	"github.com/hiroki/rayweaver/internal/types"
)

// record holds every metric collected for one lens file.
type record struct {
	Path           string
	Group          string
	Format         string
	ParseOK        bool
	ParseError     string
	Surfaces       int
	Fields         int
	FieldType      int
	FieldAngles    string
	Wavelengths    int
	Glasses         int
	UnresolvedGlass int
	UnresolvedNames string
	Stop           int
	HasDiameter    bool
	ChiefOK        bool
	ChiefRays      int
	MarginalRays   int
	GridError      int
	SpotRMS        string
	TraceTotal     int
	TraceOK        int
	ChiefFailed    int
	MarginalFailed int
	TraceError     string
	TraceStopSurf  string
	FocalLength    string
	EPD            string
	FNumber        string
	TotalTrack     string
	ParaxialOK     bool

	// Diagnostic breakdown of trace failures (see sweepMetrics).
	ApertureStop            int
	ApertureStopTraceable   int
	ApertureStopUnreachable int
	ModelFailCount          int
	Vignetted               bool
	Configs                 int
	ConfigsOK               int
}

func (r *record) row() []string {
	return []string{
		r.Path, r.Group, r.Format,
		boolStr(r.ParseOK), r.ParseError,
		itoa(r.Surfaces), itoa(r.Fields), r.FieldAngles, itoa(r.Wavelengths),
		itoa(r.FieldType),
		itoa(r.Glasses), itoa(r.UnresolvedGlass), r.UnresolvedNames, itoa(r.Stop), boolStr(r.HasDiameter),
		boolStr(r.ChiefOK), itoa(r.ChiefRays), itoa(r.MarginalRays), itoa(r.GridError), r.SpotRMS,
		itoa(r.TraceTotal), itoa(r.TraceOK), itoa(r.ChiefFailed), itoa(r.MarginalFailed), r.TraceError, r.TraceStopSurf,
		r.FocalLength, r.EPD, r.FNumber, r.TotalTrack, boolStr(r.ParaxialOK),
		itoa(r.ApertureStop), itoa(r.ApertureStopTraceable), itoa(r.ApertureStopUnreachable), itoa(r.ModelFailCount),
		boolStr(r.Vignetted), itoa(r.Configs), itoa(r.ConfigsOK),
	}
}

var header = []string{
	"path", "group", "format",
	"parse_ok", "parse_error",
	"surfaces", "fields", "field_angles", "wavelengths", "field_type",
	"glasses", "unresolved_glass", "unresolved_names", "stop", "has_diameter",
	"chief_ok", "chief_rays", "marginal_rays", "grid_error", "spot_rms_r",
	"trace_total", "trace_ok", "chief_failed", "marginal_failed", "trace_error", "trace_stop_surface",
	"paraxial_focal_length", "paraxial_epd", "paraxial_fno", "paraxial_track", "paraxial_ok",
	"aperture_stop", "aperture_stop_traceable", "aperture_stop_unreachable", "model_fail_count",
	"vignetted", "configs", "configs_ok",
}

func boolStr(b bool) string {
	if b {
		return "yes"
	}
	return "no"
}

func itoa(n int) string {
	return fmt.Sprintf("%d", n)
}

// dumpGlobal mirrors the -dump flag so processFile can inspect specific files.
var dumpGlobal string

func main() {
	root := flag.String("root", "untrack-lens-data", "data directory to sweep")
	glassDir := flag.String("glass-dir", "", "AGF glass catalog directory (empty = none, like plain rayweave import)")
	out := flag.String("out", "", "CSV output path (default: <root>/sweep_results.csv)")
	summary := flag.String("summary", "", "CSV summary output path (default: <root>/sweep_summary.csv)")
	dump := flag.String("dump", "", "debug: dump per-surface data (id,curv,thick,mat,diam) for paths containing this substring")
	flag.Parse()
	dumpGlobal = *dump

	if *out == "" {
		*out = filepath.Join(*root, "sweep_results.csv")
	}
	if *summary == "" {
		*summary = filepath.Join(*root, "sweep_summary.csv")
	}

	// Preload the AGF catalog once (shared read-only across workers).
	agfGlasses := []types.Glass(nil)
	if *glassDir != "" {
		agf, err := glass.LoadAGFDir(*glassDir)
		if err != nil {
			fmt.Fprintf(os.Stderr, "warning: cannot load AGF dir %s: %v\n", *glassDir, err)
		} else {
			agfGlasses = agf
		}
	}

	files, skipped := collectFiles(*root)

	recs := make([]record, len(files))
	var wg sync.WaitGroup
	jobs := make(chan int)
	workers := runtime.GOMAXPROCS(0)
	if workers > len(files) {
		workers = len(files)
	}
	if workers < 1 {
		workers = 1
	}
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range jobs {
				f := files[i]
				recs[i] = processFile(f.abs, f.format, agfGlasses)
			}
		}()
	}
	for i := range files {
		jobs <- i
	}
	close(jobs)
	wg.Wait()

	writeCSV(*out, append([][]string{header}, rows(recs)...))

	summaryRows := summarize(recs, skipped, *root, len(files))
	writeCSV(*summary, summaryRows)

	fmt.Printf("swept %d files (%d skipped: %s)\n", len(files), len(skipped), strings.Join(skipped, ", "))
	printSummary(summaryRows)
}

type fileEntry struct {
	abs    string
	format string
}

// collectFiles walks root and classifies each file by extension. Binary ZEMAX
// archives (.zda/.zar) are reported as skipped.
func collectFiles(root string) (files []fileEntry, skipped []string) {
	root = filepath.Clean(root)
	_ = filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		ext := strings.ToLower(filepath.Ext(path))
		var format string
		switch ext {
		case ".zmx":
			format = "zemax"
		case ".len":
			format = "oslo"
		case ".seq":
			format = "codev"
		case ".zda", ".zar":
			skipped = append(skipped, path)
			return nil
		default:
			return nil
		}
		files = append(files, fileEntry{abs: path, format: format})
		return nil
	})
	sort.Slice(files, func(i, j int) bool { return files[i].abs < files[j].abs })
	sort.Strings(skipped)
	return files, skipped
}

// processFile runs one lens through the import+chief+trace+paraxial pipeline.
func processFile(abs, format string, agfGlasses []types.Glass) record {
	rel := abs
	rec := record{
		Path:   rel,
		Format: format,
		Group:  groupOf(abs),
	}

	data, err := os.ReadFile(abs)
	if err != nil {
		rec.ParseError = "read: " + err.Error()
		return rec
	}

	if dumpGlobal != "" && strings.Contains(abs, dumpGlobal) {
		fmt.Printf("### %s (raw %d bytes)\n", abs, len(data))
		defer func() { fmt.Printf("### end %s\n", abs) }()
	}

	var result *importer.ParseResult
	if format == "oslo" {
		result, err = importer.ParseOslo(string(data))
	} else {
		result, err = importer.ParseZemax(string(data))
	}
	if err != nil {
		rec.ParseError = err.Error()
		return rec
	}
	if len(result.Surfaces) == 0 {
		rec.ParseOK = true
		rec.ParseError = "no surfaces"
		return rec
	}
	rec.ParseOK = true

	if len(agfGlasses) > 0 {
		result.GlassEntries = importer.EnhanceGlassEntriesFromAGF(result.GlassEntries, agfGlasses)
	}

	surfaces := result.Surfaces
	surface.Precompute(surfaces)

	gc := glass.NewCatalog()
	for _, g := range result.GlassEntries {
		gc.Add(g)
		// A glass entry is unresolved when it carries no nd/vd model data and
		// no catalog dispersion data (Sellmeier/Cauchy coefficients).
		if g.ND == 0 && g.VD == 0 && len(g.Coefficients) == 0 && len(g.RefractiveIndices) == 0 {
			rec.UnresolvedGlass++
			if rec.UnresolvedNames == "" {
				rec.UnresolvedNames = g.Label
			} else {
				rec.UnresolvedNames += "," + g.Label
			}
		}
	}

	lastID := surfaces[len(surfaces)-1].ID
	stopSurface := result.StopSurface
	if stopSurface <= 0 {
		stopSurface = surfaces[0].ID
	}

	// --- Basic metrics ---
	rec.Surfaces = len(surfaces)
	rec.Fields = len(result.Fields)
	rec.FieldType = result.FieldType
	rec.FieldAngles = fieldAngleStr(result.Fields)
	rec.Wavelengths = len(result.Wavelengths)
	rec.Glasses = len(result.GlassEntries)
	rec.Stop = stopSurface
	for _, s := range surfaces {
		if s.Diameter > 0 {
			rec.HasDiameter = true
			break
		}
	}

	wavelength := firstWavelength(result.Wavelengths)

	if dumpGlobal != "" && strings.Contains(abs, dumpGlobal) {
		fmt.Printf("  stopSurface=%d lastID=%d FNO=%g wl=%g fields=%d wavs=%d\n",
			stopSurface, lastID, result.FNO, wavelength, len(result.Fields), len(result.Wavelengths))
		for _, s := range surfaces {
			fmt.Printf("  surf id=%d curv=%g thick=%g mat=%q diam=%g\n", s.ID, s.Curvature, s.Thickness, s.Material, s.Diameter)
		}
		for _, g := range result.GlassEntries {
			fmt.Printf("  glass label=%q nd=%g vd=%g coeffs=%d\n", g.Label, g.ND, g.VD, len(g.Coefficients))
		}
	}

	// --- FNO -> stop diameter fill (replicates import.go) ---
	if result.FNO > 0 && !rec.HasDiameter {
		pr := paraxial.Compute(types.System{Surfaces: surfaces}, wavelength, gc, 0, nil)
		if pr.FocalLength > 0 {
			epD := pr.FocalLength / result.FNO
			for i := range surfaces {
				if surfaces[i].ID == stopSurface {
					surfaces[i].Diameter = epD
					break
				}
			}
		}
	}

	// --- Primary (config 0 / base) pipeline ---
	prim := sweepConfig(surfaces, result, gc, wavelength, stopSurface, lastID)
	rec.ChiefOK = prim.ChiefOK
	rec.ChiefRays = prim.ChiefRays
	rec.MarginalRays = prim.MarginalRays
	rec.GridError = prim.GridError
	rec.SpotRMS = prim.SpotRMS
	rec.TraceTotal = prim.TraceTotal
	rec.TraceOK = prim.TraceOK
	rec.ChiefFailed = prim.ChiefFailed
	rec.MarginalFailed = prim.MarginalFailed
	rec.TraceError = prim.TraceError
	rec.TraceStopSurf = prim.TraceStopSurf
	rec.FocalLength = prim.FocalLength
	rec.EPD = prim.EPD
	rec.FNumber = prim.FNumber
	rec.TotalTrack = prim.TotalTrack
	rec.ParaxialOK = prim.ParaxialOK
	rec.ApertureStop = prim.ApertureStop
	rec.ApertureStopTraceable = prim.ApertureStopTraceable
	rec.ApertureStopUnreachable = prim.ApertureStopUnreachable
	rec.ModelFailCount = prim.ModelFailCount
	rec.Vignetted = prim.ModelFailCount == 0 && prim.ApertureStop > 0 &&
		prim.ApertureStopTraceable > 0 && prim.ApertureStopUnreachable == 0

	if dumpGlobal != "" && strings.Contains(abs, dumpGlobal) && prim.ApertureStop > 0 {
		fmt.Printf("  aperture-stop diagnostics: blocked=%d traceable=%d unreachable=%d model_fail=%d\n",
			prim.ApertureStop, prim.ApertureStopTraceable, prim.ApertureStopUnreachable, prim.ModelFailCount)
	}

	// --- Per-config validation (multi-config lenses) ---
	cfgIdx := importer.ConfigIndexes(result)
	rec.Configs = 1 + len(cfgIdx)
	rec.ConfigsOK = 0
	if prim.TraceTotal > 0 && prim.TraceOK == prim.TraceTotal && prim.ChiefRays > 0 {
		rec.ConfigsOK++
	}
	for _, c := range cfgIdx {
		cfgSurfaces := importer.ConfigSurfaceSet(result, c)
		if dumpGlobal != "" && strings.Contains(abs, dumpGlobal) {
			fmt.Printf("  config %d overridden surfaces:\n", c)
			for _, s := range cfgSurfaces {
				fmt.Printf("    surf id=%d curv=%g thick=%g mat=%q diam=%g\n", s.ID, s.Curvature, s.Thickness, s.Material, s.Diameter)
			}
		}
		r := sweepConfig(cfgSurfaces, result, gc, wavelength, stopSurface, lastID)
		if r.ChiefRays > 0 && r.TraceTotal > 0 && r.TraceOK == r.TraceTotal {
			rec.ConfigsOK++
		}
	}

	return rec
}

// sweepMetrics is the outcome of running chief+trace+paraxial on one surface
// set (one config).
type sweepMetrics struct {
	ChiefOK        bool
	ChiefRays      int
	MarginalRays   int
	GridError      int
	SpotRMS        string
	TraceTotal     int
	TraceOK        int
	ChiefFailed    int
	MarginalFailed int
	TraceError     string
	TraceStopSurf  string
	FocalLength    string
	EPD            string
	FNumber        string
	TotalTrack     string
	ParaxialOK     bool

	// ApertureStop counts rays that fail with "ray missed surface (aperture
	// stop)". Of those, ApertureStopTraceable reach the image plane when the
	// aperture check is disabled (real vignetting: the beam is clipped by a
	// smaller clear aperture but is otherwise traceable), while
	// ApertureStopUnreachable still fail (a model/pupil error, e.g. a wide-angle
	// chief ray that cannot enter the lens). ModelFailCount counts rays that fail
	// for any other reason (missed surface, TIR, glass path).
	ApertureStop            int
	ApertureStopTraceable   int
	ApertureStopUnreachable int
	ModelFailCount          int
}

// sweepConfig runs the chief-grid + marginal-ray + trace + paraxial pipeline
// on the given surface set, mirroring import.go + runTrace + runParaxial.
func sweepConfig(surfaces []types.Surface, result *importer.ParseResult, gc *glass.Catalog, wavelength float64, stopSurface, lastID int) sweepMetrics {
	var m sweepMetrics

	// --- Chief rays (replicates import.go chief pass) ---
	pol := types.NewCircularJones(true)
	pt := &types.PassThroughTarget{Surface: stopSurface, Coordinate: types.Vec3{}}

	chiefFields := make([]types.FieldDef, len(result.Fields))
	for i, f := range result.Fields {
		chiefFields[i] = types.FieldDef{
			Angle:       f.AngleDeg,
			ImageHeight: f.ImageHeight,
		}
	}

	surface.Precompute(surfaces)
	selectedSys := types.System{Surfaces: surfaces}

	chiefResults := chief.DetermineChiefRaysGrid(
		selectedSys, chiefFields, lastID, 128, gc, pol, wavelength,
		true, types.GridPolar, pt, nil, nil,
	)

	path := dls.BuildPath(surfaces)
	rays := make([]types.Ray, 0, len(chiefResults)*4)
	chiefCRs := make([]types.ChiefRayResult, 0, len(chiefResults))

	m.ChiefOK = len(chiefResults) > 0
	for fi, r := range chiefResults {
		cr := types.ChiefRayResult{
			FieldAngle:    r.FieldAngle,
			ChiefRay:      r.ChiefRay,
			ImageHeight:   r.ImageHeight,
			EntrancePupil: r.EntrancePupil,
			SpotStats:     r.SpotStats,
		}
		chiefCRs = append(chiefCRs, cr)
		if r.SpotStats != nil {
			m.SpotRMS = fmt.Sprintf("%.4f", r.SpotStats.RMS_R)
		}

		chiefRay := r.ChiefRay
		chiefRay.ID = fmt.Sprintf("chief_%.0fdeg", r.FieldAngle)
		chiefRay.Path = path
		chiefRay.Jones = pol
		rays = append(rays, chiefRay)
		m.ChiefRays++

		margs := chief.MarginalRaysForField(fi, r, wavelength, path, pol)
		m.MarginalRays += len(margs)
		rays = append(rays, margs...)

		for i := range r.GridPoints {
			if r.GridPoints[i].ErrorCode != "" {
				m.GridError++
			}
		}
	}

	// --- Trace all chief + marginal rays (replicates runTrace) ---
	engine := ray.NewEngine(gc, nil)
	for i := range rays {
		r := &rays[i]
		r.Jones = pol
		ray.ResolveRay(r, surfaces, engine)
		res := engine.TraceRay(*r, surfaces)
		m.TraceTotal++
		if res.Error == "" {
			m.TraceOK++
			continue
		}
		isAperture := res.Error == "ray missed surface (aperture stop)"
		if isAperture {
			m.ApertureStop++
			// Re-trace with the aperture check disabled to distinguish real
			// vignetting (the ray reaches the image once unclipped) from a
			// model/pupil error (the ray still cannot complete the path).
			probe := *r
			probe.SkipApertureCheck = true
			if probeRes := engine.TraceRay(probe, surfaces); probeRes.Error == "" {
				m.ApertureStopTraceable++
			} else {
				m.ApertureStopUnreachable++
			}
		} else {
			m.ModelFailCount++
		}
		if strings.HasPrefix(r.ID, "chief_") {
			m.ChiefFailed++
		} else {
			m.MarginalFailed++
		}
		if m.TraceError == "" {
			m.TraceError = res.Error
			m.TraceStopSurf = traceStopPoint(res, surfaces)
		}
	}

	// --- Paraxial (replicates runParaxial piped after chief) ---
	prSys := types.System{Surfaces: surfaces, StopSurface: stopSurface}
	pr := paraxial.Compute(prSys, types.DefaultWavelength, gc, 0, chiefCRs)
	if pr.FocalLength > 0 && !math.IsInf(pr.FocalLength, 0) {
		m.FocalLength = fmt.Sprintf("%.4f", pr.FocalLength)
		m.ParaxialOK = true
	}
	if pr.EntrancePupilDiameter > 0 {
		m.EPD = fmt.Sprintf("%.4f", pr.EntrancePupilDiameter)
	}
	if pr.ImageSpaceFNumber > 0 {
		m.FNumber = fmt.Sprintf("%.4f", pr.ImageSpaceFNumber)
	}
	if pr.TotalTrack > 0 {
		m.TotalTrack = fmt.Sprintf("%.4f", pr.TotalTrack)
	}
	return m
}

func groupOf(path string) string {
	p := filepath.ToSlash(path)
	if strings.Contains(p, "/primes/") {
		return "primes"
	}
	if strings.Contains(p, "/zooms/") {
		return "zooms"
	}
	return "other"
}

func fieldAngleStr(fields []types.FieldItem) string {
	parts := make([]string, len(fields))
	for i, f := range fields {
		if f.ImageHeight > 0 && f.AngleDeg == 0 {
			parts[i] = fmt.Sprintf("h%.1f", f.ImageHeight)
		} else {
			parts[i] = fmt.Sprintf("%.1f", f.AngleDeg)
		}
	}
	return strings.Join(parts, ",")
}

func firstWavelength(wavelengths []types.WavelengthItem) float64 {
	for _, w := range wavelengths {
		if w.Primary && w.Value > 0 {
			return w.Value
		}
	}
	for _, w := range wavelengths {
		if w.Value > 0 {
			return w.Value
		}
	}
	return types.DefaultWavelength
}

// traceStopPoint reports the last surface a failed ray reached plus the
// material of the surface it was entering next (the usual failure site).
func traceStopPoint(res types.RayResult, surfaces []types.Surface) string {
	if len(res.Surfaces) == 0 {
		return "0"
	}
	last := res.Surfaces[len(res.Surfaces)-1]
	nextMat := ""
	for i := range surfaces {
		if surfaces[i].ID == last.SurfaceID && i+1 < len(surfaces) {
			nextMat = surfaces[i+1].Material
			break
		}
	}
	return fmt.Sprintf("%d->%s", last.SurfaceID, nextMat)
}

func rows(recs []record) [][]string {
	out := make([][]string, len(recs))
	for i, r := range recs {
		out[i] = r.row()
	}
	return out
}

func writeCSV(path string, rows [][]string) {
	f, err := os.Create(path)
	if err != nil {
		fmt.Fprintf(os.Stderr, "cannot create %s: %v\n", path, err)
		os.Exit(1)
	}
	defer f.Close()
	w := csv.NewWriter(f)
	if err := w.WriteAll(rows); err != nil {
		fmt.Fprintf(os.Stderr, "csv write %s: %v\n", path, err)
		os.Exit(1)
	}
	w.Flush()
	if err := w.Error(); err != nil {
		fmt.Fprintf(os.Stderr, "csv flush %s: %v\n", path, err)
		os.Exit(1)
	}
}

// summarize collapses the sweep into per-group status tables plus failure
// reason counts.
func summarize(recs []record, skipped []string, root string, total int) [][]string {
	type bucket struct {
		parseOK    int
		chiefOK    int
		allTraced  int
		paraxOK    int
	}
	byGroup := map[string]*bucket{}

	parseFail := 0
	noSurfaces := 0
	chiefEmpty := 0
	noTrace := 0
	traceFail := 0
	noParax := 0
	vignetted := 0
	modelFailFiles := 0

	for _, r := range recs {
		b := byGroup[r.Group]
		if b == nil {
			b = &bucket{}
			byGroup[r.Group] = b
		}
		if r.ParseOK {
			b.parseOK++
		} else {
			parseFail++
			if r.ParseError == "no surfaces" {
				noSurfaces++
			}
			continue
		}
		if r.ChiefOK {
			b.chiefOK++
		} else {
			chiefEmpty++
		}
		if r.TraceTotal > 0 && (r.TraceOK == r.TraceTotal || r.Vignetted) {
			b.allTraced++
		} else if r.TraceTotal == 0 {
			noTrace++
		} else {
			traceFail++
		}
		if r.Vignetted {
			vignetted++
		}
		if r.ModelFailCount > 0 {
			modelFailFiles++
		}
		if r.ParaxialOK {
			b.paraxOK++
		} else {
			noParax++
		}
	}

	groups := make([]string, 0, len(byGroup))
	for g := range byGroup {
		groups = append(groups, g)
	}
	sort.Strings(groups)

	// A problem is "critical" when the pipeline cannot proceed at all.
	critical := 0
	problems := 0
	for _, r := range recs {
		if !r.ParseOK || !r.ChiefOK || r.TraceTotal == 0 {
			critical++
			problems++
		} else if !r.Vignetted && (r.TraceOK < r.TraceTotal || !r.ParaxialOK || r.GridError > 0 || r.ChiefFailed > 0) {
			problems++
		}
	}

	out := [][]string{
		{"metric", "value"},
		{"root", root},
		{"total_files", itoa(total)},
		{"swept", itoa(len(recs))},
		{"skipped_zda_zar", itoa(len(skipped))},
		{"parse_ok", itoa(countBool(recs, func(r record) bool { return r.ParseOK }))},
		{"parse_fail", itoa(parseFail)},
		{"parse_fail_no_surfaces", itoa(noSurfaces)},
		{"chief_ok", itoa(countBool(recs, func(r record) bool { return r.ChiefOK }))},
		{"chief_empty", itoa(chiefEmpty)},
		{"traced_ok_all_rays", itoa(countBool(recs, func(r record) bool { return r.TraceTotal > 0 && r.TraceOK == r.TraceTotal }))},
		{"trace_ok_or_vignetted", itoa(countBool(recs, func(r record) bool { return r.TraceTotal > 0 && (r.TraceOK == r.TraceTotal || r.Vignetted) }))},
		{"trace_fail_some_rays", itoa(traceFail)},
		{"trace_no_rays", itoa(noTrace)},
		{"vignetted", itoa(vignetted)},
		{"model_fail_files", itoa(modelFailFiles)},
		{"paraxial_ok", itoa(countBool(recs, func(r record) bool { return r.ParaxialOK }))},
		{"paraxial_missing", itoa(noParax)},
		{"with_problems", itoa(problems)},
		{"critical_fail", itoa(critical)},
	}
	out = append(out, []string{})
	out = append(out, []string{"group", "files", "parse_ok", "chief_ok", "traced_all", "paraxial_ok"})
	for _, g := range groups {
		b := byGroup[g]
		cnt := 0
		for _, r := range recs {
			if r.Group == g {
				cnt++
			}
		}
		out = append(out, []string{g, itoa(cnt), itoa(b.parseOK), itoa(b.chiefOK), itoa(b.allTraced), itoa(b.paraxOK)})
	}
	return out
}

func countBool(recs []record, pred func(record) bool) int {
	n := 0
	for _, r := range recs {
		if pred(r) {
			n++
		}
	}
	return n
}

func printSummary(rows [][]string) {
	fmt.Println()
	for _, r := range rows {
		if len(r) == 2 {
			fmt.Printf("%-28s %s\n", r[0], r[1])
		} else if len(r) == 6 && r[0] != "group" {
			fmt.Printf("group %-8s files=%s parse_ok=%s chief_ok=%s traced_all=%s paraxial_ok=%s\n", r[0], r[1], r[2], r[3], r[4], r[5])
		}
	}
}
