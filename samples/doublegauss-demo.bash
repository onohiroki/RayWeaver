#!/bin/bash
set -euo pipefail

# CLI options
CLEAN=false
while [[ $# -gt 0 ]]; do
  case "$1" in
    --clean) CLEAN=true; shift ;;
    *) echo "Unknown option: $1"; exit 1 ;;
  esac
done

YAML="samples/doublegauss-init.yaml"
OUTDIR="samples"
OPT_RESULT="$OUTDIR/doublegauss-result.yaml"
OPT_LOG="$OUTDIR/doublegauss-log.jsonl"
RESULT_FILE="$OUTDIR/doublegauss-demo-result.txt"
RAYWEAVE="${RAYWEAVE:-./rayweave}"

# Clean-only mode
if [ "$CLEAN" = true ]; then
  echo "=== Cleaning up generated files ==="
  rm -f "$OPT_RESULT" "$OPT_LOG" "$RESULT_FILE"
  rm -f "$OUTDIR"/doublegauss-init.png "$OUTDIR"/doublegauss-opt.png
  echo "  Removed generated files"
  exit 0
fi

echo "=== Double-Gauss optimization demo: 6-element 50 mm f/2.8 standard lens ==="
echo
echo "Optical system:"
echo "  6-element symmetric double-Gauss (front: crown/flint/meniscus |"
echo "  stop | meniscus/flint/crown). Total 14 surfaces."
echo "  Fields: 0 deg / 10 deg / 16 deg / 23 deg (35 mm format half-diagonal)"
echo "  Wavelengths: F (486nm) / d (588nm) / C (656nm)"
echo "  36 variables: curvatures, thicknesses, glass nd/vd"
echo "  Constraints: abs_efl band 50±0.5 mm, EPD band 17.86±0.3 mm"
echo "  Merit: spot RMS (12 terms) + lateral colour + OPD RMS"
echo "  Target threshold: on-axis RMS < 0.1 mm"
echo

# ── Evaluate before state ──
echo "--- Initial state ---"
EFL_BEFORE=$( $RAYWEAVE paraxial < "$YAML" 2>/dev/null | python3 -c "import sys,yaml; d=yaml.safe_load(sys.stdin); p=d.get('paraxial_result',{}); print(p.get('focal_length',-1))" )
FNO_BEFORE=$( $RAYWEAVE paraxial < "$YAML" 2>/dev/null | python3 -c "import sys,yaml; d=yaml.safe_load(sys.stdin); p=d.get('paraxial_result',{}); print(p.get('image_space_f_number',-1))" )
BEFORE_CHIEF=$( $RAYWEAVE chief < "$YAML" 2>/dev/null )

rms_field() {
  local chief="$1" fi="$2"
  echo "$chief" | python3 -c "import sys,yaml; d=yaml.safe_load(sys.stdin); r=d['chief_rays']; print(r[$fi].get('spot_stats',{}).get('rms_r',-1))"
}
distortion() {
  local chief="$1" fi="$2" efl="$3"
  echo "$chief" | python3 -c "
import sys, yaml, math
d=yaml.safe_load(sys.stdin)
r=d['chief_rays'][$fi]
ih=r.get('image_height',[0,0,0])[1]
fa=math.radians(r.get('field_angle',0))
ph=math.tan(fa)*$efl if fa else 0
print(100*(ih-ph)/ph if ph else 0)
"
}

for fi in 0 1 2 3; do
  ang=$(echo "$BEFORE_CHIEF" | python3 -c "import sys,yaml; d=yaml.safe_load(sys.stdin); print(d['chief_rays'][$fi].get('field_angle','?'))")
  printf "  field %s°  RMS = %.4f mm\n" "$ang" "$(rms_field "$BEFORE_CHIEF" "$fi")"
done
printf "  EFL = %.2f mm   f/%.2f\n" "$EFL_BEFORE" "$FNO_BEFORE"
echo

# ── DLS optimization ──
echo "=== DLS optimization (256 rays, 500 iterations) ==="
$RAYWEAVE optimize --verbose --log "$OPT_LOG" < "$YAML" > "$OPT_RESULT"
echo "  Written: $OPT_RESULT"
echo

# ── Evaluate after state ──
echo "--- Optimized state ---"
EFL_AFTER=$( $RAYWEAVE paraxial < "$OPT_RESULT" 2>/dev/null | python3 -c "import sys,yaml; d=yaml.safe_load(sys.stdin); p=d.get('paraxial_result',{}); print(p.get('focal_length',-1))" )
FNO_AFTER=$( $RAYWEAVE paraxial < "$OPT_RESULT" 2>/dev/null | python3 -c "import sys,yaml; d=yaml.safe_load(sys.stdin); p=d.get('paraxial_result',{}); print(p.get('image_space_f_number',-1))" )
AFTER_CHIEF=$( $RAYWEAVE chief < "$OPT_RESULT" 2>/dev/null )

for fi in 0 1 2 3; do
  ang=$(echo "$AFTER_CHIEF" | python3 -c "import sys,yaml; d=yaml.safe_load(sys.stdin); print(d['chief_rays'][$fi].get('field_angle','?'))")
  printf "  field %s°  RMS = %.4f mm\n" "$ang" "$(rms_field "$AFTER_CHIEF" "$fi")"
done
printf "  EFL = %.2f mm   f/%.2f\n" "$EFL_AFTER" "$FNO_AFTER"
echo

# ── Stage summary from log ──
echo "--- Stage summary ---"
python3 -c "
import json
merits=[]; status=''
for line in open('$OPT_LOG'):
    line=line.strip()
    if not line.startswith('{'): continue
    d=json.loads(line)
    if 'merit' in d: merits.append(d['merit'])
    if 'status' in d: status=d['status']
