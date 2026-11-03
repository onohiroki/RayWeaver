package optimize

import (
	"fmt"
	"math"
	"strings"
	"sync"

	"github.com/hiroki/rayweaver/internal/glass"
	"github.com/hiroki/rayweaver/internal/paraxial"
	"github.com/hiroki/rayweaver/internal/psf"
	"github.com/hiroki/rayweaver/internal/surface"
	"github.com/hiroki/rayweaver/internal/types"
	"github.com/hiroki/rayweaver/internal/wavefront"
)

// backFocusSolveEntry binds a surface whose thickness will be adjusted to
// keep the image plane at the desired focus position.
type backFocusSolveEntry struct {
	solveID int // surface ID whose thickness is adjusted
}

// updateBackFocusType switches the active back-focus solve type based on the
// merit schedule's dominant mode. When the schedule is active and the dominant
// mode has a non-empty back_focus_type, the type is switched if the mode's
// weight exceeds the dominant_threshold. Otherwise the fixed type from
// back_focus_solve is used.
func (o *Optimizer) updateBackFocusType() {
	if o.backFocusSolve == nil {
		return
	}
	// Default to the fixed type.
	fallback := o.backFocusSolve.Type
	if fallback == "" || !backFocusTypeValid(fallback) {
		// An unknown type was already reported by SetBackFocusSolve; the
		// paraxial branch is what actually runs, so name it.
		fallback = "paraxial"
	}
	if o.backFocusSchedule == nil || o.meritSchedule == nil {
		o.currentBackFocusType = fallback
		return
	}
	// Find the dominant mode (largest weight, deterministic tie-break).
	bestMode := dominantMode(o.modeWeights)
	bestWeight := o.modeWeights[bestMode]
	threshold := o.backFocusSchedule.DominantThreshold
	if threshold == 0 {
		threshold = 0.5
	}
	if bestWeight >= threshold {
		for _, m := range o.meritSchedule.modes {
			// An unknown mode type cannot be honoured: it would silently
			// select the paraxial branch, so fall back to the static type
			// instead (SetMeritSchedule has already reported it once).
			if m.name == bestMode && m.backFocusType != "" && backFocusTypeValid(m.backFocusType) {
				o.currentBackFocusType = m.backFocusType
				return
			}
		}
	}
	o.currentBackFocusType = fallback
}

// backFocusTypeValid reports whether t names a known back-focus solve type.
// The empty string is valid: it means "unset" and resolves to the paraxial
// default. An unknown value would otherwise silently select the paraxial
// branch of the switch while the document claims another method, which is why
// the configured values are validated once at configuration time.
func backFocusTypeValid(t string) bool {
	switch t {
	case "", "paraxial", "wavefront", "spot":
		return true
	}
	return false
}

// SetBackFocusSolve configures the back-focus solve. When enabled, the
// thickness of the target surface is adjusted after every variable application
// so the image plane stays at the desired focus position (paraxial,
// wavefront- or spot-based). The target surface defaults to the last lens
// surface before the image plane (auto-detect); a positive Surface value
// overrides it.
func (o *Optimizer) SetBackFocusSolve(cfg *types.BackFocusSolveConfig) {
	if cfg == nil || !cfg.Enabled {
		return
	}
	o.backFocusSolve = cfg
	if !backFocusTypeValid(cfg.Type) {
		Warnf("warning: optimization.back_focus_solve.type %q is not a known solve type (paraxial | wavefront | spot); the paraxial solve is used", cfg.Type)
	}

	targets := make(map[string][]backFocusSolveEntry, len(o.configs))
	for ci := range o.configs {
		cfg := &o.configs[ci]
		cfg.backFocusSolve = o.backFocusSolve
		surfaces := cfg.surfaces
		sid := cfg.resolveBackFocusTarget(surfaces, o.gc)
		if sid < 0 {
			continue
		}
		targets[cfg.id] = append(targets[cfg.id], backFocusSolveEntry{solveID: sid})
	}
	o.backFocusTargets = targets
	// Set the initial back-focus type (no schedule yet, just the fixed type).
	o.updateBackFocusType()
}

