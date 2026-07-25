package chief

import (
	"fmt"
	"math"
	"os"
	"runtime"
	"sync"

	"github.com/hiroki/rayweaver/internal/glass"
	"github.com/hiroki/rayweaver/internal/ray"
	"github.com/hiroki/rayweaver/internal/types"
)

type Result struct {
	FieldAngle      float64
	FieldHeight     float64
	FieldDir        types.Vec3
	ChiefRay        types.Ray
	ImageHeight     types.Vec3
	EntrancePupil   types.Pupil
	GridPoints      []types.GridPoint
	SpotStats       *types.SpotStats
	PerSurfaceMaxY  []float64
}

func DetermineChiefRays(
	system types.System,
	fields []types.FieldDef,
	refSurfaceID int,
	numRays int,
	gc *glass.Catalog,
	pol types.JonesVector,
	wavelength float64,
	dumpMap bool,
) []Result {
	return determineChiefRays(system, fields, refSurfaceID, numRays, gc, pol, wavelength, dumpMap, types.GridPolar, nil)
}

func DetermineChiefRaysGrid(
	system types.System,
	fields []types.FieldDef,
	refSurfaceID int,
	numRays int,
	gc *glass.Catalog,
	pol types.JonesVector,
	wavelength float64,
	dumpMap bool,
	gridType types.GridType,
	passThrough *types.PassThroughTarget,
) []Result {
	if gridType == "" {
		gridType = types.GridPolar
	}
	return determineChiefRays(system, fields, refSurfaceID, numRays, gc, pol, wavelength, dumpMap, gridType, passThrough)
}

func determineChiefRays(
	system types.System,
	fields []types.FieldDef,
	refSurfaceID int,
	numRays int,
	gc *glass.Catalog,
	pol types.JonesVector,
	wavelength float64,
	dumpMap bool,
	gridType types.GridType,
	passThrough *types.PassThroughTarget,
) []Result {
	engine := ray.NewEngine(gc, nil)

	apertureRadius := findMinApertureRadius(system.Surfaces)
	if apertureRadius <= 0 {
		return nil
	}

	var results []Result

	for _, fd := range fields {
		dx, dy := 1.0, 0.0
		if len(fd.Direction) >= 2 {
			norm := math.Hypot(fd.Direction[0], fd.Direction[1])
			if norm > 0 {
				dx = fd.Direction[0] / norm
				dy = fd.Direction[1] / norm
			}
		}

		var result Result
		switch {
		case math.Abs(fd.ImageHeight) > 1e-12:
			if passThrough != nil && passThrough.Surface > 0 {
				fmt.Fprintf(os.Stderr, "Warning: pass_through is not supported with image_height fields; ignoring pass_through\n")
			}
			angle := searchAngleForImageHeight(system, engine, fd.ImageHeight,
				dx, dy, refSurfaceID, numRays, apertureRadius, pol, wavelength, gridType)
			thetaRad := angle * math.Pi / 180.0
			if passThrough != nil && passThrough.Surface > 0 {
				result = computeChiefRayAngleGridWithPassThrough(system, engine, thetaRad, dx, dy,
					refSurfaceID, numRays, apertureRadius, pol, wavelength, dumpMap, gridType, passThrough)
			} else {
				result = computeChiefRayAngleGrid(system, engine, thetaRad, dx, dy,
					refSurfaceID, numRays, apertureRadius, pol, wavelength, dumpMap, gridType)
			}
			result.FieldAngle = angle

		case math.Abs(fd.Height) > 1e-12:
			objectZ := fd.ObjectZ
			if objectZ == 0 {
				objectZ = -1000.0
			}
			if passThrough != nil && passThrough.Surface > 0 {
				result = computeChiefRayHeightGridWithPassThrough(system, engine, fd.Height, dx, dy,
					objectZ, refSurfaceID, numRays, apertureRadius, pol, wavelength, dumpMap, gridType, passThrough)
			} else {
				result = computeChiefRayHeightGrid(system, engine, fd.Height, dx, dy,
					objectZ, refSurfaceID, numRays, apertureRadius, pol, wavelength, dumpMap, gridType)
			}
			result.FieldHeight = fd.Height

		default:
			thetaRad := fd.Angle * math.Pi / 180.0
			if passThrough != nil && passThrough.Surface > 0 {
				result = computeChiefRayAngleGridWithPassThrough(system, engine, thetaRad, dx, dy,
					refSurfaceID, numRays, apertureRadius, pol, wavelength, dumpMap, gridType, passThrough)
			} else {
				result = computeChiefRayAngleGrid(system, engine, thetaRad, dx, dy,
					refSurfaceID, numRays, apertureRadius, pol, wavelength, dumpMap, gridType)
			}
			result.FieldAngle = fd.Angle
		}
		result.FieldDir = types.Vec3{X: dx, Y: dy}
		results = append(results, result)
	}

	if len(results) >= 2 {
		inferStopPosition(results)
	}

	return results
}

