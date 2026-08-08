// Package vignette iteratively settles surface diameters and per-field
// vignetting for a lens whose pupil is defined by the chief rays (dynamic
// pupil) rather than a physical stop surface.
//
// Each pass re-traces every field's pupil grid with the current surface
// diameters acting as clips and the glass-path (edge-thickness) check on,
// then sizes auto_aperture: true surfaces to the surviving-beam envelope.
// Diameters are sized to the union envelope of every field's full beam, so a
// vignetted off-axis bundle never shrinks the lens. The on-axis (field 0)
// marginal-ray envelope bounds the off-axis bundles in the plane perpendicular
// to each field's own chief ray (through its entrance pupil); rays falling
// outside that envelope are vignetted.
package vignette

import (
	"fmt"
	"math"

	"github.com/hiroki/rayweaver/internal/chief"
	"github.com/hiroki/rayweaver/internal/dls"
	"github.com/hiroki/rayweaver/internal/glass"
	"github.com/hiroki/rayweaver/internal/ray"
	"github.com/hiroki/rayweaver/internal/surface"
	"github.com/hiroki/rayweaver/internal/types"
)

// Options configures one vignette run.
type Options struct {
	Fields       []types.FieldDef
	RefSurface   int
	StopSurface  int
	NumRays      int
	GridType     types.GridType
	Wavelength   float64
	MinGlassPath float64
	MarginMM     float64
	Iterations   int
}

// FieldResult is one field's vignette report plus its marginal rays.
type FieldResult struct {
	types.VignettingField
	MarginalUpper *types.Ray
	MarginalLower *types.Ray
}

// Result is the settled system state after the vignette iteration.
type Result struct {
	Surfaces     []types.Surface
	ChiefRays    []chief.Result
	Fields       []FieldResult
	Iterations   int
	StopSurface  int
	MinGlassPath float64
}

// tracedRay is a surviving grid ray with its full per-surface positions.
type tracedRay struct {
	ids        []int
	pos        []types.Vec3
	imgX, imgY float64
	origin     types.Vec3
	dir        types.Vec3
}

// Run performs the iterative vignetting / diameter-sizing loop and returns the
// settled system. The returned surfaces include the applied min_glass_path
// values and the final auto_aperture diameters.
func Run(surfaces []types.Surface, opts Options, gc *glass.Catalog) *Result {
	work := append([]types.Surface{}, surfaces...)
	surface.Precompute(work)

	if opts.Wavelength == 0 {
		opts.Wavelength = types.DefaultWavelength
	}
	if opts.Iterations <= 0 {
		opts.Iterations = 3
	}
	applyMinGlassPath(work, opts.MinGlassPath)

	pol := types.NewCircularJones(true)

	last := chief.DetermineChiefRaysGrid(
		types.System{Surfaces: work, StopSurface: opts.StopSurface},
		opts.Fields, opts.RefSurface, opts.NumRays, gc, pol, opts.Wavelength,
		true, opts.GridType, nil, nil, nil,
	)

	used := 1
	for it := 1; it < opts.Iterations; it++ {
		used = it + 1
		results := chief.DetermineChiefRaysGrid(
			types.System{Surfaces: work, StopSurface: opts.StopSurface},
			opts.Fields, opts.RefSurface, opts.NumRays, gc, pol, opts.Wavelength,
			true, opts.GridType, nil, nil, nil,
		)
		last = results

		_, extents := analyze(work, results, opts, gc, pol)
		changed := false
		for i := range work {
			if !work[i].AutoAperture || work[i].ID == opts.StopSurface {
				continue
			}
			e := extents[work[i].ID]
			if e <= 0 {
				continue
			}
			newD := 2*e + 2*opts.MarginMM
			if math.Abs(newD-work[i].Diameter) > 1e-6 {
				changed = true
			}
			work[i].Diameter = newD
		}
		if !changed {
			break
		}
	}

	fields, _ := analyze(work, last, opts, gc, pol)

	return &Result{
		Surfaces:     work,
		ChiefRays:    last,
		Fields:       fields,
		Iterations:   used,
		StopSurface:  opts.StopSurface,
		MinGlassPath: opts.MinGlassPath,
	}
}

