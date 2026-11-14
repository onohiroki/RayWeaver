package render

import (
	"bytes"
	"image/png"
	"math"
	"regexp"
	"strconv"
	"testing"

	"github.com/hiroki/rayweaver/internal/types"
)

// panelSurfaces builds a small two-element system whose image plane sits at
// airGap-dependent Z, so panels with different zoom positions really do have
// different image-plane Z values.
func panelSurfaces(airGap, diameter float64) []types.Surface {
	return []types.Surface{
		glassSurf(1, 0.02, 8, diameter),
		{
			ID: 2, Curvature: -0.02, Thickness: airGap, Diameter: diameter,
			Material: types.Material{Key: "AIR"},
		},
		glassSurf(3, 0.01, 4, diameter*0.8),
		{
			ID: 4, Thickness: 0, Diameter: diameter * 0.8,
			Material: types.Material{Key: "AIR"},
		},
	}
}

// planeZ is the last surface's vertex Z (the image plane) for panelSurfaces.
func planeZ(airGap float64) float64 {
	return 8 + airGap + 4
}

func twoPanels() []Panel {
	return []Panel{
		{Config: Config{Surfaces: panelSurfaces(20, 10)}, ImagePlaneZ: planeZ(20)},
		{Config: Config{Surfaces: panelSurfaces(60, 12)}, ImagePlaneZ: planeZ(60)},
	}
}

var (
	panelOriginRE = regexp.MustCompile(`<g transform="translate\(([-\d.]+),([-\d.]+)\) scale\(1,-1\)">`)
	panelScaleRE  = regexp.MustCompile(`<g transform="scale\(([\d.]+)\) translate`)
	svgSizeRE     = regexp.MustCompile(`<svg [^>]*width="(\d+)" height="(\d+)"`)
)

func svgSize(t *testing.T, svg string) (int, int) {
	t.Helper()
	m := svgSizeRE.FindStringSubmatch(svg)
	if m == nil {
		t.Fatalf("SVG header not found in %q", svg[:120])
	}
	w, _ := strconv.Atoi(m[1])
	h, _ := strconv.Atoi(m[2])
	return w, h
}

// A one-panel multi-config plot is the single-config diagram: there is nothing
// to align or stack, so both formats render byte-identically.
func TestMultiPanelOfOneIsTheSingleDiagram(t *testing.T) {
	single := Config{Surfaces: panelSurfaces(20, 10)}

	gotSVG := MultiLensSVG([]Panel{{Config: Config{Surfaces: panelSurfaces(20, 10)}, ImagePlaneZ: planeZ(20)}})
	if wantSVG := LensSVG(single); gotSVG != wantSVG {
		t.Errorf("MultiLensSVG of one panel differs from LensSVG (%d vs %d bytes)", len(gotSVG), len(wantSVG))
	}

	gotPNG, err := MultiLensPNG([]Panel{{Config: Config{Surfaces: panelSurfaces(20, 10)}, ImagePlaneZ: planeZ(20)}})
	if err != nil {
		t.Fatalf("MultiLensPNG: %v", err)
	}
	wantPNG, err := LensPNG(single)
	if err != nil {
		t.Fatalf("LensPNG: %v", err)
	}
	if !bytes.Equal(gotPNG, wantPNG) {
		t.Errorf("MultiLensPNG of one panel differs from LensPNG (%d vs %d bytes)", len(gotPNG), len(wantPNG))
	}
}