// --- angle-based (infinite conjugate) ---

func computeChiefRayAngle(
	system types.System,
	engine *ray.Engine,
	thetaRad, dx, dy float64,
	refSurfaceID int,
	numRays int,
	apertureRadius float64,
	pol types.JonesVector,
	wavelength float64,
	dumpMap bool,
) Result {
	return computeChiefRayAngleGrid(system, engine, thetaRad, dx, dy, refSurfaceID, numRays, apertureRadius, pol, wavelength, dumpMap, types.GridPolar)
}

func computeChiefRayAngleGrid(
	system types.System,
	engine *ray.Engine,
	thetaRad, dx, dy float64,
	refSurfaceID int,
	numRays int,
	apertureRadius float64,
	pol types.JonesVector,
	wavelength float64,
	dumpMap bool,
	gridType types.GridType,
) Result {
	zStart := -100.0

	sinT := math.Sin(thetaRad)
	cosT := math.Cos(thetaRad)
	rayDir := types.Vec3{
		X: sinT * dx,
		Y: sinT * dy,
		Z: cosT,
	}.Normalize()

	stopZ := computeStopZ(system.Surfaces)
	tanT := sinT / cosT
	pupilCenterX := -(stopZ - zStart) * tanT * dx
	pupilCenterY := -(stopZ - zStart) * tanT * dy

	cx, cy, grid := tracePupilGrid(system, engine, numRays, apertureRadius,
		pupilCenterX, pupilCenterY, zStart, rayDir, types.Vec3{},
		refSurfaceID, pol, wavelength, dumpMap, gridType)

	originY := searchOriginForTarget(rayDir.Y, rayDir, zStart, refSurfaceID, cy,
		buildPath(system.Surfaces), wavelength, pol, engine, system.Surfaces, false,
		func(sr types.SurfaceResult) float64 { return sr.Position.Y })
	originX := 0.0
	if math.Abs(rayDir.X) > 1e-12 {
		originX = searchOriginForTarget(rayDir.X, rayDir, zStart, refSurfaceID, cx,
			buildPath(system.Surfaces), wavelength, pol, engine, system.Surfaces, true,
			func(sr types.SurfaceResult) float64 { return sr.Position.X })
	}

	origin := types.Vec3{X: originX, Y: originY, Z: zStart}
	return buildResult(engine, system, origin, rayDir, refSurfaceID, pol, wavelength, cx, cy, apertureRadius, grid, dumpMap)
}

// --- pass-through constrained chief ray (angle-based) ---

