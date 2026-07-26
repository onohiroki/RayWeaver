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
RAYWEAVE="${RAYWEAVE:-./rayweave}"

# Clean-only mode: remove generated files and exit
if [ "$CLEAN" = true ]; then
  echo "=== Cleaning up generated files ==="
  for cfg in config0 config1 config2; do
    rm -f "$OUTDIR/simple-zoom-${cfg}-init-rays.png"
    rm -f "$OUTDIR/simple-zoom-${cfg}-opt-rays.png"
  done
  rm -f "$RESULT" "$LOG"
  echo "  Removed: PNGs, $RESULT, $LOG"
  exit 0
fi

echo "=== Simple Zoom Lens Optimization Demo ==="
echo
echo "Configs: config0 (S2=20, S4=80), config1 (S2=50, S4=50), config2 (S2=80, S4=20)"
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
    | $RAYWEAVE chief --clear-aperture --config "$cfg" \
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
    | $RAYWEAVE chief --clear-aperture --config "$cfg" \
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
for cfg in config0 config1 config2; do
  efl_before=$(cat "$YAML" | $RAYWEAVE paraxial --config "$cfg" 2>/dev/null | yq '.paraxial_result.focal_length' 2>/dev/null)
  efl_after=$(cat "$RESULT" | $RAYWEAVE paraxial --config "$cfg" 2>/dev/null | yq '.paraxial_result.focal_length' 2>/dev/null)
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

THRESHOLD=0.5
echo "  (threshold = $THRESHOLD mm — all configs on-axis RMS must be below this)"

failed=false
for cfg in config0 config1 config2; do
  rms_before=$(get_onaxis_rms "$YAML" "$cfg")
  rms_after=$(get_onaxis_rms "$RESULT" "$cfg")
  printf "  %-8s %12.4f %12.4f" "$cfg" "$rms_before" "$rms_after"
  if [ "$rms_before" != "-1" ] && [ "$rms_after" != "-1" ]; then
    if (( $(echo "$rms_after < $rms_before" | bc -l) )); then
      printf "   ✓"
    else
      printf "   ✗"
    fi
  fi
  echo
  if [ "$rms_after" != "-1" ] && (( $(echo "$rms_after >= $THRESHOLD" | bc -l) )); then
    failed=true
  fi
done

if [ "$failed" = true ]; then
  echo
  echo "  >>> Optimization failed: not all configs on-axis RMS < $THRESHOLD mm"
  exit 1
fi
echo
echo "  >>> Optimization passed: all configs on-axis RMS < $THRESHOLD mm"
echo

# (cleanup is handled at the top for --clean mode)
