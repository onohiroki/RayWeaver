package render

import (
	"math"
)

// viewport is the world→canvas mapping of one render panel: world z placed so
// that anchorZ lands on originX, world y growing upwards from originY.
type viewport struct {
	w, h    int
	scale   float64
	originX float64
	originY float64
	anchorZ float64
}

// pt maps a world point (z, y) to canvas pixels.
func (v viewport) pt(z, y float64) (float32, float32) {
	return float32(v.originX + (z-v.anchorZ)*v.scale), float32(v.originY - y*v.scale)
}

// scene is one configuration's drawing data, resolved once and shared by the
// SVG and PNG renderers. The extents are the single-plot extents (object-side
// span capped at the back focal length, right margin as a fraction of the lens
// length), so a scene drawn on its own is unchanged by the multi-config path.
type scene struct {
	cfg       Config
	zPos      []float64
	rayPaths  []rayRenderData
	minZ      float64
	maxZ      float64
	zSpan     float64
	maxY      float64
	maxSurfZ  float64
	rightFrac float64
}

// Panel is one configuration of a multi-config plot. ImagePlaneZ is the world
// Z of that config's image plane; the multi-config renderer puts every panel's
// image plane on one common canvas column.
type Panel struct {
	Config      Config
	ImagePlaneZ float64
}

const (
	// multiPanelPad is the vertical padding above and below a panel's content,
	// so a stop tick or a sag curve never reaches into the gap.
	multiPanelPad = 8.0
	// multiPanelGap is the empty band between two panels.
	multiPanelGap = 24.0
)

// buildScene resolves one configuration's drawing data. It is the single
// source of the extents LensSVG/LensPNG have always used.
func buildScene(cfg Config) scene {
	sc := scene{cfg: cfg, maxY: 1.0, rightFrac: 0.2}
	if len(cfg.Surfaces) == 0 {
		// Degenerate but drawable: an empty axis in a default-sized frame.
		sc.maxSurfZ = 10
		sc.maxZ = 10
		sc.zSpan = 10
		return sc
	}

	sc.zPos = computeZPositions(cfg.Surfaces)
	sc.maxSurfZ = maxSurfaceZ(cfg.Surfaces)
	sc.rayPaths = buildRayPaths(cfg.Results, cfg.ChiefRays, cfg.MaxFanRays, cfg.InvalidRays)

	// Object-side display span: cap the extent to min(lens length, back focal
	// length), the rule the single-config diagram has always used.
	firstZ := sc.zPos[0]
	lastZ := sc.zPos[len(sc.zPos)-1]
	lensLen := lastZ - firstZ
	bfl := 0.0
	if len(cfg.Surfaces) >= 2 {
		bfl = cfg.Surfaces[len(cfg.Surfaces)-2].Thickness
	}
	if bfl <= 0 {
		bfl = lensLen
	}
	leftSpan := lensLen
	if bfl < leftSpan {
		leftSpan = bfl
	}
	candidateMinZ := firstZ - leftSpan

	sc.minZ = 0
	for _, r := range cfg.Results {
		for _, sr := range r.Surfaces {
			if sr.Position.Z < sc.minZ {
				sc.minZ = sr.Position.Z
			}
		}
	}
	if sc.minZ < candidateMinZ {
		sc.minZ = candidateMinZ
	}

	if cfg.RightMarginPct > 0 {
		sc.rightFrac = cfg.RightMarginPct / 100.0
	}
	sc.maxZ = sc.maxSurfZ * (1 + sc.rightFrac)
	sc.zSpan = sc.maxZ - sc.minZ
	if sc.zSpan <= 0 {
		sc.zSpan = sc.maxSurfZ
	}
	sc.maxY = computeMaxYInRange(cfg.Surfaces, cfg.Results, sc.minZ, sc.maxZ)
	return sc
}

// singleViewport is the layout of a one-config diagram: the fixed canvas, the
// whole content centred, exactly as before multi-config support.
func singleViewport(sc scene) viewport {
	scale := computeScale(sc.zSpan, sc.maxY)
	if sc.cfg.ScaleOverride > 0 {
		scale = sc.cfg.ScaleOverride
	}
	return viewport{
		w:       int(canvasW),
		h:       int(canvasH),
		scale:   scale,
		originX: canvasW / 2,
		originY: canvasH / 2,
		anchorZ: (sc.minZ + sc.maxZ) / 2,
	}
}

// multiViewports lays out a multi-config plot. Every panel shares one scale
// (pixels per millimetre), every image plane lands on the same canvas X, and
// the panels are stacked vertically without overlapping.
//
// The scale is the smaller of the width fit — the widest object-side extent
// plus the widest image-side margin, since the image planes must coincide —
// and the single-band height fit, so no band is taller than the one-config
// canvas. The canvas grows vertically as panels are added, and a single panel
// reproduces the one-config layout exactly.
func multiViewports(panels []Panel) ([]scene, []viewport) {
	if len(panels) == 0 {
		return nil, nil
	}
	// One panel has nothing to align or stack: draw the ordinary diagram, so
	// `--configs ID` and `--config ID` produce the same picture.
	if len(panels) == 1 {
		sc := buildScene(panels[0].Config)
		return []scene{sc}, []viewport{singleViewport(sc)}
	}
	scenes := make([]scene, len(panels))
	for i := range panels {
		scenes[i] = buildScene(panels[i].Config)
	}

	maxLeft, maxRight, maxY := 0.0, 0.0, 0.0
	for i := range scenes {
		left := panels[i].ImagePlaneZ - scenes[i].minZ
		right := scenes[i].maxZ - panels[i].ImagePlaneZ
		if left < 0 {
			left = 0
		}
		if right < 0 {
			right = 0
		}
		maxLeft = math.Max(maxLeft, left)
		maxRight = math.Max(maxRight, right)
		maxY = math.Max(maxY, scenes[i].maxY)
	}

	scale := math.Inf(1)
	if denom := maxLeft + maxRight; denom > 0 {
		scale = math.Min(scale, canvasW*0.8/denom)
	}
	if maxY > 0 {
		// The pad is part of the band, so it must fit the height budget too.
		scale = math.Min(scale, (canvasH-2*margin-2*multiPanelPad)/(2*maxY))
	}
	if math.IsInf(scale, 1) || scale <= 0 {
		scale = 1
	}
	if panels[0].Config.ScaleOverride > 0 {
		scale = panels[0].Config.ScaleOverride
	}

	bandH := make([]float64, len(scenes))
	total := 0.0
	for i := range scenes {
		bandH[i] = 2*scenes[i].maxY*scale + 2*multiPanelPad
		total += bandH[i]
	}
	if len(scenes) > 1 {
		total += multiPanelGap * float64(len(scenes)-1)
	}
	height := math.Max(canvasH, total+2*margin)

	// The image plane column: centre the common content box (widest
	// object-side extent + widest image-side margin) in the canvas.
	contentW := (maxLeft + maxRight) * scale
	originX := (canvasW-contentW)/2 + maxLeft*scale

	hPx := int(math.Round(height))
	cursor := (height - total) / 2
	vps := make([]viewport, len(scenes))
	for i := range scenes {
		vps[i] = viewport{
			w:       int(canvasW),
			h:       hPx,
			scale:   scale,
			originX: originX,
			originY: cursor + bandH[i]/2,
			anchorZ: panels[i].ImagePlaneZ,
		}
		cursor += bandH[i] + multiPanelGap
	}
	return scenes, vps
}
