package surface

import (
	"math"

	"github.com/hiroki/rayweaver/internal/types"
)

// ComputeStopZ returns the physical Z of the explicitly specified stop surface,
// or 0 when no stop is given. The stop is never inferred: with stopID <= 0 the
// system has no stop and callers fall back to the dynamic pupil.
func ComputeStopZ(surfaces []types.Surface, stopID int) float64 {
	if stopID <= 0 {
		return 0
	}
	for _, s := range surfaces {
		if s.ID == stopID {
			return s.PhysicalZ
		}
	}
	return 0
}

// MinApertureRadius returns the smallest aperture radius (diameter/2) over all
// surfaces with a positive diameter, or 0 when none exists.
func MinApertureRadius(surfaces []types.Surface) float64 {
	minR := math.MaxFloat64
	for _, s := range surfaces {
		if s.Diameter > 0 && s.Diameter/2 < minR {
			minR = s.Diameter / 2
		}
	}
	if minR == math.MaxFloat64 {
		return 0
	}
	return minR
}

// FixedMinApertureRadius returns the smallest aperture radius over surfaces
// with a positive diameter that are not auto_aperture, or 0 when none exists.
func FixedMinApertureRadius(surfaces []types.Surface) float64 {
	minR := math.MaxFloat64
	for _, s := range surfaces {
		if !s.AutoAperture && s.Diameter > 0 && s.Diameter/2 < minR {
			minR = s.Diameter / 2
		}
	}
	if minR == math.MaxFloat64 {
		return 0
	}
	return minR
}

// FixedMinApertureRadiusZ returns the physical Z of the fixed (auto_aperture:
// false) surface with the smallest aperture radius — the position where the
// beam is physically limited. Returns 0 when no such surface exists.
func FixedMinApertureRadiusZ(surfaces []types.Surface) float64 {
	minR := math.MaxFloat64
	z := 0.0
	for _, s := range surfaces {
		if !s.AutoAperture && s.Diameter > 0 && s.Diameter/2 < minR {
			minR = s.Diameter / 2
			z = s.PhysicalZ
		}
	}
	if minR == math.MaxFloat64 {
		return 0
	}
	return z
}
