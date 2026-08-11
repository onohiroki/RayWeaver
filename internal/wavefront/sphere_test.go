package wavefront

import (
	"math"
	"testing"

	"github.com/hiroki/rayweaver/internal/psf"
	"github.com/hiroki/rayweaver/internal/types"
)

// syntheticSamples builds samples on a curved reference surface (sag z=-r²/40)
// of a perfect converging wave focused at (0, y0, f). OPL = K - n·|P-F|.
func syntheticSamples(y0, f, n float64) []psf.WavefrontSample {
	var out []psf.WavefrontSample
	const K = 100.0
	ang := []float64{0, 0.5, 1, 1.5, 2, 2.5, 3}
	for _, r := range ang {
		for _, a := range []float64{0, math.Pi / 3, 2 * math.Pi / 3, math.Pi, 4 * math.Pi / 3, 5 * math.Pi / 3} {
			x := r * math.Cos(a)
			y := y0 + r*math.Sin(a)
			z := -(x*x + (y-y0)*(y-y0)) / 20.0 // radius -10 reference surface
			P := types.Vec3{X: x, Y: y, Z: z}
			F := types.Vec3{Y: y0, Z: f}
			d := P.Subtract(F)
			dir := d.Scale(1 / d.Length())
			out = append(out, psf.WavefrontSample{
				Position:  P,
				Direction: dir,
				OPL:       K - n*d.Length(),
				Area:      1,
				Intensity: 1,
			})
		}
	}
	return out
}

func TestFitSphereShiftSynthetic(t *testing.T) {
	for _, tc := range []struct {
		y0, focusZ float64
	}{
		{0, 21.40},     // on-axis, focus 0.03 beyond image plane
		{7.15, 21.40},  // off-axis at image height 7.15
		{11.09, 21.40}, // off-axis at image height 11.09
	} {
		const planeZ = 21.37
		// Perfect converging wave focused at (0, y0, focusZ).
		samples := syntheticSamples(tc.y0, tc.focusZ, 1.0)
		sph, err := FitSphereShift(samples, planeZ)
		if err != nil {
			t.Fatalf("y0=%v: %v", tc.y0, err)
		}
		want := tc.focusZ - planeZ // +0.03
		if math.Abs(sph.ShiftMM-want) > 1e-3 {
			t.Errorf("y0=%v: shift = %v, want ~%v", tc.y0, sph.ShiftMM, want)
		}
		if sph.SpotRMS > 1e-6 {
			t.Errorf("y0=%v: spot RMS = %v, want ~0 (perfect wave)", tc.y0, sph.SpotRMS)
		}
	}
}

func TestFitSphereShiftDefocus(t *testing.T) {
	// A beam with real defocus: perfect wave focused at 17 mm while the image
	// plane is at 21 mm -> best shift ≈ -4 mm (move the image toward the lens).
	const planeZ = 21.0
	samples := syntheticSamples(0, 17.0, 1.0)
	sph, err := FitSphereShift(samples, planeZ)
	if err != nil {
		t.Fatal(err)
	}
	if math.Abs(sph.ShiftMM-(-4.0)) > 0.2 {
		t.Errorf("defocus: shift = %v, want ~-4", sph.ShiftMM)
	}
}
