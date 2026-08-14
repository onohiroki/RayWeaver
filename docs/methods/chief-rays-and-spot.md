# Chief rays, pupil grids and spot statistics

This document describes how `rayweave chief` samples a beam, finds the chief
ray, and computes the statistics that feed spot diagrams, ray fans, clear
apertures and — via the optimizer — the merit function.

## 1. Aperture and entrance pupil

The aperture stop is the explicit `chief.stop_surface` if given, otherwise the
surface with the smallest fixed (non-auto) diameter. The **entrance pupil
radius** for sampling is the stop radius when a stop is known, else the minimum
aperture radius over all surfaces. When two or more fields are traced, the
entrance pupil centre is inferred from the intersection of the chief rays
(`inferStopPosition`): the two chief rays' initial directions and origins are
used to find their common intersection point.

## 2. Field definitions

A field is one of:

- **angle** — infinite conjugate: the ray bundle enters the pupil with
  direction `(sin θ · dx, sin θ · dy, cos θ)`.
- **image_height** — the field angle is solved (bisection on a full grid trace)
  so that the projected image height `dx·cx + dy·cy` on the reference surface
  equals the target.
- **height + object_z** — finite conjugate: the object point is
  `(h·dx, h·dy, z₀)` and the pupil plane is taken halfway between the object
  and the first surface.

## 3. Pupil grids

The pupil is sampled with `num_rays` points in one of three patterns
(`GenerateGridPoints`):

| Grid | Layout |
|---|---|
| `polar` (default) | √n radial rings × √n azimuthal angles, `rᵢ = (i+0.5)/n · R` |
| `square` | √n × √n cells, kept if `x² + y² ≤ 1` |
| `hex` | hexagonally packed rows inside the aperture |

For angle fields the grid is centred on the **pupil centre**, the point on the
start plane `z = z_start` whose ray (parallel to the field direction) crosses
the stop. It is computed vectorially (`raymath.WavefrontGridCenter`) from the
entrance-pupil centre `C = (0,0,z_stop)` as
`(px, py) = C − (C.z − z_start)·rayDir.XY / rayDir.Z` — the classic
`−(z_stop − z_start)·tan θ·(dx, dy)` offset expressed with direction-vector
ratios, so it stays finite up to 90° incidence (at grazing angles the grid
falls back to the wavefront plane through the pupil centre). Rays are launched
from the **wavefront plane** perpendicular to the ray direction through the grid
centre (`raymath.ProjectOntoWavefront`), so their OPL is referenced to a common
wavefront and carries no launch-geometry tilt. For finite conjugate fields the
grid is centred on the object-projected pupil.

Each pupil point becomes a ray. Rays are traced in parallel; rays that miss or
are vignetted are recorded with a nil image and `error_code`, so vignetting is
visible in the statistics.

## 4. Chief ray and spot centroid

The **spot centroid** is the intensity-weighted mean of the grid-ray image
positions on the reference surface:

```
cₓ = Σ wᵢ xᵢ / Σ wᵢ ,   wᵢ = (I_s + I_p)/2
```

The chief ray is defined in one of two ways:

- **Centroid definition (default):** the ray from the field that passes through
  the centroid. Its origin is found by a one-dimensional root solve
  (`searchOriginForTarget`, bracketing + bisection on the traced image height)
  so that the ray hits the centroid coordinates on the reference surface.
- **Pass-through definition:** the ray that passes through a given coordinate
  on a given surface (`--pass-through N` or YAML `pass_through`). The origin
  (angle case) or direction (height case) is solved the same way.

The chief ray is then traced once more for its exact image height.

## 5. Spot statistics

For each field, `computeSpotStats` accumulates, relative to the centroid:

```
RMS_X² = Σ(xᵢ − cₓ)² / N ,  RMS_Y² = Σ(yᵢ − c_y)² / N
RMS_R² = RMS_X² + RMS_Y²     (RMS radial spot size)
```

plus min/max extents, `traced_rays` (successful) and `missed_rays`. When a
config defines multiple wavelengths, the same grid origins/directions are
re-traced at each wavelength and per-wavelength stats are produced (this is how
the optimizer evaluates polychromatic spot RMS).

## 6. Marginal rays

`--marginal-rays` inspects each field's grid points and returns the rays with
the maximum and minimum image Y (and X for fields with an X-direction
component). These are appended to the output `rays` section so ray-fan-like
rays appear in diagrams.

## 7. Ray fans

A ray fan scans the pupil along a line through the pupil centre at a given
rotation angle (0° = XZ sagittal, 90° = YZ meridional), typically 256 samples
from −R to +R. For each sample the **transverse aberration** relative to the
chief-ray image is computed:

```
EX = x − x_chief ,  EY = y − y_chief
```

and a **longitudinal aberration** is derived from the local ray slope: the Z
distance from the reference surface to where the lateral offset along the scan
axis crosses zero. The full per-surface path of every fan ray is retained, so
the fan output can be re-traced or drawn.

**Vignetted fan rays are dropped.** Fan rays are traced leniently (aperture
clipping / missed surfaces / total internal reflection are recorded as per-surface
`error_code` values rather than a trace-level error), so a fan point whose path
carries `aperture_stop`, `missed_surface`, or `total_internal_reflection` is
excluded from the fan — a vignetted ray has no meaningful transverse aberration.
The fan thus reflects the true clear-aperture pupil; off-axis fields with
vignetting report fewer points than the requested `num_rays`.

## 8. Clear aperture (beam footprint)

`--clear-aperture` re-traces the grid rays (a denser grid with
`--clear-aperture-rays`) through every surface and records the maximum `|X|` or
`|Y|` at each surface. Each surface's `diameter` is set to `2 × max(|X|,|Y|)`:

- default: only **grow** diameters (never shrink the aperture stop or the
  reference surface);
- with `--shrink`: also shrink diameters down to the footprint, adding
  `--clear-aperture-margin-mm` clearance on each side; the stop keeps its
  diameter.

`--preserve-rays` keeps the user's existing `rays` section (aperture adjustment
only, chief rays omitted from the output).

## Parallelism

Grid rays are traced concurrently (a semaphore-limited worker pool); the
centroid accumulation is protected by a mutex, so results are deterministic
for any concurrency.
