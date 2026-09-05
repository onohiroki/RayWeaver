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
- `OPL_j` — optical path length to the reference surface. For angle
  (infinite-conjugate) fields the ray is launched from the **wavefront plane**
  perpendicular to the ray direction (`raymath.ProjectOntoWavefront`), so the
  OPL is referenced to a common wavefront and carries no launch-geometry tilt
  (the linear `Δpx·sinθ` ramp that used to pollute off-axis OPD)
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
the standard wavefront aberration. Because angle fields are launched from the
wavefront plane, their OPL is already free of the launch-geometry tilt; the
fitted low-order terms remove only the true field-dependent tilt and defocus.

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

---

## 5. Polychromatic (white-light) PSF, OTF, MTF

### 5.1 Principle

For incoherent broadband illumination (natural light, typical white sources),
different wavelengths are mutually incoherent. RayWeaver computes:

1. **Monochromatic PSFs** — per wavelength, via vector Huygens integration
2. **Polychromatic PSF** — incoherent intensity sum on a **common image grid**
3. **Polychromatic MTF** — via **OTF complex-weighted averaging** (not MTF averaging)

Key principle: all wavelengths share the **same physical image plane** and
**same image grid**. Lateral color and longitudinal chromatic aberration are
preserved, not removed by per-wavelength recentering or refocusing.

### 5.1.1 Pipeline

```
For each (field, polarization):
  For each wavelength λ_i:
    1. Trace polarized wavefront to reference surface
    2. Huygens integral → physical (unnormalized) intensity grid I_i
    3. Record transmittance τ_i = window_power / ref_power
    4. Compute spectral weight w_i = SPD(λ_i) · Δλ_i · τ_i
  5. Common grid: centre = ref λ chief ray, size = max envelope, pitch = λ_min
  6. Polychromatic PSF: I_poly = Σ w_i I_i / Σ w_i
  7. Normalize: Σ I_poly·Δx·Δy = 1
  8. Polychromatic MTF:
     a. Each I_i → FFT → complex OTF_i (DC=1, phase-corrected)
     b. OTF_poly = Σ w_i OTF_i / Σ w_i
     c. MTF_poly = |OTF_poly|
  9. Strehl = peak(I_poly) / peak(I_ideal_poly)
```

The polychromatic PSF is therefore the **incoherent sum** (intensity-weighted)
of monochromatic PSFs:

```
h_poly(x, y) = Σ_i w_i h_i(x, y) / Σ_i w_i
```

where `h_i` is the monochromatic PSF at wavelength `λ_i` and `w_i` is the
effective spectral weight.

**Critical physical requirement**: all monochromatic PSFs must be evaluated
on the **same physical image plane** and the **same image grid** (same `x0, y0,
dx, dy, nx, ny`). Separate centering or refocusing per wavelength would
artificially remove lateral color and longitudinal chromatic aberration, giving
an unrealistically optimistic "white-light" performance.

### 5.2 Spectral weight

The effective weight for each wavelength sample is

```
w_i = S(λ_i) · T_sys(λ_i) · Q(λ_i) · Δλ_i
```

- `S(λ)` — source SPD (e.g., CIE D65, flat, or custom)
- `T_sys(λ)` — system transmittance at this wavelength, including Fresnel
  losses, coating TMM, vignetting, and glass absorption. In RayWeaver this is
  the **per-wavelength window power / reference-surface power** (`tau` in the
  code), computed from the Huygens integral's physical (unnormalized) intensity
  grid:

  ```
  τ = (window_power × λ²) / ref_surface_power
  ```

  where `window_power = Σ I_i·Δx·Δy` from the **unnormalized** Huygens grid.
  The `λ²` factor corrects the Huygens integral's `1/λ` prefactor (field
  amplitude ∝ 1/λ, intensity ∝ 1/λ²) so that physical power is comparable
  across wavelengths.
- `Q(λ)` — detector QE or photopic response (not yet implemented; defaults to
  1)
- `Δλ_i` — integration width (trapezoidal rule on the SPD sample spacing)
  `Δλ_i = (λ_{i+1} - λ_{i-1})/2` (endpoints: half-interval)

The `spectral.Curve.IntegratedWeight(λ)` method returns `S(λ)·Δλ`.

### 5.3 Common image grid

All wavelengths share a single image grid (`ImageGridSpec`) determined from the
**reference wavelength** (the first traced wavelength with valid samples):

| Parameter | Determination |
|-----------|---------------|
| Centre (cx, cy) | Chief-ray image point of the reference wavelength |
| Half-width | max over λ of `max(4 × Airy_radius(λ, NA), 3 × spot_RMS(λ))` |
| Pixel pitch (dx, dy) | Auto-enlarged until `dx ≤ Airy_radius(λ_min)/2` |
| Grid size (nx, ny) | `--psf-grid` (default 64) |

This ensures:
- The grid encloses the broadest PSF (longest λ Airy disk + lateral color shifts)
- The shortest λ diffraction core is resolved (pixel ≤ Airy_radius/2)
- No per-wavelength recentering → lateral color preserved

### 5.4 Polychromatic MTF via OTF complex-weighted averaging

The OTF is the Fourier transform of the PSF. For incoherent broadband light:

```
OTF_poly(f_x, f_y) = Σ_i w_i OTF_i(f_x, f_y) / Σ_i w_i
MTF_poly(f)        = |OTF_poly(f)|
```

**Crucially, the complex OTFs are averaged before taking the magnitude.**
Averaging the MTFs directly (`Σ w_i MTF_i / Σ w_i`) would discard the phase
information and overestimate contrast when lateral color or other phase
differences exist between wavelengths.

RayWeaver computes the polychromatic MTF as follows:

