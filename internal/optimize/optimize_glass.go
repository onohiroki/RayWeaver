package optimize

import (
	"fmt"
	"math"
	"sort"
	"strings"
	"sync"

	"github.com/hiroki/rayweaver/internal/glass"
	"github.com/hiroki/rayweaver/internal/paraxial"
	"github.com/hiroki/rayweaver/internal/types"
)

type glassPair struct {
	ndIndex int
	vdIndex int
	name    string
}

// powerSolveEntry binds a solve surface to the thin-lens power it must
// preserve during optimisation.
type powerSolveEntry struct {
	solveID   int
	targetPhi float64
}

// glassVDForSurface returns the Abbe number of the material on the surface with
// the given ID, resolved through the (possibly in-flight) catalog for keyed
// materials and read inline for model glasses. Returns 0 for air/unknown.
func glassVDForSurface(surfaces []types.Surface, gc *glass.Catalog, id int) float64 {
	for i := range surfaces {
		if surfaces[i].ID != id {
			continue
		}
		m := surfaces[i].Material
		if m.HasModel() && !m.HasKey() {
			return m.VD
		}
		if m.HasKey() {
			if g, ok := gc.Lookup(m.Key); ok {
				return g.VD
			}
		}
		return 0
	}
	return 0
}

// glassNDForSurface returns the refractive index of the material on the surface
// with the given ID, resolved like glassVDForSurface. Returns 0 for
// air/unknown.
func glassNDForSurface(surfaces []types.Surface, gc *glass.Catalog, id int) float64 {
	for i := range surfaces {
		if surfaces[i].ID != id {
			continue
		}
		m := surfaces[i].Material
		if m.HasModel() && !m.HasKey() {
			return m.ND
		}
		if m.HasKey() {
			if g, ok := gc.Lookup(m.Key); ok {
				return g.ND
			}
		}
		return 0
	}
	return 0
}

// hullPhaseFactor scales the hull-penalty term for the current escape-cycle
// phase. During escape exploration (phase < 0.5) the term is multiplied by
// the hull's exploration_weight_factor (default 0.1), softening the real-glass
// wall so the search can cross it between basins; the glass (0.5) and clean
// (1) phases keep full strength, so a converged point is pushed back inside
// the hull. Hulls without a configured factor (tests, non-escape paths) and
// plain optimize runs (phaseActive false) are unaffected (factor 1).
func (o *Optimizer) hullPhaseFactor() float64 {
	if o.hull == nil || !o.phaseActive || o.phase >= 0.5 {
		return 1
	}
	if f := o.hull.ExplorationWeightFactor(); f > 0 {
		return f
	}
	return 1
}

// HasGlassPairs reports whether any declared nd/vd glass variable pair exists.
func (o *Optimizer) HasGlassPairs() bool { return len(o.hullPairs) > 0 }

// GlassHullViolations reports the glass surfaces whose nd/vd variable pair
// lies outside the real-glass convex hull. It returns nil when the hull is
// absent or every pair is contained. The returned IDs are sorted and
// deduplicated so callers can compare them directly across calls. An empty,
// non-nil slice is never returned: a clean state returns nil.
//
// The check is the O(n_vertices) barycentric containment test of
// glass.ConvexHull.Contains, evaluated per hull pair. This is the "core
// constraint" promotion of the hull: a converged point whose glass is outside
// the hull is classified as infeasible (not merely penalised), so the escape
// search does not record it as a solution.
func (o *Optimizer) GlassHullViolations(x []float64) []int {
	if o.hull == nil || !o.hull.Enabled() || len(o.hullPairs) == 0 {
		return nil
	}
	seen := make(map[int]bool)
	for _, pair := range o.hullPairs {
		if pair.ndIndex < 0 || pair.ndIndex >= len(x) || pair.vdIndex < 0 || pair.vdIndex >= len(x) {
			continue
		}
		nd := x[pair.ndIndex]
		vd := x[pair.vdIndex]
		if o.hull.Contains(nd, vd) {
			continue
		}
		// The element's glass surfaces come from the nd variable, which the
		// variable builder populated via elementGlassSurfaces.
		for _, id := range o.variableSurfaceSet(pair.ndIndex) {
			seen[id] = true
		}
	}
	if len(seen) == 0 {
		return nil
	}
	out := make([]int, 0, len(seen))
	for id := range seen {
		out = append(out, id)
	}
	sort.Ints(out)
	return out
}

