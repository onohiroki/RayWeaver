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

YAML="samples/simple-zoom.yaml"
OUTDIR="samples"
RESULT="$OUTDIR/simple-zoom-optimized.yaml"
LOG="$OUTDIR/simple-zoom-log.jsonl"
RESULT_FILE="$OUTDIR/simple-zoom-demo-result.txt"
RAYWEAVE="${RAYWEAVE:-./rayweave}"

# Clean-only mode: remove generated files and exit
if [ "$CLEAN" = true ]; then
  echo "=== Cleaning up generated files ==="
  for cfg in config0 config1 config2; do
    rm -f "$OUTDIR/simple-zoom-${cfg}-init-rays.png"
    rm -f "$OUTDIR/simple-zoom-${cfg}-opt-rays.png"
  done
  rm -f "$RESULT" "$LOG" "$RESULT_FILE"
  echo "  Removed: PNGs, $RESULT, $LOG, $RESULT_FILE"
  exit 0
fi

echo "=== Simple Zoom Lens Optimization Demo ==="
echo
echo "Configs: config0 (S2=20, S4=80), config1 (S2=50, S4=50), config2 (S2=80, S4=20)"
echo
echo "Constraint: config0 air gap between lens 1 and lens 2 >= 5mm"
echo

# ── 1. DLS multi-config optimization ──
echo "=== DLS Multi-Config Optimization ==="
echo "  Merit: on-axis + off-axis spot RMS (center weight 1.0, mid 0.3, edge 0.1)"
$RAYWEAVE optimize --verbose --log "$LOG" < "$YAML" > "$RESULT"

echo
echo "--- Optimization results ---"
echo "  (see stderr for iteration details)"
echo

# ── 2. Ray-overlaid layout (before optimization) ──
echo "=== Initial ray-overlaid layout ==="
for cfg in config0 config1 config2; do
  echo "  Config: $cfg"
  cat "$YAML" \
    | $RAYWEAVE chief --clear-aperture --ray-fan --config "$cfg" \
    | $RAYWEAVE chief --marginal-rays --config "$cfg" \
    | $RAYWEAVE trace --config "$cfg" \
    | $RAYWEAVE plot --config "$cfg" -o "$OUTDIR/simple-zoom-${cfg}-init-rays.png" 2>/dev/null
  echo "    PNG: $OUTDIR/simple-zoom-${cfg}-init-rays.png"
done
echo

# ── 3. Ray-overlaid layout (after optimization) ──
echo "=== Optimized ray-overlaid layout ==="
for cfg in config0 config1 config2; do
  echo "  Config: $cfg"
  cat "$RESULT" \
    | $RAYWEAVE chief --clear-aperture --ray-fan --config "$cfg" \
    | $RAYWEAVE chief --marginal-rays --config "$cfg" \
    | $RAYWEAVE trace --config "$cfg" \
    | $RAYWEAVE plot --config "$cfg" -o "$OUTDIR/simple-zoom-${cfg}-opt-rays.png" 2>/dev/null
  echo "    PNG: $OUTDIR/simple-zoom-${cfg}-opt-rays.png"
done
echo

echo "=== Iteration log saved: $LOG ==="
if [ -f "$LOG" ]; then
  echo "  Log entries:"
  wc -l "$LOG" 2>/dev/null
fi
echo

# ── 4. Focal length (before and after) ──
echo "=== Focal Length (EFL) ==="
get_efl() {
  local yaml_file="$1" cfg="$2"
  cat "$yaml_file" | $RAYWEAVE paraxial --config "$cfg" 2>/dev/null \
    | python3 -c "import sys,yaml; d=yaml.safe_load(sys.stdin); print(d.get('paraxial_result',{}).get('focal_length',-1))"
}
for cfg in config0 config1 config2; do
  efl_before=$(get_efl "$YAML" "$cfg")
  efl_after=$(get_efl "$RESULT" "$cfg")
  printf "  %-8s before=%8.2f mm  after=%8.2f mm\n" "$cfg" "$efl_before" "$efl_after"
done
echo

# ── 5. Spot RMS comparison (before vs after) ──
echo "=== Spot RMS Comparison (on-axis, field 0°) ==="
printf "  %-8s %12s %12s\n" "Config" "RMS before" "RMS after"

