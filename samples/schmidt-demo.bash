#!/bin/bash
set -euo pipefail

# Schmidt camera demo — folded primary + corrector + field flattener
#
# Evaluates ONLY the final system (samples/schmidt-flattener.yaml).  The plot
# shows ray fans for all field angles (no pupil-grid rays).

# CLI options
CLEAN=false
while [[ $# -gt 0 ]]; do
  case "$1" in
    --clean) CLEAN=true; shift ;;
    *) echo "Unknown option: $1"; exit 1 ;;
  esac
done

OUTDIR="samples"
RAYWEAVE="${RAYWEAVE:-./rayweave}"
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
"
echo "Result summary: $RESULT_FILE"