// analyze computes each field's report (vignetting, pupil-plane envelope,
// marginal rays) and the per-surface max radial extent of the surviving rays.
//
// The measurement re-traces every grid point (including ones the chief grid
// clipped) with the aperture check skipped on auto_aperture surfaces, so the
// surfaces being sized never clip their own measurement; vignetting is driven
// only by the glass-path (edge-thickness) check, the fixed (auto_aperture:
// false) surface apertures, and the crossing-Z bound.
func analyze(work []types.Surface, results []chief.Result, opts Options, gc *glass.Catalog, pol types.JonesVector) ([]FieldResult, map[int]float64) {
	path := dls.BuildPath(work)
	engine := ray.NewEngine(gc, nil)

	all := make([][]tracedRay, len(results))
	for fi, r := range results {
		for _, gp := range r.GridPoints {
			tr := engine.TraceRay(types.Ray{
				Wavelength:            opts.Wavelength,
				Initial:               types.RayState{Origin: gp.Origin, Direction: gp.Direction},
				Path:                  path,
				Jones:                 pol,
				SkipAutoApertureCheck: true,
			}, work)
			if tr.Error != "" || len(tr.Surfaces) == 0 {
				continue
			}
			ids := make([]int, len(tr.Surfaces))
			positions := make([]types.Vec3, len(tr.Surfaces))
			for j, sr := range tr.Surfaces {
				ids[j] = sr.SurfaceID
				positions[j] = sr.Position
			}
			last := tr.Surfaces[len(tr.Surfaces)-1]
			all[fi] = append(all[fi], tracedRay{
				ids:    ids,
				pos:    positions,
				imgX:   last.Position.X,
				imgY:   last.Position.Y,
				origin: gp.Origin,
				dir:    gp.Direction,
			})
		}
	}

	// Vignetting comparison frames: an off-axis field's rays must fall within
	// field 0's marginal-ray envelope, measured in the plane perpendicular to
	// the field's own chief ray (through the entrance pupil), rather than in a
	// plane perpendicular to the optical axis. The on-axis beam casts a
	// circular aperture; the off-axis bundle must fit within it as seen along
	// its chief ray.
	frames := make([]planeFrame, len(results))
	for fi := range results {
		frames[fi] = chiefPlaneFrame(engine, work, path, results[fi], pol, opts.Wavelength)
	}

	// Diameter (extent) measurement uses the FULL beam of every field, before
	// the bounding cut, so a narrowed off-axis bundle never shrinks the lens: an
	// auto_aperture surface is sized to cover the union envelope of all fields'
	// marginal rays. Only then are the per-field marginal rays re-derived
	// against those diameters (via the bounding cut below).
	extents := make(map[int]float64)
	for fi := range results {
		for _, t := range all[fi] {
			for j, p := range t.pos {
				e := math.Max(math.Abs(p.X), math.Abs(p.Y))
				if e > extents[t.ids[j]] {
					extents[t.ids[j]] = e
				}
			}
		}
	}

	// Crossing-plane bounding: an off-axis field's rays must fall within field
	// 0's marginal-ray envelope in the field's chief-ray frame, otherwise they
	// are vignetted (narrowed beam).
	if len(results) >= 2 {
		for fi := 1; fi < len(results); fi++ {
			lo, hi, ok := envelopeAtPlane(all[0], frames[fi])
			if !ok {
				continue
			}
			keep := all[fi][:0]
			for _, t := range all[fi] {
				h, ok := rayHeightAtPlane(t.pos, frames[fi])
				if !ok || (h >= lo-1e-9 && h <= hi+1e-9) {
					keep = append(keep, t)
				}
			}
			all[fi] = keep
		}
	}

	fields := make([]FieldResult, len(results))
	for fi := range results {
		traces := all[fi]
		total := len(results[fi].GridPoints)
		kept := len(traces)

		fr := FieldResult{
			VignettingField: types.VignettingField{
				FieldIndex:    fi,
				AngleDeg:      results[fi].FieldAngle,
				Vignetting:    vignettingRatio(kept, total),
				GridTotal:     total,
				GridSurviving: kept,
			},
		}
		if results[fi].EntrancePupil != nil {
			fr.EntrancePupilZ = results[fi].EntrancePupil.Center.Z
		}
		if results[fi].ExitPupil != nil {
			fr.ExitPupilZ = results[fi].ExitPupil.Center.Z
		}

		if lo, hi, ok := envelopeAtPlane(all[0], frames[fi]); ok {
			fr.BoundLower = lo
			fr.BoundUpper = hi
		}

		if up, lo := extremeRays(fi, traces); up != nil || lo != nil {
			if up != nil {
				fr.MarginalUpper = rayFromTrace(fi, *up, "Yplus", opts.Wavelength, path, pol)
				if h, ok := rayHeightAtPlane(up.pos, frames[fi]); ok {
					fr.MarginalYUpper = h
				}
			}
			if lo != nil {
				fr.MarginalLower = rayFromTrace(fi, *lo, "Yminus", opts.Wavelength, path, pol)
				if h, ok := rayHeightAtPlane(lo.pos, frames[fi]); ok {
					fr.MarginalYLower = h
				}
			}
		}
		fields[fi] = fr
	}

	return fields, extents
}

