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
  rm -f "$OPT_RESULT" "$RESULT_FILE"
  rm -f "$OUTDIR/optimize-demo-init.png" "$OUTDIR/optimize-demo-opt.png"
  echo "  Removed: PNGs, $OPT_RESULT, $RESULT_FILE"
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
$RAYWEAVE chief --clear-aperture --shrink --ray-fan < "$OPT_RESULT" | $RAYWEAVE chief --marginal-rays \
  | $RAYWEAVE trace \
  | $RAYWEAVE plot -o "$OUTDIR/optimize-demo-opt.png" >/dev/null
echo "Written: $OUTDIR/optimize-demo-opt.png"
echo

# ── Spot RMS comparison ──
rms_field() {
  local yaml_file=$1 fi=$2
  $RAYWEAVE chief < "$yaml_file" 2>/dev/null \
    | $RAYWEAVE query -r "chief_rays[$fi].spot_stats.rms_r"
}
{
  echo "=== Spot RMS Comparison ==="
  printf "  %-8s %6s  %10s  %10s\n" "Phase" "Field" "RMS before" "RMS after"
  printf "  %-8s %6s  %10s  %10s\n" "-----" "-----" "--------" "--------"
  for fi in 0 1 2; do
    rms_before=$(rms_field "$YAML" "$fi")
    rms_after=$(rms_field "$OPT_RESULT" "$fi")
    mark=""
    if [ "$rms_before" != "-1" ] && [ "$rms_after" != "-1" ]; then
      if $RAYWEAVE query --gate "a < b" --set a="$rms_after" --set b="$rms_before" < /dev/null > /dev/null; then
        mark="   ✓"
      else
        mark="   ✗"
      fi
    fi
    printf "  %-8s %6s  %10.4f  %10.4f%s\n" "optimize" "f$fi" "$rms_before" "$rms_after" "$mark"
  done
  echo
} | tee "$RESULT_FILE"

# ── On-axis RMS threshold check ──
THRESHOLD=0.3
printf "  (threshold = $THRESHOLD mm — on-axis RMS must be below this)\n"
rms_onaxis=$(rms_field "$OPT_RESULT" 0)
if [ "$rms_onaxis" != "-1" ] && $RAYWEAVE query --gate "rms >= $THRESHOLD" --set rms="$rms_onaxis" < /dev/null > /dev/null; then
  msg="  >>> Optimization failed: on-axis RMS = $(printf '%.4f' "$rms_onaxis") mm >= $THRESHOLD mm"
  echo "$msg" | tee -a "$RESULT_FILE"
  exit 1
else
  msg="  >>> Optimization passed: on-axis RMS = $(printf '%.4f' "$rms_onaxis") mm < $THRESHOLD mm"
  echo "$msg" | tee -a "$RESULT_FILE"
fi
