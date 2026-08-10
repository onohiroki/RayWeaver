#!/bin/bash
set -euo pipefail

# =============================================================================
# asphere-demo.bash — add a single asphere to an optimized spherical lens
#
# Purpose: show the `rayweave asphere` workflow on a lens that is already
# fully optimized with spherical surfaces. asphere-demo-init.yaml is an
# optimized 6-element double-Gauss (f/2.8 50 mm) whose curvatures have NO
# aspherical correction. For EACH case (full aperture + stopped-down):
#   1. ranks candidate surfaces for asphere introduction,
#   2. fits even-order coefficients from the OPD residuals,
#   3. validates each top-K fit with a short DLS solve against the geometric
#      spot RMS,
#   4. inserts (--apply) the top-ranked asphere's DLS-solved coefficients,
#   5. compares the all-spherical spot RMS against the aspherized spot RMS,
#   6. draws before/after PNG diagrams and applies the per-case gate.
#
# Options
#   --clean      remove every generated artifact (full-aperture and stopped-down
#                tagged outputs); the tracked asphere-demo-init.yaml and
#                asphere-demo.bash are never touched.
#   --epd N      stopped-down case uses entrance pupil diameter N mm instead of
#                the default half-aperture (surface 7's diameter halved via
#                yq). N must be positive.
#
# The demo always runs two cases — the full-aperture lens and a stopped-down
# variant (entrance pupil diameter halved, or N mm with --epd) — and draws a
# single side-by-side OPD-overlap chart (left = full aperture, right =
# stopped-down) whose y-range is shared across BOTH cases so the scales line
# up.
#
# How to read the result
#   - The ranking score weights how well a rotationally-symmetric asphere
#     corrects the shared (field-common) OPD while penalising inter-field
#     conflict, manufacturing difficulty and optimisation instability.
#   - The validation improvement is the geometric RMS-spot reduction the
#     short DLS achieved with the asphere coefficients as the only variables.
#   - The final comparison table prints RMS before/after per field; a '✓'
#     means the aspherized surface shrank the spot, '✗' a regression.
#   - The OPD-overlap chart shows each candidate surface's per-field mean OPD
#     vs footprint radius, drawn side by side for the full and stopped-down
#     cases with one shared y-range (and a dashed OPD=0 reference). Fields
#     whose curves overlap share OPD the asphere corrects together; fields
#     that diverge conflict.
#
# The gate (one improvement, no field regressing more than 1%) is applied to
# EVERY case; any failed case makes the demo exit non-zero.
# =============================================================================

# Resolve the script's own directory so the demo runs from any CWD.
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
OUTDIR="$SCRIPT_DIR"
BASE_INIT="$SCRIPT_DIR/asphere-demo-init.yaml"

TOP_K=3
DLS_ITER=30
NUM_RAYS=128
SENS_SAMPLES=6

CLEAN=false
EPD=""
while [[ $# -gt 0 ]]; do
  case "$1" in
    --clean) CLEAN=true; shift ;;
    --epd)
      EPD="$2"; shift 2
      if [[ ! "$EPD" =~ ^[0-9]+([.][0-9]+)?$ ]]; then
        echo "error: --epd expects a numeric diameter in mm (got '$EPD')" >&2
        exit 1
      fi
      if ! awk -v x="$EPD" 'BEGIN { exit !(x > 0) }' /dev/null; then
        echo "error: --epd expects a positive diameter in mm (got '$EPD')" >&2
        exit 1
      fi
      ;;
    *) echo "Unknown option: $1"; exit 1 ;;
  esac
done

