package optimize

import (
	"math"

	"github.com/hiroki/rayweaver/internal/chief"
	"github.com/hiroki/rayweaver/internal/dls"
	"github.com/hiroki/rayweaver/internal/glass"
	"github.com/hiroki/rayweaver/internal/pupil"
	"github.com/hiroki/rayweaver/internal/ray"
	"github.com/hiroki/rayweaver/internal/raymath"
	"github.com/hiroki/rayweaver/internal/types"
)

// fieldDefsFromItems converts the per-config field items into chief field
// definitions for the dynamic-pupil, back-focus and aperture-sizing passes.
//
// Vignetting is carried across so every one of those passes traces the field's
// prescribed (vignetted) pupil, exactly like the merit grid and the standalone
// chief / psf / wavefront commands. Dropping it made the footprint measurement
// envelope the full pupil while the merit evaluated the vignetted one, so the
// sized diameters and the traced beams disagreed.
func fieldDefsFromItems(items []types.FieldItem) []types.FieldDef {
	if len(items) == 0 {
		return nil
	}
	out := make([]types.FieldDef, len(items))
	for i, f := range items {
		out[i] = types.FieldDef{
			Angle:       f.AngleDeg,
			ImageHeight: f.ImageHeight,
			Direction:   []float64{0, 1},
			Vignetting:  f.Vignetting,
		}
	}
	return out
}

// normalizeDir returns the unit vector of a non-degenerate 2D direction,
// falling back to the Y axis.
func normalizeDir(dir []float64) (float64, float64) {
	dx, dy := dir[0], dir[1]
	norm := math.Hypot(dx, dy)
	if norm == 0 {
		return 0, 1
	}
	return dx / norm, dy / norm
}

// fieldDir resolves a field's image-plane azimuth to a unit vector. It returns
// ok=false when the field carries no explicit direction (the caller keeps the
// Y-axis default).
func fieldDir(dir []float64) (float64, float64, bool) {
	if len(dir) < 2 {
		return 0, 1, false
	}
	dx, dy := normalizeDir(dir)
	return dx, dy, true
}

// termFieldAngle returns the field angle for a merit term. A term on a field
// declared by image_height reads the angle frozen at the start geometry (see
// freezeFieldAngles); only a term the freeze did not cover — one naming a field
// the config does not carry — resolves it now.
func (o *Optimizer) termFieldAngle(cfg *config, term *meritTerm, surfaces []types.Surface, gc *glass.Catalog) float64 {
	if !term.useImageHeight {
		return term.fieldAngle
	}
	if a, ok := frozenFieldAngle(cfg, term.fieldIndex); ok {
		return a
	}
	return o.imageHeightToFieldAngle(cfg, surfaces, term.imageHeight, term.wavelength, gc)
}

// constraintFieldAngle resolves the field angle of a constraint operand, from
// the same frozen field set the merit terms read.
func (o *Optimizer) constraintFieldAngle(cfg *config, c types.ConstraintOperand, surfaces []types.Surface, gc *glass.Catalog) float64 {
	for i := range cfg.fields {
		f := &cfg.fields[i]
		if f.ID != c.Field {
			continue
		}
		if a, ok := frozenFieldAngle(cfg, i); ok {
			return a
		}
		if f.AngleDeg != 0 || f.ImageHeight == 0 {
			return f.AngleDeg
		}
		return o.imageHeightToFieldAngle(cfg, surfaces, f.ImageHeight, 0, gc)
	}
	return 0
}

// fieldVignetting returns the vignetting ellipse declared for a merit term's
// field, so the grid-trace merit kinds (spot_*, opd_rms, geometric_mtf_*,
// field_alive, pupil_fill) see the same vignetted pupil the wavefront terms and
// the chief / psf / wavefront commands use. A term without a field index (or an
// index outside the config's field list) keeps the full, unclipped pupil.
func (o *Optimizer) fieldVignetting(cfg *config, term *meritTerm) *types.VignettingDef {
	if cfg == nil || term.fieldIndex < 0 || term.fieldIndex >= len(cfg.fields) {
		return nil
	}
	return cfg.fields[term.fieldIndex].Vignetting
}