// RescueGlassHull projects every nd/vd glass pair that lies outside the
// real-glass convex hull back onto the hull (ConvexHull.EnforceBounds: anchor
// on the most-violated facet, nudged just inside) and returns the repaired
// vector. x is not mutated. ok is false when the hull is absent or every pair
// is already contained, so callers keep the original point unchanged. This is
// the escape cycle's hull-rescue repair step (matched via its structural
// interface): a converged-but-outside point starts the rescue DLS from the
// nearest feasible glass state instead of being discarded as an infeasible
// basin.
func (o *Optimizer) RescueGlassHull(x []float64) ([]float64, bool) {
	if o.hull == nil || !o.hull.Enabled() || len(o.hullPairs) == 0 {
		return nil, false
	}
	var out []float64
	changed := false
	for _, pair := range o.hullPairs {
		if pair.ndIndex < 0 || pair.ndIndex >= len(x) || pair.vdIndex < 0 || pair.vdIndex >= len(x) {
			continue
		}
		nd := x[pair.ndIndex]
		vd := x[pair.vdIndex]
		if o.hull.Contains(nd, vd) {
			continue
		}
		if out == nil {
			out = make([]float64, len(x))
			copy(out, x)
		}
		n2, v2 := o.hull.EnforceBounds(nd, vd)
		out[pair.ndIndex] = n2
		out[pair.vdIndex] = v2
		changed = true
	}
	if !changed {
		return nil, false
	}
	return out, true
}

// SnapVariables replaces each declared nd/vd glass variable in x with the
// nearest real catalog glass (normalised nd/vd distance) and returns the
// snapped vector plus a per-pair record. The catalog is taken from the
// optimizer's glass catalog; x is not mutated.
func (o *Optimizer) SnapVariables(x []float64) ([]float64, []types.SnapPairResult) {
	if o.gc == nil || len(o.hullPairs) == 0 {
		return x, nil
	}
	field := glass.BuildCatalogField(o.gc)
	if field.CatalogFieldCount() == 0 {
		return x, nil
	}
	snapped := make([]float64, len(x))
	copy(snapped, x)
	pairs := make([]types.SnapPairResult, 0, len(o.hullPairs))
	for _, pair := range o.hullPairs {
		fromND := x[pair.ndIndex]
		fromVD := x[pair.vdIndex]
		key, toND, toVD, r2 := glass.NearestNamed(field, fromND, fromVD)
		snapped[pair.ndIndex] = toND
		snapped[pair.vdIndex] = toVD
		sid := 0
		if pair.ndIndex < len(o.variables) {
			sid = o.variables[pair.ndIndex].SurfaceID
		}
		pairs = append(pairs, types.SnapPairResult{
			SurfaceID: sid,
			Name:      key,
			FromND:    fromND,
			FromVD:    fromVD,
			ToND:      toND,
			ToVD:      toVD,
			Distance:  math.Sqrt(r2),
		})
	}
	return snapped, pairs
}

// updateGlassRoles recomputes each config's per-surface glass-role
// classification (paraxial.GlassRoles) at the current variable state and
// freezes it for the rest of the DLS iteration. Called at the top of
// UpdatePupils, so the role assignment follows the lens (element powers and
// marginal-ray heights move with the variables) while staying constant within
// one iteration — keeping the base-point and Jacobian glass_role residuals
// consistent.
func (o *Optimizer) updateGlassRoles(configSurfaces map[string][]types.Surface, gc *glass.Catalog) {
	if o.roleTargets == nil {
		o.roleTargets = make(map[string]map[int]paraxial.ElementRole)
	}
	for ci := range o.configs {
		cfg := &o.configs[ci]
		surfaces := configSurfaces[cfg.id]
		roles := paraxial.GlassRoles(surfaces, gc)
		frozen := make(map[int]paraxial.ElementRole, len(roles))
		for _, r := range roles {
			for _, id := range r.SurfaceIDs {
				frozen[id] = r
			}
		}
		o.roleTargets[cfg.id] = frozen
	}
}