func computeChiefRayAngleGridWithPassThrough(
	system types.System,
	engine *ray.Engine,
	thetaRad, dx, dy float64,
	refSurfaceID int,
	numRays int,
	apertureRadius float64,
	pol types.JonesVector,
	wavelength float64,
	dumpMap bool,
	gridType types.GridType,
	pt *types.PassThroughTarget,
) Result {
	zStart := -100.0

	sinT := math.Sin(thetaRad)
	cosT := math.Cos(thetaRad)
	rayDir := types.Vec3{
		X: sinT * dx,
		Y: sinT * dy,
		Z: cosT,
	}.Normalize()

	path := buildPath(system.Surfaces)
	ptCoord := pt.Coordinate

	originY := searchOriginForTarget(rayDir.Y, rayDir, zStart, pt.Surface, ptCoord.Y,
		path, wavelength, pol, engine, system.Surfaces, false,
		func(sr types.SurfaceResult) float64 { return sr.Position.Y })

	originX := 0.0
	if math.Abs(rayDir.X) > 1e-12 || math.Abs(ptCoord.X) > 1e-12 {
		originX = searchOriginForTarget(rayDir.X, rayDir, zStart, pt.Surface, ptCoord.X,
			path, wavelength, pol, engine, system.Surfaces, true,
			func(sr types.SurfaceResult) float64 { return sr.Position.X })
	}

	origin := types.Vec3{X: originX, Y: originY, Z: zStart}

	stopZ := computeStopZ(system.Surfaces)
	t := (stopZ - origin.Z) / rayDir.Z
	pupilCenterX := origin.X + t*rayDir.X
	pupilCenterY := origin.Y + t*rayDir.Y

	cx, cy, grid := tracePupilGrid(system, engine, numRays, apertureRadius,
		pupilCenterX, pupilCenterY, zStart, rayDir, types.Vec3{},
		refSurfaceID, pol, wavelength, dumpMap, gridType)

	return buildResult(engine, system, origin, rayDir, refSurfaceID,
		pol, wavelength, cx, cy, apertureRadius, grid, dumpMap)
}

// --- pass-through constrained chief ray (height-based) ---

func computeChiefRayHeightGridWithPassThrough(
	system types.System,
	engine *ray.Engine,
	height, dx, dy, objectZ float64,
	refSurfaceID int,
	numRays int,
	apertureRadius float64,
	pol types.JonesVector,
	wavelength float64,
	dumpMap bool,
	gridType types.GridType,
	pt *types.PassThroughTarget,
) Result {
	objectPoint := types.Vec3{X: height * dx, Y: height * dy, Z: objectZ}
	path := buildPath(system.Surfaces)
	zStart := objectZ + (0.0-objectZ)*0.5
	ptCoord := pt.Coordinate

	targetZ := computeTargetZ(system.Surfaces, pt.Surface)
	baseDir := types.Vec3{
		X: ptCoord.X - objectPoint.X,
		Y: ptCoord.Y - objectPoint.Y,
		Z: targetZ - objectPoint.Z,
	}.Normalize()

	refinedDir := searchDirectionForTarget(objectPoint, pt.Surface, ptCoord.Y, baseDir,
		path, wavelength, pol, engine, system.Surfaces,
		func(sr types.SurfaceResult) float64 { return sr.Position.Y })

	if math.Abs(ptCoord.X) > 1e-12 {
		refinedDir = searchDirectionForTarget(objectPoint, pt.Surface, ptCoord.X, refinedDir,
			path, wavelength, pol, engine, system.Surfaces,
			func(sr types.SurfaceResult) float64 { return sr.Position.X })
	}

	cx, cy, grid := tracePupilGrid(system, engine, numRays, apertureRadius,
		0, 0, zStart, refinedDir, objectPoint,
		refSurfaceID, pol, wavelength, dumpMap, gridType)

	return buildResult(engine, system, objectPoint, refinedDir, refSurfaceID,
		pol, wavelength, cx, cy, apertureRadius, grid, dumpMap)
}

// --- height-based (finite conjugate) ---

func computeChiefRayHeight(
	system types.System,
	engine *ray.Engine,
	height, dx, dy, objectZ float64,
	refSurfaceID int,
	numRays int,
	apertureRadius float64,
	pol types.JonesVector,
	wavelength float64,
	dumpMap bool,
) Result {
	return computeChiefRayHeightGrid(system, engine, height, dx, dy, objectZ, refSurfaceID, numRays, apertureRadius, pol, wavelength, dumpMap, types.GridPolar)
}