first=merits[0] if merits else 0
last=merits[-1] if merits else 0
improve=(1-last/first)*100 if first>0 else 0
print(f'  Status:      {status}')
print(f'  Iterations:  {len(merits)}')
print(f'  Merit init:  {first:.6e}')
print(f'  Merit final: {last:.6e}')
print(f'  Improvement: {improve:.1f}%')
"
echo

# ── Result file ──
{
  echo "=== Double-Gauss 6-element f/2.8 — optimization result ==="
  echo
  echo "--- Lens parameters ---"
  printf "  %-12s %12s %12s\n" "Quantity" "before" "after"
  printf "  %-12s %12s %12s\n" "---------" "-------" "------"
  printf "  %-12s %12.3f %12.3f\n" "EFL (mm)" "$EFL_BEFORE" "$EFL_AFTER"
  printf "  %-12s %12.3f %12.3f\n" "f/#" "$FNO_BEFORE" "$FNO_AFTER"
  echo
  echo "--- Spot RMS (d=587.6nm, chief evaluation) ---"
  printf "  %-6s %6s  %10s  %10s\n" "Field" "Angle" "before" "after"
  printf "  %-6s %6s  %10s  %10s\n" "-----" "-----" "--------" "-------"
  for fi in 0 1 2 3; do
    ang=$(echo "$BEFORE_CHIEF" | python3 -c "import sys,yaml; d=yaml.safe_load(sys.stdin); print(d['chief_rays'][$fi].get('field_angle','?'))")
    bef=$(rms_field "$BEFORE_CHIEF" "$fi")
    aft=$(rms_field "$AFTER_CHIEF" "$fi")
    printf "  %-6s %5s°  %10.4f  %10.4f\n" "f$fi" "$ang" "$bef" "$aft"
  done
  echo
  echo "--- Distortion (d=587.6nm, chief vs paraxial image height) ---"
  printf "  %-6s %6s  %10s  %10s\n" "Field" "Angle" "before" "after"
  printf "  %-6s %6s  %10s  %10s\n" "-----" "-----" "--------" "-------"
  for fi in 0 1 2 3; do
    ang=$(echo "$BEFORE_CHIEF" | python3 -c "import sys,yaml; d=yaml.safe_load(sys.stdin); print(d['chief_rays'][$fi].get('field_angle','?'))")
    d1=$(distortion "$BEFORE_CHIEF" "$fi" "$EFL_BEFORE")
    d2=$(distortion "$AFTER_CHIEF" "$fi" "$EFL_AFTER")
    printf "  %-6s %5s°  %9.2f%%  %9.2f%%\n" "f$fi" "$ang" "$d1" "$d2"
  done
  echo
} > "$RESULT_FILE"

# ── Console summary ──
echo "--- Lens parameters ---"
printf "  %-12s %12s %12s\n" "Quantity" "before" "after"
printf "  %-12s %12.3f %12.3f\n" "EFL (mm)" "$EFL_BEFORE" "$EFL_AFTER"
printf "  %-12s %12.3f %12.3f\n" "f/#" "$FNO_BEFORE" "$FNO_AFTER"
echo
echo "  (full spot RMS and distortion tables in $RESULT_FILE)"

# ── Merit breakdown ──
if [ -f "$OPT_LOG" ]; then
  echo "--- Merit breakdown (final state) ---"
  python3 -c "
import json
for line in open('$OPT_LOG'):
    line=line.strip()
    if not line.startswith('{'): continue
    d=json.loads(line)
    if d.get('event') == 'breakdown':
        for k, v in d['terms'].items():
            print(f'  {k}: {v:.6e}')
" 2>/dev/null || true
  echo
fi

# ── Diagrams ──
echo "=== Diagrams ==="
$RAYWEAVE chief --clear-aperture --shrink --ray-fan < "$YAML" \
  | $RAYWEAVE chief --marginal-rays \
  | $RAYWEAVE trace \
  | $RAYWEAVE plot -o "$OUTDIR/doublegauss-init.png" >/dev/null 2>&1 || true
echo "Written: $OUTDIR/doublegauss-init.png"

$RAYWEAVE chief --clear-aperture --shrink --ray-fan < "$OPT_RESULT" \
  | $RAYWEAVE chief --marginal-rays \
  | $RAYWEAVE trace \
  | $RAYWEAVE plot -o "$OUTDIR/doublegauss-opt.png" >/dev/null 2>&1 || true
echo "Written: $OUTDIR/doublegauss-opt.png"
echo

# ── Threshold check: on-axis RMS < 0.1 mm ──
THRESHOLD=0.1
printf "  (threshold = %.1f mm — on-axis RMS must be below this)\n" "$THRESHOLD"
rms_onaxis=$(rms_field "$AFTER_CHIEF" 0)
if [ "$rms_onaxis" != "-1" ] && [ "$(python3 -c "print('1' if $rms_onaxis >= $THRESHOLD else '0')")" = "1" ]; then
  msg="  >>> Optimization failed: on-axis RMS = $(printf '%.4f' "$rms_onaxis") mm >= $THRESHOLD mm"
  echo "$msg" | tee -a "$RESULT_FILE"
  exit 1
else
  msg="  >>> Optimization passed: on-axis RMS = $(printf '%.4f' "$rms_onaxis") mm < $THRESHOLD mm"
  echo "$msg" | tee -a "$RESULT_FILE"
fi

echo
echo "=== Iteration log saved: $OPT_LOG ==="
echo "=== Results saved: $RESULT_FILE ==="
