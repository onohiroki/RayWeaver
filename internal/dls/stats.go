package dls

import (
    "math"

	"github.com/hiroki/rayweaver/internal/types"
)

type IPoint struct {
	X, Y float64
	OK   bool
}

type pupilPoint struct {
	X, Y float64
}

func Centroid(points []IPoint) (cx, cy float64, count int) {
	for _, p := range points {
		if !p.OK {
			continue
		}
		cx += p.X
		cy += p.Y
		count++
	}
	if count == 0 {
		return 0, 0, 0
	}
	cx /= float64(count)
	cy /= float64(count)
	return cx, cy, count
}

func ComputeSpotRMS(points []IPoint) float64 {
	cx, cy, count := Centroid(points)
	if count == 0 {
		return 1e6
	}

	var sumSq float64
	for _, p := range points {
		if !p.OK {
			continue
		}
		dx := p.X - cx
		dy := p.Y - cy
		sumSq += dx*dx + dy*dy
	}

	return math.Sqrt(sumSq / float64(count))
}

func BuildPath(surfaces []types.Surface) []int {
	path := []int{0}
	for _, s := range surfaces {
		if s.ID > 0 {
			path = append(path, s.ID)
		}
	}
	return path
}

func SurfaceIndex(surfaces []types.Surface, id int) int {
	for i, s := range surfaces {
		if s.ID == id {
			return i
		}
	}
	return -1
}