func computeChiefRayHeightGrid(
	system types.System,
	engine *ray.Engine,
	height, dx, dy, objectZ float64,
	refSurfaceID int,
	numRays int,
	apertureRadius float64,
	pol types.JonesVector,
	wavelength float64,
	dumpMap bool,
	gridType types.GridType,
) Result {
	objectPoint := types.Vec3{X: height * dx, Y: height * dy, Z: objectZ}
	path := buildPath(system.Surfaces)

	// Estimate pupil plane between object and first surface.
	zStart := objectZ + (0.0-objectZ)*0.5

	// Initial direction estimate: from object toward the optical axis at the pupil plane.
	baseDir := types.Vec3{
		X: -objectPoint.X,
		Y: -objectPoint.Y,
		Z: zStart - objectPoint.Z,
	}.Normalize()

	// Sample grid and get centroid.
	cx, cy, grid := tracePupilGrid(system, engine, numRays, apertureRadius,
		0, 0, zStart, baseDir, objectPoint,
		refSurfaceID, pol, wavelength, dumpMap, gridType)

	// Refine the direction so the chief ray passes through the centroid at reference surface.
	refinedDir := searchDirectionForTarget(objectPoint, refSurfaceID, cy, baseDir,
		path, wavelength, pol, engine, system.Surfaces,
		func(sr types.SurfaceResult) float64 { return sr.Position.Y })

	return buildResult(engine, system, objectPoint, refinedDir, refSurfaceID,
		pol, wavelength, cx, cy, apertureRadius, grid, dumpMap)
}

// --- binary search helpers ---

// searchOriginForTarget finds the initial origin component (X or Y) on the
// zStart plane such that the ray passes through targetVal at the target
// surface after refraction.
func searchOriginForTarget(
	dirComp float64,
	rayDir types.Vec3,
	zStart float64, targetID int, targetVal float64, path []int,
	wavelength float64, pol types.JonesVector,
	engine *ray.Engine, surfaces []types.Surface,
	isX bool, getPos func(types.SurfaceResult) float64,
) float64 {
	if math.Abs(dirComp) < 1e-12 {
		return 0
	}

	stopZ := computeStopZ(surfaces)
	tanComp := dirComp / math.Sqrt(1-dirComp*dirComp)
	geoEst := -(stopZ - zStart) * tanComp

	trace := func(originComp float64) (float64, bool) {
		orig := types.Vec3{X: 0, Y: 0, Z: zStart}
		if isX {
			orig.X = originComp
		} else {
			orig.Y = originComp
		}
		r := types.Ray{
			Wavelength: wavelength,
			Initial:    types.RayState{Origin: orig, Direction: rayDir},
			Path:       path,
			Jones:      pol,
		}
		res := engine.TraceRay(r, surfaces)
		for _, sr := range res.Surfaces {
			if sr.SurfaceID == targetID {
				return getPos(sr), true
			}
		}
		return 0, false
	}

	v0, ok := trace(geoEst)
	if !ok || math.Abs(v0-targetVal) < 1e-12 {
		return geoEst
	}

	lo, hi := geoEst-1.0, geoEst+1.0
	vLo, okLo := trace(lo)
	vHi, okHi := trace(hi)

	for iter := 0; iter < 40; iter++ {
		if okLo && okHi && (vLo-targetVal)*(vHi-targetVal) <= 0 {
			break
		}
		if !okLo || ((vLo > targetVal) == (v0 > targetVal)) {
			lo -= 2.0
			vLo, okLo = trace(lo)
		} else {
			hi += 2.0
			vHi, okHi = trace(hi)
		}
		if math.Abs(lo)+math.Abs(hi) > 200 {
			return geoEst
		}
	}

	if !okLo || !okHi || (vLo-targetVal)*(vHi-targetVal) > 0 {
		return geoEst
	}

	for iter := 0; iter < 50; iter++ {
		mid := (lo + hi) / 2
		vMid, ok := trace(mid)
		if !ok {
			if (mid-lo) > (hi-mid) {
				lo = mid
			} else {
				hi = mid
			}
			continue
		}
		if math.Abs(vMid-targetVal) < 1e-12 {
			return mid
		}
		if (vMid-targetVal)*(vLo-targetVal) >= 0 {
			lo = mid
			vLo = vMid
		} else {
			hi = mid
		}
	}
	return (lo + hi) / 2
}

