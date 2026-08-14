# Merit functions

The optimizer minimizes a scalar **merit function** assembled from merit terms,
each weighted and summed. This document lists the term kinds, how each is
evaluated, and how weights combine across fields, wavelengths and configs.

## 1. Assembly

In multi-config mode each config contributes its own merit. The total merit is

```
M = Σ_configs  w_cfg · M_cfg
```

and within a config

```
M_cfg = Σ_terms  w_term · r(term)
```

where `r(term)` is the per-term residual (a positive quantity to be made
small). Weighting lets the designer balance fields, wavelengths and configs
(zoom positions).

## 2. Term kinds

| Kind | Residual |
|---|---|---|
| `spot_rms` | RMS radial spot size on the reference surface (see below) |
| `spot_rms_t` / `spot_rms_s` / `spot_rms_worst` | flux-weighted tangential / sagittal spot RMS and their maximum (see below) |
| `spot_rms_weighted` | flux-weighted (pupil-cell-area × intensity) RMS spot size |
| `spot_ee_radius` | encircled-energy radius (EE fraction via `fraction`, default 0.8) |
| `opd_rms` | RMS optical path difference across the pupil grid |
| `distortion_pct` | percent distortion |
| `lateral_color` | lateral colour |
| `longitudinal_color` | longitudinal colour |
| `seidel_spherical` / `seidel_coma` / `seidel_astigmatism` / `seidel_distortion` | third-order Seidel coefficients |
| `wavefront_defocus` / `wavefront_astigmatism` / `wavefront_tilt` / `wavefront_rms_residual` | derived low-order paraboloid-fit magnitudes |
| `wavefront_x2` / `wavefront_y2` / `wavefront_xy` / `wavefront_x` / `wavefront_y` / `wavefront_constant` | raw paraboloid-fit coefficients |

### spot_rms

A field/wavelength grid of rays is traced and the intensity-weighted spot RMS
radius about the centroid is computed (see
[chief-rays-and-spot.md](chief-rays-and-spot.md), §5). The grid density follows
`optimization.num_rays`, and the pupil is scaled by
`optimization.aperture_margin` (clamped ≥ 1). Polychromatic spot RMS uses the
per-wavelength grid re-trace.

### Off-axis spot kinds (spot_rms_t / _s / _worst / spot_rms_weighted / spot_ee_radius)

Plain `spot_rms` measures a rotationally symmetric, uniformly-weighted RMS about
the centroid, which limits off-axis-field optimisation: it cannot tell coma
(a tangential flare) from astigmatism from field curvature, is dominated by a
sparse comatic tail, and ignores vignetting / reflection-loss energy weighting.
The five off-axis kinds reuse the same pupil grid but weight and decompose it:

- **Flux weighting.** Each grid ray carries `area` (its polar pupil cell area,
  ∝ radius — the entrance-pupil flux it represents) and `intensity` (mean
  transmitted s/p intensity, Fresnel/TMM reflection losses). The weight is
  `area × intensity`, falling back to area, then to equal weight, for grids
  without the data. This mirrors the chief path's intensity-weighted centroid
  and measures vignetted, asymmetric off-axis pupils correctly.
- `spot_rms_t` / `spot_rms_s` — the flux-weighted RMS of the spot deviation
  decomposed into the tangential direction (the field's image-plane azimuth,
  from `fields[].direction`, default the Y axis) and the perpendicular sagittal
  direction. A comatic spot has RMS_T ≫ RMS_S; astigmatism separates the two
  even at the circle of least confusion.
- `spot_rms_worst` — `max(RMS_T, RMS_S)`, attacking the dominant axis directly.
- `spot_rms_weighted` — the flux-weighted RMS about the flux-weighted centroid.
- `spot_ee_radius` — the radius about the flux-weighted centroid enclosing the
  `fraction` of the total flux (default 0.8 = EE80, set via the term's
  `fraction` YAML field). Insensitive to a sparse tail, so it correlates with
  MTF better than RMS for off-axis fields.

All five contribute `(value − target)²`, so `target: 0` minimises them and a
non-zero target drives them to that value.

### opd_rms

The same pupil grid is traced and each ray's **optical path length** (OPL) is
recorded. The residual is the RMS of `OPL − ⟨OPL⟩` over the accepted rays, i.e.
the wavefront error expressed as an optical path difference:

```
OPD_RMS = √( (1/N) Σ (OPLᵢ − OPL̄)² )
```

This is a convenient aberration measure that does not require a chosen
reference sphere.

### wavefront paraboloid coefficients

The `wavefront_*` kinds evaluate the least-squares quadratic (paraboloid) fit

```
P(x,y) = a·x² + b·y² + c·xy + d·x + e·y + f
```

