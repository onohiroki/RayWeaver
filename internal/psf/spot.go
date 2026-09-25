package psf

import (
	"math"

	"github.com/hiroki/rayweaver/internal/glass"
	"github.com/hiroki/rayweaver/internal/ray"
	"github.com/hiroki/rayweaver/internal/types"
)

// SpotResult is the geometric (ray) spot summary at one image plane for one
// (field, wavelength, polarization). All radii/positions are in mm.
type SpotResult struct {
	FieldIndex   int
	FieldAngle   float64
	Wavelength   float64
	Polarization string
	// SpotRMS is the flux-weighted RMS radius about the flux-weighted centroid;
	// SpotRMSX/Y decompose it into the image-plane x/y axes and SpotRMST/S into
	// the tangential (field azimuth) / sagittal axes. Both pairs are orthonormal
	// decompositions, so SpotRMS² = SpotRMSX² + SpotRMSY² = SpotRMST² + SpotRMSS².
	SpotRMS   float64
	SpotRMSX  float64
	SpotRMSY  float64
	SpotRMST  float64
	SpotRMSS  float64
	CentroidX float64
	CentroidY float64
	// BestFocusShift is the per-field best-focus shift applied when
	// Options.BestFocus is set (mm); 0 otherwise.
	BestFocusShift float64
}

// ComputeSpot evaluates the flux-weighted geometric spot RMS (and its x/y and
// tangential/sagittal decompositions) for every (field, wavelength,
// polarization) at the image plane shifted by opts.PlaneShift, without the
// Huygens integral — it only traces the reference-surface samples and
// propagates them to the plane. When opts.BestFocus is set each field is
// evaluated at its own coherent-peak best focus (BestFocusShift) and
// SpotResult.BestFocusShift reports the applied shift. Samples are weighted by
// Area·Intensity (the flux weight the optimizer's spot_rms uses); this differs
// slightly from psf.Result.SpotRMS, which weights by Intensity only.
func ComputeSpot(system types.System, gc *glass.Catalog, fields []types.FieldDef,
	wavelengths []float64, opts Options) ([]SpotResult, error) {
	engine := ray.NewEngine(gc, nil)
	if opts.ReferenceSurface <= 0 {
		opts.ReferenceSurface = DefaultReferenceSurface(system.Surfaces)
	}
	if opts.NumRays <= 0 {
		opts.NumRays = DefaultNumRays
	}
	if opts.GridType == "" {
		opts.GridType = types.GridPolar
	}
	if len(wavelengths) == 0 {
		wavelengths = []float64{types.DefaultWavelength}
	}
	pols := resolvePolStates(opts.Polarizations)
	if len(pols) == 0 {
		pols = resolvePolStates(nil)
	}
	planeZ := imagePlaneZ(system.Surfaces) + opts.PlaneShift

	var out []SpotResult
	for fi, fd := range fields {
		tx, ty := spotAzimuth(fd.Direction)
		for _, wl := range wavelengths {
			pg, err := ComputeFieldGrid(system, gc, fd, opts.ReferenceSurface, opts.NumRays, wl, opts.GridType, opts.PupilModel)
			if err != nil || len(pg.GridPoints) == 0 {
				continue
			}
			fieldAngle := angleFromDir(pg.ChiefDir)
			nImage := ImageSpaceIndex(system.Surfaces, opts.ReferenceSurface, wl, gc)
			for pi := 0; pi < len(pols); pi++ {
				st := pols[pi]
				samples, _ := TraceWavefront(system, engine, pg, fd, opts.ReferenceSurface, wl, st.jones, opts.Workers)
				if len(samples) < 3 {
					continue
				}
				evaluateZ := planeZ
				shift := 0.0
				if opts.BestFocus {
					shift = BestFocusShift(samples, planeZ, nImage, wl)
					evaluateZ = planeZ + shift
				}
				r := spotDecompose(samples, evaluateZ, tx, ty)
				r.FieldIndex = fi
				r.FieldAngle = fieldAngle
				r.Wavelength = wl
				r.Polarization = st.label
				r.BestFocusShift = shift
				if st.combined {
					// RCP+LCP: the geometric spot is state-independent (the two
					// siblings share ray geometry), so report one row.
					r.Polarization = string(types.PolRCPLCP)
					pi++
				}
				out = append(out, r)
			}
		}
	}
	return out, nil
}

// spotAzimuth returns the normalized in-plane field azimuth (the tangential
// direction), defaulting to +Y when the direction is absent or degenerate.
func spotAzimuth(direction []float64) (float64, float64) {
	dx, dy := 0.0, 1.0
	if len(direction) >= 2 {
		if n := math.Hypot(direction[0], direction[1]); n > 0 {
			dx, dy = direction[0]/n, direction[1]/n
		}
	}
	return dx, dy
}

// spotDecompose propagates the samples to the flat image plane at planeZ,
// computes the flux-weighted centroid (Area·Intensity weight, degrading to
// Intensity then equal weight) and returns the RMS radius about it along the
// image-plane x/y axes and the tangential (tx,ty) / sagittal axes.
func spotDecompose(samples []WavefrontSample, planeZ, tx, ty float64) SpotResult {
	type spt struct {
		x, y, w float64
	}
	pts := make([]spt, 0, len(samples))
	var sw float64
	for _, s := range samples {
		if math.Abs(s.Direction.Z) < 1e-9 {
			continue
		}
		t := (planeZ - s.Position.Z) / s.Direction.Z
		w := s.Area * s.Intensity
		if w <= 0 {
			w = s.Intensity
		}
		if w <= 0 {
			w = 1
		}
		pts = append(pts, spt{
			x: s.Position.X + s.Direction.X*t,
			y: s.Position.Y + s.Direction.Y*t,
			w: w,
		})
		sw += w
	}
	if sw <= 0 {
		return SpotResult{}
	}
	var cx, cy float64
	for _, p := range pts {
		cx += p.x * p.w
		cy += p.y * p.w
	}
	cx /= sw
	cy /= sw

	norm := math.Hypot(tx, ty)
	if norm == 0 {
		tx, ty = 0, 1
	} else {
		tx /= norm
		ty /= norm
	}
	sx, sy := -ty, tx

	var sxx, syy, stt, sss, sr2 float64
	for _, p := range pts {
		dx := p.x - cx
		dy := p.y - cy
		sxx += p.w * dx * dx
		syy += p.w * dy * dy
		tv := dx*tx + dy*ty
		sv := dx*sx + dy*sy
		stt += p.w * tv * tv
		sss += p.w * sv * sv
		sr2 += p.w * (dx*dx + dy*dy)
	}
	return SpotResult{
		SpotRMS:   math.Sqrt(sr2 / sw),
		SpotRMSX:  math.Sqrt(sxx / sw),
		SpotRMSY:  math.Sqrt(syy / sw),
		SpotRMST:  math.Sqrt(stt / sw),
		SpotRMSS:  math.Sqrt(sss / sw),
		CentroidX: cx,
		CentroidY: cy,
	}
}
