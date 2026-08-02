#!/bin/bash
set -euo pipefail

YAML="samples/escape-demo.yaml"
OUTDIR="samples"
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

echo "=== Escape demo: global optimisation of degraded US2645157 triplet ==="
echo

echo "--- Running escape-function global optimisation ---"
./rayweave escape --verbose < "$YAML" > "$RESULT"
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
./rayweave chief --clear-aperture --shrink --ray-fan < "$YAML" | ./rayweave trace \
  | ./rayweave plot -o "$OUTDIR/escape-demo-init.png" >/dev/null
echo "Written: $OUTDIR/escape-demo-init.png"

echo "=== Best-solution diagram ==="
python3 -c "
import sys, yaml
d = yaml.safe_load(sys.stdin)
d['chief'] = {'fields': [{'angle': 0.0, 'direction': [0, 1]}, {'angle': 16.0, 'direction': [0, 1]}, {'angle': 24.0, 'direction': [0, 1]}], 'reference_surface': 8, 'num_rays': 512, 'grid_type': 'hex', 'dump_map': False}
yaml.safe_dump(d, sys.stdout, sort_keys=False)
" < "$RESULT" | ./rayweave chief --clear-aperture --shrink --ray-fan | ./rayweave trace \
  | ./rayweave plot -o "$OUTDIR/escape-demo-best.png" >/dev/null
echo "Written: $OUTDIR/escape-demo-best.png"

echo "=== Extracting local minimum 1 ==="
./rayweave escape extract --index 1 < "$RESULT" > "$OUTDIR/escape-demo-min1.yaml"
echo "Written: $OUTDIR/escape-demo-min1.yaml"
