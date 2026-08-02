#!/bin/bash
set -euo pipefail

# =============================================================================
# escape-demo.bash — global optimisation with the escape function
#
# Purpose: show the Ishiki-Ono style escape-function optimiser on the degraded
# US2645157 triplet: DLS cycles with merit-function bumps at discovered local
# minima, so the result is a list of local minima and the best one wins.
#
# Steps
#   1. escape --verbose            : global search -> escape-demo-result.yaml
#   2. escape extract --index N    : pull one full local-minimum system out
#   3. plot                        : diagrams of the initial and best systems
#
# How to read the result
#   - Best merit is the lowest DLS merit among the discovered minima.
#   - The result YAML holds every minimum's full surfaces; use
#     `escape extract --index N` to re-optimise from a chosen one.
# =============================================================================

# Resolve the script's own directory so the demo runs from any CWD
# (repo root, `cd samples`, or a copied location). All data files are read
# from and all outputs are written to this directory.
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

YAML="$SCRIPT_DIR/escape-demo.yaml"
OUTDIR="$SCRIPT_DIR"
RESULT="$OUTDIR/escape-demo-result.yaml"
RESULT_FILE="$OUTDIR/escape-demo-result.txt"

CLEAN=false
while [[ $# -gt 0 ]]; do
  case "$1" in
    --clean) CLEAN=true; shift ;;
    *) echo "Unknown option: $1"; exit 1 ;;
  esac
done

if [ "$CLEAN" = true ]; then
  echo "=== Cleaning up generated files ==="
rm -f "$RESULT" "$RESULT_FILE" "$OUTDIR/escape-demo-min1.yaml"
rm -f "$OUTDIR/escape-demo-init.png" "$OUTDIR/escape-demo-best.png" "$OUTDIR/escape-demo-min1.png"
echo "  Removed: PNGs, $RESULT, $RESULT_FILE, $OUTDIR/escape-demo-min1.yaml"
  exit 0
fi

# Locate the rayweave binary: an explicit RAYWEAVE env value wins, then a
# binary next to the script or one directory up (samples/ -> repo root),
# then any rayweave on PATH.
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

# ── Interpretation notes: appended to the result file on exit. ──
append_interpretation() {
cat >> "$RESULT_FILE" <<'EOF'

=== How to interpret this result ===
- Escape-function global optimisation of the degraded US2645157 triplet.
- Best merit is the lowest DLS merit among all discovered local minima
  (lower = better); [0] is the best minimum, [1] a worse secondary one.
- The '*' marks the best minimum. escape-demo-result.yaml contains the full
  surfaces of every minimum; `escape extract --index N` pulls one out
  (e.g. escape-demo-min1.yaml) to re-optimise from.
- If the best merit is only marginally better than a plain local DLS run,
  the merit landscape is effectively single-modal and escape is unnecessary.
EOF
}
trap append_interpretation EXIT

echo "=== Escape demo: global optimisation of degraded US2645157 triplet ==="
echo

echo "--- Running escape-function global optimisation ---"
$RAYWEAVE escape --verbose < "$YAML" > "$RESULT"
echo

echo "--- Local minima summary ---"
python3 -c "
import sys, yaml
d = yaml.safe_load(sys.stdin)
er = d.get('escape_result', {})
print(f'  Best index: {er.get(\"best_index\")}  Best merit: {er.get(\"best_merit\", 0):.6e}')
for m in er.get('minima', []):
    mark = '*' if m.get('index') == er.get('best_index') else ' '
    print(f'  {mark}[{m.get(\"index\")}] merit={m.get(\"merit\", 0):.6e}')
" < "$RESULT" | tee "$RESULT_FILE"
echo

echo "--- PNG diagrams ---"
echo "=== Initial diagram ==="
$RAYWEAVE chief --clear-aperture --shrink --ray-fan < "$YAML" | $RAYWEAVE trace \
  | $RAYWEAVE plot -o "$OUTDIR/escape-demo-init.png" >/dev/null
echo "Written: $OUTDIR/escape-demo-init.png"

echo "=== Best-solution diagram ==="
python3 -c "
import sys, yaml
d = yaml.safe_load(sys.stdin)
d['chief'] = {'fields': [{'angle': 0.0, 'direction': [0, 1]}, {'angle': 16.0, 'direction': [0, 1]}, {'angle': 24.0, 'direction': [0, 1]}], 'reference_surface': 8, 'num_rays': 512, 'grid_type': 'hex', 'dump_map': False}
yaml.safe_dump(d, sys.stdout, sort_keys=False)
" < "$RESULT" | $RAYWEAVE chief --clear-aperture --shrink --ray-fan | $RAYWEAVE trace \
  | $RAYWEAVE plot -o "$OUTDIR/escape-demo-best.png" >/dev/null
echo "Written: $OUTDIR/escape-demo-best.png"

echo "=== Extracting local minimum 1 ==="
$RAYWEAVE escape extract --index 1 < "$RESULT" > "$OUTDIR/escape-demo-min1.yaml"
echo "Written: $OUTDIR/escape-demo-min1.yaml"