// resolveBackFocusTarget resolves the config's back-focus target surface ID
// (see resolveBackFocusTargetID).
func (cfg *config) resolveBackFocusTarget(surfaces []types.Surface, gc *glass.Catalog) int {
	return resolveBackFocusTargetID(surfaces, cfg.backFocusSolve, gc)
}

// backFocusPowerEps is the thin-lens power magnitude (mm⁻¹) at or below which
// an element counts as powerless (a window / filter / dummy plate) for the
// back-focus target auto-detect.
const backFocusPowerEps = 1e-6

// resolveBackFocusTargetID returns the surface ID whose thickness will be
// adjusted by the back-focus solve. When cfg specifies a positive Surface ID it
// is validated and returned directly. Otherwise the target is the back air gap
// immediately after the image-side-most *powered* element — the rear surface of
// the last element whose thin-lens power is non-zero. Adjusting that thickness
// refocuses the image plane without changing any element's thickness; when a
// powerless element (window / filter) sits before the image plane the chosen
// gap is the one *before* it, so the downstream block translates rigidly with
// the image plane. gc is the glass catalog for the element-power test; a nil
// catalog skips the power filter and returns the last glass element's rear
// surface. A return value of -1 means no suitable surface was found.
func resolveBackFocusTargetID(surfaces []types.Surface, cfg *types.BackFocusSolveConfig, gc *glass.Catalog) int {
	if len(surfaces) < 3 {
		return -1
	}
	// user-specified surface ID
	if cfg != nil && cfg.Surface > 0 {
		sid := cfg.Surface
		for _, s := range surfaces {
			if s.ID == sid {
				return sid
			}
		}
		return -1
	}
	// Auto-detect: the back air gap of the image-side-most powered element. A
	// mirror is its own (single-surface) element and its following gap is the
	// back focus. A rear surface of a glass element has air after it and glass
	// before it; its thickness is the air gap (the back focal distance).
	imgIdx := len(surfaces) - 1
	for i := imgIdx - 1; i >= 1; i-- {
		s := surfaces[i]
		if s.Reflects() {
			return s.ID
		}
		if !s.Material.IsAir() || surfaces[i-1].Material.IsAir() {
			continue
		}
		if gc != nil && math.Abs(paraxial.ElementPowerCurvature(surfaces, gc, s.ID)) <= backFocusPowerEps {
			continue // powerless element (window / filter): keep scanning object-ward
		}
		return s.ID
	}
	return surfaces[imgIdx-1].ID
}

