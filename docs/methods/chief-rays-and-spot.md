# Chief rays, pupil grids and spot statistics

This document describes how `rayweave chief` samples a beam, finds the chief
ray, and computes the statistics that feed spot diagrams, ray fans, clear
apertures and — via the optimizer — the merit function.

## 1. Aperture and entrance pupil

There is no implicit aperture stop: the stop is used only when
`chief.stop_surface` is set explicitly. With a stop, the **entrance-pupil
radius** for sampling is the **paraxial entrance-pupil radius** (the stop's
image), so the F-number is preserved and image-side fixed surfaces that
comfortably exceed the local beam do not shrink the pupil, and the reported
`entrance_pupil` centre sits on the **paraxial entrance-pupil plane**
(`paraxial.EntrancePupilLocation`) — the stop's image, not the stop itself.
That is the value the grid aim and `rayweave paraxial` agree on; reporting the
stop plane here was a bug (`chief | paraxial` used to disagree with a
standalone `paraxial` run). Without a stop the
pupil is **dynamic**: each field's entrance-pupil Z is the in-lens crossing of
that field's chief ray with field 0's chief ray (the aperture position), and
`chief` iterates this (≤ 3 passes) until the pupil settles. The grid radius is
then the **beam-aware fixed-aperture cap** (`fixedApertureAtPupil`): every
`auto_aperture: false` surface's aperture is projected back to the aperture
position along the paraxial marginal ray, so a surface only caps when its clear
aperture is smaller than the beam there (image-side surfaces like a field
flattener do not shrink the entrance pupil). The entrance-pupil centre is the
per-field dynamic-pupil crossing; the exit pupil is the image-side
outgoing-segment crossing (omitted when ill-conditioned).

### Low-angle probe

For a stop-free system (and when the field set has an infinite-conjugate field
and no `pass_through` is pinned), `chief` also runs a **low-angle probe**: a
≈1° pupil grid (2×-radius aperture, so the centroid stays unbiased even when
the seed aperture Z is far from the physical one) whose centroid chief ray is
traced and its crossing with the **optical axis** taken as a stable,
field-set-independent estimate of the aperture Z. The probe is used only as a
*seed/fallback*, never as the primary pupil:

- **Single field** — the field's grid (and its reported `entrance_pupil`
  centre) aims at the probe aperture, since there is no second field to cross
  against. Previously a lone field left its entrance-pupil centre unset, which
  centred downstream grids at the origin.
- **Multiple fields** — the per-field crossing updates remain the primary path
  (each field keeps its own entrance pupil); the probe only backs a field whose
  crossing is ill-conditioned (no clean in-lens intersection), so a field's
  pupil never stays pinned to a stale seed.

The probe is reported per result as `pupil_probe` / `pupil_probe_z`. It is not
computed for finite-conjugate-only field sets or when `pass_through` is set.

### Virtual entrance pupil

A third mode overrides both of the above: when `chief.pupil_model.mode` is
`virtual_entrance_pupil`, the pupil is a fixed virtual plane at
`axial_position` (Z from the surface-0 vertex) with diameter `diameter`. The
chief ray of **every** field is forced through its centre `(0, 0,
axial_position)`, the grid radius is `diameter / 2`, and the dynamic-pupil
iteration, the low-angle probe and the stop-based sizing are all skipped. This
is the primary beam-specification origin for initial exploration and escape
optimisation (`samples/escape-6elements-init.yaml`); the optimizer can vary
`axial_position`/`diameter` as `pupil_model` variables.

The `chief` CLI exposes shorthands for this section: `--epz Z` (sets
`axial_position` and activates virtual mode), `--epd D` (sets `diameter`) and
`--fnum N` (sets `diameter = |EFL| / N`, with the EFL from a one-shot
`paraxial.Compute`). `--fnum` and `--epd` are mutually exclusive. The
`paraxial` command honors the virtual pupil too: it derives the entrance pupil
from a virtual-mode chief pass, so `entrance_pupil_location`,
`entrance_pupil_diameter` and the F-number all reflect the virtual plane.

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
(`raymath.PupilGrid`, wrapped by the shared `internal/pupil` package):

| Grid | Layout |
|---|---|
| `polar` (default) | √n radial rings × √n azimuthal angles, `rᵢ = (i+0.5)/n · R` |
| `square` | √n × √n cells, kept if `x² + y² ≤ 1` |
| `hex` | hexagonally packed rows inside the aperture |

`pupil.Launch` builds the per-ray launch states (including the field's
vignetting clip) and `pupil.Trace` runs the rays; both are shared by `chief`,
the optimizer (`dls`), `wavefront` and `asphere`. (`GenerateGridPoints` still
generates the pupil for the ray-fan and marginal-ray scans, which are
one-dimensional and trace their own rays.)

For angle fields the grid is centred on the **pupil centre**, the point on the
start plane `z = z_start` whose ray (parallel to the field direction) crosses
the stop. It is computed vectorially (`raymath.WavefrontGridCenter`) from the
entrance-pupil centre `C = (0,0,z_stop)` as
`(px, py) = C − (C.z − z_start)·rayDir.XY / rayDir.Z` — the classic
`−(z_stop − z_start)·tan θ·(dx, dy)` offset expressed with direction-vector
ratios, so it stays finite up to 90° incidence (at grazing angles the grid
falls back to the wavefront plane through the pupil centre). Rays are launched
from the **wavefront plane** perpendicular to the ray direction through the grid
centre (`pupil.OPLLaunch`; the projection itself is
`raymath.ProjectOntoWavefront`), so their OPL is referenced to a common
wavefront and carries no launch-geometry tilt. For finite conjugate fields the
grid is centred on the object-projected pupil and the rays launch from the
object point (`pupil.HeightOrigin`).

