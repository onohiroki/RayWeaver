package surface

import (
	"math"

	"github.com/hiroki/rayweaver/internal/types"
)

// FindStopID returns the aperture stop: the fixed (non-auto_aperture) surface
// with the smallest diameter, since auto_aperture surfaces are sized by the
// beam and must never define the stop. Falls back to the smallest diameter
// overall when every surface is auto_aperture.
func FindStopID(surfaces []types.Surface) int {
	stopID := 0
	minD := math.MaxFloat64
	for _, s := range surfaces {
		if !s.AutoAperture && s.Diameter > 0 && s.Diameter < minD {
			minD = s.Diameter
			stopID = s.ID
		}
	}
	if stopID != 0 {
		return stopID
	}
	minD = math.MaxFloat64
	for _, s := range surfaces {
		if s.Diameter > 0 && s.Diameter < minD {
			minD = s.Diameter
			stopID = s.ID
		}
	}
	return stopID
}

// ComputeStopZ returns the physical Z of the stop surface, resolving the stop
// via FindStopID when stopID is not positive.
func ComputeStopZ(surfaces []types.Surface, stopID int) float64 {
	if stopID <= 0 {
		stopID = FindStopID(surfaces)
	}
	if stopID == 0 {
		return 0
	}
	for _, s := range surfaces {
		if s.ID == stopID {
			return s.PhysicalZ
		}
	}
	return 0
}
