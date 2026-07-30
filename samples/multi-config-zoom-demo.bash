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

YAML="samples/multi-config-zoom.yaml"
OUTDIR="samples"
RESULT="$OUTDIR/multi-config-zoom-result.yaml"
LOG="$OUTDIR/multi-config-zoom-log.jsonl"
RESULT_FILE="$OUTDIR/multi-config-zoom-demo-result.txt"
RAYWEAVE="${RAYWEAVE:-./rayweave}"

# Clean-only mode: remove generated files and exit
if [ "$CLEAN" = true ]; then
  echo "=== Cleaning up generated files ==="
  for cfg in config0 config1 config2; do
    rm -f "$OUTDIR/multi-config-zoom-${cfg}-init-rays.png"
    rm -f "$OUTDIR/multi-config-zoom-${cfg}-opt-rays.png"
  done
  rm -f "$RESULT" "$LOG" "$RESULT_FILE"
  echo "  Removed: PNGs, $RESULT, $LOG, $RESULT_FILE"
  exit 0
fi

echo "=== Multi-Config Zoom Lens Demo ==="
echo
echo "Layout: 3 configs (config0 / config1 / config2)"
echo "Shared variables: variables following simple-zoom pattern"
echo "Local variables: 2 air gaps per config"
echo

echo "=== DLS Multi-Config Optimization ==="
$RAYWEAVE optimize --verbose --log "$LOG" < "$YAML" > "$RESULT"

echo
echo "--- Optimization results ---"
grep -i -E "^=== |status|merit" "$RESULT" 2>/dev/null | head -10 || true
echo

echo "--- RMS spot size comparison (before → after) ---"
for cfg in config0 config1 config2; do
  echo "  Config: $cfg"
  for label in "Before" "After"; do
    [ "$label" = "Before" ] && src="$YAML" || src="$RESULT"
    echo "    $label:"
    cat "$src" | $RAYWEAVE chief --config "$cfg" 2>/dev/null \
      | awk '
        BEGIN{ang=""; in_spot=0}
        /field_angle:/{ ang = $NF }
        /spot_stats:/{ in_spot = 1; rms = "" }
        in_spot && /rms_r:/{ rms = $NF }
        in_spot && /traced_rays:/ && rms != "" {
          tr = $NF
          if (tr + 0 > 0)
            printf "      field %.3f° RMS=%.4fmm (%d rays)\n", ang, rms + 0, tr
          in_spot = 0
        }'
  done
done
echo

echo "=== Ray-overlaid layout (after optimization) ==="
for cfg in config0 config1 config2; do
  echo "  Config: $cfg"
  cat "$RESULT" \
    | $RAYWEAVE chief --clear-aperture --ray-fan --config "$cfg" \
    | $RAYWEAVE chief --marginal-rays --config "$cfg" \
    | $RAYWEAVE trace --config "$cfg" \
    | $RAYWEAVE plot --config "$cfg" -o "$OUTDIR/multi-config-zoom-${cfg}-opt-rays.png" 2>/dev/null
  echo "    Written: $OUTDIR/multi-config-zoom-${cfg}-opt-rays.png"
done
echo

echo "=== Ray-overlaid layout (before optimization) ==="
for cfg in config0 config1 config2; do
  echo "  Config: $cfg"
  cat "$YAML" \
    | $RAYWEAVE chief --clear-aperture --ray-fan --config "$cfg" \
    | $RAYWEAVE chief --marginal-rays --config "$cfg" \
    | $RAYWEAVE trace --config "$cfg" \
    | $RAYWEAVE plot --config "$cfg" -o "$OUTDIR/multi-config-zoom-${cfg}-init-rays.png" 2>/dev/null
  echo "    Written: $OUTDIR/multi-config-zoom-${cfg}-init-rays.png"
done
echo

{
  echo "=== Performance comparison ==="
  printf "  %-8s %6s  %10s  %10s\n" "Config" "Field" "RMS bef" "RMS aft"
  printf "  %-8s %6s  %10s  %10s\n" "------" "-----" "-------" "-------"
  for cfg in config0 config1 config2; do
    # Extract field angles and RMS before/after
    bef=$(cat "$YAML" | $RAYWEAVE chief --config "$cfg" 2>/dev/null \
      | awk 'BEGIN{ang=""; r=0} /field_angle:/{ang=$NF} /spot_stats:/{in_spot=1; r=0} in_spot&&/rms_r:/{r=$NF} in_spot&&/traced_rays:/{if(r+0>0 && $NF+0>0)printf "%.3f %.4f\n", ang, r; in_spot=0}')
    aft=$(cat "$RESULT" | $RAYWEAVE chief --config "$cfg" 2>/dev/null \
      | awk 'BEGIN{ang=""; r=0} /field_angle:/{ang=$NF} /spot_stats:/{in_spot=1; r=0} in_spot&&/rms_r:/{r=$NF} in_spot&&/traced_rays:/{if(r+0>0 && $NF+0>0)printf "%.3f %.4f\n", ang, r; in_spot=0}')
    efl=$(cat "$RESULT" | $RAYWEAVE paraxial --config "$cfg" 2>/dev/null \
      | awk -F': ' '/focal_length:/{printf "%.1f",$2; exit}')
    # Show each field with before/after RMS
    line_no=0
    while IFS= read -r bline; do
      aline=$(echo "$aft" | sed -n "$((line_no+1))p")
      fa=$(echo "$bline" | awk '{print $1}')
      br=$(echo "$bline" | awk '{print $2}')
      ar=$(echo "$aline" | awk '{print $2}')
      if [ "$line_no" -eq 0 ]; then
        printf "  %-8s %5s°  %8.4f  %8.4f    EFL=%smm\n" "$cfg" "$fa" "$br" "$ar" "$efl"
      else
        printf "  %-8s %5s°  %8.4f  %8.4f\n" "" "$fa" "$br" "$ar"
      fi
      line_no=$((line_no + 1))
    done <<< "$bef"

  done
  echo
} | tee -a "$RESULT_FILE"

# ── On-axis RMS threshold check (all configs) ──
THRESHOLD=0.03
echo "=== On-axis RMS threshold check ==="
printf "  (threshold = $THRESHOLD mm — all configs on-axis RMS must be below this)\n"

get_onaxis_rms() {
  local yaml_file="$1"
  local cfg="$2"
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

failed=false
for cfg in config0 config1 config2; do
  rms_after=$(get_onaxis_rms "$RESULT" "$cfg")
  printf "  %-8s on-axis RMS = %8.4f mm" "$cfg" "$rms_after"
  if [ "$rms_after" != "-1" ] && (( $(echo "$rms_after >= $THRESHOLD" | bc -l) )); then
    echo "   ✗"
    failed=true
  else
    echo "   ✓"
  fi
done

{
  echo
  if [ "$failed" = true ]; then
    echo "  >>> Optimization failed: not all configs on-axis RMS < $THRESHOLD mm"
  else
    echo "  >>> Optimization passed: all configs on-axis RMS < $THRESHOLD mm"
  fi
  echo
} | tee -a "$RESULT_FILE"

echo "=== Iteration log saved: $LOG ==="
if [ -f "$LOG" ]; then
  echo "  Log entries:"
  wc -l "$LOG" 2>/dev/null
fi
echo

if [ "$failed" = true ]; then
  exit 1
fi

# (cleanup is handled at the top for --clean mode)
