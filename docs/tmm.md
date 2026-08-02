# `rayweave tmm` — thin-film coating analysis

Computes the reflectance and transmittance of a stack of thin-film layers on a
substrate using the transfer-matrix method (TMM), for both s- and p-polarised
light at a given angle of incidence.

```
rayweave tmm < input.yaml
```

## Input YAML

```yaml
n_air: 1.0                    # incident medium refractive index
n_sub: 1.5                    # substrate refractive index
theta_deg: 0                  # angle of incidence (degrees)
lambda: 0.00055               # wavelength (mm)
layers:
  - thickness: 100            # layer thickness (nm)
    n: 1.38                   # layer refractive index
  - thickness: 150
    n: 1.65
```

Layer refractive indices may be given directly with `n`, or resolved by
`material` name against a `glass_catalog` section (in which case `n` is looked
up at `lambda`).

## Output

YAML with:

```yaml
Rs: ...    # s-polarisation reflectance
Ts: ...    # s-polarisation transmittance
Rp: ...    # p-polarisation reflectance
Tp: ...    # p-polarisation transmittance
```

## Examples

```sh
rayweave tmm < samples/ar-coating.yaml         # single-layer AR
rayweave tmm < samples/dielectric-mirror.yaml  # 9-layer Bragg reflector
```

## Method

The transfer-matrix formalism is described in
[methods/thin-film-tmm.md](methods/thin-film-tmm.md).