// applyBackFocusSolve adjusts the thickness of the target surface so the image
// plane stays at the desired focus position. For paraxial type it computes the
// finite-conjugate image position from the paraxial trace and adjusts the
// thickness accordingly. For wavefront type it runs a minimal wavefront
// analysis and applies the best-focus shift; for spot type it runs the
// geometric spot-RMS solve (psf.SpotFocusShift). It is called from
// applyVariables (pure) after applyPowerSolve and before Precompute.
func (o *Optimizer) applyBackFocusSolve(configSurfaces map[string][]types.Surface, pupils map[string]appliedPupil, gc *glass.Catalog) {
	if o.backFocusSolve == nil || len(o.backFocusTargets) == 0 {
		return
	}
	for cid, entries := range o.backFocusTargets {
		surfaces, ok := configSurfaces[cid]
		if !ok {
			continue
		}
		cfg := o.findConfig(cid)
		if cfg == nil {
			continue
		}
		// paraxial.Compute reads ParaxialRadius, which Precompute refreshes
		// from the surface curvatures. The solve runs before the caller's
		// Precompute loop, so refresh here or the shift is computed from the
		// stale radii of the previous variable state.
		surface.Precompute(surfaces)
		for _, e := range entries {
			idx := surfaceIndex(surfaces, e.solveID)
			if idx < 0 {
				continue
			}
			var shift float64
			switch o.currentBackFocusType {
			case "wavefront":
				out, bfErr := o.wavefrontBackFocusShift(cfg, surfaces, pupils[cfg.id], gc)
				if bfErr != nil {
					// The field-tolerant solve already dropped every
					// degenerate field, so this is a total failure. Falling
					// back to the cheap paraxial solve keeps the image plane
					// at least at the design focus; leaving it stale ships a
					// system tens of mm out of focus while the merit keeps
					// improving against the wrong plane.
					shift = o.paraxialBackFocusShift(cfg, surfaces, gc)
					o.backFocusStats.record("paraxial", shift, 0, nil, true)
					o.backFocusWarn(bfErr, "paraxial focus applied")
				} else {
					shift = out.ShiftMM
					o.backFocusStats.record("wavefront", out.ShiftMM, out.FieldsUsed, out.FieldsDropped, false)
					if len(out.FieldsDropped) > 0 {
						o.backFocusWarn(
							fmt.Errorf("%d of %d field(s) dropped: %s",
								len(out.FieldsDropped), out.FieldsUsed+len(out.FieldsDropped),
								strings.Join(out.FieldsDropped, "; ")),
							"wavefront best focus of the surviving fields applied")
					}
				}
			case "spot":
				// The geometric spot-RMS solve: same partial/fallback
				// contract as the wavefront case, cheaper per evaluation.
				out, bfErr := o.spotBackFocusShift(cfg, surfaces, pupils[cfg.id], gc)
				if bfErr != nil {
					shift = o.paraxialBackFocusShift(cfg, surfaces, gc)
					o.backFocusStats.record("paraxial", shift, 0, nil, true)
					o.backFocusWarn(bfErr, "paraxial focus applied")
				} else {
					shift = out.ShiftMM
					o.backFocusStats.record("spot", out.ShiftMM, out.FieldsUsed, out.FieldsDropped, false)
					if len(out.FieldsDropped) > 0 {
						o.backFocusWarn(
							fmt.Errorf("%d of %d field(s) dropped: %s",
								len(out.FieldsDropped), out.FieldsUsed+len(out.FieldsDropped),
								strings.Join(out.FieldsDropped, "; ")),
							"spot best focus of the surviving fields applied")
					}
				}
			default: // "paraxial"
				shift = o.paraxialBackFocusShift(cfg, surfaces, gc)
				o.backFocusStats.record("paraxial", shift, 0, nil, false)
			}
			newThk := surfaces[idx].Thickness + shift
			if newThk < 0.1 {
				newThk = 0.1
			}
			surfaces[idx].Thickness = newThk
		}
	}
}

// paraxialBackFocusShift computes the image-plane shift needed to bring the
// image plane to the paraxial focus position (see paraxialBackFocusShiftFor).
func (o *Optimizer) paraxialBackFocusShift(cfg *config, surfaces []types.Surface, gc *glass.Catalog) float64 {
	wlOverride := 0.0
	if o.backFocusSolve != nil {
		wlOverride = o.backFocusSolve.Wavelength
	}
	return paraxialBackFocusShiftFor(surfaces, cfg.stopSurface, cfg.referenceWavelength, cfg.wavelengths, wlOverride, gc)
}

