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
|---|---|
| `spot_rms` | RMS radial spot size on the reference surface (see below) |
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
