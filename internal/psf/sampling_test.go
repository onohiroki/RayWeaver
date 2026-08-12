package psf

import (
	"math"
	"testing"

	"github.com/hiroki/rayweaver/internal/types"
)

// gridSamples builds N samples on a flat reference surface (a disk of radius
// R at z = 0) whose emergent rays land within +-spread of the axis at the
// image plane z = planeZ (the geometric spot). The pupil footprint R sets the
// image NA.
func gridSamples(n int, R, spread, planeZ float64) []WavefrontSample {
	out := make([]WavefrontSample, 0, n)
	rows := int(math.Ceil(math.Sqrt(float64(n))))
	for i := 0; i < rows; i++ {
		for j := 0; j < rows; j++ {
			// Deterministic pseudo-random position on the disk [-R, R]^2.
			x := R * (2*frac(0.1234567+float64(i*7+j)*0.7919) - 1)
			y := R * (2*frac(0.7654321+float64(i*3+j)*0.3183) - 1)
			// Landing at the image plane within +-spread of the axis.
			lx := spread * (2*frac(0.333333+float64(i*5+j)*0.9173) - 1)
			ly := spread * (2*frac(0.666666+float64(i*2+j)*0.5397) - 1)
			dir := types.Vec3{X: lx - x, Y: ly - y, Z: planeZ}.Normalize()
			out = append(out, WavefrontSample{
				Position:  types.Vec3{X: x, Y: y, Z: 0},
				Direction: dir,
				Intensity: 1,
				Area:      1,
				OPL:       planeZ,
			})
		}
	}
	return out
}

func frac(v float64) float64 {
	return v - math.Floor(v)
}

func TestDefaultImageGridResolvesAiryCore(t *testing.T) {
	const wl = 0.0005876 // 587.6 nm in mm
	const planeZ = 40.0
	const R = 10.0 // pupil radius -> NA = 0.25, airy = 1.43 um

	// Fast, aberrated system: a 50 um geometric spot dominates the auto window
	// (half = 3*spot = 150 um), so the requested 64-pixel grid would leave the
	// Airy core (1.43 um) smaller than a pixel. The grid must auto-enlarge
	// until res <= airy/2.
	fast := gridSamples(400, R, 0.025, planeZ)
	spec := DefaultImageGrid(fast, types.Vec3{Z: planeZ}, 1, wl, planeZ, 0, 0, 0, 64)
	airy := AiryRadius(wl, ComputeImageNA(fast, types.Vec3{Z: planeZ}, 1))
	if spec.NX <= 64 {
		t.Errorf("fast system: grid %d not enlarged (want > 64, airy=%.5g mm)", spec.NX, airy)
	}
	if spec.DX > airy/2 {
		t.Errorf("fast system: DX %.5g mm > airy/2 (%.5g mm), core under-resolved", spec.DX, airy/2)
	}

	// Slow / well-corrected system: a sub-micron spot means the Airy disk
	// dominates the auto window (half = 4*airy), where the default 64 grid
	// already resolves the core (res ~ airy/8). It must not grow.
	slow := gridSamples(400, R, 0.0005, planeZ)
	spec = DefaultImageGrid(slow, types.Vec3{Z: planeZ}, 1, wl, planeZ, 0, 0, 0, 64)
	if spec.NX != 64 {
		t.Errorf("slow system: grid enlarged to %d, want 64", spec.NX)
	}
}