// paraxialBackFocusShiftFor computes the image-plane shift needed to bring the
// image plane to the paraxial focus position. The shift is the difference
// between the current image-plane distance (from the target surface to the
// image plane) and the desired distance from the paraxial trace. A positive
// wlOverride forces the evaluation wavelength (back_focus_solve.wavelength);
// zero falls back to the config's first wavelength or its reference.
func paraxialBackFocusShiftFor(surfaces []types.Surface, stopSurface int, refWavelength float64, wavelengths []types.WavelengthItem, wlOverride float64, gc *glass.Catalog) float64 {
	if len(surfaces) < 3 {
		return 0
	}
	wl := wlOverride
	if wl <= 0 {
		wl = effectiveReferenceWavelength(refWavelength)
		if len(wavelengths) > 0 {
			wl = wavelengths[0].Value
		}
	}
	if wl <= 0 {
		wl = types.DefaultWavelength
	}
	// Compute paraxial result (finite conjugate or infinite).
	sys := types.System{Surfaces: surfaces, StopSurface: stopSurface}
	pr := paraxial.Compute(sys, wl, gc, 0, nil)

	// Reference vertex for the paraxial BFL: paraxial.Compute measures
	// SecondPrincipalFocus from the last surface that is not a degenerate
	// trailing plane (Radius()==0 && Thickness==0), which may be an air
	// spacing surface carrying a radius. Use the same vertex so the desired
	// image position is consistent (using the last non-air surface instead
	// would offset the focus by any dummy air surface before the image).
	refIdx := len(surfaces) - 1
	for refIdx > 0 && surfaces[refIdx].Radius() == 0 && surfaces[refIdx].Thickness == 0 {
		refIdx--
	}

	imgIdx := len(surfaces) - 1

	// Physical positions after Precompute (must be refreshed).
	physZ := surface.PhysicalZ(surfaces)
	if len(physZ) == 0 {
		return 0
	}
	lastLensVertexZ := physZ[refIdx]

	// Current image-plane Z (sum of thicknesses from target surface onward
	// plus the target vertex position).
	currentImageZ := physZ[imgIdx]
	// Desired image-plane Z: reference vertex + paraxial BFL.
	bfl := pr.SecondPrincipalFocus
	if bfl == 0 {
		return 0
	}
	desiredImageZ := lastLensVertexZ + bfl
	return desiredImageZ - currentImageZ
}

// backFocusOutcome is one wavefront back-focus solve's report: the shift it
// applied and which of the requested fields actually contributed to it.
type backFocusOutcome struct {
	ShiftMM       float64
	FieldsUsed    int
	FieldsDropped []string
}

// backFocusDiagnostics accumulates the in-run back-focus solve outcomes. It is
// embedded in the Optimizer and guarded by its mutex because the solve runs
// from applyVariables, which the DLS Jacobian sweep calls concurrently for
// every column.
type backFocusDiagnostics struct {
	mu sync.Mutex
	v  types.BackFocusDiagnostics
}

// BackFocusDiagnostics returns the run-level back-focus solve report for
// opt_results.back_focus, or nil when the solve is not configured.
func (o *Optimizer) BackFocusDiagnostics() *types.BackFocusDiagnostics {
	if o.backFocusSolve == nil || !o.backFocusSolve.Enabled {
		return nil
	}
	snap := o.backFocusStats.snapshot()
	return &snap
}

// backFocusWarn reports a back-focus solve problem (a dropped field set or a
// total failure that fell back) without flooding stderr: the first three
// events are always shown, then one in 50, each carrying its sequence number
// so a long run still shows progress.
func (o *Optimizer) backFocusWarn(err error, action string) {
	n := o.backFocusWarnings.Add(1)
	if n > 3 && n%50 != 0 {
		return
	}
	Warnf("back_focus: %v; %s (event %d)", err, action, n)
}

