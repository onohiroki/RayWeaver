#!/bin/bash
set -euo pipefail

YAML="samples/us2645157-degraded.yaml"
OUTDIR="samples"
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

echo "=== Optimize demo: degraded US2645157 triplet ==="
echo

echo "--- Initial state (degraded curvatures) ---"
echo "=== DLS optimization ==="
./rayweave optimize --verbose < "$YAML" > "$OPT_RESULT"
echo

echo "--- PNG diagrams ---"
echo "=== Initial diagram ==="
./rayweave chief --clear-aperture --shrink --ray-fan < "$YAML" | ./rayweave chief --marginal-rays \
  | ./rayweave trace \
  | ./rayweave plot -o "$OUTDIR/optimize-demo-init.png" >/dev/null
echo "Written: $OUTDIR/optimize-demo-init.png"
echo

echo "=== Optimized diagram ==="
python3 -c "
import sys, yaml
d = yaml.safe_load(sys.stdin)
d['chief'] = {'fields': [{'angle': 0.0, 'direction': [0, 1]}, {'angle': 16.0, 'direction': [0, 1]}, {'angle': 24.0, 'direction': [0, 1]}], 'reference_surface': 8, 'num_rays': 512, 'grid_type': 'hex', 'dump_map': False}
yaml.safe_dump(d, sys.stdout, sort_keys=False)
" < "$OPT_RESULT" > "$OPT_WITH_CHIEF"
./rayweave chief --clear-aperture --shrink --ray-fan < "$OPT_WITH_CHIEF" | ./rayweave chief --marginal-rays \
  | ./rayweave trace \
  | ./rayweave plot -o "$OUTDIR/optimize-demo-opt.png" >/dev/null
echo "Written: $OUTDIR/optimize-demo-opt.png"
echo

# ── Spot RMS comparison ──
rms_field() {
  local yaml_file=$1 fi=$2
  ./rayweave chief < "$yaml_file" 2>/dev/null | python3 -c "
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
