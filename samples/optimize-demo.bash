#!/bin/bash
set -euo pipefail

YAML="samples/us2645157-degraded.yaml"
OUTDIR="samples"
OPT_RESULT="$OUTDIR/us2645157-optimize-result.yaml"
OPT_WITH_CHIEF="$OUTDIR/us2645157-opt-with-chief.yaml"

echo "=== Optimize demo: degraded US2645157 triplet ==="
echo

echo "--- Initial state (degraded curvatures) ---"
echo "=== DLS optimization ==="
./rayweave optimize --verbose < "$YAML" > "$OPT_RESULT"
echo

echo "--- SVG diagrams ---"
echo "=== Initial SVG ==="
./rayweave chief --clear-aperture < "$YAML" | ./rayweave chief --marginal-rays \
  | ./rayweave trace \
  | ./rayweave plot -o "$OUTDIR/us2645157-init.svg"
echo "Written: $OUTDIR/us2645157-init.svg"
echo

echo "=== Optimized SVG ==="
yq '.chief = {"fields": [{"angle": 0.0, "direction": [0, 1]}, {"angle": 16.0, "direction": [0, 1]}, {"angle": 24.0, "direction": [0, 1]}], "reference_surface": 8, "num_rays": 512, "grid_type": "hex", "dump_map": false}' "$OPT_RESULT" > "$OPT_WITH_CHIEF"
./rayweave chief --clear-aperture < "$OPT_WITH_CHIEF" | ./rayweave chief --marginal-rays \
  | ./rayweave trace \
  | ./rayweave plot -o "$OUTDIR/us2645157-opt.svg"
echo "Written: $OUTDIR/us2645157-opt.svg"
echo