// wavefrontBackFocusShift computes the image-plane shift needed to bring the
// image plane to the wavefront best-focus position (see
// wavefrontBackFocusShiftFor). An error means the wavefront could not be
// evaluated for *any* field; the shift is then 0 and the caller falls back.
// backFocusPupilModel resolves the virtual entrance pupil one back-focus solve
// must sample for a given evaluation: the config's declared model with that
// evaluation's pupil-model variable values (appliedPupil) on top.
//
// The declared model is only the seed — axial_position is an optimisation
// variable (the vp_z demo sweeps -20..+50 mm) — and every consumer of the
// pupil already reads the variable's value: the merit grid (gridCentring), the
// diffraction MTF (frozenZ/EPDOverride) and, post-run, `psf`, `wavefront` and
// the gate evaluation of the delivered document. Solving the plane at the
// declared z therefore focused a *different* bundle than the one the merit
// evaluated and the deliverable ships: measured on the v50 minima, the saved
// plane was the declared-15 mm fixed point to 1e-8 mm while the document's own
// pupil asked for 0.01..0.77 mm of travel, and applying that travel moved the
// on-axis MTF10 of the second-best minimum from 0.164 to 0.887.
//
// Returns nil only when the document declares no pupil model and this
// evaluation has no active pupil (the "no virtual pupil" case), which keeps the
// solve's behaviour identical to a plain wavefront analysis.
func backFocusPupilModel(cfg *config, p appliedPupil) *types.PupilModelConfig {
	if cfg.pupilModel == nil && !p.active {
		return nil
	}
	var m types.PupilModelConfig
	if cfg.pupilModel != nil {
		m = *cfg.pupilModel
	}
	if p.active {
		m.Mode = "virtual_entrance_pupil"
		m.AxialPosition = p.z
	}
	if p.dia > 0 {
		m.Diameter = p.dia
	}
	return &m
}

// wavefrontBackFocusShift solves the image plane for this evaluation's pupil
// (see backFocusPupilModel).
func (o *Optimizer) wavefrontBackFocusShift(cfg *config, surfaces []types.Surface, p appliedPupil, gc *glass.Catalog) (backFocusOutcome, error) {
	return wavefrontBackFocusShiftFor(surfaces, o.backFocusSolve, cfg.stopSurface, cfg.referenceWavelength, cfg.fieldDefs, gc, cfg.rayDefinition, backFocusPupilModel(cfg, p))
}

// backFocusWavelength resolves the wavelength a wavefront back-focus solve is
// evaluated at: the configured value, else the configuration's reference
// wavelength, else the d-line (types.BackFocusSolveConfig.Wavelength's
// documented default). The config's wavelength *list* deliberately does not
// participate: preferring its first entry optimised the plane at whatever
// wavelength the document happened to list first — the F line of a standard
// [F, d, C] list — a different focus from the one the reference wavelength
// asks for, so `focus --planes all` reported a shift against a plane the
// deliverable never carried.
func backFocusWavelength(bfs *types.BackFocusSolveConfig, refWavelength float64) float64 {
	if bfs != nil && bfs.Wavelength > 0 {
		return bfs.Wavelength
	}
	return effectiveReferenceWavelength(refWavelength)
}