if [ "$CLEAN" = true ]; then
  echo "=== Cleaning up generated files ==="
  rm -f "$OUTDIR"/asphere-demo-result*.txt "$OUTDIR"/asphere-demo-*-result*.txt
  rm -f "$OUTDIR"/asphere-demo-rank*.yaml "$OUTDIR"/asphere-demo-*-rank*.yaml
  rm -f "$OUTDIR"/asphere-demo-validated*.yaml "$OUTDIR"/asphere-demo-*-validated*.yaml
  rm -f "$OUTDIR"/asphere-demo-applied*.yaml "$OUTDIR"/asphere-demo-*-applied*.yaml
  rm -f "$OUTDIR"/asphere-demo-spot*.tbl "$OUTDIR"/asphere-demo-*-spot*.tbl
  rm -f "$OUTDIR"/asphere-demo-spot*.csv "$OUTDIR"/asphere-demo-*-spot*.csv
  rm -f "$OUTDIR"/asphere-demo-opd*.dat "$OUTDIR"/asphere-demo-*-opd*.dat
  rm -f "$OUTDIR"/asphere-demo-opd*.gnu "$OUTDIR"/asphere-demo-*-opd*.gnu
  rm -f "$OUTDIR"/asphere-demo-opd-overlap*.png "$OUTDIR"/asphere-demo-*-opd-overlap*.png
  rm -f "$OUTDIR"/asphere-demo-before*.png "$OUTDIR"/asphere-demo-*-before*.png
  rm -f "$OUTDIR"/asphere-demo-after*.png "$OUTDIR"/asphere-demo-*-after*.png
  rm -f "$OUTDIR"/asphere-demo-init-half*.yaml "$OUTDIR"/asphere-demo-init-epd*.yaml
  echo "  Removed: result txts, ranking/validation/apply YAMLs, spot tables, OPD charts/data, PNGs, --epd init variants"
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

# fit prints a numeric field as %8.4f, or a dash when the query returned -1
# (field absent, e.g. a candidate whose coefficients fit failed).
fit() {
  if [ -z "$1" ] || [ "$1" = "nan" ] || [ "$1" = "-1" ]; then printf "%9s" "-"; else printf "%9.4f" "$1"; fi
}

rms_field() {
  "$RAYWEAVE" chief < "$1" 2>/dev/null | "$RAYWEAVE" query -r "chief_rays[$2].spot_stats.rms_r"
}

append_notes() {
  cat >> "$1" <<'EOF'

=== How to interpret this result ===
- "RMS before / RMS after" is the geometric RMS spot radius (mm) per field
  from the chief-ray pupil grid (all-spherical vs aspherized).
- The validation merit is the same geometric spot RMS, solved by a short DLS
  over the inserted surface's a4..a12 only (all other surfaces frozen).
- A '✓' means the added asphere shrank the spot for that field; '✗' a
  regression. The demo applies the top-ranked (highest-scoring) surface.
EOF
}