// SetPowerSolveEnabled toggles the power-preserving hard solve. The escape
// glass phase enables it so the element powers stay constant while only the
// glass dispersions are free; the other escape phases disable it to keep the
// full curvature freedom. A false value leaves the (empty) powerSolve map in
// place so EnterGlassPhase can re-enable it.
func (o *Optimizer) SetPowerSolveEnabled(enabled bool) {
	o.powerSolveEnabled = enabled
}

// SetGlassMerit installs the per-config merit terms used during the glass
// phase (colour-only axial/lateral chromatic aberration). It is inactive until
// EnterGlassPhase activates it. Passing empty terms (or nil) removes the
// installed glass merit for the config.
func (o *Optimizer) SetGlassMerit(configID string, terms []types.MeritTerm) {
	if o.glassMerit == nil {
		o.glassMerit = make(map[string][]meritTerm)
	}
	if len(terms) == 0 {
		delete(o.glassMerit, configID)
		return
	}
	mt := make([]meritTerm, 0, len(terms))
	for _, t := range terms {
		tm := meritTerm{
			kind:                 t.Kind,
			fieldDirX:            0,
			fieldDirY:            1,
			fieldIndex:           -1,
			wavelength:           t.Wavelength,
			comparisonWavelength: t.ComparisonWavelength,
			weight:               t.Weight,
			target:               t.Target,
			fraction:             t.Fraction,
			frequency:            t.Frequency,
			fieldWeight:          1.0,
			wavWeight:            1.0,
		}
		// Resolve the term's field angle from the config's fields (angle or
		// image height), matching buildMeritTermFromTypes.
		if c := findConfigByID(o.configs, configID); c != nil {
			for i, f := range c.fields {
				if f.ID == t.Field {
					tm.fieldIndex = i
					if f.AngleDeg != 0 || f.ImageHeight == 0 {
						tm.fieldAngle = f.AngleDeg
					} else {
						tm.useImageHeight = true
						tm.imageHeight = f.ImageHeight
					}
					if f.Weight > 0 {
						tm.fieldWeight = f.Weight
					}
					break
				}
			}
		}
		mt = append(mt, tm)
	}
	o.glassMerit[configID] = mt
}

// EnterGlassPhase starts the glass phase: it enables the power-preserving
// solve, locks every variable except the glass dispersions (nd/vd) to its
// current value x (so only the glasses move), and routes the merit to the
// installed glass (colour-only) terms. x is the physical variable vector at
// the start of the phase (the escape-DLS result). The variable dimension is
// unchanged, so the escape store/distance semantics stay intact.
func (o *Optimizer) EnterGlassPhase(x []float64) {
	// Reported before lockNonGlassVariables: the lock itself gives every
	// non-glass variable Min==Max, which would make the check below see a
	// "fixed" curvature variable that is only pinned for the phase.
	o.warnUnsettledPowerSolve()
	// Re-anchor the power-preserving solve at the phase entry first: the
	// powers to hold are those of the incoming lens (the escape-DLS result x),
	// not the run-initial snapshot SetPowerSolve took. Anchoring at the run
	// start would pin the layout to the pre-escape state while the glass
	// optimises against the post-escape one, so applyVariables would hand the
	// solve a chimeric lens and the delivered (solve-off) document would drift
	// far outside its target band.
	o.reanchorPowerSolve(x)
	o.powerSolveEnabled = true
	o.lockNonGlassVariables(x)
	o.glassMeritActive = true
}

