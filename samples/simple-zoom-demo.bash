#!/bin/bash
set -euo pipefail

YAML="samples/simple-zoom.yaml"
OUTDIR="samples"
RESULT="$OUTDIR/simple-zoom-optimized.yaml"
LOG="$OUTDIR/simple-zoom-log.jsonl"
RAYWEAVE="${RAYWEAVE:-./rayweave}"

echo "=== Simple Zoom Lens Optimization Demo ==="
echo
echo "Configs: config0 (S2=20, S4=80), config1 (S2=50, S4=50), config2 (S2=80, S4=20)"
echo

# ── 1. Initial lens layout (without rays) ──
echo "=== Initial lens layout (no rays) ==="
for cfg in config0 config1 config2; do
  echo "  Config: $cfg"
  cat "$YAML" \
    | $RAYWEAVE plot --config "$cfg" -o "$OUTDIR/simple-zoom-${cfg}-init.svg" 2>/dev/null
  echo "    SVG: $OUTDIR/simple-zoom-${cfg}-init.svg"
  if command -v rsvg-convert >/dev/null 2>&1; then
    rsvg-convert "$OUTDIR/simple-zoom-${cfg}-init.svg" \
      -o "$OUTDIR/simple-zoom-${cfg}-init.png" 2>/dev/null \
      && echo "    PNG: $OUTDIR/simple-zoom-${cfg}-init.png"
  fi
done
echo

# ── 2. DLS multi-config optimization (on-axis) ──
echo "=== DLS Multi-Config Optimization ==="
echo "  Merit: on-axis spot RMS (center field priority)"
$RAYWEAVE optimize --verbose --log "$LOG" < "$YAML" > "$RESULT"

echo
echo "--- Optimization results ---"
yq '{before_merit, after_merit, iterations, status}' "$RESULT" 2>/dev/null || true
echo

# ── 3. Optimized lens layout (without rays) ──
echo "=== Optimized lens layout (no rays) ==="
for cfg in config0 config1 config2; do
  echo "  Config: $cfg"
  cat "$RESULT" \
    | $RAYWEAVE plot --config "$cfg" -o "$OUTDIR/simple-zoom-${cfg}-opt.svg" 2>/dev/null
  echo "    SVG: $OUTDIR/simple-zoom-${cfg}-opt.svg"
  if command -v rsvg-convert >/dev/null 2>&1; then
    rsvg-convert "$OUTDIR/simple-zoom-${cfg}-opt.svg" \
      -o "$OUTDIR/simple-zoom-${cfg}-opt.png" 2>/dev/null \
      && echo "    PNG: $OUTDIR/simple-zoom-${cfg}-opt.png"
  fi
done
echo

# ── 4. Ray-overlaid layout (before optimization, on-axis) ──
echo "=== Initial ray-overlaid layout (on-axis only) ==="
for cfg in config0 config1 config2; do
  echo "  Config: $cfg"
  cat "$YAML" \
    | $RAYWEAVE chief --clear-aperture --config "$cfg" \
    | $RAYWEAVE chief --marginal-rays --config "$cfg" \
    | $RAYWEAVE trace --config "$cfg" \
    | $RAYWEAVE plot --config "$cfg" -o "$OUTDIR/simple-zoom-${cfg}-init-rays.svg" 2>/dev/null
  echo "    SVG: $OUTDIR/simple-zoom-${cfg}-init-rays.svg"
  if command -v rsvg-convert >/dev/null 2>&1; then
    rsvg-convert "$OUTDIR/simple-zoom-${cfg}-init-rays.svg" \
      -o "$OUTDIR/simple-zoom-${cfg}-init-rays.png" 2>/dev/null \
      && echo "    PNG: $OUTDIR/simple-zoom-${cfg}-init-rays.png"
  fi
done
echo

# ── 5. Ray-overlaid layout (after optimization, on-axis) ──
echo "=== Optimized ray-overlaid layout (on-axis only) ==="
for cfg in config0 config1 config2; do
  echo "  Config: $cfg"
  cat "$RESULT" \
    | yq -o yaml '.chief = {"reference_surface": 7, "num_rays": 256, "fields": [{"angle": 0}]}' 2>/dev/null \
    | $RAYWEAVE chief --clear-aperture --config "$cfg" \
    | $RAYWEAVE chief --marginal-rays --config "$cfg" \
    | $RAYWEAVE trace --config "$cfg" \
    | $RAYWEAVE plot --config "$cfg" -o "$OUTDIR/simple-zoom-${cfg}-opt-rays.svg" 2>/dev/null
  echo "    SVG: $OUTDIR/simple-zoom-${cfg}-opt-rays.svg"
  if command -v rsvg-convert >/dev/null 2>&1; then
    rsvg-convert "$OUTDIR/simple-zoom-${cfg}-opt-rays.svg" \
      -o "$OUTDIR/simple-zoom-${cfg}-opt-rays.png" 2>/dev/null \
      && echo "    PNG: $OUTDIR/simple-zoom-${cfg}-opt-rays.png"
  fi
done
echo

echo "=== Iteration log saved: $LOG ==="
if [ -f "$LOG" ]; then
  echo "  Log entries:"
  wc -l "$LOG" 2>/dev/null
fi
