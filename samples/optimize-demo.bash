#!/bin/bash
set -euo pipefail

YAML="samples/us2645157-degraded.yaml"
OUTDIR="samples"
OPT_RESULT="$OUTDIR/optimize-demo-result.yaml"
OPT_WITH_CHIEF="$OUTDIR/optimize-demo-with-chief.yaml"

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
  rm -f "$OPT_RESULT" "$OPT_WITH_CHIEF"
  rm -f "$OUTDIR/optimize-demo-init.png" "$OUTDIR/optimize-demo-opt.png"
  echo "  Removed: PNGs, $OPT_RESULT, $OPT_WITH_CHIEF"
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
./rayweave chief --clear-aperture < "$YAML" | ./rayweave chief --marginal-rays \
  | ./rayweave trace \
  | ./rayweave plot -o "$OUTDIR/optimize-demo-init.png"
echo "Written: $OUTDIR/optimize-demo-init.png"
echo

echo "=== Optimized diagram ==="
yq '.chief = {"fields": [{"angle": 0.0, "direction": [0, 1]}, {"angle": 16.0, "direction": [0, 1]}, {"angle": 24.0, "direction": [0, 1]}], "reference_surface": 8, "num_rays": 512, "grid_type": "hex", "dump_map": false}' "$OPT_RESULT" > "$OPT_WITH_CHIEF"
./rayweave chief --clear-aperture < "$OPT_WITH_CHIEF" | ./rayweave chief --marginal-rays \
  | ./rayweave trace \
  | ./rayweave plot -o "$OUTDIR/optimize-demo-opt.png"
echo "Written: $OUTDIR/optimize-demo-opt.png"
echo