// searchDirectionForTarget varies the tangent component of the ray direction
// so that the ray passes through targetVal at the target surface.
func searchDirectionForTarget(
	objectPoint types.Vec3, targetID int, targetVal float64,
	baseDir types.Vec3, path []int,
	wavelength float64, pol types.JonesVector,
	engine *ray.Engine, surfaces []types.Surface,
	getPos func(types.SurfaceResult) float64,
) types.Vec3 {
	tanComp := baseDir.Y / baseDir.Z
	useX := math.Abs(baseDir.X) > math.Abs(baseDir.Y)
	if useX {
		tanComp = baseDir.X / baseDir.Z
	} else if math.Abs(baseDir.Y) < 1e-12 {
		return baseDir
	}

	trace := func(tan float64) (float64, bool) {
		dz := 1.0 / math.Sqrt(1+tan*tan)
		dx, dy := 0.0, 0.0
		if useX {
			dx = tan * dz
		} else {
			dy = tan * dz
		}
		dir := types.Vec3{X: dx, Y: dy, Z: dz}.Normalize()
		r := types.Ray{
			Wavelength: wavelength,
			Initial:    types.RayState{Origin: objectPoint, Direction: dir},
			Path:       path,
			Jones:      pol,
		}
		res := engine.TraceRay(r, surfaces)
		for _, sr := range res.Surfaces {
			if sr.SurfaceID == targetID {
				return getPos(sr), true
			}
		}
		return 0, false
	}

	v0, ok := trace(tanComp)
	if !ok || math.Abs(v0-targetVal) < 1e-12 {
		return baseDir
	}

	// Lever arm for delta estimate: distance from object to target surface
	delta := math.Abs(v0-targetVal) / (computeTargetZ(surfaces, targetID) - objectPoint.Z)
	if delta < 1e-6 {
		delta = 1e-6
	}
	lo, hi := tanComp-delta*100, tanComp+delta*100
	vLo, okLo := trace(lo)
	vHi, okHi := trace(hi)

	for iter := 0; iter < 40; iter++ {
		if okLo && okHi && (vLo-targetVal)*(vHi-targetVal) <= 0 {
			break
		}
		if !okLo || ((vLo > targetVal) == (v0 > targetVal)) {
			lo -= delta * 100
			vLo, okLo = trace(lo)
		} else {
			hi += delta * 100
			vHi, okHi = trace(hi)
		}
		if math.Abs(tanComp-lo)+math.Abs(tanComp-hi) > math.Abs(tanComp)*2+0.1 {
			return baseDir
		}
	}

	if !okLo || !okHi || (vLo-targetVal)*(vHi-targetVal) > 0 {
		return baseDir
	}

	for iter := 0; iter < 50; iter++ {
		mid := (lo + hi) / 2
		vMid, ok := trace(mid)
		if !ok {
			if (mid-lo) > (hi-mid) {
				lo = mid
			} else {
				hi = mid
			}
			continue
		}
		if math.Abs(vMid-targetVal) < 1e-12 {
			dz := 1.0 / math.Sqrt(1+mid*mid)
			dx, dy := 0.0, 0.0
			if useX {
				dx = mid * dz
			} else {
				dy = mid * dz
			}
			return types.Vec3{X: dx, Y: dy, Z: dz}.Normalize()
		}
		if (vMid-targetVal)*(vLo-targetVal) >= 0 {
			lo = mid
			vLo = vMid
		} else {
			hi = mid
		}
	}
	mid := (lo + hi) / 2
	dz := 1.0 / math.Sqrt(1+mid*mid)
	dx, dy := 0.0, 0.0
	if useX {
		dx = mid * dz
	} else {
		dy = mid * dz
	}
	return types.Vec3{X: dx, Y: dy, Z: dz}.Normalize()
}

// --- shared helpers ---

