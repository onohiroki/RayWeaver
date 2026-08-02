#!/bin/bash
set -euo pipefail

# =============================================================================
# scale-demo.bash — focal-length scaling
#
# Purpose: show the `scale` subcommand turning the 25 mm US2645157 triplet
# into a 50 mm standard lens by uniform scaling: every length (radii,
# thicknesses, diameters) is multiplied by target/current_EFL, so the EFL
# lands exactly on target while the f-number and the normalised aberrations
# are preserved.
#
# Steps
#   1. scale --efl 50 : produce us2645157-scaled50.yaml
#   2. paraxial       : verify EFL and f/# before/after
#   3. plot           : SVG raytrace diagram of the scaled system
#
# How to read the result
#   - EFL after = 50 mm (gate: +/- 0.01 mm); f/# unchanged (5.419).
#   - The scaled YAML is a good starting point for re-optimisation.
# =============================================================================

# Resolve the script's own directory so the demo runs from any CWD
# (repo root, `cd samples`, or a copied location). All data files are read
# from and all outputs are written to this directory.
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

# CLI options
CLEAN=false
while [[ $# -gt 0 ]]; do
  case "$1" in
    --clean) CLEAN=true; shift ;;
    *) echo "Unknown option: $1"; exit 1 ;;
  esac
done

YAML="$SCRIPT_DIR/us2645157.yaml"
OUTDIR="$SCRIPT_DIR"
SCALED="$OUTDIR/us2645157-scaled50.yaml"
RESULT_FILE="$OUTDIR/scale-demo-result.txt"

# Clean-only mode: remove generated files and exit
if [ "$CLEAN" = true ]; then
  echo "=== Cleaning up generated files ==="
  rm -f "$SCALED" "$RESULT_FILE"
  rm -f "$OUTDIR/us2645157-scaled50.svg"
  echo "  Removed generated files"
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

# ── Interpretation notes: appended to the result file on exit, so they stay
# as the closing section even when the gate check exits early. ──
append_interpretation() {
cat >> "$RESULT_FILE" <<'EOF'

=== How to interpret this result ===
- The US2645157 triplet (EFL 25.033 mm) is uniformly scaled so the EFL lands
  exactly on 50 mm.
- Every length (radii, thicknesses, diameters) is multiplied by the same
  factor, so the f-number (5.419) is unchanged and the normalised aberrations
  (measured in focal-length units) are preserved.
- A uniformly scaled system is the natural starting point for re-optimising a
  longer lens: pipe us2645157-scaled50.yaml into `optimize`.
- Pass gate: EFL after scaling = 50 +/- 0.01 mm.
EOF
}
trap append_interpretation EXIT

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
    | $RAYWEAVE query -r "paraxial_result.$key"
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
if $RAYWEAVE query --gate "abs(efl-50.0)<=0.01" --set efl="$EFL_AFTER" < /dev/null; then
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