func vignettingRatio(kept, total int) float64 {
	if total <= 0 {
		return 0
	}
	return float64(kept) / float64(total)
}

// extremeRays returns the surviving traces with the maximum and minimum image Y
// (upper / lower marginal rays).
func extremeRays(_ int, traces []tracedRay) (up, lo *tracedRay) {
	for i := range traces {
		if up == nil || traces[i].imgY > up.imgY {
			t := traces[i]
			up = &t
		}
		if lo == nil || traces[i].imgY < lo.imgY {
			t := traces[i]
			lo = &t
		}
	}
	return up, lo
}

func rayFromTrace(fi int, t tracedRay, tag string, wavelength float64, path []int, pol types.JonesVector) *types.Ray {
	r := types.Ray{
		ID:         fmt.Sprintf("marginal_f%d_%s", fi, tag),
		Wavelength: wavelength,
		Initial:    types.RayState{Origin: t.origin, Direction: t.dir},
		Path:       path,
		Jones:      pol,
	}
	return &r
}

// planeFrame is a comparison plane for one field: the plane through the
// field's entrance pupil, perpendicular to its chief ray. The meridional axis
// m is the unit direction in the meridional (Y-Z) plane perpendicular to the
// chief ray, so heights are measured along the true off-axis bundle direction
// rather than along the optical axis.
type planeFrame struct {
	center types.Vec3
	axis   types.Vec3 // unit chief-ray direction (plane normal)
	m      types.Vec3 // unit meridional axis (in-plane height direction)
}

// chiefPlaneFrame builds the chief-ray-perpendicular comparison plane for one
// field. The chief ray is traced so the plane normal reflects the in-lens beam
// direction at the entrance pupil. Falls back to the optical axis when no
// entrance pupil or chief direction is available.
func chiefPlaneFrame(engine *ray.Engine, surfaces []types.Surface, path []int, r chief.Result, pol types.JonesVector, wavelength float64) planeFrame {
	axis := types.Vec3{X: 0, Y: 0, Z: 1}
	if r.EntrancePupil != nil && r.ChiefRay.Initial.Direction.LengthSq() > 0 {
		dir := r.ChiefRay.Initial.Direction.Normalize()
		// Trace the chief ray so the plane normal matches the beam direction at
		// the entrance pupil (in-lens), not the object-space direction.
		tr := engine.TraceRay(types.Ray{
			Wavelength: wavelength,
			Initial:    types.RayState{Origin: r.ChiefRay.Initial.Origin, Direction: dir},
			Path:       path,
			Jones:      pol,
		}, surfaces)
		if tr.Error == "" {
			z := r.EntrancePupil.Center.Z
			if d, ok := rayDirAtZ(tr.Surfaces, z); ok && d.LengthSq() > 1e-18 {
				axis = d.Normalize()
			}
		}
	}
	m := meridionalAxis(axis)
	center := types.Vec3{X: 0, Y: 0, Z: 0}
	if r.EntrancePupil != nil {
		center = r.EntrancePupil.Center
	}
	return planeFrame{center: center, axis: axis, m: m}
}

