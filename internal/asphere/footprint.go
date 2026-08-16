package asphere

import (
	"runtime"

	"github.com/hiroki/rayweaver/internal/dls"
	"github.com/hiroki/rayweaver/internal/glass"
	"github.com/hiroki/rayweaver/internal/pupil"
	"github.com/hiroki/rayweaver/internal/ray"
	"github.com/hiroki/rayweaver/internal/raymath"
	"github.com/hiroki/rayweaver/internal/surface"
	"github.com/hiroki/rayweaver/internal/types"
)

// Field is one object-space field used by the asphere analysis.
type Field struct {
	ID        int
	Angle     float64 // object field angle (degrees)
	Weight    float64
	Direction []float64 // [dx, dy] azimuth of the field (normalised)
}

// SurfaceHit is a ray's intersection with one surface: the intersection point
// in global coordinates and the emergent direction after the interaction.
type SurfaceHit struct {
	Position  types.Vec3
	Direction types.Vec3
}

// RayHit is one traced ray's data: the image OPL (optical path length to the
// image plane), the pupil-plane grid coordinate, and the per-surface
// intersections along the path.
type RayHit struct {
	OPL    float64
	OPD    float64 // wavefront error referenced to the field mean (post-preprocessing)
	Weight float64
	PupilX float64
	PupilY float64
	Hits   map[int]SurfaceHit
	OK     bool
}

// FieldFootprintData is all ray hits for one (field, wavelength) pair.
type FieldFootprintData struct {
	FieldID    int
	Wavelength float64
	Weight     float64
	RayHits    []RayHit
}

// GenerateFootprints traces a polar pupil grid for each (field, wavelength)
// and returns per-ray image OPL plus per-surface intersection data. The grid
// radius and centring follow the same conventions as the chief/optimize grid
// traces (dls.ApertureRadiusForGrid plus a per-field pupil offset), so the
// beam fills the entrance pupil for every field. pupilZs gives each field's
// entrance-pupil Z (the aperture position) used to centre its grid. It uses
// the pupil package's wavefront-plane launch (OPLLaunch): the projected origin
// carries no launch-geometry tilt, and the recorded OPL is the raw OPLTotal
// (OPLDelta is zero for the projected launch).
func GenerateFootprints(surfaces []types.Surface, fields []Field, wavelengths []float64, pupilSamples int, gc *glass.Catalog, pupilZs []float64) []FieldFootprintData {
	if len(wavelengths) == 0 {
		wavelengths = []float64{types.DefaultWavelength}
	}
	engine := ray.NewEngine(gc, nil)
	path := dls.BuildPath(surfaces)
	zStart := -100.0

	var out []FieldFootprintData
	for fi, f := range fields {
		dir := rayDirection(f)
		pupilZ := 0.0
		if fi < len(pupilZs) {
			pupilZ = pupilZs[fi]
		}
		for _, wl := range wavelengths {
			fd := FieldFootprintData{FieldID: f.ID, Wavelength: wl, Weight: f.Weight}

			radius := dls.ApertureRadiusForGrid(surfaces, 0, wl, gc, 1.0)
			if radius <= 0 {
				radius = surface.MinApertureRadius(surfaces)
			}
			if radius <= 0 {
				continue
			}

			offsetX, offsetY := pupil.GridCentre(dir, pupilZ, zStart)
			samples := pupil.Launch(pupil.LaunchSpec{
				NumRays:        pupilSamples * pupilSamples,
				GridType:       types.GridPolar,
				ApertureRadius: radius,
				RayDir:         dir,
				CentreX:        offsetX,
				CentreY:        offsetY,
				ZStart:         zStart,
				OPLMode:        pupil.OPLLaunch,
			})
			pupil.Trace(engine, path, surfaces, samples, wl, types.NewCircularJones(true), runtime.GOMAXPROCS(0))

			fd.RayHits = make([]RayHit, len(samples))
			for i, s := range samples {
				hit := RayHit{Weight: f.Weight, PupilX: s.PupilX, PupilY: s.PupilY}
				if !s.OK || len(s.Surfaces) == 0 {
					fd.RayHits[i] = hit
					continue
				}
				hit.OPL = s.OPL
				hit.OK = true
				hit.Hits = make(map[int]SurfaceHit, len(s.Surfaces))
				for _, sr := range s.Surfaces {
					if sr.SurfaceID <= 0 {
						continue
					}
					hit.Hits[sr.SurfaceID] = SurfaceHit{Position: sr.Position, Direction: sr.Direction}
				}
				fd.RayHits[i] = hit
			}

			out = append(out, fd)
		}
	}
	return out
}

// rayDirection returns the object-space ray direction for a field: an angle in
// the XY plane, rotated by the field azimuth Direction.
func rayDirection(f Field) types.Vec3 {
	return raymath.DirectionFromField(f.Angle, f.Direction)
}
