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

# (cleanup is handled at the top for --clean mode)