// wavefrontBackFocusShiftFor computes the image-plane shift needed to bring the
// image plane to the wavefront best-focus position. A minimal wavefront
// analysis is run with the configured settings (num_rays, weight_type) at
// backFocusWavelength. It
// returns an error when *no* field could be analysed (a degenerate pupil grid,
// a failed paraboloid fit, ...); the caller must surface that instead of
// silently keeping a stale image plane.
//
// The analysis runs with Options.Partial: a single degenerate field — a
// clipped off-axis bundle under a tight aperture, a singular fit — is dropped
// and reported in the outcome's FieldsDropped instead of failing the whole
// solve, so the image plane still follows the fields that did analyse. Only a
// total failure (every field degenerate) returns an error, which the caller
// turns into a paraxial fallback.
//
// pupilModel is the virtual entrance pupil the analysis must sample: the
// document's model with the current pupil-model variables applied (see
// backFocusPupilModel). It must be forwarded so the solve samples the same
// bundle the merit grid, `psf`, `wavefront` and the gate evaluation use.
// Without it wavefront falls back to its stop/dynamic pupil, which a
// stopped-down or tightly auto-apertured system clips down to a degenerate
// sample set (observed as "paraboloid fit: singular normal matrix" on a
// 6-element whose deliverable apertures were sized for the real pupil).
func wavefrontBackFocusShiftFor(surfaces []types.Surface, bfs *types.BackFocusSolveConfig, stopSurface int, refWavelength float64, fieldDefs []types.FieldDef, gc *glass.Catalog, rayDefinition string, pupilModel *types.PupilModelConfig) (backFocusOutcome, error) {
	if bfs == nil || len(surfaces) < 2 {
		return backFocusOutcome{}, nil
	}
	wl := backFocusWavelength(bfs, refWavelength)
	refSurf := bfs.ReferenceSurface
	if refSurf <= 0 {
		refSurf = psf.DefaultReferenceSurface(surfaces)
	}
	numRays := bfs.NumRays
	if numRays <= 0 {
		numRays = 200
	}

	// Select fields based on weight_type.
	fields := backFocusFieldsFor(fieldDefs, bfs.WeightType)

	// wavefront.FocusConfig combines the selected fields with "uniform" or
	// "custom" weights only; "on_axis_only" is a field-selection mode, so the
	// single selected field is combined uniformly.
	focusCfg := wavefront.FocusConfig{WeightType: bfs.WeightType}
	switch bfs.WeightType {
	case "custom":
		focusCfg.CustomWeights = bfs.CustomWeights
	case "uniform":
		// pass through
	default: // "" or "on_axis_only"
		focusCfg.WeightType = "uniform"
	}

	sys := types.System{Surfaces: surfaces, StopSurface: stopSurface}
	opts := wavefront.Options{
		ReferenceSurface: refSurf,
		NumRays:          numRays,
		Workers:          1,
		ZernikeMaxOrder:  0,
		BestFocus:        &focusCfg,
		PupilModel:       pupilModel,
		RayDefinition:    rayDefinition,
		// Field-tolerant: one degenerate field is dropped (and reported),
		// not fatal. An error here therefore means every field failed.
		Partial: true,
	}

	result, err := wavefront.Compute(sys, gc, fields, []float64{wl}, opts)
	if err != nil {
		return backFocusOutcome{}, fmt.Errorf("back-focus wavefront solve at %.3f nm: %w", wl*1e6, err)
	}
	if result == nil || result.BestFocus == nil {
		return backFocusOutcome{}, fmt.Errorf("back-focus wavefront solve at %.3f nm: no best focus returned", wl*1e6)
	}
	out := backFocusOutcome{
		ShiftMM:    result.BestFocus.ShiftMM,
		FieldsUsed: len(result.BestFocus.PerField),
	}
	for _, e := range result.Errors {
		out.FieldsDropped = append(out.FieldsDropped, e.Error())
	}
	return out, nil
}

// spotBackFocusShift solves the image plane for this evaluation's pupil with
// the geometric spot-RMS solve (see backFocusPupilModel).
func (o *Optimizer) spotBackFocusShift(cfg *config, surfaces []types.Surface, p appliedPupil, gc *glass.Catalog) (backFocusOutcome, error) {
	return spotBackFocusShiftFor(surfaces, o.backFocusSolve, cfg.stopSurface, cfg.referenceWavelength, cfg.fieldDefs, gc, cfg.rayDefinition, backFocusPupilModel(cfg, p))
}

