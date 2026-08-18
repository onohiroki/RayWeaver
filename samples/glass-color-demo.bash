#!/bin/bash
set -euo pipefail

# =============================================================================
# glass-color-demo.bash — power-preserving glass chromatic optimisation
#
# Purpose: demonstrate `optimize --glass-color --power-solve` on a Cooke
# triplet with the flint/crown glasses SWAPPED (strong axial + lateral colour).
# The CLI flags auto-generate everything needed for a pure glass-swap colour
# study:
#   --glass-color           auto-create nd/vd variables for every element and a
#                           merit of only longitudinal_color + lateral_color
#   --power-solve           preserve each element's thin-lens power (hard solve:
#                           a curvature per element becomes dependent so the
#                           power = initial value while only the glasses move)
#   --power-solve-surfaces  the curvature surface to pin per element (2,4,7)
#
# The element powers (and therefore the nominal focal lengths) stay FIXED, so
# a lower merit means the glasses themselves were rebalanced to cancel the
# chromatic error — not that the lens was decollimated. A convex-hull glass
# domain can be added via optimization.glass_hull to keep the result on real
# glass.
#
# Outputs (all alongside this script):
#   - glass-color-before.yaml / optimized pipe output
#   - before/after element powers (paraxial element_roles) — should match
#   - before/after LCA (longitudinal color, ~Delta EFL) and TCA (mm)
# =============================================================================

# Resolve the script's own directory so it runs from any CWD.
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

CLEAN=false
while [[ $# -gt 0 ]]; do
  case "$1" in
    --clean) CLEAN=true; shift ;;
    *) echo "Unknown option: $1"; exit 1 ;;
  esac
done

YAML="$SCRIPT_DIR/glass-color-demo.yaml"
BEFORE="$SCRIPT_DIR/glass-color-before.yaml"
AFTER="$SCRIPT_DIR/glass-color-after.yaml"
LOG="$SCRIPT_DIR/glass-color-log.jsonl"

if [ "$CLEAN" = true ]; then
  echo "=== Cleaning up generated files ==="
  rm -f "$BEFORE" "$AFTER" "$LOG"
  echo "  Removed generated files"
  exit 0
fi

# Locate the rayweave binary.
if [[ -z "${RAYWEAVE:-}" ]]; then
  for cand in "$SCRIPT_DIR/rayweave" "$SCRIPT_DIR/../rayweave"; do
    if [[ -x "$cand" ]]; then RAYWEAVE="$cand"; break; fi
  done
  RAYWEAVE="${RAYWEAVE:-$(command -v rayweave || true)}"
  if [[ -z "${RAYWEAVE:-}" ]]; then
    echo "error: rayweave binary not found; set RAYWEAVE or put rayweave on PATH" >&2
    exit 1
  fi
fi

SOLVE_SURFACES="2,4,7"

element_powers() {
  $RAYWEAVE paraxial < "$1" \
    | $RAYWEAVE query --csv "paraxial_result.element_roles[]:phi"
}

echo "=== Initial element powers (thin-lens phi) ==="
element_powers "$YAML"

echo
echo "=== Running power-preserving glass chromatic optimisation ==="
$RAYWEAVE optimize \
  --glass-color \
  --power-solve \
  --power-solve-surfaces "$SOLVE_SURFACES" \
  --log "$LOG" \
  < "$YAML" > "$AFTER"

echo "  Optimised output: $AFTER"
echo "  Auto-generated variables + merit + power_solve are echoed into the output:"
$RAYWEAVE query --yaml "optimization.power_solve" < "$AFTER"

echo
echo "=== Element powers AFTER (must equal the initial values) ==="
element_powers "$AFTER"

echo
echo "=== Optimization summary (from the run) ==="
$RAYWEAVE query --yaml "opt_results" < "$AFTER" | head -20
echo
echo "The power-preserving solve kept the element powers fixed, so the merit"
echo "drop reflects a true glass rebalance (axial + lateral colour), not a"
echo "de-collimation."
