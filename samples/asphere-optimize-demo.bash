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

YAML="samples/asphere-optimize.yaml"
OUTDIR="samples"
SPHERICAL_YAML="$OUTDIR/asphere-optimize-spherical.yaml"
SPH_RESULT="$OUTDIR/asphere-optimize-spherical-result.yaml"
SPH_LOG="$OUTDIR/asphere-optimize-spherical-log.jsonl"
ASP_RESULT="$OUTDIR/asphere-optimize-result.yaml"
ASP_LOG="$OUTDIR/asphere-optimize-log.jsonl"
RESULT_FILE="$OUTDIR/asphere-optimize-demo-result.txt"
RAYWEAVE="${RAYWEAVE:-./rayweave}"

# Clean-only mode: remove generated files and exit
if [ "$CLEAN" = true ]; then
  echo "=== Cleaning up generated files ==="
  rm -f "$SPHERICAL_YAML" "$SPH_RESULT" "$SPH_LOG" "$ASP_RESULT" "$ASP_LOG" "$RESULT_FILE"
  rm -f "$OUTDIR"/asphere-optimize-init.png "$OUTDIR"/asphere-optimize-spherical.png "$OUTDIR"/asphere-optimize-opt.png
  echo "  Removed generated files"
  exit 0
fi

echo "=== Asphere optimization demo: singlet with aspheric front surface ==="
echo
echo "Two-stage comparison:"
echo "  Stage 1 (spherical): optimize s1/s2 curvature only — the asphere"
echo "    coefficients (conic, a4, a6) stay fixed at 0."
echo "  Stage 2 (asphere):   additionally optimize conic / a4 / a6."
echo
echo "Optical system:"
echo "  Surface 1: asphere_polynomial (N-BK7),  Surface 2: sphere,  Surface 3: image"
echo "  Fields: 0 deg / 5 deg, wavelength d (587.6 nm)"
echo

# ── Stage 1: spherical-only variant (asphere coefficients are NOT variables) ──
python3 -c "
import yaml
d = yaml.safe_load(open('$YAML'))
d['optimization']['variables'] = [
    v for v in d['optimization']['variables']
    if v['target']['param'] not in ('conic', 'a4', 'a6', 'coefficient_0', 'coefficient_1')
]
yaml.safe_dump(d, open('$SPHERICAL_YAML', 'w'), sort_keys=False)
"
echo "=== Stage 1: optimize with curvature only (no asphere variables) ==="
$RAYWEAVE optimize --log "$SPH_LOG" < "$SPHERICAL_YAML" > "$SPH_RESULT"
echo "  Written: $SPH_RESULT"
echo

echo "=== Stage 2: optimize with asphere variables (conic, a4, a6) ==="
$RAYWEAVE optimize --verbose --log "$ASP_LOG" < "$YAML" > "$ASP_RESULT"
echo "  Written: $ASP_RESULT"
echo

# ── Final merit / status from both logs (the log's first "merit" line is
# already after the first step, so only the final merit is shown) ──
log_summary() {
  local logfile=$1
  python3 -c "
import json
merits = []; status = ''
for line in open('$logfile'):
    line = line.strip()
    if not line.startswith('{'): continue
    d = json.loads(line)
    if 'merit' in d: merits.append(d['merit'])
    if 'status' in d: status = d['status']
last = merits[-1] if merits else 0
print(f'{last:.6e} {status}')
"
}
read -r SPH_AFTER SPH_STATUS < <(log_summary "$SPH_LOG")
read -r ASP_AFTER ASP_STATUS < <(log_summary "$ASP_LOG")

# ── Asphere coefficients (before / spherical-opt / asphere-opt) ──
extract_coeffs() {
  local yaml="$1"
  python3 -c "
import yaml
d = yaml.safe_load(open('$yaml'))
s = d['configs'][0]['surfaces'][0]
c = s.get('coefficients', [0, 0])
print(f'{s.get(\"conic\", 0):.6f}')
print(f'{c[0] if len(c) > 0 else 0:.6e}')
print(f'{c[1] if len(c) > 1 else 0:.6e}')
"
}
readarray -t COEF_BEFORE < <(extract_coeffs "$YAML")
readarray -t COEF_SPH    < <(extract_coeffs "$SPH_RESULT")
readarray -t COEF_ASP    < <(extract_coeffs "$ASP_RESULT")

# ── Spot RMS (before / spherical-opt / asphere-opt) ──
BEFORE_CHIEF=$($RAYWEAVE chief < "$YAML" 2>/dev/null)
SPH_CHIEF=$($RAYWEAVE chief < "$SPH_RESULT" 2>/dev/null)
ASP_CHIEF=$($RAYWEAVE chief < "$ASP_RESULT" 2>/dev/null)
rms_field() {
  local chief="$1" fi="$2"
  echo "$chief" | python3 -c "import sys,yaml; d=yaml.safe_load(sys.stdin); r=d['chief_rays']; print(r[$fi].get('spot_stats',{}).get('rms_r',-1))"
}

# OPD RMS = RMS of (OPL - mean OPL) over the accepted pupil-grid rays.
opd_field() {
  local chief="$1" fi="$2"
  echo "$chief" | python3 -c "
import sys, yaml, math
d = yaml.safe_load(sys.stdin)
cr = d['chief_rays'][$fi]
op = [g['opl'] for g in cr['grid_points'] if g.get('opl') is not None and g.get('image_x') is not None]
if not op:
    print(-1)
else:
    m = sum(op)/len(op)
    print(math.sqrt(sum((o-m)**2 for o in op)/len(op)))
"
}