# ── OPD-overlap chart (side by side) ──
# For every candidate surface, plots each field's mean OPD across its
# footprint radius (the opd_profiles emitted by the asphere command) for two
# cases — left = full aperture, right = stopped-down. Overlapping profiles =
# shared (common) OPD a rotationally-symmetric asphere can correct; diverging
# profiles = inter-field conflict. All subplots on both sides share ONE y-range
# (the global OPD min/max across every surface/field of BOTH cases) plus a
# dashed OPD=0 reference so the two cases are directly comparable. Requires
# gnuplot; silently skipped when absent.
plot_opd_overlap() {
  local out="$1" rank_left="$2" base_left="$3" label_left="$4"
  local rank_right="$5" base_right="$6" label_right="$7"
  local gnuplot="${GNUPLOT:-$(command -v gnuplot || true)}"
  if [[ -z "$gnuplot" && -x /opt/homebrew/bin/gnuplot ]]; then
    gnuplot=/opt/homebrew/bin/gnuplot
  fi
  if [[ -z "$gnuplot" ]]; then
    echo "  (OPD-overlap chart skipped: gnuplot not available)"
    return 0
  fi

  local nsid
  nsid=$("$RAYWEAVE" query --len asphere_candidate_result.opd_profiles < "$rank_left" 2>/dev/null || echo 0)
  if [[ -z "$nsid" || "$nsid" -eq 0 ]]; then
    echo "  (OPD-overlap chart skipped: no opd_profiles in ranking)"
    return 0
  fi

  # Emit one data file per surface per case; each field is a block of
  # "r opd" rows separated by a blank line, so gnuplot's `index` selects the
  # field's curve.
  build_opd_dat() {
    local rank="$1" base="$2"
    local si fi j nf npoints sid r opd
    for ((si=0; si<nsid; si++)); do
      sid=$("$RAYWEAVE" query -r "asphere_candidate_result.opd_profiles[$si].surface_id" < "$rank")
      : > "$base.$si.dat"
      nf=$("$RAYWEAVE" query --len "asphere_candidate_result.opd_profiles[$si].fields" < "$rank" 2>/dev/null || echo 0)
      for ((fi=0; fi<nf; fi++)); do
        npoints=$("$RAYWEAVE" query --len "asphere_candidate_result.opd_profiles[$si].fields[$fi].opd" < "$rank" 2>/dev/null || echo 0)
        for ((j=0; j<npoints; j++)); do
          r=$("$RAYWEAVE" query -r "asphere_candidate_result.opd_profiles[$si].fields[$fi].ring_radius[$j]" < "$rank")
          opd=$("$RAYWEAVE" query -r "asphere_candidate_result.opd_profiles[$si].fields[$fi].opd[$j]" < "$rank")
          printf "%s %s\n" "$r" "$opd" >> "$base.$si.dat"
        done
        printf "\n" >> "$base.$si.dat"
      done
    done
  }
  build_opd_dat "$rank_left" "$base_left"
  build_opd_dat "$rank_right" "$base_right"

  # Shared y-range across BOTH cases so the two side-by-side columns line up.
  # Pad by 5 % so the outer envelope is not clipped.
  local yrange_cmd=""
  if compgen -G "$base_left.*.dat" > /dev/null || compgen -G "$base_right.*.dat" > /dev/null; then
    local lo hi pad
    read -r lo hi < <(awk 'NF==2 { if (min=="" || $2<min) min=$2; if (max=="" || $2>max) max=$2 }
                       END { if (min!="") print min, max; else print "", "" }' \
                      "$base_left".*.dat "$base_right".*.dat)
    if [[ -n "$lo" && -n "$hi" ]]; then
      pad=$(awk -v lo="$lo" -v hi="$hi" 'BEGIN { p=(hi-lo)*0.05; if (p==0) p=0.05; printf "%.9g", p }')
      lo=$(awk -v v="$lo" -v p="$pad" 'BEGIN { printf "%.9g", v-p }')
      hi=$(awk -v v="$hi" -v p="$pad" 'BEGIN { printf "%.9g", v+p }')
      yrange_cmd="set yrange [$lo:$hi]"
    fi
  fi

  # Build the gnuplot script in bash (avoids heredoc expansion under set -u)
  # and pipe it to gnuplot: one ROW per candidate surface, two columns (left =
  # full aperture, right = stopped-down), one line per field.
  local gs="$base_left.gnu"
  {
    echo "set terminal pngcairo size 2400,$((nsid * 400))"
    echo "set output \"$out\""
    echo "set multiplot layout $nsid,2 rowsfirst title \"Per-field OPD per candidate surface — left: $label_left, right: $label_right (shared y-range, overlap = common OPD)\" font \",12\""
    echo "set xlabel \"footprint radius r (mm)\""
    echo "set ylabel \"OPD (mm)\""
    echo "set grid"
    echo "set key outside right"
    echo "set yzeroaxis dt 2 linecolor rgb \"#999999\""
    if [[ -n "$yrange_cmd" ]]; then echo "$yrange_cmd"; fi
    for ((si=0; si<nsid; si++)); do
      sid=$("$RAYWEAVE" query -r "asphere_candidate_result.opd_profiles[$si].surface_id" < "$rank_left")
      nf_l=$("$RAYWEAVE" query --len "asphere_candidate_result.opd_profiles[$si].fields" < "$rank_left" 2>/dev/null || echo 0)
      nf_r=$("$RAYWEAVE" query --len "asphere_candidate_result.opd_profiles[$si].fields" < "$rank_right" 2>/dev/null || echo 0)
      echo "set title sprintf(\"surface ${sid} — $label_left\")"
      echo "plot for [f=0:$((nf_l-1))] sprintf(\"$base_left.%d.dat\", $si) index f using 1:2 with linespoints pt 7 ps 0.7 title sprintf(\"field %d\", f)"
      echo "set title sprintf(\"surface ${sid} — $label_right\")"
      echo "plot for [f=0:$((nf_r-1))] sprintf(\"$base_right.%d.dat\", $si) index f using 1:2 with linespoints pt 7 ps 0.7 title sprintf(\"field %d\", f)"
    done
    echo "unset multiplot"
  } > "$gs"
  GNUTERM=pngcairo "$gnuplot" "$gs" 2>/dev/null
  echo "Written: $out"
}

# ── One full demo case ──
# Runs steps 1–5 (ranking → validation → apply → spot RMS → PNG + gate) for a
# single input system. Every artifact carries the given tag
# (asphere-demo<TAG>-{rank,validated,applied,result,spot,...}). A per-field
# spot CSV is written alongside the table for the cross-case comparison.
# The OPD-overlap chart is drawn once, after all cases, by plot_opd_overlap.
run_case() {
  local from="$1" tag="$2" label="$3"
  local applied="$OUTDIR/asphere-demo${tag}-applied.yaml"
  local result="$OUTDIR/asphere-demo${tag}-result.txt"
  local rank="$OUTDIR/asphere-demo${tag}-rank.yaml"
  local validated="$OUTDIR/asphere-demo${tag}-validated.yaml"
  local spot="$OUTDIR/asphere-demo${tag}-spot.tbl"
  local spot_csv="$OUTDIR/asphere-demo${tag}-spot.csv"

  : > "$result"
  echo "=== Case: $label ===" | tee -a "$result"
  echo | tee -a "$result"

  # ── Step 1: candidate ranking ──
  echo "--- Step 1: candidate ranking (top $TOP_K) ---"
  "$RAYWEAVE" asphere --top-k "$TOP_K" --sensitivity-samples "$SENS_SAMPLES" < "$from" > "$rank"
  {
    printf "  %-6s %10s %10s\n" "surf" "score" "sens.imprv"
    for ri in $(seq 0 $((TOP_K - 1))); do
      sid=$("$RAYWEAVE" query -r "asphere_candidate_result.rankings[$ri].surface_id" < "$rank")
      score=$("$RAYWEAVE" query -r "asphere_candidate_result.rankings[$ri].score" < "$rank")
      sens=$("$RAYWEAVE" query -r "asphere_candidate_result.rankings[$ri].sensitivity.improvement" < "$rank" 2>/dev/null || echo "-1")
      printf "  %-6s %10s %10s\n" "$sid" "$(fit "$score")" "$(fit "$sens")"
    done
  } | tee -a "$result"
  echo

  # ── Step 2: validation ──
  echo "--- Step 2: short-DLS validation (spot-RMS merit, $DLS_ITER it + ${NUM_RAYS} rays) ---"
  "$RAYWEAVE" asphere --validate --top-k "$TOP_K" --sensitivity-samples "$SENS_SAMPLES" \
    --dls-iter "$DLS_ITER" --num-rays "$NUM_RAYS" < "$from" > "$validated"
  {
    printf "  %-6s %12s %12s %12s %13s\n" "surf" "before" "after" "imprv" "a4"
    for ri in $(seq 0 $((TOP_K - 1))); do
      sid=$("$RAYWEAVE" query -r "asphere_candidate_result.rankings[$ri].surface_id" < "$validated")
      before=$("$RAYWEAVE" query -r "asphere_candidate_result.rankings[$ri].validation.before_merit" < "$validated" 2>/dev/null || echo "-")
      after=$("$RAYWEAVE" query -r "asphere_candidate_result.rankings[$ri].validation.after_merit" < "$validated" 2>/dev/null || echo "-")
      imprv=$("$RAYWEAVE" query -r "asphere_candidate_result.rankings[$ri].validation.improvement" < "$validated" 2>/dev/null || echo "-")
      a4=$("$RAYWEAVE" query -r "asphere_candidate_result.rankings[$ri].validation.coefficients.A4" < "$validated" 2>/dev/null || echo "-")
      if [ "$before" = "-1" ] || [ "$before" = "-" ]; then
        printf "  %-6s %12s %12s %12s %13s\n" "$sid" "(no fit)" "" "" ""
      elif [ "$before" = "-1" ] || [ "$after" = "-1" ]; then
        printf "  %-6s %12s %12s %12s %13s\n" "$sid" "-" "-" "-" "-"
      else
        printf "  %-6s %12.4f %12.4f %12.4f %13.3e\n" "$sid" "$before" "$after" "$imprv" "$a4"
      fi
    done
  } | tee -a "$result"
  echo

  # ── Step 3: apply the top-ranked aspherical ──
  echo "--- Step 3: inserting the top-ranked aspherical surface (--apply) ---"
  "$RAYWEAVE" asphere --apply --top-k "$TOP_K" \
    --dls-iter "$DLS_ITER" --num-rays "$NUM_RAYS" < "$from" > "$applied"
  echo "Written: $applied" | tee -a "$result"
  echo

  # ── Step 4: spot RMS before/after ──
  echo "--- Step 4: spot RMS comparison (all-spherical vs aspherical) ---"
  nfield=$("$RAYWEAVE" chief < "$from" | "$RAYWEAVE" query --len chief_rays)
  : > "$spot"
  : > "$spot_csv"
  nregress=0
  ngood=0
  printf "  %-6s %12s %12s %10s\n" "field" "RMS before" "RMS after" "" >> "$spot"
  printf "  %-6s %12s %12s %10s\n" "-----" "-----------" "----------" "-------" >> "$spot"
  for fi in $(seq 0 $((nfield - 1))); do
    before=$(rms_field "$from" "$fi")
    after=$(rms_field "$applied" "$fi")
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
    printf "  %-6s %12.4f %12.4f %10s\n" "f$fi" "$before" "$after" "$mark" >> "$spot"
    printf "f%s %s %s\n" "$fi" "$before" "$after" >> "$spot_csv"
  done
  echo >> "$spot"
  printf "  (gate: no field may regress by more than 1%%, and at least one must improve)\n" >> "$spot"
  cat "$spot" | tee -a "$result"

  # ── Step 5: PNG diagrams ──
  echo "--- Step 5: PNG diagrams ---"
  "$RAYWEAVE" chief --clear-aperture --ray-fan < "$from" | "$RAYWEAVE" trace \
    | "$RAYWEAVE" plot -o "$OUTDIR/asphere-demo${tag}-before.png" >/dev/null
  echo "Written: $OUTDIR/asphere-demo${tag}-before.png"
  "$RAYWEAVE" chief --clear-aperture --ray-fan < "$applied" | "$RAYWEAVE" trace \
    | "$RAYWEAVE" plot -o "$OUTDIR/asphere-demo${tag}-after.png" >/dev/null
  echo "Written: $OUTDIR/asphere-demo${tag}-after.png"

  # ── Step 6: gate check ──
  echo "--- Step 6: gate check ---"
  if [ "$nregress" -eq 0 ] && [ "$ngood" -gt 0 ]; then
    echo "  >>> Passed ($label): $ngood field(s) improved, none regressed > 1%." | tee -a "$result"
  else
    echo "  >>> Failed ($label): $nregress field(s) regressed > 1%, $ngood improved." | tee -a "$result"
    GATE_OK=false
  fi
  append_notes "$result"
  echo
}

# ── Main: full-aperture case, then a stopped-down case ──
RESULT_DEFAULT="$OUTDIR/asphere-demo-result.txt"
SPOT_DEFAULT_CSV="$OUTDIR/asphere-demo-spot.csv"
GATE_OK=true

STOP_TAG="-half"
STOP_LABEL="half EPD"
STOP_EPD=""
if [[ -n "$EPD" ]]; then
  STOP_TAG="-epd${EPD}"
  STOP_LABEL="EPD=${EPD} mm"
  STOP_EPD="$EPD"
fi

run_case "$BASE_INIT" "" "full aperture"

# Build the stopped-down variant: only surface 7 (the fixed-aperture surface
# that defines the entrance pupil) is resized — to the half pupil by default,
# or to EPD mm with --epd. Requires yq.
local_yq="${YQ:-$(command -v yq || true)}"
if [[ -z "$local_yq" && -x /opt/homebrew/bin/yq ]]; then
  local_yq=/opt/homebrew/bin/yq
fi
if [[ -z "$local_yq" ]]; then
  echo "error: the stopped-down case requires yq (set YQ or put yq on PATH)" >&2
  exit 1
fi
variant="$OUTDIR/asphere-demo-init${STOP_TAG}.yaml"
if [ ! -f "$variant" ]; then
  if [[ -n "$STOP_EPD" ]]; then
    "$local_yq" e ".configs[0].surfaces[] |= (select(.id == 7) | .diameter = $STOP_EPD)" "$BASE_INIT" > "$variant"
  else
    "$local_yq" e ".configs[0].surfaces[] |= (select(.id == 7) | .diameter = (.diameter / 2))" "$BASE_INIT" > "$variant"
  fi
fi
echo "=== Stopped-down case (input: $variant) ==="
run_case "$variant" "$STOP_TAG" "$STOP_LABEL"

# ── Step 7: single side-by-side OPD-overlap chart ──
echo "--- Step 7: side-by-side OPD-overlap chart (full vs $STOP_LABEL, shared y-range) ---"
plot_opd_overlap "$OUTDIR/asphere-demo-opd-overlap.png" \
  "$OUTDIR/asphere-demo-rank.yaml" "$OUTDIR/asphere-demo-opd" "full aperture" \
  "$OUTDIR/asphere-demo${STOP_TAG}-rank.yaml" "$OUTDIR/asphere-demo${STOP_TAG}-opd" "$STOP_LABEL"
echo

# ── Step 8: full vs stopped-down comparison ──
echo "--- Step 8: full-aperture vs $STOP_LABEL spot-RMS comparison ---"
{
  printf "  %-6s %14s %14s %14s %14s\n" "field" "full.before" "full.after" "stop.before" "stop.after"
  printf "  %-6s %14s %14s %14s %14s\n" "-----" "----------" "---------" "----------" "---------"
  nf=$("$RAYWEAVE" chief < "$BASE_INIT" | "$RAYWEAVE" query --len chief_rays)
  for fi in $(seq 0 $((nf - 1))); do
    line_def=$(grep -E "^f$fi " "$SPOT_DEFAULT_CSV")
    line_stop=$(grep -E "^f$fi " "$OUTDIR/asphere-demo${STOP_TAG}-spot.csv")
    read -r _ db da <<< "$line_def"
    read -r _ eb ea <<< "$line_stop"
    printf "  %-6s %14.4f %14.4f %14.4f %14.4f\n" "f$fi" "$db" "$da" "$eb" "$ea"
  done
} | tee -a "$RESULT_DEFAULT"
echo

# ── Overall gate summary ──
if [ "$GATE_OK" = true ]; then
  echo "  >>> All cases passed the gate." | tee -a "$RESULT_DEFAULT"
  exit 0
else
  echo "  >>> At least one case failed the gate." | tee -a "$RESULT_DEFAULT"
  exit 1
fi