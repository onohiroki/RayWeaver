// Package vignette iteratively settles surface diameters and per-field
// vignetting for a lens whose pupil is defined by the chief rays (dynamic
// pupil) rather than a physical stop surface.
//
// Each pass re-traces every field's pupil grid with the current surface
// diameters acting as clips and the glass-path (edge-thickness) check on,
// then sizes auto_aperture: true surfaces to the surviving-beam envelope.
// The on-axis (field 0) marginal-ray envelope at each field's entrance pupil
// plane bounds the off-axis bundles; rays falling outside it are vignetted.
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

	// Crossing-Z bounding: an off-axis field's rays must fall within field 0's
	// marginal-ray envelope at the field's entrance pupil plane, otherwise they
	// are vignetted (narrowed beam).
	if len(results) >= 2 {
		for fi := 1; fi < len(results); fi++ {
			z := 0.0
			if results[fi].EntrancePupil != nil {
				z = results[fi].EntrancePupil.Center.Z
			}
			lo, hi, ok := envelopeAtZ(all[0], z)
			if !ok {
				continue
			}
			keep := all[fi][:0]
			for _, t := range all[fi] {
				y, ok := rayYAtZ(t.pos, z)
				if !ok || (y >= lo-1e-9 && y <= hi+1e-9) {
					keep = append(keep, t)
				}
			}
			all[fi] = keep
		}
	}

	extents := make(map[int]float64)
	fields := make([]FieldResult, len(results))
	for fi := range results {
		traces := all[fi]
		fdExt := make(map[int]float64)
		for _, t := range traces {
			for j, p := range t.pos {
				e := math.Max(math.Abs(p.X), math.Abs(p.Y))
				if e > fdExt[t.ids[j]] {
					fdExt[t.ids[j]] = e
				}
			}
		}
		for id, e := range fdExt {
			if e > extents[id] {
				extents[id] = e
			}
		}

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

		z := fr.EntrancePupilZ
		if lo, hi, ok := envelopeAtZ(all[0], z); ok {
			fr.BoundLower = lo
			fr.BoundUpper = hi
		}

		if up, lo := extremeRays(fi, traces); up != nil || lo != nil {
			if up != nil {
				fr.MarginalUpper = rayFromTrace(fi, *up, "Yplus", opts.Wavelength, path, pol)
				if y, ok := rayYAtZ(up.pos, z); ok {
					fr.MarginalYUpper = y
				}
			}
			if lo != nil {
				fr.MarginalLower = rayFromTrace(fi, *lo, "Yminus", opts.Wavelength, path, pol)
				if y, ok := rayYAtZ(lo.pos, z); ok {
					fr.MarginalYLower = y
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

// envelopeAtZ returns the min/max interpolated Y of the given rays at the plane
// Z. ok is false when no ray spans that plane.
func envelopeAtZ(rays []tracedRay, z float64) (lo, hi float64, ok bool) {
	lo, hi = math.Inf(1), math.Inf(-1)
	hit := false
	for _, t := range rays {
		y, yok := rayYAtZ(t.pos, z)
		if !yok {
			continue
		}
		hit = true
		if y < lo {
			lo = y
		}
		if y > hi {
			hi = y
		}
	}
	if !hit {
		return 0, 0, false
	}
	return lo, hi, true
}

// rayYAtZ interpolates the ray's Y at the plane Z along its piecewise-linear
// path.
func rayYAtZ(pos []types.Vec3, z float64) (float64, bool) {
	for k := 0; k+1 < len(pos); k++ {
		a, b := pos[k], pos[k+1]
		if (a.Z <= z && z <= b.Z) || (b.Z <= z && z <= a.Z) {
			dz := b.Z - a.Z
			if math.Abs(dz) < 1e-18 {
				if math.Abs(a.Z-z) < 1e-9 {
					return a.Y, true
				}
				continue
			}
			t := (z - a.Z) / dz
			return a.Y + t*(b.Y-a.Y), true
		}
	}
	return 0, false
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
		if m == "" || m == "AIR" {
			continue
		}
		prev := "AIR"
		for j := i - 1; j >= 0; j-- {
			if !surfaces[j].Reflects() {
				prev = surfaces[j].Material
				break
			}
		}
		if (prev == "" || prev == "AIR") && surfaces[i].MinGlassPath <= 0 {
			surfaces[i].MinGlassPath = min
		}
	}
}
