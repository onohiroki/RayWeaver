package chief

import (
	"fmt"
	"math"

	"github.com/hiroki/rayweaver/internal/types"
)

// MarginalRaysForField extracts the grid rays at the extreme pupil-plane
// positions (max/min PupilY and optionally PupilX) for one chief result and
// returns them as marginal rays. Pupil-plane coordinates come from the grid
// so that the marginal rays trace the aperture boundary.
func MarginalRaysForField(fi int, r Result, wavelength float64, path []int, pol types.JonesVector) []types.Ray {
	var maxY, minY *types.GridPoint
	var maxX, minX *types.GridPoint
	hasX := math.Abs(r.FieldDir.X) > 1e-6

	for i := range r.GridPoints {
		gp := &r.GridPoints[i]
		if gp.ImageX == nil || gp.ImageY == nil {
			continue
		}
		py := gp.PupilY
		if maxY == nil || py > maxY.PupilY {
			maxY = gp
		}
		if minY == nil || py < minY.PupilY {
			minY = gp
		}
		if hasX {
			px := gp.PupilX
			if maxX == nil || px > maxX.PupilX {
				maxX = gp
			}
			if minX == nil || px < minX.PupilX {
				minX = gp
			}
		}
	}

	fid := fmt.Sprintf("f%d", fi)
	var rays []types.Ray
	if maxY != nil && *maxY.ImageY != 0 {
		rays = append(rays, types.Ray{
			ID:         fmt.Sprintf("marginal_%s_Yplus", fid),
			Wavelength: wavelength,
			Initial:    types.RayState{Origin: maxY.Origin, Direction: maxY.Direction},
			Path:       path,
			Jones:      pol,
		})
	}
	if minY != nil && *minY.ImageY != 0 {
		rays = append(rays, types.Ray{
			ID:         fmt.Sprintf("marginal_%s_Yminus", fid),
			Wavelength: wavelength,
			Initial:    types.RayState{Origin: minY.Origin, Direction: minY.Direction},
			Path:       path,
			Jones:      pol,
		})
	}
	if hasX {
		if maxX != nil && *maxX.ImageX != 0 {
			rays = append(rays, types.Ray{
				ID:         fmt.Sprintf("marginal_%s_Xplus", fid),
				Wavelength: wavelength,
				Initial:    types.RayState{Origin: maxX.Origin, Direction: maxX.Direction},
				Path:       path,
				Jones:      pol,
			})
		}
		if minX != nil && *minX.ImageX != 0 {
			rays = append(rays, types.Ray{
				ID:         fmt.Sprintf("marginal_%s_Xminus", fid),
				Wavelength: wavelength,
				Initial:    types.RayState{Origin: minX.Origin, Direction: minX.Direction},
				Path:       path,
				Jones:      pol,
			})
		}
	}
	return rays
}