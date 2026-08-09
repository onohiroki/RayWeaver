#!/bin/bash
set -euo pipefail

# =============================================================================
# asphere-demo.bash — add a single asphere to an optimized spherical lens
#
# Purpose: show the `rayweave asphere` workflow on a lens that is already
# fully optimized with spherical surfaces. asphere-demo-init.yaml is an
# optimized 6-element double-Gauss (f/2.8 50 mm) whose curvatures have NO
# aspherical correction. The demo:
#   1. ranks candidate surfaces for asphere introduction,
#   2. fits even-order coefficients from the OPD residuals,
#   3. validates each top-K fit with a short DLS solve against the geometric
#      spot RMS,
#   4. inserts (--apply) the top-ranked asphere's DLS-solved coefficients,
#   5. compares the all-spherical spot RMS against the aspherized spot RMS.
#
# How to read the result
#   - The ranking score weights how well a rotationally-symmetric asphere
#     corrects the shared (field-common) OPD while penalising inter-field
#     conflict, manufacturing difficulty and optimisation instability.
#   - The validation improvement is the geometric RMS-spot reduction the
#     short DLS achieved with the asphere coefficients as the only variables.
#   - The final comparison table prints RMS before/after per field; a '✓'
#     means the aspherized surface shrank the spot, '✗' a regression.
# =============================================================================

# Resolve the script's own directory so the demo runs from any CWD.
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
OUTDIR="$SCRIPT_DIR"

FROM="$SCRIPT_DIR/asphere-demo-init.yaml"
APPLIED="$OUTDIR/asphere-demo-applied.yaml"
RESULT_FILE="$OUTDIR/asphere-demo-result.txt"

TOP_K=3
DLS_ITER=30
NUM_RAYS=128
SENS_SAMPLES=6

CLEAN=false
while [[ $# -gt 0 ]]; do
  case "$1" in
    --clean) CLEAN=true; shift ;;
    *) echo "Unknown option: $1"; exit 1 ;;
  esac
done

if [ "$CLEAN" = true ]; then
  echo "=== Cleaning up generated files ==="
  rm -f "$APPLIED" "$RESULT_FILE"
  rm -f "$OUTDIR/asphere-demo-rank.yaml" "$OUTDIR/asphere-demo-validated.yaml" "$OUTDIR/asphere-demo-spot.tbl"
  rm -f "$OUTDIR/asphere-demo-before.png" "$OUTDIR/asphere-demo-after.png"
  echo "  Removed: $RESULT_FILE, $APPLIED, ranking/validation YAMLs, PNGs"
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

append_notes() {
cat >> "$RESULT_FILE" <<'EOF'

=== How to interpret this result ===
- "RMS before / RMS after" is the geometric RMS spot radius (mm) per field
  from the chief-ray pupil grid (all-spherical vs aspherized).
- The validation merit is the same geometric spot RMS, solved by a short DLS
  over the inserted surface's a4..a12 only (all other surfaces frozen).
- A '✓' means the added asphere shrank the spot for that field; '✗' a
  regression. The demo applies the top-ranked (highest-scoring) surface.
EOF
}
trap append_notes EXIT

: > "$RESULT_FILE"
echo "=== Asphere demo: adding one asphere to an optimized spherical double-Gauss ===" | tee -a "$RESULT_FILE"
echo | tee -a "$RESULT_FILE"

# ── Step 1: candidate ranking ──
echo "--- Step 1: candidate ranking (top $TOP_K) ---"
"$RAYWEAVE" asphere --top-k "$TOP_K" --sensitivity-samples "$SENS_SAMPLES" < "$FROM" > "$OUTDIR/asphere-demo-rank.yaml"
# fit prints a numeric field as %8.4f, or a dash when the query returned -1
# (field absent, e.g. a candidate whose coefficients fit failed).
fit() {
  if [ -z "$1" ] || [ "$1" = "nan" ] || [ "$1" = "-1" ]; then printf "%9s" "-"; else printf "%9.4f" "$1"; fi
}
{
  printf "  %-6s %10s %10s\n" "surf" "score" "sens.imprv"
  for ri in $(seq 0 $((TOP_K - 1))); do
    sid=$("$RAYWEAVE" query -r "asphere_candidate_result.rankings[$ri].surface_id" < "$OUTDIR/asphere-demo-rank.yaml")
    score=$("$RAYWEAVE" query -r "asphere_candidate_result.rankings[$ri].score" < "$OUTDIR/asphere-demo-rank.yaml")
    sens=$("$RAYWEAVE" query -r "asphere_candidate_result.rankings[$ri].sensitivity.improvement" < "$OUTDIR/asphere-demo-rank.yaml" 2>/dev/null || echo "-1")
    printf "  %-6s %10s %10s\n" "$sid" "$(fit "$score")" "$(fit "$sens")"
  done
} | tee -a "$RESULT_FILE"
echo