# Extract on-axis RMS from chief output (field_angle: 0)
get_onaxis_rms() {
  local yaml_file="$1"
  local cfg="$2"
  # Use python3 to safely parse the YAML chief output
  python3 -c "
import sys, yaml
with open('/dev/stdin') as f:
    data = yaml.safe_load(f)
if data and 'chief_rays' in data:
    for ray in data['chief_rays']:
        if ray.get('field_angle') == 0 or abs(ray.get('field_angle', 1)) < 1e-10:
            ss = ray.get('spot_stats', {})
            rms = ss.get('rms_r', -1)
            if rms > 0:
                print(rms)
                sys.exit(0)
print(-1)
" < <($RAYWEAVE chief --config "$cfg" < "$yaml_file" 2>/dev/null)
}

THRESHOLD=0.3
echo "  (threshold = $THRESHOLD mm — all configs on-axis RMS must be below this)"

failed=false
for cfg in config0 config1 config2; do
  rms_before=$(get_onaxis_rms "$YAML" "$cfg")
  rms_after=$(get_onaxis_rms "$RESULT" "$cfg")
  printf "  %-8s %12.4f %12.4f" "$cfg" "$rms_before" "$rms_after"
  if [ "$rms_before" != "-1" ] && [ "$rms_after" != "-1" ]; then
    if [ "$(python3 -c "print('1' if $rms_after < $rms_before else '0')")" = "1" ]; then
      printf "   ✓"
    else
      printf "   ✗"
    fi
  fi
  echo
  if [ "$rms_after" != "-1" ] && [ "$(python3 -c "print('1' if $rms_after >= $THRESHOLD else '0')")" = "1" ]; then
    failed=true
  fi
done

# ── Save RMS comparison to result file ──
{
  echo "=== Spot RMS Comparison (on-axis, field 0°) ==="
  printf "  %-8s %12s %12s\n" "Config" "RMS before" "RMS after"
  echo "  (threshold = $THRESHOLD mm)"
  for cfg in config0 config1 config2; do
    rms_before=$(get_onaxis_rms "$YAML" "$cfg")
    rms_after=$(get_onaxis_rms "$RESULT" "$cfg")
    printf "  %-8s %12.4f %12.4f\n" "$cfg" "$rms_before" "$rms_after"
  done
  echo
} > "$RESULT_FILE"
echo "  (RMS comparison saved to $RESULT_FILE)"

if [ "$failed" = true ]; then
  echo
  echo "  >>> Optimization failed: not all configs on-axis RMS < $THRESHOLD mm"
  echo "  >>> Optimization failed: not all configs on-axis RMS < $THRESHOLD mm" >> "$RESULT_FILE"
  exit 1
fi
echo
echo "  >>> Optimization passed: all configs on-axis RMS < $THRESHOLD mm"
echo "  >>> Optimization passed: all configs on-axis RMS < $THRESHOLD mm" >> "$RESULT_FILE"

# ── Lens 1-2 gap threshold check (config0) ──
get_gap12() {
  local yaml_file="$1"
  local cfg="$2"
  python3 -c "
import sys, yaml
d = yaml.safe_load(open('$yaml_file'))
for c in d.get('configs', []):
    if c.get('id') == '$cfg':
        for s in c['surfaces']:
            if s['id'] == 2:
                print(s['thickness'])
                sys.exit(0)
print(-1)
"
}

GAP_TARGET=5.0
echo
echo "=== Lens1-Lens2 gap threshold check (config0) ==="
printf "  (threshold = $GAP_TARGET mm — air gap between lens 1 and lens 2 must be >= this)\n"
gap_before=$(get_gap12 "$YAML" "config0")
gap_after=$(get_gap12 "$RESULT" "config0")
printf "  %-8s gap before = %8.4f mm\n" "config0" "$gap_before"
printf "  %-8s gap after  = %8.4f mm" "config0" "$gap_after"
{
  echo "=== Lens1-Lens2 gap check (config0) ==="
  echo "  (threshold = $GAP_TARGET mm)"
  printf "  %-8s gap before = %8.4f mm\n" "config0" "$gap_before"
  printf "  %-8s gap after  = %8.4f mm\n" "config0" "$gap_after"
  echo
} >> "$RESULT_FILE"
if [ "$gap_after" != "-1" ] && [ "$(python3 -c "print('1' if $gap_after < $GAP_TARGET else '0')")" = "1" ]; then
  echo "   ✗"
  echo "  >>> Optimization failed: config0 lens1-lens2 gap $(printf '%.4f' "$gap_after") mm < $GAP_TARGET mm" >> "$RESULT_FILE"
  exit 1
else
  echo "   ✓"
  echo "  >>> Optimization passed: config0 lens1-lens2 gap $(printf '%.4f' "$gap_after") mm >= $GAP_TARGET mm" >> "$RESULT_FILE"
fi

# (cleanup is handled at the top for --clean mode)
