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

YAML="samples/us2645157.yaml"
OUTDIR="samples"
SCALED="$OUTDIR/us2645157-scaled50.yaml"
RESULT_FILE="$OUTDIR/scale-demo-result.txt"
RAYWEAVE="${RAYWEAVE:-./rayweave}"

# Clean-only mode: remove generated files and exit
if [ "$CLEAN" = true ]; then
  echo "=== Cleaning up generated files ==="
  rm -f "$SCALED" "$RESULT_FILE"
  rm -f "$OUTDIR/us2645157-scaled50.svg"
  echo "  Removed generated files"
  exit 0
fi

echo "=== Scale demo: 25 mm patent triplet -> 50 mm standard lens ==="
echo
echo "The US2645157 triplet (EFL 25.03 mm) is a 50 mm standard once scaled 2x."
echo "The 'scale' subcommand resizes every length (radii, thicknesses, diameters)"
echo "by s = target/current_EFL, so the EFL becomes exactly the target and the"
echo "f-number (and normalized aberration balance) is preserved."
echo

get_parax() {
  local key=$1
  $RAYWEAVE paraxial < "$2" 2>/dev/null \
    | python3 -c "import sys,yaml; d=yaml.safe_load(sys.stdin); print(d.get('paraxial_result',{}).get('$key',-1))"
}

EFL_BEFORE=$(get_parax focal_length "$YAML")
FNO_BEFORE=$(get_parax image_space_f_number "$YAML")

echo "=== Scale to EFL 50 ==="
$RAYWEAVE scale --efl 50 < "$YAML" > "$SCALED"
echo "Written: $SCALED"
echo

EFL_AFTER=$(get_parax focal_length "$SCALED")
FNO_AFTER=$(get_parax image_space_f_number "$SCALED")

{
  echo "=== Scale demo: EFL and f-number before / after ==="
  printf "  %-12s %12s %12s\n" "Quantity" "before" "after"
  printf "  %-12s %12s %12s\n" "--------" "-------" "------"
  printf "  %-12s %12.3f %12.3f\n" "EFL (mm)" "$EFL_BEFORE" "$EFL_AFTER"
  printf "  %-12s %12.3f %12.3f\n" "f/#" "$FNO_BEFORE" "$FNO_AFTER"
  echo
} | tee "$RESULT_FILE"

# Check the EFL landed on target.
if [ "$(python3 -c "print('1' if abs($EFL_AFTER - 50.0) <= 0.01 else '0')")" = "1" ]; then
  echo "  >>> Scale passed: EFL = $EFL_AFTER mm (~50 mm)" | tee -a "$RESULT_FILE"
else
  echo "  >>> Scale failed: EFL = $EFL_AFTER mm (expected 50)" | tee -a "$RESULT_FILE"
  exit 1
fi

echo
echo "=== Scaled raytrace diagram ==="
$RAYWEAVE chief --clear-aperture --shrink --ray-fan < "$SCALED" \
  | $RAYWEAVE chief --marginal-rays \
  | $RAYWEAVE trace \
  | $RAYWEAVE plot -o "$OUTDIR/us2645157-scaled50.svg" 2>/dev/null || true
echo "Written: $OUTDIR/us2645157-scaled50.svg"
echo
echo "Next step: pipe the scaled system into 'optimize' to refine it at 50 mm."