// TestMultiPanelLayout pins the three layout promises: one shared scale, the
// image plane on a common vertical line, and the panels stacked without
// overlapping.
func TestMultiPanelLayout(t *testing.T) {
	panels := twoPanels()
	svg := MultiLensSVG(panels)

	if w, _ := svgSize(t, svg); w != int(canvasW) {
		t.Errorf("width = %d, want %.0f", w, canvasW)
	}

	origins := panelOriginRE.FindAllStringSubmatch(svg, -1)
	if len(origins) != len(panels) {
		t.Fatalf("found %d panel groups, want %d", len(origins), len(panels))
	}
	x0, _ := strconv.ParseFloat(origins[0][1], 64)
	y0, _ := strconv.ParseFloat(origins[0][2], 64)
	x1, _ := strconv.ParseFloat(origins[1][1], 64)
	y1, _ := strconv.ParseFloat(origins[1][2], 64)
	if x0 != x1 {
		t.Errorf("image planes not aligned: x = %g and %g", x0, x1)
	}
	if y1 <= y0 {
		t.Errorf("panels not stacked downwards: y = %g and %g", y0, y1)
	}

	scales := panelScaleRE.FindAllStringSubmatch(svg, -1)
	if len(scales) != len(panels) {
		t.Fatalf("found %d scale transforms, want %d", len(scales), len(panels))
	}
	for i := range scales {
		if scales[i][1] != scales[0][1] {
			t.Errorf("panel %d scale = %s, want the shared %s", i, scales[i][1], scales[0][1])
		}
	}

	// The band centre distance is exactly half of each band plus the gap; that
	// is what keeps the drawings from overlapping.
	scale, _ := strconv.ParseFloat(scales[0][1], 64)
	band0 := 2*buildScene(panels[0].Config).maxY*scale + 2*multiPanelPad
	band1 := 2*buildScene(panels[1].Config).maxY*scale + 2*multiPanelPad
	wantGap := band0/2 + multiPanelGap + band1/2
	// The SVG prints the transform in whole pixels, hence the half-pixel slack.
	if math.Abs((y1-y0)-wantGap) > 0.51 {
		t.Errorf("band centre distance = %g, want %g (bands %g and %g)", y1-y0, wantGap, band0, band1)
	}
}

func TestMultiLensPNGStacksPanels(t *testing.T) {
	data, err := MultiLensPNG(twoPanels())
	if err != nil {
		t.Fatalf("MultiLensPNG: %v", err)
	}
	img, err := png.Decode(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("decode PNG: %v", err)
	}
	b := img.Bounds()
	if b.Dx() != int(canvasW) {
		t.Errorf("width = %d, want %.0f", b.Dx(), canvasW)
	}
	if b.Dy() < int(canvasH) {
		t.Errorf("height = %d, want at least %.0f", b.Dy(), canvasH)
	}

	// Both bands must carry ink: count non-white pixels in the top and bottom
	// thirds of the image (the panels sit in separate bands).
	nonWhite := func(yMin, yMax int) int {
		n := 0
		for y := yMin; y < yMax; y++ {
			for x := b.Min.X; x < b.Max.X; x++ {
				r, g, bl, _ := img.At(x, y).RGBA()
				if r < 0xf000 || g < 0xf000 || bl < 0xf000 {
					n++
				}
			}
		}
		return n
	}
	if n := nonWhite(b.Min.Y, b.Min.Y+int(canvasH)/3); n == 0 {
		t.Error("top band is empty")
	}
	if n := nonWhite(b.Max.Y-int(canvasH)/3, b.Max.Y); n == 0 {
		t.Error("bottom band is empty")
	}

	again, err := MultiLensPNG(twoPanels())
	if err != nil {
		t.Fatalf("MultiLensPNG (second run): %v", err)
	}
	if !bytes.Equal(data, again) {
		t.Error("MultiLensPNG is not deterministic")
	}
}

// TestMultiConfigCanvasGrows checks that 5 tall panels grow the canvas instead
// of shrinking the drawing into unreadable bands.
func TestMultiConfigCanvasGrows(t *testing.T) {
	var panels []Panel
	for i := 0; i < 5; i++ {
		airGap := 20 + float64(i)*10
		panels = append(panels, Panel{
			Config:      Config{Surfaces: panelSurfaces(airGap, 40)},
			ImagePlaneZ: planeZ(airGap),
		})
	}
	svg := MultiLensSVG(panels)
	if _, h := svgSize(t, svg); h <= int(canvasH) {
		t.Errorf("height = %d, want more than %.0f for 5 panels", h, canvasH)
	}
}
