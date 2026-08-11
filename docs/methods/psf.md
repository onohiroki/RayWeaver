# Point-spread function: polarized ray tracing and vector Huygens integration

This document describes the numerical method behind the `psf` subcommand. The
goal is a 2D PSF on a **fixed flat image plane** for a given field, wavelength
and input polarization, computed without FFT so that fisheye and other strongly
non-paraxial systems are handled robustly.

## Scope

- **Flat image plane**: the PSF is evaluated on one fixed plane (the last
  surface). No 3D PSF, no refocusing per field — field curvature and defocus
  appear naturally in the result.
- **Per-field pupil**: each field's beam is traced and its actual (possibly
  vignetted, non-circular) wavefront sampled. There is no single flat exit
  pupil shared across fields.
- **Vector Huygens**: the image-plane field is the coherent sum of secondary
  wavelets launched from the sampled wavefront, including the electric-field
  vector (so `E_z` and polarization effects are represented).
- **No FFT**: evaluation is a direct `O(N_rays × N_pixels)` integral.

## Pipeline

```
per-field polarized ray tracing
  → non-uniform wavefront samples on a reference surface
  → Delaunay-triangulated area weights
  → direct vector Huygens integral
  → intensity PSF + Strehl / FWHM / encircled energy
```

## 1. Polarized ray tracing

The ray tracer (`internal/ray`) propagates a **3D complex electric field**
through every surface. The input is a Jones vector expressed in the transverse
frame `(u, v)` of the field's chief ray (`u` horizontal, `v` in the meridional
plane), so the input polarization has a well-defined meaning for off-axis
fields. At each surface:

1. Compute the local `s/p` basis of the plane of incidence:

   ```
   s = normalize(d_in × n)
   p = s × d_in
   ```

   with `d_in` the incident direction and `n` the surface normal oriented
   against the ray. `s` is invariant under refraction/reflection within the
   plane of incidence.

2. Project the field onto `(s, p)`, apply the Fresnel **amplitude** coefficients
   (`diag(t_s, t_p)` for transmission, `diag(r_s, r_p)` for reflection; the
   square roots of the coating TMM intensity factors multiply these), and
   reconstruct in the outgoing frame `(s, p′ = s × d_out)`.

3. Ideal fold mirrors reflect the field vector across the surface normal
   (`E_out = E − 2(E·n)n`), preserving `|E|` and the transverse orientation.

The propagated field is recorded on every surface result (`SurfaceResult.Field`,
a 3D complex vector in global coordinates).

## 2. Wavefront sampling

For each (field, wavelength), the entrance pupil is determined exactly as in
`chief`:

- with an explicit `stop_surface`, the grid radius is the paraxial entrance
  pupil radius and the grid is centred on the chief ray at the stop;
- without one, the **dynamic pupil** is used: the per-field entrance pupil is
  the in-lens crossing of the field's chief ray with field 0's chief ray,
  iterated (≤ 3 passes) until it settles; when ill-conditioned it falls back to
  the tightest fixed aperture's Z.

A polar (default), square or hex pupil grid of `num_rays` rays is then traced
with full polarization tracking **up to the reference surface** (default: the
last optical surface, the one before the image plane). Rays that are vignetted
by an aperture or fail to reach the reference surface are dropped; the surviving
samples therefore carry the actual, per-field pupil shape.

Each sample records:

```
(q_j, s_j, OPL_j, E_j)
```

- `q_j` — global position on the reference surface
- `s_j` — emergent direction
- `OPL_j` — optical path length from the object (launch plane for infinite
  conjugates) to the reference surface
- `E_j` — global complex electric field vector at the surface

### Area weights (Delaunay triangulation)

The samples are not equally weighted: their footprints on the reference surface
are irregular (curved surface, vignetting). The sample positions are projected
to the global XY plane, triangulated with a 2D Bowyer–Watson Delaunay
algorithm (`internal/mesh`), and each triangle's area is measured in **3D** (the
true reference-surface area element). Each sample's weight `ΔA_j` is one third
of the sum of the areas of the triangles that touch it. Vignetted samples are
excluded from the triangulation, so the weights automatically reflect the true
pupil support.

## 3. Vector Huygens integral

The complex field at an image-plane point `P` is the coherent sum of secondary
wavelets:

```
       1        ┌                                          ┐
E(P) = ── · Σ_j │ E_j · exp(ik(OPL_j + n·R_j)) · K_j · ΔA_j │
       λ        └              R_j                          ┘

R_j = |P − q_j|
k   = 2π/λ            (vacuum wavenumber)
n   = image-space index (the medium after the reference surface)
K_j = (1 + s_j·R̂_j)/2   obliquity factor, R̂_j = (P − q_j)/R_j
```

The `OPL_j + n·R_j` phase separates the path to the reference surface from the
free-space propagation to the image point, avoiding a double-counted phase. The
`1/R` factor is the spherical spreading of each secondary wavelet and `K_j` the
obliquity (both standard in the Huygens–Fresnel diffraction integral). The field
is summed as a 3D vector, so longitudinal components are kept.

### Intensity and normalization

```
I(P) = |E_x(P)|² + |E_y(P)|² + |E_z(P)|²
```

The intensity grid is normalized to unit sum over the sampled window
(`Σ I·Δx·Δy = 1`). Absolute photometric calibration is out of scope.

### Reference (ideal) PSF and Strehl

For the Strehl ratio, a second integration is performed with the OPL replaced by
a **perfect converging sphere to the window centre**:

```
OPL_ideal_j = − n·|q_j − P0|
```

so the phase at `P0` is constant across the pupil. The ideal PSF is the
diffraction-limited pattern of the *same* (possibly vignetted) pupil, and

```
Strehl = max(I_actual) / max(I_ideal)
```

A perfect system gives 1.0. Note this is a *fixed-plane* Strehl: the actual PSF
is evaluated at the nominal image plane, not refocused.

### Wavefront OPD

The per-sample wavefront error is

```
OPD_j = OPL_j + n·|q_j − P0|
```

referenced to the converging sphere to `P0`. A best-fit reference sphere
(piston + tilt + defocus) is subtracted so the reported `rms_opd` / `pv_opd` are
the standard wavefront aberration (not the launch geometry). For an angle-based
(infinite-conjugate) field the OPL already contains the geometric tilt that
focuses the beam; removing the fitted tilt leaves the true aberration.

## 4. Analysis

From the intensity grid:

- **Peak / centroid / FWHM**: the peak pixel (with sub-pixel parabolic
  interpolation along each axis for the FWHM), and the intensity-weighted
  centroid.
- **Encircled energy**: the fraction of total intensity within a radius of the
  centroid; `encircled_energy_50` is the radius enclosing 50 %.
- **Image-space NA and Airy radius**: the NA is the half-angle subtended by the
  reference-surface footprint as seen from the focus, `NA = n·sin θ_max`; the
  Airy radius `0.61·λ/NA` is reported as a diffraction reference.

## Polarization averaging

For `RCP+LCP` the two coherent states are integrated separately and their
**intensities averaged** (`½(I_RCP + I_LCP)`). Complex amplitudes are never
summed across incoherent states, so no fictitious interference is introduced.

## Numerical notes and limitations

- The integral cost is `O(N_rays × N_pixels)`; for a 64×64 grid and 400 rays
  this is ~1.6M complex operations per field per wavelength, trivial in Go.
- **Parallelism**: the wavefront tracing and the Huygens integral are
  row-parallel across `runtime.NumCPU()` workers (or `huygens_workers` /
  `--psf-workers`). Each image-plane row is computed by exactly one worker and
  writes to a disjoint output slice, so no locking is needed. The actual and
  ideal (diffraction-reference) grids of a state share one pass over the pixel
  geometry, and the RCP+LCP states are traced concurrently and evaluated
  through a single shared pool. Wavefront samples are sorted by their
  entrance-pupil launch coordinates before integration so the summation order —
  and hence the result — is independent of the worker count (the residual
  run-to-run variation is one floating-point ULP, inherited from the shared
  `chief` centroid accumulation).
- Strongly aberrated fields produce a coherent speckle pattern; the peak (and
  hence Strehl) of a speckle is sensitive to the pupil sampling, so off-axis
  metrics need a denser grid (`--num-rays` 900..1600). Near-diffraction-limited
  systems converge at the default 400 rays.
- The reference-surface triangulation uses the global XY projection for
  connectivity. For very steeply tilted reference surfaces this is an
  approximation, but the area weights themselves are exact 3D triangle areas.
- The method treats each ray's tube as coherent across the pupil (valid for
  single-mode illumination). Incoherent broadband illumination should be
  evaluated wavelength by wavelength and the intensities summed.