// flatnessFieldTerm clones a spot_flatness term for config field i so the
// across-field mean is traced with that field's own angle (or image height),
// vignetting ellipse and direction — the same state gridForTerm uses for the
// field's own terms, hence the shared cache key.
func flatnessFieldTerm(cfg *config, base *meritTerm, i int) meritTerm {
	t := *base
	t.fieldIndex = i
	t.useImageHeight = false
	t.imageHeight = 0
	t.fieldAngle = 0
	f := cfg.fields[i]
	if f.AngleDeg != 0 || f.ImageHeight == 0 {
		t.fieldAngle = f.AngleDeg
	} else {
		t.useImageHeight = true
		t.imageHeight = f.ImageHeight
	}
	t.fieldWeight = 1.0
	if f.Weight > 0 {
		t.fieldWeight = f.Weight
	}
	if dx, dy, ok := fieldDir(f.Direction); ok {
		t.fieldDirX, t.fieldDirY = dx, dy
	}
	return t
}

// resolveImageHeightField finds the field angle that lands the chief ray at the
// target image height, via bisection on the image-plane intersection, and
// reports the height the angle actually delivers and whether the request was
// met.
//
// The probe is launched through the entrance-pupil centre (the frozen
// per-iteration pupil from UpdatePupils), not from the optical axis: an axis
// launch at a non-zero field angle leaves z = -100 at (0,0) and therefore
// arrives at the lens 100*tan(theta) off the axis, far outside every aperture.
// With a stop on the first surface (the wide-angle zoom layouts) that clipped
// every off-axis probe, so the bisection collapsed and the optimizer silently
// worked the fields on-axis. When no pupil has been resolved the probe keeps
// the historical axis launch.
//
// Meeting the request is verified, not assumed: the height function is not
// necessarily monotone or continuous — as the angle grows the traced chief ray
// can switch branch, leaving a step that no angle lands on — so a collapsed
// bracket can sit on such a step and deliver a height nowhere near the request
// (the seed's wide position reported 1.533 mm for a declared 4.868 mm). A
// solved angle is therefore accepted only when it delivers the requested height
// within chief.ImageHeightTol, the same acceptance the `chief` command applies;
// otherwise the probed angle whose delivered height comes closest is returned
// with resolved=false, so the caller can never mistake it for the declared
// field.
func (o *Optimizer) resolveImageHeightField(cfg *config, surfaces []types.Surface, targetHeight, wavelength float64, gc *glass.Catalog) (angle, reached float64, resolved bool) {
	apertureRadius := dls.ApertureRadiusForGrid(surfaces, cfg.stopSurface, wavelength, gc, o.apertureMargin, 0)
	if apertureRadius <= 0 {
		return 0, 0, false
	}

	heightAt := func(angle float64) (float64, bool) {
		return o.imageHeightAtAngle(cfg, surfaces, angle, wavelength, gc)
	}

	// Signed target: the traced image height carries the field sign (angle
	// theta lands at +height for the +Y direction), so the search compares
	// magnitudes and the recovered angle takes the sign of the target —
	// matching chief.searchAngleForImageHeight.
	sgn := 1.0
	targetMag := targetHeight
	if targetHeight < 0 {
		sgn = -1.0
		targetMag = -targetHeight
	}

	// bestDeg/bestHeight track the probed angle whose delivered height comes
	// closest to the request — the closest reachable field, reported when the
	// request cannot be met. So an unreachable field is never folded onto the
	// axis (angle 0), which a spot-only merit would reward.
	bestDeg, bestHeight := 0.0, 0.0
	bestErr, bestSet := math.Inf(1), false
	height := func(angle float64) (float64, bool) {
		y, ok := heightAt(angle)
		if !ok {
			return y, false
		}
		if e := math.Abs(math.Abs(y) - targetMag); !bestSet || e < bestErr {
			bestDeg, bestHeight, bestErr, bestSet = angle, y, e, true
		}
		return y, true
	}
	closest := func() (float64, float64, bool) {
		if !bestSet {
			return 0, 0, false
		}
		return sgn * math.Abs(bestDeg), math.Abs(bestHeight), false
	}

	// Ascending scan: advance in steps until the traced height magnitude
	// reaches the target (a bracket is found) or the probe stops tracing — the
	// field's clip angle, where the stop or a surface aperture cuts the chief
	// ray. Overshooting the clip is what a fixed/expanding bracket does, and
	// with the stop on the first surface it made the whole off-axis range fail
	// and silently folded the field onto the axis — the failure this function
	// must avoid. The scan starts at 0 (the on-axis probe always traces) and
	// covers well past the widest zoom field.
	const step = 3.0
	lastA := 0.0
	lo, hi := math.NaN(), math.NaN()
	for a := step; a <= 80.0; a += step {
		y, ok := height(a)
		if !ok {
			break
		}
		if math.Abs(y) >= targetMag {
			lo, hi = lastA, a
			break
		}
		lastA = a
	}

	if math.IsNaN(lo) {
		// The target is beyond this field's traceable range (or the scan only
		// met the clip): report the closest reachable field, never 0.
		return closest()
	}

	for iter := 0; iter < 40; iter++ {
		mid := (lo + hi) / 2
		y, ok := height(mid)
		if !ok {
			// A clip inside the bracket: keep the side that still traces.
			hi = mid
			continue
		}
		a := math.Abs(y)
		if math.Abs(a-targetMag) < 1e-12 {
			return sgn * mid, a, true
		}
		if a < targetMag {
			lo = mid
		} else {
			hi = mid
		}
		if hi-lo < 1e-12 {
			break
		}
	}

	// Verify the bracket's result against the height it delivers: a bracket can
	// also stop on a step of the height function, and reporting that angle would
	// hand the merit a field the document did not declare.
	mid := (lo + hi) / 2
	if y, ok := heightAt(mid); ok && math.Abs(math.Abs(y)-targetMag) <= chief.ImageHeightTol(targetMag) {
		return sgn * mid, math.Abs(y), true
	}
	if bestSet && bestErr <= chief.ImageHeightTol(targetMag) {
		return sgn * math.Abs(bestDeg), math.Abs(bestHeight), true
	}
	return closest()
}