{
  echo "=== Asphere optimization demo: two-stage comparison ==="
  echo
  echo "--- Asphere coefficient (surface 1) ---"
  printf "  %-8s %14s %14s %14s\n" "Coef" "before" "spherical-opt" "asphere-opt"
  printf "  %-8s %14s %14s %14s\n" "----" "-------" "-------------" "-----------"
  printf "  %-8s %14s %14s %14s\n" "conic" "${COEF_BEFORE[0]}" "${COEF_SPH[0]}" "${COEF_ASP[0]}"
  printf "  %-8s %14s %14s %14s\n" "a4"    "${COEF_BEFORE[1]}" "${COEF_SPH[1]}" "${COEF_ASP[1]}"
  printf "  %-8s %14s %14s %14s\n" "a6"    "${COEF_BEFORE[2]}" "${COEF_SPH[2]}" "${COEF_ASP[2]}"
  echo
  echo "--- Spot RMS (d=587.6nm, chief evaluation) ---"
  printf "  %-6s %6s  %10s  %10s  %10s\n" "Field" "Angle" "before" "spherical" "asphere"
  printf "  %-6s %6s  %10s  %10s  %10s\n" "-----" "-----" "--------" "---------" "-------"
  for fi in 0 1; do
    ang=$(echo "$BEFORE_CHIEF" | python3 -c "import sys,yaml; d=yaml.safe_load(sys.stdin); print(d['chief_rays'][$fi].get('field_angle','?'))")
    printf "  %-6s %5s°  %10.4f  %10.4f  %10.4f\n" "f$fi" "$ang" \
      "$(rms_field "$BEFORE_CHIEF" "$fi")" \
      "$(rms_field "$SPH_CHIEF" "$fi")" \
      "$(rms_field "$ASP_CHIEF" "$fi")"
  done
  echo
  echo "--- OPD RMS (d=587.6nm, chief evaluation) ---"
  printf "  %-6s %6s  %12s  %12s  %12s\n" "Field" "Angle" "before" "spherical" "asphere"
  printf "  %-6s %6s  %12s  %12s  %12s\n" "-----" "-----" "--------" "---------" "-------"
  for fi in 0 1; do
    ang=$(echo "$BEFORE_CHIEF" | python3 -c "import sys,yaml; d=yaml.safe_load(sys.stdin); print(d['chief_rays'][$fi].get('field_angle','?'))")
    printf "  %-6s %5s°  %12.3e  %12.3e  %12.3e\n" "f$fi" "$ang" \
      "$(opd_field "$BEFORE_CHIEF" "$fi")" \
      "$(opd_field "$SPH_CHIEF" "$fi")" \
      "$(opd_field "$ASP_CHIEF" "$fi")"
  done
  echo
} | tee "$RESULT_FILE"

# ── Console summary ──
echo "--- Stage summaries (DLS-internal merit; spot RMS above is the reference) ---"
printf "  %-18s status=%-15s final merit=%.3e\n" "spherical-opt" "$SPH_STATUS" "$SPH_AFTER"
printf "  %-18s status=%-15s final merit=%.3e\n" "asphere-opt"   "$ASP_STATUS" "$ASP_AFTER"
echo

# ── Merit breakdown (asphere stage) from the log ──
if [ -f "$ASP_LOG" ]; then
  echo "--- Merit breakdown (asphere stage, final state) ---"
  python3 -c "
import json
for line in open('$ASP_LOG'):
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
  | $RAYWEAVE plot -o "$OUTDIR/asphere-optimize-init.png" >/dev/null 2>&1 || true
echo "Written: $OUTDIR/asphere-optimize-init.png"

$RAYWEAVE chief --clear-aperture --shrink --ray-fan < "$SPH_RESULT" \
  | $RAYWEAVE chief --marginal-rays \
  | $RAYWEAVE trace \
  | $RAYWEAVE plot -o "$OUTDIR/asphere-optimize-spherical.png" >/dev/null 2>&1 || true
echo "Written: $OUTDIR/asphere-optimize-spherical.png"

$RAYWEAVE chief --clear-aperture --shrink --ray-fan < "$ASP_RESULT" \
  | $RAYWEAVE chief --marginal-rays \
  | $RAYWEAVE trace \
  | $RAYWEAVE plot -o "$OUTDIR/asphere-optimize-opt.png" >/dev/null 2>&1 || true
echo "Written: $OUTDIR/asphere-optimize-opt.png"
echo

# ── Threshold check: asphere stage on-axis RMS ──
THRESHOLD=0.3
printf "  (threshold = $THRESHOLD mm — asphere-opt on-axis RMS must be below this)\n"
rms_onaxis=$(rms_field "$ASP_CHIEF" 0)
if [ "$rms_onaxis" != "-1" ] && [ "$(python3 -c "print('1' if $rms_onaxis >= $THRESHOLD else '0')")" = "1" ]; then
  msg="  >>> Optimization failed: asphere on-axis RMS = $(printf '%.4f' "$rms_onaxis") mm >= $THRESHOLD mm"
  echo "$msg" | tee -a "$RESULT_FILE"
  exit 1
else
  msg="  >>> Optimization passed: asphere on-axis RMS = $(printf '%.4f' "$rms_onaxis") mm < $THRESHOLD mm"
  echo "$msg" | tee -a "$RESULT_FILE"
fi

echo
echo "=== Iteration logs: $SPH_LOG, $ASP_LOG ==="
echo "=== Results saved: $RESULT_FILE ==="