// generateGridPoints returns a list of pupil (px, py) sample coordinates
// for the given grid type and aperture radius.
func generateGridPoints(numRays int, apertureRadius float64, gridType types.GridType) []struct{ X, Y float64 } {
	var pts []struct{ X, Y float64 }

	switch gridType {
	case types.GridSquare:
		n := int(math.Sqrt(float64(numRays)))
		if n < 2 {
			n = 2
		}
		for i := 0; i < n; i++ {
			for j := 0; j < n; j++ {
				x := (float64(i)+0.5)/float64(n)*2 - 1
				y := (float64(j)+0.5)/float64(n)*2 - 1
				if x*x+y*y <= 1 {
					pts = append(pts, struct{ X, Y float64 }{
						X: x * apertureRadius,
						Y: y * apertureRadius,
					})
				}
			}
		}

	case types.GridHex:
		n := int(math.Sqrt(float64(numRays))) + 1
		if n < 2 {
			n = 2
		}
		dy := apertureRadius * 2 / float64(n)
		dx := dy * math.Sqrt(3) / 2
		for i := 0; i < n; i++ {
			y := -apertureRadius + (float64(i)+0.5)*dy
			xOff := 0.0
			if i%2 == 1 {
				xOff = dx / 2
			}
			nx := int(float64(n) * apertureRadius / (dx * float64(n) / 2))
			if nx < 1 {
				nx = 1
			}
			for j := 0; j < nx; j++ {
				x := -apertureRadius + (float64(j)+0.5)*dx + xOff
				if x*x+y*y <= apertureRadius*apertureRadius {
					pts = append(pts, struct{ X, Y float64 }{
						X: x,
						Y: y,
					})
				}
			}
		}

	default: // GridPolar
		n := int(math.Sqrt(float64(numRays)))
		if n < 2 {
			n = 2
		}
		for i := 0; i < n; i++ {
			for j := 0; j < n; j++ {
				r := (float64(i) + 0.5) / float64(n) * apertureRadius
				theta := 2 * math.Pi * (float64(j) + 0.5) / float64(n)
				pts = append(pts, struct{ X, Y float64 }{
					X: r * math.Cos(theta),
					Y: r * math.Sin(theta),
				})
			}
		}
	}

	return pts
}

// tracePupilGrid distributes numRays samples in the given grid pattern
// centred on (pupilCenterX, pupilCenterY) at plane zStart.
func tracePupilGrid(
	system types.System,
	engine *ray.Engine,
	numRays int,
	apertureRadius float64,
	pupilCenterX, pupilCenterY, zStart float64,
	rayDir, rayOrigin types.Vec3,
	refSurfaceID int,
	pol types.JonesVector,
	wavelength float64,
	dumpMap bool,
	gridType types.GridType,
) (cx, cy float64, grid []types.GridPoint) {
	var totalWeight float64
	var weightedX, weightedY float64

	path := buildPath(system.Surfaces)
	isHeightBased := rayOrigin.Z != 0 || rayOrigin.X != 0 || rayOrigin.Y != 0

	var mu sync.Mutex
	var wg sync.WaitGroup
	sem := make(chan struct{}, runtime.NumCPU())

	samples := generateGridPoints(numRays, apertureRadius, gridType)
	for i := range samples {
		px := pupilCenterX + samples[i].X
		py := pupilCenterY + samples[i].Y

		wg.Add(1)
		sem <- struct{}{}
		go func(px, py float64) {
			defer wg.Done()
			defer func() { <-sem }()

			var rDir types.Vec3
			var rOrg types.Vec3
			if isHeightBased {
				rOrg = rayOrigin
				rDir = types.Vec3{
					X: px - rayOrigin.X,
					Y: py - rayOrigin.Y,
					Z: zStart - rayOrigin.Z,
				}.Normalize()
			} else {
				rOrg = types.Vec3{X: px, Y: py, Z: zStart}
				rDir = rayDir
			}

			ray := types.Ray{
				Wavelength: wavelength,
				Initial:    types.RayState{Origin: rOrg, Direction: rDir},
				Path:       path,
				Jones:      pol,
			}

			traceResult := engine.TraceRay(ray, system.Surfaces)
			if traceResult.Error != "" {
				mu.Lock()
				grid = append(grid, types.GridPoint{
					PupilX: px, PupilY: py,
					ImageX: nil, ImageY: nil, Intensity: 0,
					Origin: rOrg, Direction: rDir,
				})
				mu.Unlock()
				return
			}

			for _, sr := range traceResult.Surfaces {
				if sr.SurfaceID == refSurfaceID {
					weight := (sr.IntensityS + sr.IntensityP) / 2.0

					mu.Lock()
					totalWeight += weight
					weightedX += sr.Position.X * weight
					weightedY += sr.Position.Y * weight
					ix, iy := sr.Position.X, sr.Position.Y
					grid = append(grid, types.GridPoint{
						PupilX: px, PupilY: py,
						ImageX: &ix, ImageY: &iy, Intensity: weight,
						Origin: rOrg, Direction: rDir,
					})
					mu.Unlock()
				}
			}
		}(px, py)
	}

	wg.Wait()
	close(sem)

	if totalWeight > 0 {
		cx = weightedX / totalWeight
		cy = weightedY / totalWeight
	}
	return
}