// imageHeightToFieldAngle resolves an image-height field to its angle for a
// caller that has no frozen value to read (see frozenFieldAngle); the resolved
// flag and the delivered height are dropped, so use resolveImageHeightField
// wherever the field's reachability matters.
func (o *Optimizer) imageHeightToFieldAngle(cfg *config, surfaces []types.Surface, targetHeight, wavelength float64, gc *glass.Catalog) float64 {
	angle, _, _ := o.resolveImageHeightField(cfg, surfaces, targetHeight, wavelength, gc)
	return angle
}

// fieldResolution is one config field's resolved angle, fixed at the
// optimisation's start geometry by freezeFieldAngles.
type fieldResolution struct {
	index   int     // position in config.fields
	fieldID int     // declared field id
	angle   float64 // the angle the run uses; the closest reachable when unresolved
	// requested is the declared image height (mm, magnitude), 0 for a field
	// declared by angle; reached is the height the frozen angle delivers.
	requested float64
	reached   float64
	resolved  bool
}

// freezeFieldAngles resolves every config field declared by image_height once,
// at the optimisation's start geometry, and stores the angle the whole run then
// uses — merit terms, constraints, aperture sizing and the throughput warnings
// alike. Re-resolving per evaluation made the field a function of the variables:
// the same field could be the declared 4.868 mm at one evaluation and a 1.533 mm
// near-axis field at the next, and a spot-only merit is lowered by exactly that
// substitution. A frozen angle cannot be gamed, and freezeFieldAngles records how
// each field resolved, so an unreachable one is reported instead of hidden.
//
// The pupils are resolved first (one UpdatePupils at the start state) so the
// probe launches through the start geometry's entrance pupil, exactly as the
// first DLS iteration would. A model whose configs declare no image-height
// field needs neither the probe nor the pupil pass.
func (o *Optimizer) freezeFieldAngles() {
	probe := false
	for ci := range o.configs {
		for i := range o.configs[ci].fields {
			if o.configs[ci].fields[i].ImageHeight != 0 {
				probe = true
			}
		}
	}
	var surfaces map[string][]types.Surface
	var gc *glass.Catalog
	if probe {
		x := o.InitialState()
		o.UpdatePupils(x)
		var tempGC *glass.Catalog
		surfaces, tempGC, _ = o.applyVariables(x)
		gc = effectiveGC(o.gc, tempGC)
	}
	for ci := range o.configs {
		cfg := &o.configs[ci]
		wl := cfg.referenceWavelength
		if wl <= 0 {
			wl = types.DefaultWavelength
		}
		res := make([]fieldResolution, len(cfg.fields))
		for i := range cfg.fields {
			f := &cfg.fields[i]
			r := fieldResolution{index: i, fieldID: f.ID, angle: f.AngleDeg, resolved: true}
			if probe && f.ImageHeight != 0 {
				r.requested = math.Abs(f.ImageHeight)
				r.angle, r.reached, r.resolved = o.resolveImageHeightField(cfg, surfaces[cfg.id], f.ImageHeight, wl, gc)
			}
			res[i] = r
		}
		cfg.fieldResolutions = res
	}
}

