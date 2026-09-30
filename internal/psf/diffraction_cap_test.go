package psf

import "testing"

// TestCapImageGridLimitsPixelsOnly pins the semantics the diffraction_mtf merit
// kinds rely on: the cap must never shorten the image window. Truncating the
// window would apply a box window whose sinc first zero sits at 1/(2·half) and
// would falsely zero the low-frequency MTF the gate reads, so only the pixel
// count may be limited — the origin stays where DefaultImageGrid put it and
// the pixel size grows to span/max, which leaves the frequency spacing
// df = 1/(2·half) (and with it the 10 c/mm bin) untouched.
func TestCapImageGridLimitsPixelsOnly(t *testing.T) {
	const half = 0.125 // mm
	spec := ImageGridSpec{NX: 1024, NY: 1024, X0: -half, Y0: -half, DX: 2 * half / 1024, DY: 2 * half / 1024}

	capped := capImageGrid(spec, 256)
	if capped.NX != 256 || capped.NY != 256 {
		t.Errorf("NX/NY = %d/%d, want 256/256", capped.NX, capped.NY)
	}
	if capped.X0 != spec.X0 || capped.Y0 != spec.Y0 {
		t.Errorf("window origin moved: (%v,%v), want (%v,%v)", capped.X0, capped.Y0, spec.X0, spec.Y0)
	}
	// The window span must be exactly preserved (X0 + NX·DX).
	spanBefore := float64(spec.NX) * spec.DX
	spanAfter := float64(capped.NX) * capped.DX
	if diff := spanAfter - spanBefore; diff > 1e-12 || diff < -1e-12 {
		t.Errorf("window span changed from %v to %v (Δ=%v)", spanBefore, spanAfter, diff)
	}
	if capped.DX <= spec.DX {
		t.Errorf("capping must coarsen dx: %v -> %v", spec.DX, capped.DX)
	}
}

// TestCapImageGridDisabledOrAlreadySmall covers the two no-op paths: a
// negative max disables the cap entirely (the standalone psf measurement) and
// a grid already at or below the cap is left bit-identical.
func TestCapImageGridDisabledOrAlreadySmall(t *testing.T) {
	spec := ImageGridSpec{NX: 512, NY: 512, X0: -1, Y0: -1, DX: 0.004, DY: 0.004}

	for _, max := range []int{-1, 0, 8, 512, 1024} {
		got := capImageGrid(spec, max)
		if got.NX != spec.NX || got.NY != spec.NY || got.DX != spec.DX || got.X0 != spec.X0 {
			t.Errorf("capImageGrid(spec, %d) changed an untouched grid: %+v", max, got)
		}
	}
}