// spotBackFocusShiftFor computes the image-plane shift needed to bring the
// image plane to the geometric spot-RMS minimum (psf.SpotFocusShift): the
// plane minimising the weighted flux-weighted spot radius² summed over the
// selected fields at backFocusWavelength. It is the cheap middle member of the
// back-focus family — a real-ray focus (so it tracks aberration, vignetting and
// field curvature, unlike the paraxial solve) without the coherent peak search
// of the wavefront solve: the spot radius is an exact quadratic of the plane,
// so one pupil-grid trace per field gives the minimiser in closed form.
//
// Field selection (weight_type) and the combination weights are the wavefront
// solve's: on_axis_only picks the on-axis field, uniform weighs every selected
// field equally, custom passes CustomWeights through (a length mismatch is an
// error, as in wavefront.ComputeBestFocus). pupilModel is forwarded for the
// same reason as in the wavefront solve — the solve must sample the bundle the
// merit grid, `psf` and the gate evaluation use.
//
// An error means *no* field could be sampled (or the bundle is afocal); the
// caller falls back to the paraxial focus exactly as it does for a failed
// wavefront solve. A field that cannot be sampled is otherwise dropped and
// reported in the outcome's FieldsDropped (partial solve).
func spotBackFocusShiftFor(surfaces []types.Surface, bfs *types.BackFocusSolveConfig, stopSurface int, refWavelength float64, fieldDefs []types.FieldDef, gc *glass.Catalog, rayDefinition string, pupilModel *types.PupilModelConfig) (backFocusOutcome, error) {
	if bfs == nil || len(surfaces) < 2 {
		return backFocusOutcome{}, nil
	}
	wl := backFocusWavelength(bfs, refWavelength)
	refSurf := bfs.ReferenceSurface
	if refSurf <= 0 {
		refSurf = psf.DefaultReferenceSurface(surfaces)
	}
	numRays := bfs.NumRays
	if numRays <= 0 {
		numRays = 200
	}

	fields := backFocusFieldsFor(fieldDefs, bfs.WeightType)
	var weights []float64
	switch bfs.WeightType {
	case "custom":
		if len(bfs.CustomWeights) != len(fields) {
			return backFocusOutcome{}, fmt.Errorf("back-focus spot solve at %.3f nm: %d custom weights for %d fields",
				wl*1e6, len(bfs.CustomWeights), len(fields))
		}
		weights = bfs.CustomWeights
	default: // "uniform", "on_axis_only" and empty: equal weights
	}

	sys := types.System{Surfaces: surfaces, StopSurface: stopSurface}
	res, err := psf.SpotFocusShift(sys, gc, fields, []float64{wl}, weights, psf.Options{
		ReferenceSurface: refSurf,
		NumRays:          numRays,
		Workers:          1,
		PupilModel:       pupilModel,
		RayDefinition:    rayDefinition,
	})
	if err != nil {
		return backFocusOutcome{}, fmt.Errorf("back-focus spot solve at %.3f nm: %w", wl*1e6, err)
	}
	return backFocusOutcome{
		ShiftMM:       res.ShiftMM,
		FieldsUsed:    res.FieldsUsed,
		FieldsDropped: res.FieldsDropped,
	}, nil
}

// backFocusFieldsFor selects the fields used by the back-focus spot and
// wavefront solves from the config's field definitions and the configured
// weight type.
func backFocusFieldsFor(fieldDefs []types.FieldDef, weightType string) []types.FieldDef {
	switch weightType {
	case "uniform":
		if len(fieldDefs) > 0 {
			return fieldDefs
		}
	case "custom":
		if len(fieldDefs) > 0 {
			return fieldDefs
		}
	default: // "on_axis_only" or empty
		for _, fd := range fieldDefs {
			if fd.Angle == 0 {
				return []types.FieldDef{fd}
			}
		}
		if len(fieldDefs) > 0 {
			return []types.FieldDef{fieldDefs[0]}
		}
	}
	return fieldDefs
}

