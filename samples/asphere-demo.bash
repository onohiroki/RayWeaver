#!/bin/bash
set -euo pipefail

# =============================================================================
# asphere-demo.bash — add a single asphere to an optimized spherical lens
#
# Purpose: show the `rayweave asphere` workflow on a lens that is already
# fully optimized with spherical surfaces. asphere-demo-init.yaml is an
# optimized 6-element double-Gauss (f/2.8 50 mm) whose curvatures have NO
# aspherical correction. For EACH case (full aperture, and the stopped-down
# variant when one is requested):
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
#   --epd X      stopped-down EPD as a FRACTION of the full entrance pupil when
#                0 < X < 1 (e.g. --epd 0.5 = half aperture), or as an absolute
#                diameter in mm when X >= 1 (e.g. --epd 12). Surface 7's
#                fixed aperture is resized via yq. Requires yq.
#   --fno N      stopped-down EPD set by F-number instead: N = EFL/N mm (e.g.
#                --fno 5.6). Requires yq.
#
# The demo runs one case — the full-aperture lens — and ONLY when a stop is
# requested (--epd/--fno) also runs a stopped-down variant, then draws a single
# side-by-side OPD-overlap chart (left = full aperture, right = stopped-down).
# Each column gets its OWN y-range (the per-case OPD min/max): aligning scales
# across the same EPD is meaningful, but two different EPDs should each be read
# on their own scale. With no stop the chart is a plain single-column plot.
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
#     vs footprint radius; with a stop, the full and stopped-down cases are
#     drawn side by side, each column on its own y-range (plus a dashed OPD=0
#     reference per column). Fields whose curves overlap share OPD the asphere
#     corrects together; fields that diverge conflict.
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
STOP_MODE=""   # "frac" | "mm" | "fno"
STOP_VALUE=""  # the numeric argument from --epd/--fno
STOP_TAG=""
STOP_LABEL=""
while [[ $# -gt 0 ]]; do
  case "$1" in
    --clean) CLEAN=true; shift ;;
    --epd)
      STOP_MODE=mm
      STOP_VALUE="$2"; shift 2
      if [[ ! "$STOP_VALUE" =~ ^[0-9]+([.][0-9]+)?$ ]]; then
        echo "error: --epd expects a numeric EPD (got '$STOP_VALUE')" >&2
        exit 1
      fi
      if ! awk -v x="$STOP_VALUE" 'BEGIN { exit !(x > 0) }' /dev/null; then
        echo "error: --epd expects a positive EPD (got '$STOP_VALUE')" >&2
        exit 1
      fi
      if awk -v x="$STOP_VALUE" 'BEGIN { exit !(x < 1) }' /dev/null; then
        STOP_MODE=frac
      fi
      ;;
    --fno)
      STOP_MODE=fno
      STOP_VALUE="$2"; shift 2
      if [[ ! "$STOP_VALUE" =~ ^[0-9]+([.][0-9]+)?$ ]]; then
        echo "error: --fno expects a numeric F-number (got '$STOP_VALUE')" >&2
        exit 1
      fi
      if ! awk -v x="$STOP_VALUE" 'BEGIN { exit !(x > 0) }' /dev/null; then
        echo "error: --fno expects a positive F-number (got '$STOP_VALUE')" >&2
        exit 1
      fi
      ;;
    *) echo "Unknown option: $1"; exit 1 ;;
  esac
done

# Resolve the stopped-down EPD:
#   frac — diameter = FULL_DIAM * X        (X in (0,1))
#   mm   — diameter = X                   (X >= 1, absolute mm)
#   fno  — diameter = EFL / N             (N = EFL/EPD)
# The labelling and file tag differ per mode.
if [[ -n "$STOP_MODE" ]]; then
  case "$STOP_MODE" in
    frac)
      STOP_TAG="-epd${STOP_VALUE}"
      pct=$(awk -v x="$STOP_VALUE" 'BEGIN { printf "%g", x * 100 }')
      STOP_LABEL="EPD ${pct}%"
      ;;
    mm)
      STOP_TAG="-epd${STOP_VALUE}"
      STOP_LABEL="EPD ${STOP_VALUE} mm"
      ;;
    fno)
      raw_tag=${STOP_VALUE}            # strip trailing zeros for the tag
      if [[ "$raw_tag" =~ ^([0-9]+)([.]0*)?$ ]]; then
        raw_tag=${BASH_REMATCH[1]}
      elif [[ "$raw_tag" =~ ^([0-9]+)[.]([0-9]*[1-9])0*$ ]]; then
        raw_tag="${BASH_REMATCH[1]}.${BASH_REMATCH[2]}"
      fi
      STOP_TAG="-fno${raw_tag}"
      STOP_LABEL="F/${STOP_VALUE}"
      ;;
  esac