// frozenFieldAngle returns the angle the run uses for position i in cfg.fields,
// fixed by freezeFieldAngles. ok is false when the freeze does not cover the
// position — a caller naming a field the config does not carry — and the caller
// resolves the field on the fly instead.
func frozenFieldAngle(cfg *config, i int) (float64, bool) {
	if cfg == nil || i < 0 || i >= len(cfg.fieldResolutions) {
		return 0, false
	}
	return cfg.fieldResolutions[i].angle, true
}

// imageHeightAtAngle traces the chief-style ray for the field angle and returns
// the signed image-plane height it delivers. The launch matches
// resolveImageHeightField: parallel bundle through the resolved entrance-pupil
// centre, so the two functions invert each other.
func (o *Optimizer) imageHeightAtAngle(cfg *config, surfaces []types.Surface, angle, wavelength float64, gc *glass.Catalog) (float64, bool) {
	path := dls.BuildPath(surfaces)
	engine := ray.NewEngine(gc, nil)
	pol := types.NewCircularJones(true)
	dir := raymath.DirectionFromAngle(angle)
	origin := types.Vec3{X: 0, Y: 0, Z: -100.0}
	if pupilZ := resolvedPupilZ(cfg); pupilZ != nil {
		origin.X, origin.Y = pupil.GridCentre(dir, *pupilZ, -100.0)
	}
	r := types.Ray{
		Wavelength:         wavelength,
		Initial:            types.RayState{Origin: origin, Direction: dir},
		Path:               path,
		Jones:              pol,
		SkipGlassPathCheck: angle == 0,
	}
	result := engine.TraceRay(r, surfaces, false)
	if result.Error != "" || len(result.Surfaces) == 0 {
		return 0, false
	}
	return result.Surfaces[len(result.Surfaces)-1].Position.Y, true
}

// VerifyFieldResolutions re-measures every image-height field at x.
//
// The frozen angle is kept — the merit, the aperture sizing and the constraints
// all used it, and re-resolving would move the field the run optimised — but
// the delivered height and the resolved flag are re-evaluated at the solved
// geometry. A run whose design moved away from the frozen angle used to report
// resolved: true with the start-geometry height; on the zoom study that
// document traced 26.3 mm and 0/74 rays at the very angle it called resolved.
func (o *Optimizer) VerifyFieldResolutions(x []float64) {
	if len(o.configs) == 0 {
		return
	}
	configSurfaces, tempGC, _ := o.applyVariables(x)
	gc := effectiveGC(o.gc, tempGC)
	for ci := range o.configs {
		cfg := &o.configs[ci]
		surfaces, ok := configSurfaces[cfg.id]
		if !ok {
			continue
		}
		wl := cfg.referenceWavelength
		if wl <= 0 {
			wl = types.DefaultWavelength
		}
		for i := range cfg.fieldResolutions {
			r := &cfg.fieldResolutions[i]
			if r.requested == 0 {
				continue
			}
			y, ok := o.imageHeightAtAngle(cfg, surfaces, r.angle, wl, gc)
			if !ok {
				r.reached = 0
				r.resolved = false
				continue
			}
			r.reached = math.Abs(y)
			r.resolved = math.Abs(r.reached-r.requested) <= chief.ImageHeightTol(r.requested)
		}
	}
}

// FieldResolutionDiagnostics reports every config field declared by image
// height together with the angle the run froze for it and whether that angle
// delivers the declared height. It is the run-level answer to "which fields did
// this optimisation actually measure?" — the document's own fields record the
// declaration, not what was solved, and a saved minimum carries no other trace.
func (o *Optimizer) FieldResolutionDiagnostics() []types.FieldResolution {
	var out []types.FieldResolution
	for ci := range o.configs {
		cfg := &o.configs[ci]
		for _, r := range cfg.fieldResolutions {
			if r.requested == 0 {
				continue
			}
			out = append(out, types.FieldResolution{
				Config:     cfg.id,
				FieldIndex: r.index,
				FieldID:    r.fieldID,
				Requested:  r.requested,
				Reached:    r.reached,
				AngleDeg:   r.angle,
				Resolved:   r.resolved,
			})
		}
	}
	return out
}