// reanchorPowerSolve recomputes every power-solve entry's target thin-lens
// power from the lens described by x (solve off), so the glass phase holds the
// element powers of the state it actually starts from. It is called by
// EnterGlassPhase before the solve is enabled, which makes the materialisation
// below the solve-off lens.
//
// applyVariables' bookkeeping is saved and restored: this materialisation is
// an implementation detail of the re-anchor, not a solve, so it must not
// appear in the run's back-focus diagnostics. A no-op when no solve surfaces
// are configured (the plain optimize path).
func (o *Optimizer) reanchorPowerSolve(x []float64) {
	if len(o.powerSolve) == 0 {
		return
	}
	savedStats := o.backFocusStats.snapshot()
	wasEnabled := o.powerSolveEnabled
	o.powerSolveEnabled = false
	cfgSurfaces, tempGC, _ := o.applyVariables(x)
	o.powerSolveEnabled = wasEnabled
	o.backFocusStats.restore(savedStats)

	gc := effectiveGC(o.gc, tempGC)
	for cid, entries := range o.powerSolve {
		surfaces, ok := cfgSurfaces[cid]
		if !ok {
			continue
		}
		// entries shares its backing array with o.powerSolve, so the write
		// lands on the stored entry.
		for i := range entries {
			if phi, ok := powerTargetForSurface(surfaces, gc, entries[i].solveID); ok {
				entries[i].targetPhi = phi
			}
		}
	}
}

// ExitGlassPhase ends the glass phase: it disables the power-preserving solve,
// restores every variable's original Min/Max (full curvature freedom), and
// restores the ordinary merit. Called before the clean DLS.
func (o *Optimizer) ExitGlassPhase() {
	o.powerSolveEnabled = false
	o.unlockVariables()
	o.glassMeritActive = false
}

// SettlePowerSolve returns a copy of x with every power-solve surface's
// curvature variable rewritten to the power-preserving curvature the solve
// applies at x. A solve-off evaluation of the result therefore reproduces the
// element powers the glass phase held, so the glass result carries its power
// compensation into the clean phase instead of dropping it at the phase
// boundary: without this the element powers jump by the nd-induced d-line
// drift as soon as ExitGlassPhase disables the solve, and the colour-only
// glass result hands the clean phase a different lens than the one it judged.
//
// The solve is materialised temporarily (and the previous enabled state is
// restored), so the method works whether it is called inside or after the
// phase; when the phase's Min==Max locks are in effect the clamp uses the
// remembered original bounds, not the locked ones. A solve surface driven by a
// fixed variable (Min >= Max) or with no declared curvature variable has
// nowhere to write, so it is left untouched (SetPowerSolve warns about that
// configuration). x is returned unchanged when no solve is configured.
func (o *Optimizer) SettlePowerSolve(x []float64) []float64 {
	if len(o.powerSolve) == 0 || len(x) != len(o.variables) {
		return x
	}
	savedStats := o.backFocusStats.snapshot()
	wasEnabled := o.powerSolveEnabled
	o.powerSolveEnabled = true
	cfgSurfaces, _, _ := o.applyVariables(x)
	o.powerSolveEnabled = wasEnabled
	o.backFocusStats.restore(savedStats)

	out := append([]float64(nil), x...)
	for cid, entries := range o.powerSolve {
		surfaces, ok := cfgSurfaces[cid]
		if !ok {
			continue
		}
		for _, e := range entries {
			idx := surfaceIndex(surfaces, e.solveID)
			if idx < 0 {
				continue
			}
			cSolved := surfaces[idx].Curvature
			for i := range o.variables {
				v := &o.variables[i]
				lo, hi := o.variableBounds(i)
				if lo >= hi {
					continue // fixed variable: no freedom to settle into
				}
				apply := func(param string, scale, offset float64) {
					val, ok := varValueForCurvature(param, cSolved)
					if !ok {
						return
					}
					out[i] = clampVar((val-offset)/scale, lo, hi)
				}
				if v.IsShared {
					for _, b := range v.Bindings {
						if b.Config != cid || b.ID != e.solveID {
							continue
						}
						scale := b.Scale
						if scale == 0 {
							scale = 1.0
						}
						apply(b.Param, scale, b.Offset)
					}
					continue
				}
				if v.Config == cid && v.SurfaceID == e.solveID {
					apply(v.Param, 1.0, 0)
				}
			}
		}
	}
	return out
}