fi

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
  rm -f "$OUTDIR"/asphere-demo-init-half*.yaml "$OUTDIR"/asphere-demo-init-epd*.yaml "$OUTDIR"/asphere-demo-init-fno*.yaml
  echo "  Removed: result txts, ranking/validation/apply YAMLs, spot tables, OPD charts/data, PNGs, stop init variants"
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

# ── OPD-overlap chart (per-column y-range, optional side-by-side) ──
# For every candidate surface, plots each field's mean OPD across its
# footprint radius (the opd_profiles emitted by the asphere command). When a
# right-hand case is given, left = full aperture and right = stopped-down are
# drawn side by side; otherwise a single column. Overlapping profiles = shared
# (common) OPD a rotationally-symmetric asphere can correct; diverging profiles
# = inter-field conflict. Each COLUMN gets its OWN y-range (the OPD min/max
# over that case's surfaces/fields): aligning scales is meaningful within one
# EPD but two different EPDs are each read on their own scale. Plus a dashed
# OPD=0 reference per column. Requires gnuplot; silently skipped when absent.
plot_opd_overlap() {
  local out="$1" rank_left="$2" base_left="$3" label_left="$4"
  local rank_right="${5:-}" base_right="${6:-}" label_right="${7:-}"
  local dual=false
  if [[ -n "$rank_right" && -f "$rank_right" ]]; then dual=true; fi
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
  if $dual; then build_opd_dat "$rank_right" "$base_right"; fi

  # Per-column y-range (min/max over that case's own dat files), padded by 5 %
  # so the outer envelope is not clipped. Each column is on its OWN scale.
  opd_range() {
    local base="$1" lo hi pad
    read -r lo hi < <(awk 'NF==2 { if (min=="" || $2<min) min=$2; if (max=="" || $2>max) max=$2 }
                       END { if (min!="") print min, max; else print "", "" }' "$base".*.dat)
    if [[ -n "$lo" && -n "$hi" ]]; then
      pad=$(awk -v lo="$lo" -v hi="$hi" 'BEGIN { p=(hi-lo)*0.05; if (p==0) p=0.05; printf "%.9g", p }')
      lo=$(awk -v v="$lo" -v p="$pad" 'BEGIN { printf "%.9g", v-p }')
      hi=$(awk -v v="$hi" -v p="$pad" 'BEGIN { printf "%.9g", v+p }')
      printf "set yrange [%s:%s]\n" "$lo" "$hi"
    fi
  }
  local yrange_l yrange_r
  yrange_l=$(opd_range "$base_left")
  yrange_r=""
  if $dual; then yrange_r=$(opd_range "$base_right"); fi

  # Build the gnuplot script in bash (avoids heredoc expansion under set -u)
  # and pipe it to gnuplot: one ROW per candidate surface; either one column or
  # two columns (left = full aperture, right = stopped-down); one line per
  # field. The column's y-range is applied right before each plot so the two
  # scales stay independent.
  local cols=1
  if $dual; then cols=2; fi
  local width=1200
  if $dual; then width=2400; fi
  local title="Per-field OPD per candidate surface — $label_left"
  if $dual; then
    title="Per-field OPD per candidate surface — left: $label_left, right: $label_right (per-case y-ranges, overlap = common OPD)"
  fi
  local gs="$base_left.gnu"
  {
    echo "set terminal pngcairo size ${width},$((nsid * 400))"
    echo "set output \"$out\""
    echo "set multiplot layout $nsid,$cols rowsfirst title \"$title\" font \",12\""
    echo "set xlabel \"footprint radius r (mm)\""
    echo "set ylabel \"OPD (mm)\""
    echo "set grid"
    echo "set key outside right"
    echo "set yzeroaxis dt 2 linecolor rgb \"#999999\""
    for ((si=0; si<nsid; si++)); do
      sid=$("$RAYWEAVE" query -r "asphere_candidate_result.opd_profiles[$si].surface_id" < "$rank_left")
      nf_l=$("$RAYWEAVE" query --len "asphere_candidate_result.opd_profiles[$si].fields" < "$rank_left" 2>/dev/null || echo 0)
      if [[ -n "$yrange_l" ]]; then echo "$yrange_l"; fi
      echo "set title sprintf(\"surface ${sid} — $label_left\")"
      echo "plot for [f=0:$((nf_l-1))] sprintf(\"$base_left.%d.dat\", $si) index f using 1:2 with linespoints pt 7 ps 0.7 title sprintf(\"field %d\", f)"
      if $dual; then
        nf_r=$("$RAYWEAVE" query --len "asphere_candidate_result.opd_profiles[$si].fields" < "$rank_right" 2>/dev/null || echo 0)
        if [[ -n "$yrange_r" ]]; then echo "$yrange_r"; fi
        echo "set title sprintf(\"surface ${sid} — $label_right\")"
        echo "plot for [f=0:$((nf_r-1))] sprintf(\"$base_right.%d.dat\", $si) index f using 1:2 with linespoints pt 7 ps 0.7 title sprintf(\"field %d\", f)"
      fi
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

# ── Main: full-aperture case; a stopped-down case only when requested ──
RESULT_DEFAULT="$OUTDIR/asphere-demo-result.txt"
SPOT_DEFAULT_CSV="$OUTDIR/asphere-demo-spot.csv"
GATE_OK=true

run_case "$BASE_INIT" "" "full aperture"

if [[ -n "$STOP_MODE" ]]; then
  # Build the stopped-down variant: only surface 7 (the fixed-aperture surface
  # that defines the entrance pupil) is resized. Diameter by mode:
  #   frac — FULL_DIAM * X    (X in (0,1), fraction of the full pupil)
  #   mm   — X                (X >= 1, absolute mm)
  #   fno  — EFL / N          (N = EFL/EPD, F-number)
  # FULL_DIAM and EFL come from a paraxial pass on the full-aperture system.
  # Requires yq.
  local_yq="${YQ:-$(command -v yq || true)}"
  if [[ -z "$local_yq" && -x /opt/homebrew/bin/yq ]]; then
    local_yq=/opt/homebrew/bin/yq
  fi
  if [[ -z "$local_yq" ]]; then
    echo "error: --epd/--fno requires yq (set YQ or put yq on PATH)" >&2
    exit 1
  fi

  case "$STOP_MODE" in
    frac)
      full_diam=$("$RAYWEAVE" paraxial < "$BASE_INIT" | "$RAYWEAVE" query -r paraxial_result.entrance_pupil_diameter)
      stop_diam=$(awk -v d="$full_diam" -v x="$STOP_VALUE" 'BEGIN { printf "%.12g", d * x }')
      ;;
    mm)
      stop_diam="$STOP_VALUE"
      ;;
    fno)
      efl=$("$RAYWEAVE" paraxial < "$BASE_INIT" | "$RAYWEAVE" query -r paraxial_result.focal_length)
      stop_diam=$(awk -v f="$efl" -v n="$STOP_VALUE" 'BEGIN { printf "%.12g", f / n }')
      ;;
  esac

  variant="$OUTDIR/asphere-demo-init${STOP_TAG}.yaml"
  if [ ! -f "$variant" ]; then
    "$local_yq" e ".configs[0].surfaces[] |= (select(.id == 7) | .diameter = $stop_diam)" "$BASE_INIT" > "$variant"
  fi
  echo "=== Stopped-down case (input: $variant, $STOP_LABEL) ==="
  run_case "$variant" "$STOP_TAG" "$STOP_LABEL"

  # ── Step 7: single side-by-side OPD-overlap chart ──
  echo "--- Step 7: side-by-side OPD-overlap chart (full vs $STOP_LABEL, per-case y-ranges) ---"
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
else
  # ── Step 7: single-column OPD-overlap chart (full aperture only) ──
  echo "--- Step 7: OPD-overlap chart (full aperture) ---"
  plot_opd_overlap "$OUTDIR/asphere-demo-opd-overlap.png" \
    "$OUTDIR/asphere-demo-rank.yaml" "$OUTDIR/asphere-demo-opd" "full aperture"
  echo
fi

# ── Overall gate summary ──
if [ "$GATE_OK" = true ]; then
  echo "  >>> All cases passed the gate." | tee -a "$RESULT_DEFAULT"
  exit 0
else
  echo "  >>> At least one case failed the gate." | tee -a "$RESULT_DEFAULT"
  exit 1
fi