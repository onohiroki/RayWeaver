# Glass catalog and dispersion

RayWeaver resolves every material name to a refractive index at a given
wavelength. This document describes the dispersion models and the glass hull
used to constrain glass optimization.

## 1. Catalog sources

The catalog is populated from three sources:

- **Inline entries** in the YAML `glass_catalog.entries` (`nd`/`vd`, or full
  dispersion coefficients);
- **AGF files** listed in `glass_catalog.files` or loaded from a directory
  (`--glass-dir`, or `glass_catalog.directory`);
- **AGF catalogs** scanned at runtime (e.g. `GLASS/*.agf` in the repository).

Glasses are keyed by name with aliases; `AIR` and the empty string always
resolve to `n = 1`.

## 2. Dispersion formulas

`CalcRefractiveIndex` dispatches on the glass's `type` and `dispersion_formula`:

| Formula | Form |
|---|---|
| `sellmeier_1` | `n² = 1 + Σᵢ Bᵢλ²/(λ² − Cᵢ)`, 6 coefficients |
| `schott` | `n² = A₀ + A₁λ² + A₂λ⁻² + A₃λ⁻⁴ + A₄λ⁻⁶ + A₅λ⁻⁸` |
| `extended_2` | `n² = 1 + Σᵢ Bᵢλ²/(λ² − Cᵢ)`, 5 Sellmeier terms (10 coefficients) |
| `extended_3` | polynomial `n²` in λ² and λ⁻² … λ⁻¹², 9 coefficients |
| `constant` | `n = nd` (no dispersion) |
| tabulated | piecewise-linear interpolation of an `(λ, n)` table |
| model | `nd`/`vd`-based approximation (below) |

## 3. Model glasses (nd/vd → dispersion)

A glass defined only by `nd` and `vd` — including **model glasses** whose
`nd`/`vd` are optimization variables — is turned into a full dispersion curve
as follows (`RefractiveIndexFromNDVD`):

1. The standard-line indices `nₙ`, `n_C`, `n_F`, `n_d`, `n_g`, … are derived
   from `nd`/`vd` using the industry approximation `n = 1 + (n_d − 1)(C + Aλ² +
   Bλ⁻² + …)` fitted to the known (λ, index) knots (`internal/glass/indeces.go`).
2. In the visible band (≈365–2058 nm) the (λ, n) knots are interpolated with a
   **cubic spline** (`SplineInterpolate`), so the model is smooth and
   differentiable — important for the DLS Jacobian.
3. Outside that band, a **Cauchy fit** `n = A + B/λ² + C/λ⁴ (+ D/λ⁶)` is fitted
   to the same knots by least squares and evaluated. Cauchy is well-behaved
   beyond the fitted band.

Because the dispersion is a smooth function of `nd`/`vd`, the optimizer can
differentiate merit terms with respect to glass variables.

## 4. Cauchy fit

`FitCauchy` builds the normal equations for `n(λ) = A + B x + C x²` (or a
fourth term), `x = 1/λ²`, and solves them with `raymath.SolveLinear`
(least-squares normal equations). This is the fallback dispersion model used
outside the spline band.

## 5. Index caching

Refractive indices are cached per (glass, nd, vd, wavelength). Model glasses
optimized by nd/vd change values between evaluations, so nd/vd are part of the
cache key; catalog glasses use stable nd/vd markers. This caching is what makes
the grid traces inside a single merit evaluation fast.

## 6. The glass hull

The **glass hull** is the convex hull of all glasses in the reference catalog
in `(nd, vd)` space (computed by `cmd/hullgen` from `GLASS/glass_nd_vd_data.yaml`
using Andrew's monotone chain, and baked into `internal/glass/hull_data.go`).

When `optimization.glass_hull.enabled: true`, the optimizer adds a merit term
that penalizes model-glass `(nd, vd)` points **outside** the hull (by a margin
`glass_hull.margin` with weight `glass_hull.weight`). This keeps optimized
glasses inside the region of commercially realizable glasses. The interior
point test uses the precomputed hull vertices and its nd/vd bounds.

### Regenerating the hull

The hull data is auto-generated and marked `DO NOT EDIT`:

```sh
go run ./cmd/hullgen/
```

This reads `GLASS/glass_nd_vd_data.yaml` and rewrites
`internal/glass/hull_data.go`. See also `GLASS/GLASS_README.md`.