// varValueForCurvature converts a surface curvature into the value a variable
// controlling that surface must hold: a "curvature" variable takes it
// directly, a "radius" variable carries the reciprocal. ok is false for any
// other parameter (the surface is not curvature-controlled by that variable).
func varValueForCurvature(param string, c float64) (value float64, ok bool) {
	switch param {
	case "curvature":
		return c, true
	case "radius":
		if c == 0 {
			// A zero curvature has no finite radius; the caller clamps the
			// sentinel to the variable's bound.
			return math.MaxFloat64, true
		}
		return 1.0 / c, true
	}
	return 0, false
}

// hasFreeCurvatureVar reports whether some optimisation variable drives the
// curvature of surface surfaceID in config cid over a non-empty range.
func (o *Optimizer) hasFreeCurvatureVar(cid string, surfaceID int) bool {
	for i := range o.variables {
		v := &o.variables[i]
		if v.Param != "curvature" && v.Param != "radius" {
			continue
		}
		if v.Min >= v.Max {
			continue
		}
		if v.IsShared {
			for _, b := range v.Bindings {
				if b.Config == cid && b.ID == surfaceID && (b.Param == "curvature" || b.Param == "radius") {
					return true
				}
			}
			continue
		}
		if v.Config == cid && v.SurfaceID == surfaceID {
			return true
		}
	}
	return false
}

// warnUnsettledPowerSolve reports power-solve surfaces that no free curvature
// variable drives. The escape glass phase settles the solve's curvature
// compensation back into the variable vector (SettlePowerSolve) so the clean
// phase inherits the power-preserved lens; a surface whose curvature variable
// is fixed (Min == Max) or undeclared has nowhere to write, so its element
// power drifts the moment the phase ends. It is called from EnterGlassPhase
// rather than SetPowerSolve: a plain optimize run keeps the solve on for its
// whole life and never hits the problem, while only a run that actually runs a
// glass phase can. Messages are deduplicated process-wide because escape
// builds one Optimizer per worker from a single shared document.
func (o *Optimizer) warnUnsettledPowerSolve() {
	for cid, entries := range o.powerSolve {
		for _, e := range entries {
			if o.hasFreeCurvatureVar(cid, e.solveID) {
				continue
			}
			msg := fmt.Sprintf("power_solve: surface %d (%s) has no free curvature variable "+
				"(fixed or undeclared); the glass-phase power compensation cannot be carried "+
				"into the clean phase, so that element's power will drift when the solve is "+
				"disabled", e.solveID, cid)
			if _, dup := powerSolveWarningSeen.LoadOrStore(msg, struct{}{}); dup {
				continue
			}
			Warnf("%s", msg)
		}
	}
}

// powerSolveWarningSeen deduplicates the power-settle warning across the
// per-worker Optimizers of an escape run.
var powerSolveWarningSeen sync.Map

// lockNonGlassVariables pins every variable not in the glass-free set (nd/vd)
// to its current value x[i] by setting Min==Max, remembering the originals for
// ExitGlassPhase. DLS keeps a Min==Max variable at its fixed value (solver
// clamps the scale to 1.0).
func (o *Optimizer) lockNonGlassVariables(x []float64) {
	o.pinnedMin = make([]float64, len(o.variables))
	o.pinnedMax = make([]float64, len(o.variables))
	for i := range o.variables {
		o.pinnedMin[i] = o.variables[i].Min
		o.pinnedMax[i] = o.variables[i].Max
		v := &o.variables[i]
		if isGlassParam(v.Param) {
			continue
		}
		val := x[i]
		v.Min = val
		v.Max = val
	}
}

// isGlassParam reports whether a variable parameter belongs to the glass-free
// set that stays active during the glass phase.
func isGlassParam(param string) bool {
	return param == "nd" || param == "vd"
}

