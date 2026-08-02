#!/bin/bash
set -euo pipefail

# =============================================================================
# optimize-demo.bash — DLS optimisation of a deliberately degraded triplet
#
# Purpose: show how `rayweave optimize` (damped least-squares) recovers a
# good lens from a broken starting point. us2645157-degraded.yaml has the
# US2645157 triplet curvatures distorted so every field is badly out of focus.
#
# Steps
#   1. optimize --verbose : run DLS -> optimize-demo-result.yaml
#   2. plot               : PNG diagrams of the initial (degraded) and
#                           optimised layouts
#   3. chief              : re-evaluate the RMS spot radius per field,
#                           before vs after, and write optimize-demo-result.txt
#
# How to read the result
#   - RMS before/after is the geometric RMS spot radius (mm) per field.
#   - Pass gate: on-axis (f0) RMS < 0.3 mm after optimisation.
#   - init vs opt PNGs: the beams collapse onto the image after optimisation.
# =============================================================================

# Resolve the script's own directory so the demo runs from any CWD
# (repo root, `cd samples`, or a copied location). All data files are read
# from and all outputs are written to this directory.
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

YAML="$SCRIPT_DIR/us2645157-degraded.yaml"
OUTDIR="$SCRIPT_DIR"
OPT_RESULT="$OUTDIR/optimize-demo-result.yaml"
OPT_WITH_CHIEF="$OUTDIR/optimize-demo-with-chief.yaml"
RESULT_FILE="$OUTDIR/optimize-demo-result.txt"

# CLI options
CLEAN=false
while [[ $# -gt 0 ]]; do
  case "$1" in
    --clean) CLEAN=true; shift ;;
    *) echo "Unknown option: $1"; exit 1 ;;
  esac
done

# Clean-only mode: remove generated files and exit
if [ "$CLEAN" = true ]; then
  echo "=== Cleaning up generated files ==="
  rm -f "$OPT_RESULT" "$OPT_WITH_CHIEF" "$RESULT_FILE"
  rm -f "$OUTDIR/optimize-demo-init.png" "$OUTDIR/optimize-demo-opt.png"
  echo "  Removed: PNGs, $OPT_RESULT, $OPT_WITH_CHIEF, $RESULT_FILE"
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

# ── Interpretation notes: appended to the result file on exit, so they stay
# as the closing section even when a gate check exits early. ──
append_interpretation() {
cat >> "$RESULT_FILE" <<'EOF'

=== How to interpret this result ===
- "RMS before / RMS after" is the geometric RMS spot radius (mm) of the
  chief-ray pupil grid, before and after DLS optimisation.
- f0/f1/f2 = 0/16/24 deg fields. A ✓ means the spot shrank.
- Pass gate: on-axis (f0) RMS < 0.3 mm after optimisation.
- If "Optimization failed" appears, DLS did not converge; try more
  iterations or relax the merit weights in us2645157-degraded.yaml.
EOF
}
trap append_interpretation EXIT

echo "=== Optimize demo: degraded US2645157 triplet ==="
echo

echo "--- Initial state (degraded curvatures) ---"
echo "=== DLS optimization ==="
$RAYWEAVE optimize --verbose < "$YAML" > "$OPT_RESULT"
echo

echo "--- PNG diagrams ---"
echo "=== Initial diagram ==="
$RAYWEAVE chief --clear-aperture --shrink --ray-fan < "$YAML" | $RAYWEAVE chief --marginal-rays \
  | $RAYWEAVE trace \
  | $RAYWEAVE plot -o "$OUTDIR/optimize-demo-init.png" >/dev/null
echo "Written: $OUTDIR/optimize-demo-init.png"
echo

echo "=== Optimized diagram ==="
python3 -c "
import sys, yaml
d = yaml.safe_load(sys.stdin)
d['chief'] = {'fields': [{'angle': 0.0, 'direction': [0, 1]}, {'angle': 16.0, 'direction': [0, 1]}, {'angle': 24.0, 'direction': [0, 1]}], 'reference_surface': 8, 'num_rays': 512, 'grid_type': 'hex', 'dump_map': False}
yaml.safe_dump(d, sys.stdout, sort_keys=False)
" < "$OPT_RESULT" > "$OPT_WITH_CHIEF"
$RAYWEAVE chief --clear-aperture --shrink --ray-fan < "$OPT_WITH_CHIEF" | $RAYWEAVE chief --marginal-rays \
  | $RAYWEAVE trace \
  | $RAYWEAVE plot -o "$OUTDIR/optimize-demo-opt.png" >/dev/null
echo "Written: $OUTDIR/optimize-demo-opt.png"
echo

# ── Spot RMS comparison ──
rms_field() {
  local yaml_file=$1 fi=$2
  $RAYWEAVE chief < "$yaml_file" 2>/dev/null | python3 -c "
import sys, yaml
with open('/dev/stdin') as f:
    data = yaml.safe_load(f)
if data and 'chief_rays' in data and $fi < len(data['chief_rays']):
    ss = data['chief_rays'][$fi].get('spot_stats', {})
    rms = ss.get('rms_r', -1)
    if rms > 0: print(rms)
    else: print(-1)
else: print(-1)
"
}
{
  echo "=== Spot RMS Comparison ==="
  printf "  %-8s %6s  %10s  %10s\n" "Phase" "Field" "RMS before" "RMS after"
  printf "  %-8s %6s  %10s  %10s\n" "-----" "-----" "--------" "--------"
  for fi in 0 1 2; do
    rms_before=$(rms_field "$YAML" "$fi")
    rms_after=$(rms_field "$OPT_WITH_CHIEF" "$fi")
    mark=""
    if [ "$rms_before" != "-1" ] && [ "$rms_after" != "-1" ]; then
      improved=$(python3 -c "print('1' if $rms_after < $rms_before else '0')")
      [ "$improved" = "1" ] && mark="   ✓" || mark="   ✗"
    fi
    printf "  %-8s %6s  %10.4f  %10.4f%s\n" "optimize" "f$fi" "$rms_before" "$rms_after" "$mark"
  done
  echo
} | tee "$RESULT_FILE"

# ── On-axis RMS threshold check ──
THRESHOLD=0.3
printf "  (threshold = $THRESHOLD mm — on-axis RMS must be below this)\n"
rms_onaxis=$(rms_field "$OPT_WITH_CHIEF" 0)
if [ "$rms_onaxis" != "-1" ] && [ "$(python3 -c "print('1' if $rms_onaxis >= $THRESHOLD else '0')")" = "1" ]; then
  msg="  >>> Optimization failed: on-axis RMS = $(printf '%.4f' "$rms_onaxis") mm >= $THRESHOLD mm"
  echo "$msg" | tee -a "$RESULT_FILE"
  exit 1
else
  msg="  >>> Optimization passed: on-axis RMS = $(printf '%.4f' "$rms_onaxis") mm < $THRESHOLD mm"
  echo "$msg" | tee -a "$RESULT_FILE"
fi