// ApplyBackFocusSolve adjusts the target surface's thickness in place so the
// image plane sits at the back-focus position for the given solve type
// ("paraxial", "wavefront" or "spot"). It is the exported, schedule-free
// counterpart of the optimizer's back-focus hard solve, used to persist the
// same image plane the escape evaluated into its saved minima. cfg nil or
// disabled is a no-op; surface 0 auto-detects the last non-air surface before
// the image plane.
// fields are the config's field definitions in order, wavelengths its
// wavelength list, and stopSurface/refWavelength as in the optimizer.
// pupilModel is the document's virtual entrance pupil (nil when unused); it is
// forwarded to the wavefront analysis so the solve samples the same bundle the
// merit grid and the psf/wavefront commands do.
//
// It returns true when the solve ran and a shift was applied, false when it
// was not applicable (cfg nil/disabled, no resolvable target) or when the
// wavefront / spot analysis failed — a failure is reported through Warnf
// instead of being silently swallowed, because the caller then ships the
// unshifted image plane it was given.
func ApplyBackFocusSolve(surfaces []types.Surface, cfg *types.BackFocusSolveConfig, bfType string, stopSurface int, refWavelength float64, fields []types.FieldItem, wavelengths []types.WavelengthItem, gc *glass.Catalog, rayDefinition string, pupilModel *types.PupilModelConfig) bool {
	if cfg == nil || !cfg.Enabled {
		return false
	}
	targetID := resolveBackFocusTargetID(surfaces, cfg, gc)
	if targetID < 0 {
		return false
	}
	idx := surfaceIndex(surfaces, targetID)
	if idx < 0 {
		return false
	}
	fieldDefs := fieldDefsFromItems(fields)
	// paraxial.Compute / wavefront.Compute expect freshly precomputed radii.
	surface.Precompute(surfaces)
	var shift float64
	switch bfType {
	case "wavefront", "spot":
		// Both real-ray solves share the partial contract: a degenerate
		// field set is dropped and reported, a total failure leaves the
		// plane untouched (and is reported here instead of being swallowed).
		var out backFocusOutcome
		var err error
		if bfType == "wavefront" {
			out, err = wavefrontBackFocusShiftFor(surfaces, cfg, stopSurface, refWavelength, fieldDefs, gc, rayDefinition, pupilModel)
		} else {
			out, err = spotBackFocusShiftFor(surfaces, cfg, stopSurface, refWavelength, fieldDefs, gc, rayDefinition, pupilModel)
		}
		if err != nil {
			Warnf("warning: %v; the image plane keeps its current thickness", err)
			return false
		}
		shift = out.ShiftMM
		if len(out.FieldsDropped) > 0 {
			// Partial solve: the shift is the weighted best focus of the
			// surviving fields. Worth stating — the persisted plane is not
			// the one every field would have asked for.
			Warnf("back_focus: %d of %d field(s) dropped (%s); the %s best focus of the surviving fields was applied",
				len(out.FieldsDropped), out.FieldsUsed+len(out.FieldsDropped), strings.Join(out.FieldsDropped, "; "), bfType)
		}
	default: // "paraxial" (also the fallback for an unknown type)
		shift = paraxialBackFocusShiftFor(surfaces, stopSurface, refWavelength, wavelengths, cfg.Wavelength, gc)
	}
	newThk := surfaces[idx].Thickness + shift
	if newThk < 0.1 {
		newThk = 0.1
	}
	surfaces[idx].Thickness = newThk
	return true
}

// restoreDiameters resets auto_aperture surfaces to their initial diameters
// so sizeAutoApertures can measure the true geometric extent.
func (o *Optimizer) restoreDiameters(cfg *config, surfaces []types.Surface) {
	for i := range surfaces {
		key := cfgSurfKey(cfg.id, surfaces[i].ID)
		if d, ok := o.initialDiameters[key]; ok {
			surfaces[i].Diameter = d
		}
	}
}

// sizeAperturesForBackFocus sizes every config's auto_aperture surfaces from the
// current variable state so the immediately following back-focus solve uses the
// same beam envelope the merit grid traces. It mirrors the evaluators'
// restoreDiameters + sizeAutoApertures pair; the sizing the evaluators run
// afterwards reproduces identical diameters (deterministic traces), so the only
// thing the earlier call changes is the solve's input. During a Jacobian sweep
// sizeAutoApertures reads the diameters cached by BeginJacobian, so no trace is
// repeated.
func (o *Optimizer) sizeAperturesForBackFocus(configSurfaces map[string][]types.Surface, pupils map[string]appliedPupil, gc *glass.Catalog) {
	for ci := range o.configs {
		cfg := &o.configs[ci]
		surfaces, ok := configSurfaces[cfg.id]
		if !ok {
			continue
		}
		// The variable-applied curvatures have not been precomputed yet in
		// applyVariables; the aperture measurement (ApertureRadiusForGrid ->
		// paraxial.Compute) reads ParaxialRadius, so refresh first.
		surface.Precompute(surfaces)
		o.restoreDiameters(cfg, surfaces)
		o.sizeAutoApertures(cfg, surfaces, gc, nil, pupils[cfg.id])
	}
}
