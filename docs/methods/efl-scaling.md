# EFL scaling

This document explains the scaling law used by `rayweave scale`: uniformly
scaling every length by `s` scales the effective focal length by exactly `s`
and preserves the f-number and the normalized aberration balance.

## 1. The scale factor

Given a target EFL `f_target`, the system's current EFL `f₀` is computed (by a
paraxial trace, see [paraxial.md](paraxial.md)) and every length is multiplied
by

```
s = f_target / f₀
```

The quantities scaled are:

- surface **radii** (`radius = 1/curvature`)
- **thicknesses** and surface positions
- **diameters**
- asphere **coefficients** `a₄, a₆, …` (which scale as `1/s^(2i)`, so that the
  physical sag `aᵢ h^(2i+4)` scales as `s`)
- asphere **normalization radii**
- the `scale`/`offset`-style glass model is untouched (glass indices have no
  units)

## 2. Why the EFL scales by exactly `s`

The paraxial power of a surface is `φ = (n′ − n)/R`. Since `R → s·R`, the power
scales as `φ → φ/s`. The EFL is `f = −n/φ_equivalent`; a surface power divided
by `s` yields an EFL multiplied by `s` (assuming the paraxial heights follow
the same geometry, which they do because every thickness and radius scales by
the same factor). Hence `f → s·f = f_target` exactly.

## 3. Why the f/# and aberration balance are preserved

- **f/#**: `f/# = f / D_ep`. Both the EFL and the entrance pupil diameter (and
  all surface diameters) scale by `s`, so the ratio is invariant.
- **Aberrations**: the third-order aberrations of a system scale with the
  *normalized* aperture and field. Because every length scales uniformly, the
  normalized ray heights, angles, and conic/asphere contributions are
  unchanged, so the (normalized) aberration coefficients — and therefore the
  relative spot sizes and balance — are preserved. Only the absolute scale
  changes.

This makes scaling ideal for building a starting point at a different focal
length before running `rayweave optimize`: the design is already aberration-
balanced, just at the wrong scale.

## 4. Multi-config mode

With `--config ID`, the selected config's EFL sets the scale factor, which is
then applied uniformly to **every** config. This keeps the relative zoom
positions consistent while retargeting the focal length.