// fieldSizingAngle returns the beam angle used by sizeAutoApertures for a field
// item: angle fields directly, image-height fields converted through the current
// surfaces.
// fieldSizingAngle returns the beam angle sizeAutoApertures uses for the config
// field at position i — the frozen angle for an image-height field, so the
// sized apertures cover the field the merit actually traces.
func (o *Optimizer) fieldSizingAngle(cfg *config, i int, surfaces []types.Surface, gc *glass.Catalog, wl float64) float64 {
	if a, ok := frozenFieldAngle(cfg, i); ok {
		return a
	}
	f := &cfg.fields[i]
	if f.AngleDeg != 0 || f.ImageHeight == 0 {
		return f.AngleDeg
	}
	return o.imageHeightToFieldAngle(cfg, surfaces, f.ImageHeight, wl, gc)
}

// fieldExtents traces the per-surface max radial ray extent for one grid merit
// term, centred on the per-field entrance pupil when one is resolved. When a
// virtual entrance pupil is active (p.dia > 0), the override is forwarded so
// the beam envelope matches the merit grid.
//
// Eight rays on the pupil edge (cardinal + diagonal) are enough for the sizing,
// and measurably conservative: the merit's polar grid puts its outermost ring
// inside the launched radius, and its spokes land on the same azimuths, while
// the extent is symmetric under x -> -x and y -> -y for any system the
// cardinal/diagonal rays bound. Measured on axisymmetric and tilted systems the
// 8-ray envelope is >= the dense hex envelope the final sizing uses for 8, 16
// and 64 azimuths alike, so the sized diameter covers the bundle the merit
// traces. WarnBeamClipping reports the residual case: a fixed aperture that
// does clip.
func (o *Optimizer) fieldExtents(cfg *config, surfaces []types.Surface, gc *glass.Catalog, term *meritTerm, angle float64, p appliedPupil) map[int]float64 {
	pupilZ := o.gridCentring(cfg, p, angle)
	return dls.TraceFieldExtents8Rays(gc, surfaces, cfg.stopSurface, pupilZ, angle, []float64{0, 1}, term.wavelength, o.apertureMargin, o.gridWorkers(), p.dia)
}

// fieldThroughputWarn is the surviving-grid fraction at which
// WarnFieldThroughput reports a dying field. field_alive starts penalising only
// at 0.3, so the warning fires well before then: the point is to make the
// collapse visible while there is still a lens to look at, not to duplicate the
// merit penalty.
const fieldThroughputWarn = 0.5

// configGuardsFieldLiveliness reports whether cfg already penalises a dying
// field: an active field_alive / pupil_fill / beam_clearance merit term, or a
// vignetting_factor constraint. When it does, the field-killing warning stays
// quiet — the user has asked for the guard, so the optimizer is not silently
// exploiting a merit that cannot see the dead field.
func (o *Optimizer) configGuardsFieldLiveliness(cfg *config) bool {
	guards := func(terms []meritTerm) bool {
		for i := range terms {
			if terms[i].kind == MeritFieldAlive || terms[i].kind == MeritPupilFill || terms[i].kind == MeritBeamClearance {
				return true
			}
		}
		return false
	}
	if guards(cfg.meritTerms) {
		return true
	}
	for _, terms := range cfg.meritModes {
		if guards(terms) {
			return true
		}
	}
	for _, c := range cfg.constraints {
		if c.Measure == types.MeasureVignettingFactor {
			return true
		}
	}
	return false
}

// WarnFieldThroughput warns about fields whose pupil grid collapsed at x.
//
// The DLS objective is evaluated over the rays that survive, so a merit that
// does not itself guard field liveliness (a bare spot_rms, say) can lower its
// residual by clipping a field's pupil away — the "field killing" basin the
// gap analysis hit repeatedly. This is a warning only: the merit values and the
// solve are untouched. A config that already carries a field_alive / pupil_fill
// term or a vignetting_factor constraint is skipped, so the message appears
// exactly for the merit the gap is about.
// FieldThroughput is how much of one field's pupil grid still traces at a
// given variable state.
type FieldThroughput struct {
	Config string
	Field  int
	// Angle is the effective field angle (an image-height field is resolved
	// by inversion first).
	Angle float64
	// Ratio is the fraction of grid points that traced through the field.
	Ratio float64
	// Points is the number of grid points sampled; 0 means the field's grid
	// could not be built at all (an unreachable field).
	Points int
}