// SetPowerSolve enables the power-preserving hard solve for the given solve
// surface IDs (optimization.power_solve.surfaces). For each listed surface the
// thin-lens power of the element containing it is snapshotted from the config's
// initial surfaces as the target, and applyPowerSolve recomputes that surface's
// curvature after every variable application so the power stays fixed. Surfaces
// not part of a refractive lens element are silently skipped. An empty list is
// a no-op. The snapshot is per-config so a multi-config zoom pins each config's
// own element powers.
func (o *Optimizer) SetPowerSolve(solveSurfaces []int) {
	if len(solveSurfaces) == 0 {
		return
	}
	// Surfaces driven by a "power" variable are variable-controlled: their
	// element power is an optimisation variable (applyPowerVariables), so a
	// snapshot-pinned entry here would fight the variable. The snapshot
	// preserves only the surfaces no power variable drives.
	powerDriven := make(map[string]map[int]bool)
	for i := range o.variables {
		v := &o.variables[i]
		if v.Param != "power" {
			continue
		}
		add := func(cfgID string, id int) {
			if powerDriven[cfgID] == nil {
				powerDriven[cfgID] = make(map[int]bool)
			}
			powerDriven[cfgID][id] = true
		}
		if v.IsShared {
			for _, b := range v.Bindings {
				add(b.Config, b.ID)
			}
			continue
		}
		add(v.Config, v.SurfaceID)
	}
	m := make(map[string][]powerSolveEntry, len(o.configs))
	// The targets come from a curvature-based element-power snapshot (the
	// initial surfaces have not been Precomputed), matching the power the solve
	// preserves.
	for ci := range o.configs {
		cfg := &o.configs[ci]
		for _, id := range solveSurfaces {
			if powerDriven[cfg.id][id] {
				continue
			}
			phi, ok := powerTargetForSurface(cfg.surfaces, o.gc, id)
			if !ok {
				continue
			}
			m[cfg.id] = append(m[cfg.id], powerSolveEntry{solveID: id, targetPhi: phi})
		}
	}
	o.powerSolve = m
	// The solve is active for a plain optimize run; the escape glass phase
	// toggles it (SetPowerSolveEnabled) so it applies only during that phase.
	o.powerSolveEnabled = true
}

// powerTargetForSurface returns the current thin-lens power (curvature-based)
// of the refractive element containing surface id (for use as a power-preserve
// target), with a boolean reporting whether the surface belongs to a solvable
// element. Mirrors report not-ok so they are never pinned.
func powerTargetForSurface(surfaces []types.Surface, gc *glass.Catalog, id int) (float64, bool) {
	for _, s := range surfaces {
		if s.ID == id && s.Reflects() {
			return 0, false
		}
	}
	return paraxial.ElementPowerCurvature(surfaces, gc, id), true
}

// applyPowerSolve reconciles every pinned solve surface's curvature so the
// thin-lens power of its element equals the snapshotted target. It is called
// from applyVariables (pure), so the DLS base-point and Jacobian residuals
// share one consistent power-preserving layout. It only runs while the
// power-preserving solve is enabled (powerSolveEnabled), so the escape
// glass phase can enable it selectively.
func (o *Optimizer) applyPowerSolve(configSurfaces map[string][]types.Surface, gc *glass.Catalog) {
	if !o.powerSolveEnabled {
		return
	}
	if len(o.powerSolve) == 0 {
		return
	}
	for cid, entries := range o.powerSolve {
		surfaces, ok := configSurfaces[cid]
		if !ok {
			continue
		}
		for _, e := range entries {
			paraxial.SolveElementPower(surfaces, gc, e.solveID, e.targetPhi)
		}
	}
}

// materializeGlasses rewrites only the surfaces an nd/vd variable actually
// targets (bindings only) as inline model glasses carrying the optimised
// values. A surface sharing the catalogue key but not targeted keeps its key.
// applyVariables already sets the targeted inline material, so this exists for
// the chained callers (FinalConfigs) that materialise from a raw vector.
func (o *Optimizer) materializeGlasses(configSurfaces map[string][]types.Surface, x []float64) []types.Glass {
	type ndvd struct {
		nd, vd *float64
	}
	acc := map[string]*ndvd{}
	set := func(cfg string, sid int, param string, val float64) {
		k := cfgSurfKey(cfg, sid)
		p := acc[k]
		if p == nil {
			p = &ndvd{}
			acc[k] = p
		}
		v := val
		if param == "nd" {
			p.nd = &v
		} else {
			p.vd = &v
		}
	}
	for i, v := range o.variables {
		if v.Param != "nd" && v.Param != "vd" {
			continue
		}
		if v.IsShared {
			for _, b := range v.Bindings {
				if b.Param == "nd" || b.Param == "vd" {
					set(b.Config, b.ID, b.Param, x[i])
				}
			}
		} else {
			set(v.Config, v.SurfaceID, v.Param, x[i])
		}
	}
	for ci := range o.configs {
		cfgID := o.configs[ci].id
		for i := range configSurfaces[cfgID] {
			p := acc[cfgSurfKey(cfgID, configSurfaces[cfgID][i].ID)]
			if p == nil || p.nd == nil || p.vd == nil {
				continue
			}
			configSurfaces[cfgID][i].Material = types.Material{ND: *p.nd, VD: *p.vd}
		}
	}
	return nil
}