1. For each wavelength `λ_i`:
   - Use the **physical (unnormalized) intensity grid** `I_i(x,y)`
   - Zero-pad to next power of 2
   - 2D FFT → complex array
   - fftshift so DC at center
   - Extract sagittal (center row) and tangential (center column) 1D OTFs
   - Phase-correct: `OTF_c(f) = OTF_raw(f) · exp(-2πi·f·(origin - centroid))`
   - Normalize to DC = 1
2. Compute weighted complex average: `OTF_poly = Σ(w_i OTF_i) / Σ w_i`
3. Take magnitude: `MTF_poly = |OTF_poly|`
4. Extract sagittal (fx-axis) and tangential (fy-axis) 1D curves
5. Build `PSFMTFSummary` with thresholds, evaluated frequencies, and
   per-wavelength `WavelengthMTF` data

This correctly captures lateral color effects: a wavelength-dependent lateral
shift introduces a linear phase tilt in its OTF, and the complex average
produces the correct contrast reduction.

### 5.5 Configuration

**PSF spectral settings** (`psf:` section):
```yaml
psf:
  spectral_curve: D65          # or FLAT, or custom spectral_entries
  spectral_entries:
    - wavelength: 486.13       # nm
      relative: 1.0
    - wavelength: 587.56
      relative: 1.0
    - wavelength: 656.28
      relative: 1.0
```

**MTF spectral settings** (`psf.mtf_config:` section, independent of PSF):
```yaml
psf:
  mtf_config:
    spectral_curve: D65        # independent of psf.spectral_curve
    combination_method: otf    # currently only "otf" implemented
    max_frequency: 200         # cycles/mm
    thresholds: [0.50, 0.30, 0.10]
    frequencies: [10, 25, 50, 100]
```

CLI flags:
```bash
rayweave psf --spectral D65 --mtf-spectral D65 --mtf-combination otf < input.yaml
```

### 5.6 Output

The pipeline YAML (`psf_results[]`) includes:
- `mtf` — `PSFMTFSummary` with combined sagittal/tangential thresholds and
  evaluated frequencies
- `mtf.spectral_curve` / `mtf.combination_method` — effective settings
- `mtf.wavelength_mtfs[]` — per-wavelength threshold crossings and evaluated
  points (for diagnosis of which wavelengths limit the contrast)

Full per-wavelength and combined PSF/OTF/MTF grids are written to `--yaml`
and `--csv` files (one per result).

### 5.7 Best focus and polychromatic evaluation

- **Fixed image plane (default)**: all wavelengths evaluated at the nominal
  image plane. Longitudinal color appears as defocus blur in the combined PSF;
  the polychromatic Strehl and MTF reflect real sensor-plane performance.
- **`--best-focus`**: the best-focus shift is determined from the **reference
  wavelength's** geometric spot RMS minimum, and **applied to all wavelengths
  identically**. This removes the field-curvature defocus common to all
  wavelengths while preserving lateral color and the relative defocus between
  wavelengths. Use for comparing intrinsic wavefront quality.

Per-wavelength independent best focus is not provided for polychromatic
evaluation as it would remove longitudinal chromatic aberration.

### 5.8 Convergence

The `--converge-check` mechanism (enabled by default) re-evaluates at 1.5×
ray count and reports the relative Strehl change. For polychromatic results,
the convergence check applies to the combined Strehl. Strongly aberrated
polychromatic fields may require higher `--num-rays` (900..1600) to converge.

### 5.9 Output details

#### 5.9.1 Pipeline YAML (`psf_results[]`)

```yaml
psf_results:
  - field_index: 0
    field_angle: 0.0
    wavelength: 0          # 0 = polychromatic
    polarization: RCP+LCP
    spectral_curve: D65
    strehl_ratio: 0.85
    mtf:
      spectral_curve: D65
      combination_method: otf
      sagittal:
        thresholds:
          - mtf: 0.5
            frequency: 42.3
          - mtf: 0.3
            frequency: 68.1
        evaluated:
          - frequency: 10
            mtf: 0.92
            otf_real: 0.92
            otf_imag: 0.0
            ptf: 0.0
      tangential:
        thresholds: [...]
        evaluated: [...]
      wavelength_mtfs:       # per-wavelength threshold crossings
        - wavelength: 4.8613e-07
          spectral_weight: 0.25
          sagittal:
            thresholds: [...]
          tangential:
            thresholds: [...]
        - wavelength: 5.8756e-07
          ...
```

#### 5.9.2 Full output files (`--yaml`, `--csv`)

Per-result files contain:
- Combined polychromatic intensity grid
- Per-wavelength intensity grids (`wavelength_contributions[].intensity`)
- Combined and per-wavelength MTF curves (`mtf.curve`, `wavelength_contributions[].mtf`)
- Encircled energy, wavefront OPD, wavefront samples

### 5.10 Implementation files

| File | Role |
|------|------|
| `internal/psf/psf.go` | `whiteGroup` — collects per-λ physical grids, calls MTF |
| `internal/psf/mtf.go` | `ComputePolychromaticMTF` — OTF complex average |
| `internal/spectral/spectrum.go` | `IntegratedWeight` — SPD × Δλ |
| `internal/types/types.go` | `PSFMTFConfig`, `PSFMTFSummary`, `WavelengthMTF` |
| `cmd/rayweave/psf.go` | CLI flags, write-back, output summary |

### 5.11 Unit tests

- `TestComputePolychromaticMTF` — two Gaussian PSFs, equal weights, verifies
  combined MTF = average of individual MTFs (centered Gaussians → real OTF)
- `TestComputePolychromaticMTFWithShift` — lateral shift between λ, verifies
  combined MTF < single-λ MTF due to phase cancellation

### 5.12 References

- `docs/psf.md` — user-facing PSF command documentation