of the field's OPD sampled on the reference surface (default: the last optical
surface, overridable via `chief.reference_surface`). The OPD is referenced to
the **best-focus point** — the geometric spot-RMS minimization along the
image-plane normal — exactly like the `wavefront` command, so:
`wavefront_defocus = (a+b)/2`, `wavefront_astigmatism = √(((a−b)/2)² + (c/2)²)`,
`wavefront_tilt = √(d²+e²)`, and `wavefront_rms_residual` is the area-weighted
RMS of `OPD − P` (the high-order residual after removing
piston/tilt/defocus/astigmatism). The raw coefficients `wavefront_x2` … 
`wavefront_constant` address `a…f` directly.

Setting `target` on such a term drives the corresponding low-order aberration to
zero (or any value): e.g. `wavefront_astigmatism` with `target: 0` forces
astigmatism-free design, while `wavefront_rms_residual` minimises the residual
aberration. The values match `wavefront_result.fields[].paraboloid`.

The entrance-pupil grid follows `optimization.num_rays` and
`optimization.aperture_margin`, and — like every grid term — is centred on the
config's per-iteration **frozen** pupil Z, so the DLS base point and its
Jacobian perturbations share one pupil (the wavefront analysis itself settles
the dynamic pupil once per iteration). A degenerate fit (no grid, fewer than
six valid rays) returns the 1e6 penalty.

**Reference-surface fallback.** The wavefront fit needs a sampling surface
strictly before the image plane. A `chief.reference_surface` set to the image
plane (the conventional last surface) is rejected and falls back to the last
optical surface — the same default the standalone `wavefront` command uses —
instead of returning the penalty.

**Dynamic-pupil retry.** The frozen-grid fit does not apply the fixed-surface
vignetting cut, so a strongly off-axis field whose beam clips a fixed aperture
can produce a singular paraboloid fit (e.g. the 24° field of the US2645157
triplet at an un-resolved `pupilZ`). On a failed fit the term retries once with
the dynamic pupil (chief resolves the entrance pupil), matching the standalone
`wavefront` command's grid, before giving the 1e6 penalty.

**Weight design (escape-demo).** The term weight must be balanced against the
measured residual, not the idealised value: with `wavefront_rms_residual` the
contribution is `weight × value²`, so a weight chosen from an optimistic
`value` can dominate the merit ~10× once the solver moves. In `escape-demo.yaml`
the 24° `wavefront_rms_residual` term at weight 8000 (designed for value 4.5e-5)
measured 3.9e-4 against the spot terms' ~3.5e-5 and drove the solver to
solutions with a **broken 24° pupil** (spot RMS ~1 mm); the same term at any
weight up to 1000 still degraded the 24° spot in DLS (frozen-pupil evaluation
diverging from the chief-measured spot). The 24° residual is better controlled
through its spot terms (`spot_rms_worst` / `spot_rms_weighted` /
`spot_ee_radius` raised to weight 1.0) than through the wavefront residual. The
16° `wavefront_astigmatism` term (weight 1000) is safe: a DLS re-optimisation
of the escape best lifts the 16° Strehl from 0.06 to 0.15 and the 24° Strehl
from 0.77 to 0.91 while keeping 0° near-diffraction-limited.

### distortion_pct

```
distortion = 100 · (y_chief − y_paraxial) / y_paraxial
```

where `y_chief` is the traced chief-ray image height for the field and
`y_paraxial = EFL · tan θ` is the paraxial (perfect) height.

### lateral_color

The difference between the chief-ray image heights at two wavelengths:

```
lateral_color = y_chief(λ₂) − y_chief(λ₁)
```

### longitudinal_color

The difference in paraxial EFL between two wavelengths:

```
longitudinal_color = EFL(λ₂) − EFL(λ₁)
```

### Seidel coefficients

Third-order (Seidel) aberration coefficients computed for a field and
wavelength (see `internal/paraxial/seidel.go`). Each kind targets one
coefficient (spherical, coma, astigmatism, distortion).

## 3. Constraints

Constraints are *not* merit terms: they are enforced by the DLS solver through
an augmented-Lagrangian penalty (see
[dls-optimization.md](dls-optimization.md), §5). The solver adds
`λ·c + ½μ·c²` per constraint residual `c`, so a constraint pushes the solution
toward `c = 0` while the penalty weight is managed automatically. Constraint
kinds: `equality`, `inequality_upper`, `inequality_lower`, `band`, `fuzzy`.

## 4. Notes

- An evaluation that degenerates (all rays missing, division by zero) returns a
  large merit (1e6) so the solver is pushed away rather than misled.
- `distortion`/`colour`/`seidel` terms are evaluated from chief-ray / paraxial
  traces rather than a full grid, so they are cheap.
- The `breakdown` event in `optimize --log`/`--verbose` JSONL output lists the
  per-term residual values, which helps find which term dominates the merit.