func computeSpotStats(grid []types.GridPoint, cx, cy float64) *types.SpotStats {
	s := &types.SpotStats{
		Centroid: types.Vec3{X: cx, Y: cy},
		MinX:     1e18,
		MinY:     1e18,
		MaxX:     -1e18,
		MaxY:     -1e18,
	}
	var sumSqX, sumSqY float64

	for _, gp := range grid {
		s.TotalRays++
		if gp.ImageX == nil || gp.ImageY == nil {
			s.MissedRays++
			continue
		}
		s.TracedRays++
		dx := *gp.ImageX - cx
		dy := *gp.ImageY - cy
		sumSqX += dx * dx
		sumSqY += dy * dy

		if *gp.ImageX < s.MinX {
			s.MinX = *gp.ImageX
		}
		if *gp.ImageX > s.MaxX {
			s.MaxX = *gp.ImageX
		}
		if *gp.ImageY < s.MinY {
			s.MinY = *gp.ImageY
		}
		if *gp.ImageY > s.MaxY {
			s.MaxY = *gp.ImageY
		}
	}

	if s.TracedRays > 0 {
		n := float64(s.TracedRays)
		s.RMS_X = math.Sqrt(sumSqX / n)
		s.RMS_Y = math.Sqrt(sumSqY / n)
		s.RMS_R = math.Sqrt((sumSqX + sumSqY) / n)
	}
	return s
}

func buildResult(
	engine *ray.Engine, system types.System,
	origin, rayDir types.Vec3,
	refSurfaceID int,
	pol types.JonesVector,
	wavelength float64,
	cx, cy, apertureRadius float64,
	grid []types.GridPoint,
	dumpMap bool,
) Result {
	path := buildPath(system.Surfaces)
	chiefRay := types.Ray{
		Wavelength: wavelength,
		Initial:    types.RayState{Origin: origin, Direction: rayDir},
		Path:       path,
		Jones:      pol,
	}

	// Trace chief ray for actual image height
	if tr := engine.TraceRay(chiefRay, system.Surfaces); tr.Error == "" {
		for _, sr := range tr.Surfaces {
			if sr.SurfaceID == refSurfaceID {
				cx = sr.Position.X
				cy = sr.Position.Y
			}
		}
	}

	res := Result{
		ImageHeight:   types.Vec3{X: cx, Y: cy},
		EntrancePupil: types.Pupil{Radius: apertureRadius},
		ChiefRay:      chiefRay,
		GridPoints:    grid,
	}
	if len(grid) > 0 {
		res.SpotStats = computeSpotStats(grid, cx, cy)
	}
	return res
}

func computeStopZ(surfaces []types.Surface) float64 {
	stopID := 0
	minD := math.MaxFloat64
	for _, s := range surfaces {
		if s.Diameter > 0 && s.Diameter < minD {
			minD = s.Diameter
			stopID = s.ID
		}
	}
	if stopID == 0 {
		return 0
	}
	z := 0.0
	for _, s := range surfaces {
		if s.ID == stopID {
			return z
		}
		z += s.Thickness
	}
	return 0
}

func computeTargetZ(surfaces []types.Surface, targetID int) float64 {
	z := 0.0
	for _, s := range surfaces {
		if s.ID == targetID {
			return z
		}
		z += s.Thickness
	}
	return z
}

