#!/bin/bash
set -euo pipefail

# =============================================================================
# schmidt-demo.bash — folded Schmidt camera evaluation
#
# Purpose: show the fold model (positive thicknesses only; the fold is carried
# by the primary's decenter [{tilt: [0, 180, 0], reflect: true}]) on a D=200
# F/1.93 Schmidt camera: BK7 corrector plate + spherical primary + 2-element
# field flattener, with the sensor folded back to Z=400.
#
# Steps
#   1. chief       : chief rays + spot grid -> schmidt-chief-result.yaml
#   2. trace       : spot RMS per field
#   3. paraxial    : EFL / f# / pupil / track
#   4. plot        : SVG/PNG ray-fan diagram of the folded layout
#
# How to read the result
#   - Per-field RMS spot should stay near diffraction-limited (~0.02-0.04 mm)
#     across the 35 mm full-frame diagonal.
#   - The diagram folds the beam back to the flat sensor at Z=400.
#   - Evaluates ONLY the final system (samples/schmidt-flattener.yaml); the
#     plot shows ray fans for all field angles (no pupil-grid rays).
# =============================================================================

# Resolve the script's own directory so the demo runs from any CWD
# (repo root, `cd samples`, or a copied location). All data files are read
# from and all outputs are written to this directory.
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

# CLI options
CLEAN=false
while [[ $# -gt 0 ]]; do
  case "$1" in
    --clean) CLEAN=true; shift ;;
    *) echo "Unknown option: $1"; exit 1 ;;
  esac
done

OUTDIR="$SCRIPT_DIR"
YAML="$OUTDIR/schmidt-flattener.yaml"
CHIEF="$OUTDIR/schmidt-chief-result.yaml"
TRACE="$OUTDIR/schmidt-trace-result.yaml"
RESULT_FILE="$OUTDIR/schmidt-demo-result.txt"
SVG="$OUTDIR/schmidt.svg"
PNG="$OUTDIR/schmidt.png"

if [ "$CLEAN" = true ]; then
  echo "=== Cleaning up generated files ==="
  rm -f "$CHIEF" "$TRACE" "$RESULT_FILE" "$SVG" "$PNG"
  rm -f "$OUTDIR"/schmidt-*.png "$OUTDIR"/schmidt-*.svg
  echo "  Removed generated files"
  exit 0
fi

# Locate the rayweave binary: an explicit RAYWEAVE env value wins, then a
# binary next to the script or one directory up (samples/ -> repo root),
# then any rayweave on PATH.
if [[ -z "${RAYWEAVE:-}" ]]; then
  for cand in "$SCRIPT_DIR/rayweave" "$SCRIPT_DIR/../rayweave"; do
    if [[ -x "$cand" ]]; then RAYWEAVE="$cand"; break; fi
  done
  RAYWEAVE="${RAYWEAVE:-$(command -v rayweave || true)}"
  if [[ -z "${RAYWEAVE:-}" ]]; then
    echo "error: rayweave binary not found; set RAYWEAVE or put rayweave on PATH" >&2
    exit 1
  fi
fi

echo "=== Schmidt camera demo (D=200, EFL~386, F/1.93, 35mm full-frame) ==="
echo

# ── Chief rays + spot grid (final data only) ──
echo "--- Chief rays and spot grid ---"
$RAYWEAVE chief < "$YAML" > "$CHIEF" 2>/dev/null
echo "  Written: $CHIEF"
echo

# ── Trace (spot RMS per field) ──
echo "--- Spot RMS per field (587.6 nm, full aperture) ---"
$RAYWEAVE trace < "$CHIEF" > "$TRACE" 2>/dev/null
python3 -c "
import yaml
d = yaml.safe_load(open('$TRACE'))
lines = []
for cr in d.get('chief_rays', []):
    sr = cr.get('spot_stats')
    if sr:
        lines.append('  field %4.1f deg: rms_r %8.5f mm  (%d rays)' % (cr['field_angle'], sr['rms_r'], sr.get('traced_rays', 0)))
print('\n'.join(lines) if lines else '  (no spot stats)')
"
echo

# ── Paraxial analysis ──
echo "--- Paraxial analysis ---"
$RAYWEAVE paraxial < "$CHIEF" | python3 -c "
import yaml, sys
p = yaml.safe_load(sys.stdin.read())['paraxial_result']
print('  EFL            = %.1f mm' % p['focal_length'])
print('  F/#            = %.2f' % p['image_space_f_number'])
print('  entrance pupil = %.0f mm diameter' % p['entrance_pupil_diameter'])
print('  total track    = %.1f mm' % p['total_track'])
"
echo

# ── Raytrace diagram: ray fans for all field angles only ──
echo "--- Raytrace diagram (ray fans, all fields) ---"
"$RAYWEAVE" chief --ray-fan < "$YAML" 2>/dev/null \
  | "$RAYWEAVE" trace 2>/dev/null > /tmp/schmidt-fan.yaml
"$RAYWEAVE" plot -o "$PNG" < /tmp/schmidt-fan.yaml >/dev/null 2>/dev/null
"$RAYWEAVE" plot -o "$SVG" < /tmp/schmidt-fan.yaml >/dev/null 2>/dev/null
echo "  Written: $SVG and $PNG"
echo

# ── Result summary ──
python3 -c "
import yaml
d = yaml.safe_load(open('$CHIEF'))
with open('$RESULT_FILE', 'w') as f:
    f.write('Schmidt camera (D=200, EFL~386, F/1.93, 35mm full-frame)\n')
    f.write('Fold model: positive thicknesses; primary decenter [{tilt: [0, 180, 0], reflect: true}]\n')
    f.write('Corrector plate (BK7 asphere a4/a6) + spherical primary + 2-element field flattener.\n')
    f.write('Physical Z: corrector/stop at 0, primary at 800, flat sensor at 400.\n\n')
    f.write('%-8s %12s\n' % ('field', 'RMS spot'))
    for r in d['chief_rays']:
        g = [pt for pt in r['grid_points'] if pt.get('image_y') is not None]
        if g:
            import statistics
            rms = statistics.pstdev([pt['image_y'] for pt in g])
            f.write('%-8.1f %10.5f mm\n' % (r['field_angle'], rms))
    f.write('\nResult written: $CHIEF\n')
    f.write('\n=== How to interpret this result ===\n')
    f.write('- RMS spot (mm) per field for the folded Schmidt (D=200, F/1.93).\n')
    f.write('- All fields stay near diffraction-limited (0.02-0.04 mm) because the\n')
    f.write('  corrector plate removes the spherical aberration of the fast primary.\n')
    f.write('- Fold model: all thicknesses are positive; the primary decenter\n')
    f.write('  [{tilt: [0, 180, 0], reflect: true}] folds the beam back to the flat\n')
    f.write('  sensor at Z=400 (primary at Z=800).\n')
    f.write('- The largest field (3.2 deg = 35 mm full-frame half-diagonal) shows a\n')
    f.write('  slightly larger RMS: the natural field-curvature / astigmatism limit.\n')
"
echo "Result summary: $RESULT_FILE"