// FieldThroughputs measures, at x, the surviving fraction of every field's
// full prescribed pupil grid.
//
// The measurement is dls.PrescribedSurvival: the hex lattice `zoom` reports at
// margin 1.0 with no declared vignetting ellipse, on the current surfaces
// (including the auto_aperture diameters the run uses). That makes the number a
// caller compares with `zoom` / `chief` the same number, which is what the
// escape feasibility check needs; the earlier merit-grid variant applied the
// field's ellipse and the optimizer's inflated launch, so a point could pass
// here and fail a direct trace (or the reverse) and `list global` disagreed
// with the delivered design.
//
// rayCount <= 0 keeps the optimizer's own merit ray count; a larger value
// validates on a denser grid than the DLS objective sampled.
func (o *Optimizer) FieldThroughputs(x []float64, rayCount int) []FieldThroughput {
	if rayCount <= 0 {
		rayCount = o.numRays
	}
	configSurfaces, tempGC, pupils := o.applyVariables(x)
	gc := effectiveGC(o.gc, tempGC)
	o.updateGlassRoles(configSurfaces, gc)
	cache := newEvalGridCache()

	var out []FieldThroughput
	for ci := range o.configs {
		cfg := &o.configs[ci]
		if len(cfg.fields) == 0 {
			continue
		}
		surfaces := configSurfaces[cfg.id]
		p := pupils[cfg.id]

		// Match the merit grid: the auto-aperture diameters and the pupil
		// centring the terms themselves use.
		o.restoreDiameters(cfg, surfaces)
		o.sizeAutoApertures(cfg, surfaces, gc, cache, p)

		wl := cfg.referenceWavelength
		if wl <= 0 {
			wl = types.DefaultWavelength
		}
		for fi, f := range cfg.fields {
			angle := f.AngleDeg
			if a, ok := frozenFieldAngle(cfg, fi); ok {
				angle = a
			} else if f.ImageHeight != 0 {
				angle = o.imageHeightToFieldAngle(cfg, surfaces, f.ImageHeight, wl, gc)
			}
			pupilZ := o.gridCentring(cfg, p, angle)
			// The same prescribed-pupil measurement `zoom` / the constraint /
			// the schedule gate use: hex, full pupil, margin 1.0. A field that
			// declares vignetting is measured on the full pupil here, because
			// this is the feasibility gate ("how much of the declared pupil is
			// delivered"), not the optical merit's ellipsed sampling.
			ratio, points := dls.PrescribedSurvivalPoints(gc, surfaces, cfg.stopSurface, pupilZ, angle,
				[]float64{0, 1}, wl, rayCount, o.gridRotation, o.gridWorkers(), p.dia)
			out = append(out, FieldThroughput{
				Config: cfg.id,
				Field:  fi,
				Angle:  angle,
				Ratio:  ratio,
				Points: points,
			})
		}
	}
	return out
}

// WarnFieldThroughput reports every field whose pupil grid has collapsed at x,
// unless the config's merit already guards field liveliness (a field_alive /
// pupil_fill term, or a vignetting_factor constraint) — in that case the merit
// cannot be lowered by clipping the field away, so there is nothing to warn
// about.
func (o *Optimizer) WarnFieldThroughput(x []float64) {
	for _, ft := range o.FieldThroughputs(x, 0) {
		if ft.Ratio >= fieldThroughputWarn {
			continue
		}
		cfg, ok := o.configByID(ft.Config)
		if !ok || o.configGuardsFieldLiveliness(cfg) {
			continue
		}
		Warnf("warning: config %q field %d (%.2f deg) keeps only %.0f%% of its pupil grid; the merit is computed over the surviving rays, so a spot-only merit can be lowered by clipping the field away. Add a field_alive / pupil_fill merit term (or a vignetting_factor constraint) to guard it.",
			ft.Config, ft.Field, ft.Angle, ft.Ratio*100)
	}
}
