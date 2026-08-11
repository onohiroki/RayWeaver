# `rayweave scale` — scale a system to a target EFL

Uniformly scales a system so its effective focal length (EFL) equals a target
value. Every length (radii, thicknesses, diameters, asphere coefficients and
normalization radii) is multiplied by `s = TARGET / current_EFL`. This scales
the EFL exactly by `s`, preserves the f-number and the normalised aberration
balance, and is useful for building a starting point at a target focal length
before optimizing.

```
rayweave scale --efl TARGET [--config ID] < system.yaml > scaled.yaml
```

## Options

| Flag | Description |
|---|---|
| `--efl TARGET` | target effective focal length in mm (required unless `scale.efl` is set) |
| `--config ID` | select a config by id (multi-config mode); its EFL sets the scale factor applied to every config |
| `--glass-dir DIR` | AGF glass catalog directory |

## Input YAML — `scale` section (optional)

```yaml
scale:
  efl: 50.0        # target effective focal length (mm); --efl overrides
```

The effective value is echoed back into the output `scale:` section when it
came from a flag (CLI/YAML rule).

## Output

A new YAML document with all lengths scaled. The system's `paraxial_result`
will change so that `focal_length == TARGET` (within the accuracy of the
paraxial computation of the current EFL).

## Examples

```sh
# Scale a 25 mm patent lens to a 50 mm standard, then optimize
cat reference25mm.yaml | rayweave scale --efl 50 | rayweave optimize > optimized.yaml

# Verify the EFL
rayweave scale --efl 50 < ref25.yaml | rayweave paraxial \
  | rayweave query -r paraxial_result.focal_length
```

## Method

The scaling law and why it preserves f/# is described in
[methods/efl-scaling.md](methods/efl-scaling.md).
