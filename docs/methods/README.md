# RayWeaver calculation methods

These documents explain the **numerical methods** behind RayWeaver's analyses
and optimizations — what is computed and how. For command-line usage, flag
reference, and YAML structure, see the [subcommand manuals](../README.md)
instead.

| Document | Method |
|---|---|
| [ray-tracing.md](ray-tracing.md) | sequential ray tracing: surface intersection, Snell's law, Fresnel coefficients, aspheres, folds, ghost rays |
| [pupil-grids.md](pupil-grids.md) | entrance-pupil grid generation and grid-ray tracing: Launch/Trace, wavefront-plane launch, OPL normalization, vignetting clip |
| [chief-rays-and-spot.md](chief-rays-and-spot.md) | pupil grids, centroid chief ray, spot statistics, ray fans, clear aperture |
| [paraxial.md](paraxial.md) | first-order ray trace, cardinal points, entrance/exit pupils, f/# |
| [merit-functions.md](merit-functions.md) | merit terms (spot RMS, distortion, colour, Seidel, OPD RMS), weights |
| [dls-optimization.md](dls-optimization.md) | damped least squares: normalised variables, finite-difference Jacobian, augmented-Lagrangian constraints, damping control |
| [region-active.md](region-active.md) | Okudaira Region Active Method: Lagrange-multiplier-based dynamic active-set with hysteresis for inequality constraints |
| [escape-function.md](escape-function.md) | escape-function global optimization |
| [glass-dispersion.md](glass-dispersion.md) | Sellmeier / Schott / Cauchy / nd-vd dispersion models, glass hull |
| [thin-film-tmm.md](thin-film-tmm.md) | transfer-matrix method for thin-film coatings |
| [asphere-candidates.md](asphere-candidates.md) | asphere candidate ranking, initial sag estimation, measured sensitivity, DLS validation |
| [psf.md](psf.md) | point-spread function: polarized ray tracing, Delaunay-weighted wavefront sampling, vector Huygens integration |
| [efl-scaling.md](efl-scaling.md) | uniform EFL scaling and why it preserves f/# |

## Units and conventions used throughout

- **Lengths** (thickness, radius, diameter, coordinates) in millimetres.
- **Wavelengths** in millimetres (coating layer thicknesses in nanometres,
  converted internally).
- **Z** is the optical axis, positive to the right. Surface 0 is the implicit
  object plane.
- Surfaces are described by **curvature** `c = 1/R`; a positive radius places
  the centre of curvature to the right of the vertex.
- Refractive indices are real (absorption is not modelled).