# ── Step 2: validation ──
echo "--- Step 2: short-DLS validation (spot-RMS merit, $DLS_ITER it + ${NUM_RAYS} rays) ---"
"$RAYWEAVE" asphere --validate --top-k "$TOP_K" --sensitivity-samples "$SENS_SAMPLES" \
  --dls-iter "$DLS_ITER" --num-rays "$NUM_RAYS" < "$FROM" > "$OUTDIR/asphere-demo-validated.yaml"
{
  printf "  %-6s %12s %12s %12s %13s\n" "surf" "before" "after" "imprv" "a4"
  for ri in $(seq 0 $((TOP_K - 1))); do
    sid=$("$RAYWEAVE" query -r "asphere_candidate_result.rankings[$ri].surface_id" < "$OUTDIR/asphere-demo-validated.yaml")
    before=$("$RAYWEAVE" query -r "asphere_candidate_result.rankings[$ri].validation.before_merit" < "$OUTDIR/asphere-demo-validated.yaml" 2>/dev/null || echo "-")
    after=$("$RAYWEAVE" query -r "asphere_candidate_result.rankings[$ri].validation.after_merit" < "$OUTDIR/asphere-demo-validated.yaml" 2>/dev/null || echo "-")
    imprv=$("$RAYWEAVE" query -r "asphere_candidate_result.rankings[$ri].validation.improvement" < "$OUTDIR/asphere-demo-validated.yaml" 2>/dev/null || echo "-")
    a4=$("$RAYWEAVE" query -r "asphere_candidate_result.rankings[$ri].validation.coefficients.A4" < "$OUTDIR/asphere-demo-validated.yaml" 2>/dev/null || echo "-")
    if [ "$before" = "-1" ] || [ "$before" = "-" ]; then
      printf "  %-6s %12s %12s %12s %13s\n" "$sid" "(no fit)" "" "" ""
    elif [ "$before" = "-1" ] || [ "$after" = "-1" ]; then
      printf "  %-6s %12s %12s %12s %13s\n" "$sid" "-" "-" "-" "-"
    else
      printf "  %-6s %12.4f %12.4f %12.4f %13.3e\n" "$sid" "$before" "$after" "$imprv" "$a4"
    fi
  done
} | tee -a "$RESULT_FILE"
echo

# ── Step 3: apply the top-ranked aspherical ──
echo "--- Step 3: inserting the top-ranked aspherical surface (--apply) ---"
"$RAYWEAVE" asphere --apply --top-k "$TOP_K" \
  --dls-iter "$DLS_ITER" --num-rays "$NUM_RAYS" < "$FROM" > "$APPLIED"
echo "Written: $APPLIED"
echo

# ── Step 4: spot RMS before/after ──
echo "--- Step 4: spot RMS comparison (all-spherical vs aspherical) ---"
rms_field() {
  "$RAYWEAVE" chief < "$1" 2>/dev/null | "$RAYWEAVE" query -r "chief_rays[$2].spot_stats.rms_r"
}
nfield=$("$RAYWEAVE" chief < "$FROM" | "$RAYWEAVE" query --len chief_rays)
SPOT_TBL="$OUTDIR/asphere-demo-spot.tbl"
: > "$SPOT_TBL"
nregress=0
ngood=0
printf "  %-6s %12s %12s %10s\n" "field" "RMS before" "RMS after" "" >> "$SPOT_TBL"
printf "  %-6s %12s %12s %10s\n" "-----" "-----------" "----------" "-------" >> "$SPOT_TBL"
for fi in $(seq 0 $((nfield - 1))); do
  before=$(rms_field "$FROM" "$fi")
  after=$(rms_field "$APPLIED" "$fi")
  mark=""
  if [ "$before" != "-1" ] && [ "$after" != "-1" ]; then
    if "$RAYWEAVE" query --gate "b * 1.01 > a" --set a="$after" --set b="$before" < /dev/null > /dev/null; then
      mark="   ✓"
      ngood=$((ngood+1))
    else
      mark="   ✗"
      nregress=$((nregress+1))
    fi
  fi
  printf "  %-6s %12.4f %12.4f %10s\n" "f$fi" "$before" "$after" "$mark" >> "$SPOT_TBL"
done
echo >> "$SPOT_TBL"
printf "  (gate: no field may regress by more than 1%%, and at least one must improve)\n" >> "$SPOT_TBL"
cat "$SPOT_TBL" | tee -a "$RESULT_FILE"

# ── Step 5: PNG diagrams ──
echo "--- Step 5: PNG diagrams ---"
"$RAYWEAVE" chief --clear-aperture --ray-fan < "$FROM" | "$RAYWEAVE" trace \
  | "$RAYWEAVE" plot -o "$OUTDIR/asphere-demo-before.png" >/dev/null
echo "Written: $OUTDIR/asphere-demo-before.png"
"$RAYWEAVE" chief --clear-aperture --ray-fan < "$APPLIED" | "$RAYWEAVE" trace \
  | "$RAYWEAVE" plot -o "$OUTDIR/asphere-demo-after.png" >/dev/null
echo "Written: $OUTDIR/asphere-demo-after.png"

# ── Step 6: gate check ──
echo "--- Step 6: gate check ---"
if [ "$nregress" -eq 0 ] && [ "$ngood" -gt 0 ]; then
  echo "  >>> Passed: $ngood field(s) improved, none regressed > 1%." | tee -a "$RESULT_FILE"
else
  echo "  >>> Failed: $nregress field(s) regressed > 1%, $ngood improved." | tee -a "$RESULT_FILE"
  exit 1
fi