// meridionalAxis returns the unit direction in the Y-Z (meridional) plane
// perpendicular to the given chief-ray direction, pointing toward +Y.
func meridionalAxis(axis types.Vec3) types.Vec3 {
	// Project +Y onto the plane perpendicular to the axis.
	y := types.Vec3{X: 0, Y: 1, Z: 0}
	along := axis.Scale(y.Dot(axis))
	m := y.Subtract(along)
	if m.LengthSq() < 1e-18 {
		return y
	}
	return m.Normalize()
}

// envelopeAtPlane returns the min/max meridional height of the given rays at
// the comparison plane. ok is false when no ray spans that plane.
func envelopeAtPlane(rays []tracedRay, f planeFrame) (lo, hi float64, ok bool) {
	lo, hi = math.Inf(1), math.Inf(-1)
	hit := false
	for _, t := range rays {
		h, hok := rayHeightAtPlane(t.pos, f)
		if !hok {
			continue
		}
		hit = true
		if h < lo {
			lo = h
		}
		if h > hi {
			hi = h
		}
	}
	if !hit {
		return 0, 0, false
	}
	return lo, hi, true
}

// rayHeightAtPlane interpolates the ray's meridional height at the field's
// comparison plane (through center, normal axis) along its piecewise-linear
// path. The height is the signed projection of (pos - center) onto the
// meridional axis m.
func rayHeightAtPlane(pos []types.Vec3, f planeFrame) (float64, bool) {
	dist := func(p types.Vec3) float64 {
		return (p.X-f.center.X)*f.axis.X + (p.Y-f.center.Y)*f.axis.Y + (p.Z-f.center.Z)*f.axis.Z
	}
	height := func(p types.Vec3) float64 {
		return (p.X-f.center.X)*f.m.X + (p.Y-f.center.Y)*f.m.Y + (p.Z-f.center.Z)*f.m.Z
	}
	for k := 0; k+1 < len(pos); k++ {
		a, b := pos[k], pos[k+1]
		da, db := dist(a), dist(b)
		if (da <= 0 && db >= 0) || (db <= 0 && da >= 0) {
			dz := db - da
			if math.Abs(dz) < 1e-18 {
				if math.Abs(da) < 1e-9 {
					return height(a), true
				}
				continue
			}
			t := -da / dz
			return height(a) + t*(height(b)-height(a)), true
		}
	}
	return 0, false
}

// rayDirAtZ returns the interpolated ray direction at the plane Z along its
// piecewise-linear trace.
func rayDirAtZ(pts []types.SurfaceResult, z float64) (types.Vec3, bool) {
	for k := 0; k+1 < len(pts); k++ {
		a, b := pts[k], pts[k+1]
		if (a.Position.Z <= z && z <= b.Position.Z) || (b.Position.Z <= z && z <= a.Position.Z) {
			dz := b.Position.Z - a.Position.Z
			if math.Abs(dz) < 1e-18 {
				if math.Abs(a.Position.Z-z) < 1e-9 {
					return a.Direction, true
				}
				continue
			}
			t := (z - a.Position.Z) / dz
			return a.Direction.Add(b.Direction.Subtract(a.Direction).Scale(t)), true
		}
	}
	return types.Vec3{}, false
}

// applyMinGlassPath sets opts.MinGlassPath on every glass element's entry
// surface that does not already have a value. The entry surface is a
// non-AIR surface whose preceding medium is AIR.
func applyMinGlassPath(surfaces []types.Surface, min float64) {
	if min <= 0 {
		return
	}
	for i := range surfaces {
		m := surfaces[i].Material
		if m.IsAir() {
			continue
		}
		prev := types.Material{}
		for j := i - 1; j >= 0; j-- {
			if !surfaces[j].Reflects() {
				prev = surfaces[j].Material
				break
			}
		}
		if prev.IsAir() && surfaces[i].MinGlassPath <= 0 {
			surfaces[i].MinGlassPath = min
		}
	}
}
