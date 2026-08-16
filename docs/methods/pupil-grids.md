# Pupil grids and grid-ray tracing

This document describes `internal/pupil`, the shared entrance-pupil grid
generation and ray-trace path used by `chief` (spot statistics, dynamic pupil,
clear aperture), the optimizer (`dls`, i.e. `optimize`/`escape` merit grids),
`wavefront` (frozen pupil grids) and `asphere` (candidate footprints). Before
this package existed, each of those paths carried its own grid-generation,
launch and trace loop; they now all go through the same `Launch`/`Trace` pair,
so the grid layout, launch geometry and OPL normalization stay consistent
across every consumer.

## 1. API

`pupil.Launch(spec)` distributes the grid and builds the per-ray launch states;
it never traces. `pupil.Trace(engine, path, surfaces, samples, wavelength, pol,
workers)` traces the samples in parallel and writes the results back into the
slice. `pupil.GridCentre(rayDir, pupilZ, zStart)` returns the grid centre on the
launch plane whose ray (parallel to `rayDir`) passes through the entrance-pupil
centre `(0, 0, pupilZ)`.

```go
type LaunchSpec struct {
    NumRays           int
    GridType          types.GridType // polar | square | hex
    RotationOffset    float64
    ApertureRadius    float64
    RayDir            types.Vec3
    CentreX, CentreY  float64        // grid centre on the zStart plane
    ZStart            float64
    Vig               *types.VignettingDef // nil = no vignetting clip
    OPLMode           OPLMode        // OPLLaunch | OPLScalar
    SkipApertureCheck bool
    SkipGlassPath     bool
    HeightOrigin      *types.Vec3    // finite-conjugate bundle origin
}
```

`Sample` carries the launch state (`PupilX/PupilY` relative grid offsets,
`Area` pupil-cell weight, `Origin`, `Dir`, `OPLDelta`) and, once traced,
`OK`/`Err`/`ErrorCode`, `OPL`, `Intensity` (mean transmitted s/p intensity at
the last surface) and `Surfaces` (every `types.SurfaceResult` along the path).

## 2. Grid generation and centring

The grid points come from `raymath.PupilGrid(numRays, radius, gridType,
rotationOffset)` — `polar` (default, √n rings × √n angles), `square` (cells kept
inside the aperture) or `hex`. Each grid point is offset by the grid centre
(`CentreX`/`CentreY`), which for angle fields is the wavefront-plane point that
crosses the entrance pupil, computed vectorially (`raymath.WavefrontGridCenter`,
no `tanθ`, finite up to 90° incidence). `Sample.PupilX/PupilY` are the **relative**
offsets (consistent with `types.GridPoint` semantics); the absolute launch
offsets are `Centre + PupilX/Y`.

## 3. Launch modes

Two ways to launch a parallel angle-field bundle, selected by `OPLMode`; both
remove the launch-geometry OPL tilt (the linear `Δpx·sinθ` ramp that would
otherwise pollute off-axis OPD), differing only in how the ray positions are
kept:

- **`OPLLaunch`** (chief, wavefront, asphere): each origin is projected along
  `rayDir` onto the wavefront plane through the grid centre
  (`raymath.ProjectOntoWavefront`), so the recorded OPLTotal already carries no
  tilt and `OPLDelta` is 0. Moving the origin along the ray leaves the ray line
  (and every surface intersection) unchanged.
- **`OPLScalar`** (dls): each origin stays on the `zStart` plane and
  `OPLDelta = (wavefrontC − origin)·RayDir` is subtracted from OPLTotal. The
  traced ray positions are then bit-identical to a plain launch, which keeps the
  DLS merit and Jacobian free of ~1e-15 floating-point noise from a moved
  origin, while the OPD-based merit terms still see the corrected OPL.
- **`HeightOrigin`** (finite-conjugate `height`+`object_z` fields): every
  sample origin is the object point and the direction points from there through
  the (centred) grid sample; no OPL delta is applied.

The vignetting clip (`Vig`, a ZEMAX-style `types.VignettingDef`) is applied
during `Launch`: samples outside the entrance-pupil ellipse are dropped before
tracing.

## 4. OPL normalization

After tracing, `Sample.OPL = OPLTotal − OPLDelta`. Under `OPLLaunch` and
`HeightOrigin` the delta is 0, so `OPL` is the raw OPLTotal; under `OPLScalar`
it is the launch-tilt-corrected value. Consumers that need the raw total (e.g.
asphere's OPD analysis, which removes piston anyway) use the projected-launch
modes; consumers whose merit depends on the exact ray positions (spot RMS,
aperture extents) use `OPLScalar`.

## 5. Trace and determinism

`pupil.Trace` runs the samples over a semaphore-limited worker pool (default
`runtime.NumCPU()`) but writes each result back into its grid-ordered slot by
index, so the returned order — and any downstream accumulation, sorting or
triangulation — is **deterministic for any worker count** and independent of
trace completion order. (This replaces the previous chief implementation, which
appended grid points under a mutex in completion order; the mutex guarded the
accumulation but floating-point summation order was still nondeterministic.)

## 6. Callers

| Entry point | OPLMode | Vignetting clip | Skip flags |
|---|---|---|---|
| `chief.tracePupilGrid` | `OPLLaunch` / `HeightOrigin` | field `Vignetting` | — |
| `dls.traceGridRays` | `OPLScalar` | — | aperture / glass-path from the DLS flags |
| `wavefront.frozenPupilGrid` | `OPLLaunch` | field `Vignetting` | — |
| `asphere.GenerateFootprints` | `OPLLaunch` | — | — |

`chief.computeRayFan`/`marginalsAtStop` are one-dimensional scans and are not
part of this package: they still build their pupil samples and call
`raymath.ProjectOntoWavefront` directly.