Each pupil point becomes a ray. Rays are traced in parallel; rays that miss or
are vignetted are recorded with a nil image and `error_code`, so vignetting is
visible in the statistics.

## 4. Chief ray and spot centroid

The **spot centroid** is the intensity-weighted mean of the grid-ray image
positions on the reference surface:

```
cₓ = Σ wᵢ xᵢ / Σ wᵢ ,   wᵢ = (I_s + I_p)/2
```

The chief ray is defined by `chief.chief_ray_definition` (`--chief-ray`), with
`pass_through` taking precedence over it:

- **`centroid` (the default for a stop-free dynamic pupil):** the ray from the
  field that passes through the centroid. Its origin is found by a
  one-dimensional root solve (`searchOriginForTarget`, bracketing + bisection on
  the traced image height) so that the ray hits the centroid coordinates on the
  reference surface. This is the only definition available when the pupil
  position itself is discovered from the trace: without a stop or a virtual
  pupil model, `entrance_pupil_centre` and `vignetting_centre` have no
  entrance-pupil Z to aim at yet, so they are rejected (`ValidateRayDefinition`,
  exit 1) rather than silently downgraded.
- **`entrance_pupil_centre` (with a stop or a virtual pupil):** the ray through
  the entrance-pupil centre `(0, 0, z_EP)` — the object-space chief ray of a
  prescribed pupil, taken analytically (no search and no dependence on the
  trace).
- **`vignetting_centre` (the default when the pupil is prescribed):** the ray
  through the centre of the field's vignetting ellipse
  (`fields[].vignetting` decenter·R in the pupil plane). Without a vignetting
  specification the decenter is zero, so this coincides with
  `entrance_pupil_centre` and the default only differs for a field the document
  marks as vignetted.
- **`pass_through`:** the ray that passes through a given coordinate on a given
  surface (`--pass-through N` or YAML `pass_through`). The origin (angle case)
  or direction (height case) is solved the same way; it overrides the definition
  for that run.

`""` (no flag, no YAML) resolves to the per-system default above, so the
selection is a single definition for the whole run either way
(`resolveRayDefinition`), and `--chief-ray` is written back into the output
pipeline document when given.

The chief ray is then traced once more for its exact image height. The centroid
target it is compared against (`cx`, `cy`) is the centroid of the **statistics
grid**, so for a heavily vignetted field it follows the effective-vignetting
remap below rather than the nominal full-aperture grid.

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

### Heavily clipped beams: the effective vignetting ellipse

The full-aperture grid is always the emitted `grid_points` (BeamEnvelope
re-traces those launch origins to size `auto_aperture` diameters), but the
statistics and the centroid target above come from it only while at least
`gridSurviveFraction` (25%) of its rays reach the reference surface. Below that
the beam that passes is an off-centre, non-circular effective pupil, and `chief`
estimates it instead of shrinking the grid radius concentrically:

1. **Fit** — the min-area ellipse containing every surviving ray of the full
   grid, about the surviving bundle's centroid. The orientation is scanned over
   the convex hull's edge directions, the principal axis and a uniform sweep;
   for each candidate the exact smallest axis ratio is found by a ternary search
   on the convex objective `a²·t = maxᵢ(t·uᵢ² + vᵢ²/t)`. The axes are inflated by
   0.1% (`effectiveVignettingSafety`) so every measured survivor still passes
   `VignettingDef.Contains` after the round trip through the compressed-axis +
   `tangent` convention; the reported angle is folded into ±45° (swapping the
   axes when needed) so `tangent` stays within ±1 instead of running away at the
   ±90° fold.
2. **Report** — `chief_rays[].effective_vignetting`, a `VignettingDef` in the
   same convention as `fields[].vignetting` (decenter/compression relative to
   `entrance_pupil.radius`, rotated by `atan(tangent)`), so it can be reused as
   a vignetting specification. It is present only for a clipped field.
3. **Re-lay** — the statistics/centroid grid is mapped affinely into the
   ellipse (`pupil.LaunchSpec.Remap`: the full grid, not a thinned one), each
   cell's `Area` scaled by the map's determinant
   `(1−compressionₓ)(1−compression_y)`, so all `num_rays` samples measure the
   beam that actually passes. The declared `fields[].vignetting` clip still
   applies on top of the remap.

The estimate is only accepted when the re-laid grid itself survives as well as
the legacy probe guarantees (≥ `gridSurviveFraction` of `num_rays`, and ≥ 8
survivors for the fit); otherwise the adaptive concentric radius probe
(`probeGridRadius`, shrinking by 0.8 per step until 25% survive) takes over and
nothing is reported. A field that is not clipped that heavily never reports an
`effective_vignetting`.

This matters in practice: for a strongly off-axis field whose transmitted rays
hug the pupil rim, the survivors lie far from the pupil centre, so a concentric
probe can shrink away from all of them — `traced_rays: 0`, a zeroed `rms_*` and
`min`/`max` of ±1e18. The ellipse remap keeps such a field's statistics (and its
centroid target) meaningful: the 23° field of
`samples/escape-6elements-init.yaml` goes from `traced_rays: 0` to
`traced_rays: 254`.

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

Grid rays are traced concurrently by `pupil.Trace`'s semaphore-limited worker
pool, writing each result back into its grid-ordered sample slot. The spot
centroid is then accumulated sequentially over the returned samples, so the
results are deterministic for any worker count and independent of trace
completion order.
