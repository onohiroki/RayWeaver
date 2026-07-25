#!/bin/bash

YAML="samples/multi-config-zoom.yaml"
OUTDIR="samples"
RESULT="$OUTDIR/multi-config-zoom-result.yaml"
LOG="$OUTDIR/multi-config-zoom-log.jsonl"
RAYWEAVE="${RAYWEAVE:-./rayweave}"

echo "=== Multi-Config Zoom Lens Demo ==="
echo
echo "Layout: 3 configs (Wide / Mid / Tele)"
echo "Shared variables: group3_curvature, group4_thickness, aperture_stop_radius"
echo "Local variables: wide_space, tele_space"
echo

echo "=== DLS Multi-Config Optimization ==="
$RAYWEAVE optimize --verbose --log "$LOG" < "$YAML" > "$RESULT"

echo
echo "--- Optimization results ---"
grep -E "^=== |Status|Merit|Improvement|Iterations" "$RESULT" 2>/dev/null | head -10
echo

echo "--- Config surfaces (after optimization, lens bodies only) ---"
for cfg in wide mid tele; do
  echo "  Config: $cfg"
  cat "$RESULT" | $RAYWEAVE plot --config "$cfg" -o "$OUTDIR/multi-config-zoom-${cfg}-opt.svg" 2>/dev/null
  echo "    Written: $OUTDIR/multi-config-zoom-${cfg}-opt.svg"
  if command -v rsvg-convert >/dev/null 2>&1; then
    rsvg-convert "$OUTDIR/multi-config-zoom-${cfg}-opt.svg" -o "$OUTDIR/multi-config-zoom-${cfg}-opt.png" 2>/dev/null && echo "      PNG: $OUTDIR/multi-config-zoom-${cfg}-opt.png"
  fi
done
echo

echo "=== Before optimization (initial lens bodies) ==="
for cfg in wide mid tele; do
  cat "$YAML" | $RAYWEAVE plot --config "$cfg" -o "$OUTDIR/multi-config-zoom-${cfg}-init.svg" 2>/dev/null
  echo "    Written: $OUTDIR/multi-config-zoom-${cfg}-init.svg"
  if command -v rsvg-convert >/dev/null 2>&1; then
    rsvg-convert "$OUTDIR/multi-config-zoom-${cfg}-init.svg" -o "$OUTDIR/multi-config-zoom-${cfg}-init.png" 2>/dev/null && echo "      PNG: $OUTDIR/multi-config-zoom-${cfg}-init.png"
  fi
done
echo

echo "=== Ray-overlaid layout (after optimization) ==="
for cfg in wide mid tele; do
  echo "  Config: $cfg"
  cat "$RESULT" \
    | $RAYWEAVE chief --clear-aperture --config "$cfg" \
    | $RAYWEAVE chief --marginal-rays --config "$cfg" \
    | $RAYWEAVE trace --config "$cfg" \
    | $RAYWEAVE plot --config "$cfg" -o "$OUTDIR/multi-config-zoom-${cfg}-opt-rays.svg" 2>/dev/null
  echo "    Written: $OUTDIR/multi-config-zoom-${cfg}-opt-rays.svg"
  if command -v rsvg-convert >/dev/null 2>&1; then
    rsvg-convert "$OUTDIR/multi-config-zoom-${cfg}-opt-rays.svg" -o "$OUTDIR/multi-config-zoom-${cfg}-opt-rays.png" 2>/dev/null \
      && echo "      PNG: $OUTDIR/multi-config-zoom-${cfg}-opt-rays.png"
  fi
done
echo

echo "=== Ray-overlaid layout (before optimization) ==="
for cfg in wide mid tele; do
  echo "  Config: $cfg"
  cat "$YAML" \
    | $RAYWEAVE chief --clear-aperture --config "$cfg" \
    | $RAYWEAVE chief --marginal-rays --config "$cfg" \
    | $RAYWEAVE trace --config "$cfg" \
    | $RAYWEAVE plot --config "$cfg" -o "$OUTDIR/multi-config-zoom-${cfg}-init-rays.svg" 2>/dev/null
  echo "    Written: $OUTDIR/multi-config-zoom-${cfg}-init-rays.svg"
  if command -v rsvg-convert >/dev/null 2>&1; then
    rsvg-convert "$OUTDIR/multi-config-zoom-${cfg}-init-rays.svg" -o "$OUTDIR/multi-config-zoom-${cfg}-init-rays.png" 2>/dev/null \
      && echo "      PNG: $OUTDIR/multi-config-zoom-${cfg}-init-rays.png"
  fi
done
echo

echo "=== Iteration log saved: $LOG ==="
echo "  Log entries:"
wc -l "$LOG" 2>/dev/null