func findMinApertureRadius(surfaces []types.Surface) float64 {
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

func buildPath(surfaces []types.Surface) []int {
	path := []int{0}
	for _, s := range surfaces {
		if s.ID > 0 {
			path = append(path, s.ID)
		}
	}
	return path
}

// imageHeightForAngle returns the centroid Y at the reference surface for a
// given field angle (degrees).  Uses a full grid trace with the given numRays.
func imageHeightForAngle(
	system types.System,
	engine *ray.Engine,
	angleDeg, dx, dy float64,
	refSurfaceID int,
	numRays int,
	apertureRadius float64,
	pol types.JonesVector,
	wavelength float64,
	gridType types.GridType,
) (float64, bool) {
	thetaRad := angleDeg * math.Pi / 180.0
	sinT := math.Sin(thetaRad)
	cosT := math.Cos(thetaRad)
	zStart := -100.0
	rayDir := types.Vec3{X: sinT * dx, Y: sinT * dy, Z: cosT}.Normalize()

	stopZ := computeStopZ(system.Surfaces)
	tanT := sinT / cosT
	pupilCenterX := -(stopZ - zStart) * tanT * dx
	pupilCenterY := -(stopZ - zStart) * tanT * dy

	_, cy, _ := tracePupilGrid(system, engine, numRays, apertureRadius,
		pupilCenterX, pupilCenterY, zStart, rayDir, types.Vec3{},
		refSurfaceID, pol, wavelength, false, gridType)

	if math.Abs(cy) > 0 || numRays > 0 {
		return cy, true
	}
	return 0, false
}

// searchAngleForImageHeight finds the field angle (degrees) whose centroid
// Y at the reference surface equals the target Y.
func searchAngleForImageHeight(
	system types.System,
	engine *ray.Engine,
	targetY, dx, dy float64,
	refSurfaceID int,
	numRays int,
	apertureRadius float64,
	pol types.JonesVector,
	wavelength float64,
	gridType types.GridType,
) float64 {
	// Bracket search: start with 0–15° and expand as needed.
	loDeg, hiDeg := 0.0, 15.0
	yLo, okLo := imageHeightForAngle(system, engine, loDeg, dx, dy, refSurfaceID, numRays, apertureRadius, pol, wavelength, gridType)
	yHi, okHi := imageHeightForAngle(system, engine, hiDeg, dx, dy, refSurfaceID, numRays, apertureRadius, pol, wavelength, gridType)

	for iter := 0; iter < 25; iter++ {
		if okLo && okHi && yLo <= targetY && targetY <= yHi {
			break
		}
		if hiDeg > 70 {
			hiDeg = 70
			yHi, okHi = imageHeightForAngle(system, engine, hiDeg, dx, dy, refSurfaceID, numRays, apertureRadius, pol, wavelength, gridType)
		}
		if !okLo || yLo > targetY {
			loDeg -= 3.0
			if loDeg < -80 {
				return 0
			}
			yLo, okLo = imageHeightForAngle(system, engine, loDeg, dx, dy, refSurfaceID, numRays, apertureRadius, pol, wavelength, gridType)
		} else if !okHi || yHi < targetY {
			hiDeg += 3.0
			if hiDeg > 80 {
				return 0
			}
			yHi, okHi = imageHeightForAngle(system, engine, hiDeg, dx, dy, refSurfaceID, numRays, apertureRadius, pol, wavelength, gridType)
		} else {
			break
		}
	}

	if !okLo || !okHi || yLo > targetY || yHi < targetY {
		return 0
	}

	for iter := 0; iter < 40; iter++ {
		midDeg := (loDeg + hiDeg) / 2
		yMid, ok := imageHeightForAngle(system, engine, midDeg, dx, dy, refSurfaceID, numRays, apertureRadius, pol, wavelength, gridType)
		if !ok {
			if math.Abs(midDeg-loDeg) > math.Abs(hiDeg-midDeg) {
				loDeg = midDeg
			} else {
				hiDeg = midDeg
			}
			continue
		}
		if math.Abs(yMid-targetY) < 1e-12 {
			return midDeg
		}
		if yMid < targetY {
			loDeg = midDeg
			yLo = yMid
		} else {
			hiDeg = midDeg
			yHi = yMid
		}
		if math.Abs(hiDeg-loDeg) < 1e-12 {
			break
		}
	}
	return (loDeg + hiDeg) / 2
}

func inferStopPosition(results []Result) {
	if len(results) < 2 {
		return
	}

	r0 := results[0].ChiefRay
	r1 := results[1].ChiefRay

	d0 := r0.Initial.Direction
	d1 := r1.Initial.Direction
	p0 := r0.Initial.Origin
	p1 := r1.Initial.Origin

	cross := d0.Cross(d1)
	if cross.Length() < 1e-12 {
		return
	}

	diff := p1.Subtract(p0)
	t := diff.Cross(d1).Dot(cross) / cross.Dot(cross)
	intersection := p0.Add(d0.Scale(t))

	for i := range results {
		results[i].EntrancePupil.Center = intersection
	}
}