// glassNDVD returns a material's nd/vd — the inline model's own values, or the
// catalogue entry a key resolves to. ok is false when neither is available.
func glassNDVD(m types.Material, gc *glass.Catalog) (nd, vd float64, ok bool) {
	if m.HasModel() && !m.HasKey() {
		return m.ND, m.VD, true
	}
	if m.HasKey() && gc != nil {
		if g, found := gc.Lookup(m.Key); found {
			return g.ND, g.VD, true
		}
	}
	return 0, 0, false
}

func buildHullPairs(variables []Variable) []glassPair {
	// Group nd/vd variables by the glass they target: a named catalog glass
	// may be shared across surfaces, while an inline model glass is
	// identified by its (config, surface) location. Keying inline models by
	// an empty GlassName would collapse every pair onto one entry.
	pairKey := func(v Variable) string {
		if v.GlassName != "" {
			return "glass:" + v.GlassName
		}
		if v.IsShared {
			var parts []string
			for _, b := range v.Bindings {
				if b.Param == "nd" || b.Param == "vd" {
					parts = append(parts, fmt.Sprintf("%s:%d", b.Config, b.ID))
				}
			}
			sort.Strings(parts)
			return "shared:" + strings.Join(parts, ",")
		}
		return fmt.Sprintf("surf:%s:%d", v.Config, v.SurfaceID)
	}
	ndMap := make(map[string]int)
	vdMap := make(map[string]int)
	for i, v := range variables {
		switch v.Param {
		case "nd":
			ndMap[pairKey(v)] = i
		case "vd":
			vdMap[pairKey(v)] = i
		}
	}
	var pairs []glassPair
	for key, ndIdx := range ndMap {
		if vdIdx, ok := vdMap[key]; ok {
			pairs = append(pairs, glassPair{ndIndex: ndIdx, vdIndex: vdIdx, name: variables[ndIdx].GlassName})
		}
	}
	// Deterministic order (map iteration is randomised) so diagnostics and
	// sensitivity scales are reproducible.
	sort.Slice(pairs, func(i, j int) bool {
		if pairs[i].ndIndex != pairs[j].ndIndex {
			return pairs[i].ndIndex < pairs[j].ndIndex
		}
		return pairs[i].vdIndex < pairs[j].vdIndex
	})
	return pairs
}

// elementGlassSurfaces returns the IDs of all glass surfaces in the same lens
// element as the surface with the given ID. A lens element is a contiguous
// run of non-air surfaces bounded by air (or system boundaries). If the
// target surface is not in glass, returns nil.
func elementGlassSurfaces(surfaces []types.Surface, targetID int) []int {
	// Find the index of the target surface.
	targetIdx := -1
	for i, s := range surfaces {
		if s.ID == targetID {
			targetIdx = i
			break
		}
	}
	if targetIdx < 0 {
		return nil
	}
	// Check that the target surface is glass (non-air).
	if surfaces[targetIdx].Material.IsAir() {
		return nil
	}
	// Walk backward to find the start of the element (first glass surface).
	start := targetIdx
	for start > 0 && !surfaces[start-1].Material.IsAir() {
		start--
	}
	// Walk forward to find the end of the element (last glass surface).
	end := targetIdx
	for end < len(surfaces)-1 && !surfaces[end+1].Material.IsAir() {
		end++
	}
	// Collect all glass surface IDs in the element.
	var ids []int
	for i := start; i <= end; i++ {
		if !surfaces[i].Material.IsAir() {
			ids = append(ids, surfaces[i].ID)
		}
	}
	return ids
}
