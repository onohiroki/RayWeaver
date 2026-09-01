#!/bin/bash
set -euo pipefail

# =============================================================================
# escape-demo.bash — global optimisation with the escape function
#
# Purpose: show the Ishiki-Ono style escape-function optimiser: DLS cycles
# with merit-function bumps at discovered local minima, so the result is a
# list of local minima and the best one wins.
#
# Two lenses are supported:
#   escape-demo.bash                     : degraded US2645157 triplet (default)
#   escape-demo.bash --lens doublegauss  : 6-element double-Gauss (f/2.8 50 mm)
# The double-Gauss run is much slower (36 variables, 256 rays) but uses the
# same escape section baked into samples/doublegauss-init.yaml.
#
# Steps
#   1. escape                  : global search with merit_schedule
#                                (spot_phase -> wavefront_phase step switch
#                                at merit_ratio=0.1) -> <prefix>result.yaml
#   2. PSF verification        : per-field Strehl at best focus (RCP+LCP, d
#                                line) -> table; the demo gates on every field
#                                reaching >= 0.5
#   3. doublegauss --save/--log: every discovered minimum written to a clean
#                                <prefix>min1.yaml, <prefix>min2.yaml, ... as
#                                it is found (interrupt/kill safe), progress
#                                streamed to <prefix>progress.jsonl
#      triplet                 : escape extract --index N pulls one full
#                                local-minimum system out
#   4. plot                    : diagrams of the initial and best systems
#   5. element-powers chart    : a PNG showing every local minimum's
#                                element_powers offset from a merit baseline
#   The double-Gauss init uses inline model glasses (nd/vd per element) so the
#   escape optimises each element's glass independently; the minima summary
#   prints the per-element vd (a '.' marks a crown/flint role flip across 45)
#   and a gate checks that the glass actually changed between minima.
#   The double-Gauss input also carries an optimization.power_solve section, so
#   every escape cycle gains a dedicated power-preserving glass phase: the
#   non-glass variables are locked and the element thin-lens powers held fixed
#   while a colour-only merit rebalances the glasses (see the glass-phase note
#   in the output).
#
# How to read the result
#   - Best merit is the lowest DLS merit among the discovered minima.
#   - The result YAML holds every minimum's full surfaces; use
#     `escape extract --index N` to re-optimise from a chosen one. With
#     --save the per-minimum clean lens files are already on disk.
# =============================================================================

# Resolve the script's own directory so the demo runs from any CWD
# (repo root, `cd samples`, or a copied location). All data files are read
# from and all outputs are written to this directory.
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
OUTDIR="$SCRIPT_DIR"

# CLI options
LENS="triplet"
CLEAN=false
while [[ $# -gt 0 ]]; do
  case "$1" in
    --clean) CLEAN=true; shift ;;
    --lens)
      shift
      case "${1:-}" in
        triplet|doublegauss) LENS="$1"; shift ;;
        *) echo "Error: --lens must be 'triplet' or 'doublegauss' (got '${1:-}')"; exit 1 ;;
      esac
      ;;
    *) echo "Unknown option: $1"; exit 1 ;;
  esac
done

case "$LENS" in
  triplet)
    YAML="$SCRIPT_DIR/escape-demo.yaml"
    PREFIX="escape-demo-"
    LENS_NAME="degraded US2645157 triplet"
    SAVE_BASE=""   # triplet: keep the demo light, no per-minimum save/log
    LOG_FILE=""
    ;;
  doublegauss)
    YAML="$SCRIPT_DIR/doublegauss-init.yaml"
    PREFIX="escape-demo-doublegauss-"
    LENS_NAME="6-element double-Gauss f/2.8 (50 mm)"
    # Long run: save every discovered minimum to a clean lens file (--save) and
    # stream the JSONL progress to a log (--log), so a killed run never loses
    # already-found minima and the progress is inspectable afterwards.
    SAVE_BASE="$OUTDIR/${PREFIX}min"
    LOG_FILE="$OUTDIR/${PREFIX}progress.jsonl"
    ;;
esac
RESULT="$OUTDIR/${PREFIX}result.yaml"
RESULT_FILE="$OUTDIR/${PREFIX}result.txt"

if [ "$CLEAN" = true ]; then
  echo "=== Cleaning up generated files ==="
  rm -f "$OUTDIR"/escape-demo-result.yaml "$OUTDIR"/escape-demo-result.txt
  rm -f "$OUTDIR"/escape-demo-init.png "$OUTDIR"/escape-demo-best.png "$OUTDIR"/escape-demo-min1.png "$OUTDIR"/escape-demo-min1.yaml
  rm -f "$OUTDIR"/escape-demo-best.yaml "$OUTDIR"/escape-demo-psf.yaml
  rm -f "$OUTDIR"/escape-demo-doublegauss-result.yaml "$OUTDIR"/escape-demo-doublegauss-result.txt
  rm -f "$OUTDIR"/escape-demo-doublegauss-progress.jsonl
  rm -f "$OUTDIR"/escape-demo-doublegauss-min*.yaml
  rm -f "$OUTDIR"/escape-demo-doublegauss-init.png "$OUTDIR"/escape-demo-doublegauss-best.png "$OUTDIR"/escape-demo-doublegauss-min1.png
  rm -f "$OUTDIR"/escape-demo-doublegauss-best.yaml "$OUTDIR"/escape-demo-doublegauss-psf.yaml
  rm -f "$OUTDIR"/escape-demo-element-powers.png "$OUTDIR"/escape-demo-doublegauss-element-powers.png
  rm -f "$OUTDIR"/escape-demo-element-powers.dat "$OUTDIR"/escape-demo-doublegauss-element-powers.dat
  rm -f "$OUTDIR"/escape-powers-*.dat "$OUTDIR"/escape-powers-*.png
  echo "  Removed: triplet and double-Gauss escape outputs"
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
cat >> "$RESULT_FILE" <<EOF

=== How to interpret this result ===
- Escape-function global optimisation of the $LENS_NAME.
- Best merit is the lowest DLS merit among all discovered local minima
  (lower = better); [0] is the best minimum, [1] a worse secondary one.
- The '*' marks the best minimum. $(basename "$RESULT") contains the full
  surfaces of every minimum; \`escape extract --index N\` pulls one out
  (e.g. ${PREFIX}min1.yaml) to re-optimise from.
- \`element_powers\` (per minimum, per config) is the thin-lens power of each
  lens element at the d-line — a fingerprint for comparing the minima against
  each other.
- The per-field PSF Strehl at best focus is printed in the verification table
  above; the demo gates on every field reaching >= 0.5.
- If the best merit is only marginally better than a plain local DLS run,
  the merit landscape is effectively single-modal and escape is unnecessary.
- The double-Gauss glass surfaces are inline model glasses: each element's
  nd/vd is an independent escape variable (with wide 1.4-2.0 / 20-80 ranges),
  so different local minima can carry different glasses — the per-minimum
  vd[1 3 5 8 10 12] column in the minima summary shows the crown/flint
  arrangement of every solution, and a '.' marks a vd that flipped across the
  45 boundary relative to the SK18/SF12 start. The escape merit adds six
  low-weight glass_role terms steering each element toward its chromatic-role
  target on top of the spot/colour/opd merit.
EOF
if [[ -n "$SAVE_BASE" ]]; then
cat >> "$RESULT_FILE" <<EOF
- This double-Gauss run used --save: every discovered minimum was written to a
  clean lens file (${PREFIX}min1.yaml, ${PREFIX}min2.yaml, ...) as it was found
  (atomic writes, so even a killed run keeps them). When a minimum is improved,
  the previous version is archived as ${PREFIX}minN.<version>.yaml. The JSONL
  progress went to ${PREFIX}progress.jsonl (read it with \`query --jsonl\`).
EOF
fi
}
trap append_interpretation EXIT

# ── Element-powers chart: one offset line per local minimum ──
# Reads the result YAML and draws a PNG whose vertical axis is merit (log
# scale); each minimum's element_powers are a polyline offset from its merit
# baseline. Requires gnuplot (fallback to /opt/homebrew/bin) and the rayweave
# binary (RAYWEAVE). Silently returns when gnuplot is absent.
plot_element_powers() {
  local out="$1"
  local data="$OUTDIR/${PREFIX}element-powers.dat"
  local gnuplot="${GNUPLOT:-$(command -v gnuplot || true)}"
  if [[ -z "$gnuplot" && -x /opt/homebrew/bin/gnuplot ]]; then
    gnuplot=/opt/homebrew/bin/gnuplot
  fi
  if [[ -z "$gnuplot" ]]; then
    echo "  (element-powers chart skipped: gnuplot not available)"
    return 0
  fi

  local nmin nel
  nmin=$("$RAYWEAVE" query --len escape_result.minima < "$RESULT" 2>/dev/null || echo 0)
  if [[ -z "$nmin" || "$nmin" -eq 0 ]]; then
    echo "  (element-powers chart skipped: no minima in result)"
    return 0
  fi
  nel=$("$RAYWEAVE" query --len escape_result.minima[0].features[0].element_powers < "$RESULT" 2>/dev/null || echo 6)
  if [[ -z "$nel" || "$nel" -eq 0 ]]; then nel=6; fi

  # One row per (minimum, element): idx merit element_index power
  : > "$data"
  local i e merit p
  for ((i=0; i<nmin; i++)); do
    merit=$("$RAYWEAVE" query -r "escape_result.minima[$i].merit" < "$RESULT")
    for ((e=0; e<nel; e++)); do
      p=$("$RAYWEAVE" query -r "escape_result.minima[$i].features[0].element_powers[$e]" < "$RESULT")
      echo "$i $merit $e $p" >> "$data"
    done
  done

  GNUTERM=pngcairo "$gnuplot" <<GPLOT 2>/dev/null
  set terminal pngcairo size 1100,750
  set output "$out"

  set xlabel "element index"
  set ylabel "merit (log scale)"
  set title "A: local minima by merit — element_powers as offset lines from the merit baseline"
  set key outside right
  set grid ytics

  set xrange [-0.5:"$((nel-1)).5"]
  set xtics 0,1,$((nel-1))

  SCALE = 6.0
  # merit baseline for each minimum: log10(merit), drawn as a faint rule
  plot for [i=0:$((nmin-1))] \
    "$data" using 3:(log10(\$2) + \$4*SCALE) every ::i*$nel::(i*$nel+$nel-1) \
      with linespoints pt 7 ps 1.2 lw 2 title sprintf("min %d", i), \
    for [i=0:$((nmin-1))] \
    "$data" using 3:(log10(\$2)) every ::i*$nel::(i*$nel+$nel-1) \
      with lines lc rgb "#888888" lw 0.5 notitle
GPLOT
  echo "Written: $out"
}

echo "=== Escape demo: global optimisation of the $LENS_NAME ==="
echo

echo "--- Running escape-function global optimisation (JSONL progress on stderr) ---"
ESCAPE_ARGS=(--verbose)
if [[ -n "$LOG_FILE" ]]; then
  ESCAPE_ARGS+=(--log "$LOG_FILE")
fi
if [[ -n "$SAVE_BASE" ]]; then
  ESCAPE_ARGS+=(--save "$SAVE_BASE")
fi
$RAYWEAVE escape "${ESCAPE_ARGS[@]}" < "$YAML" > "$RESULT"
echo

echo "--- Local minima summary ---"
{
  BEST_IDX=$($RAYWEAVE query -r escape_result.best_index < "$RESULT")
  BEST_MERIT=$($RAYWEAVE query --printf '%.6e' escape_result.best_merit < "$RESULT")
  echo "  Best index: $BEST_IDX  Best merit: $BEST_MERIT"
  # Glass surfaces of the double-Gauss (inline model glasses): the first
  # surface of each element carries its nd/vd. Shown as vd per element so the
  # crown/flint arrangement (and any role flips vs the nominal SK18/SF12 start)
  # is visible; a '.' marks a vd that crossed the 45 crown/flint boundary.
  # The variable names follow the doublegauss-init.yaml convention s<N>_<g>_vd.
  GLASS_VARS="s1_sk18_vd s3_sf12_vd s5_sk18_vd s8_sk18_vd s10_sf12_vd s12_sk18_vd"
  $RAYWEAVE query --each 'escape_result.minima[]:index,merit' --printf '%d %.6e' < "$RESULT" \
    | while read -r idx merit; do
        mark=" "
        [ "$idx" = "$BEST_IDX" ] && mark="*"
        powers=$($RAYWEAVE query --each "escape_result.minima[$idx].features[0].element_powers[]" \
          --printf '%.4g' < "$RESULT" | paste -sd ',' -)
        glass=""
        for vn in $GLASS_VARS; do
          vd=$($RAYWEAVE query -r "escape_result.minima[$idx].variables[name=\"$vn\"].after" < "$RESULT")
          if [[ -n "$vd" && "$vd" != "-1" ]]; then
            fl=" "
            $RAYWEAVE query --gate "v < 45" --set v="$vd" < /dev/null > /dev/null 2>&1 && fl="."
            glass="$glass $vd$fl"
          fi
        done
        if [[ -n "$SAVE_BASE" ]]; then
          file=$(basename "$($RAYWEAVE query -r "escape_result.minima[$idx].file" < "$RESULT")")
          printf "  %s[%s] merit=%s  file=%s  element_powers=%s  vd[1 3 5 8 10 12]=%s\n" "$mark" "$idx" "$merit" "$file" "$powers" "$glass"
        else
          printf "  %s[%s] merit=%s  element_powers=%s  vd[1 3 5 8 10 12]=%s\n" "$mark" "$idx" "$merit" "$powers" "$glass"
        fi
      done
} | tee "$RESULT_FILE"
echo

# Glass-role gate (double-Gauss only): with inline model glasses the escape
# can change each element's glass independently, so different local minima
# should carry different glasses. Check that at least two minima differ in the
# vd of a glass surface (the crown/flint arrangement moves between solutions).
if [ "$LENS" = "doublegauss" ]; then
  echo "--- Power-preserving glass phase (double-Gauss) ---"
  echo "  Each cycle added a glass_dls phase: it locked every variable except the"
  echo "  glass dispersions, reversed the merit to a colour-only (LCA/TCA) objective,"
  echo "  and held the element thin-lens powers fixed so only the glasses rebalanced."
  if [[ -n "$LOG_FILE" && -f "$LOG_FILE" ]]; then
    GPHASES=$(grep -c '"phase":"glass_dls"' "$LOG_FILE" || true)
    echo "  glass_dls phases run: ${GPHASES:-0}"
  fi
  echo "--- Glass-change gate (glass optimized per element) ---"
  NMIN=$($RAYWEAVE query --len escape_result.minima < "$RESULT")
  GATE_GLASS_OK=false
  if [ "${NMIN:-0}" -ge 2 ]; then
    VD0=$($RAYWEAVE query -r 'escape_result.minima[0].variables[name="s3_sf12_vd"].after' < "$RESULT")
    VD1=$($RAYWEAVE query -r 'escape_result.minima[1].variables[name="s3_sf12_vd"].after' < "$RESULT")
    if [[ -n "$VD0" && -n "$VD1" && "$VD0" != "$VD1" ]]; then
      GATE_GLASS_OK=true
      echo "  S3 vd differs between minima: $VD0 vs $VD1"
    fi
  fi
  if [ "$GATE_GLASS_OK" = true ]; then
    echo "  => The escape changed the glass between minima (per-element nd/vd)."
  else
    echo "  => No glass change detected between the first two minima (merit may need retuning)."
  fi
  echo
fi

BEST_IDX=$($RAYWEAVE query -r escape_result.best_index < "$RESULT")
$RAYWEAVE escape extract --index "$BEST_IDX" < "$RESULT" > "$OUTDIR/${PREFIX}best.yaml"
echo "Written: $OUTDIR/${PREFIX}best.yaml (best minimum $BEST_IDX)"
echo

echo "--- PSF verification (all fields Strehl >= 0.5) ---"
$RAYWEAVE psf --polarization RCP+LCP --wavelengths 0.0005876 --best-focus \
  < "$OUTDIR/${PREFIX}best.yaml" > "$OUTDIR/${PREFIX}psf.yaml"
NF=$($RAYWEAVE query --len psf_results < "$OUTDIR/${PREFIX}psf.yaml")
GATE_OK=true
for ((i = 0; i < NF; i++)); do
  ang=$($RAYWEAVE query -r "psf_results[$i].field_angle" < "$OUTDIR/${PREFIX}psf.yaml")
  s=$($RAYWEAVE query -r "psf_results[$i].strehl_ratio" < "$OUTDIR/${PREFIX}psf.yaml")
  ok=$(awk -v x="$s" 'BEGIN { if (x + 0 >= 0.5) print "OK"; else print "BELOW 0.5" }')
  [ "$ok" = "OK" ] || GATE_OK=false
  printf "  %s deg: Strehl %.4f  %s\n" "$ang" "$s" "$ok"
done
if [ "$GATE_OK" = true ]; then
  echo "  => All fields reach Strehl >= 0.5."
else
  echo "  => Some fields are below Strehl 0.5."
fi
echo

echo "--- PNG diagrams ---"
echo "=== Initial diagram ==="
$RAYWEAVE chief --clear-aperture --ray-fan < "$YAML" | $RAYWEAVE trace \
  | $RAYWEAVE plot -o "$OUTDIR/${PREFIX}init.png" >/dev/null
echo "Written: $OUTDIR/${PREFIX}init.png"

echo "=== Best-solution diagram ==="
$RAYWEAVE chief --clear-aperture --ray-fan < "$OUTDIR/${PREFIX}best.yaml" | $RAYWEAVE trace \
  | $RAYWEAVE plot -o "$OUTDIR/${PREFIX}best.png" >/dev/null
echo "Written: $OUTDIR/${PREFIX}best.png"

echo "=== Element-powers chart ==="
plot_element_powers "$OUTDIR/${PREFIX}element-powers.png"

if [[ -n "$SAVE_BASE" ]]; then
  echo "=== Saved local minima (--save) ==="
  echo "  Every discovered minimum was written as a clean lens file:"
  ls -1 "$OUTDIR"/${PREFIX}min[0-9]*.yaml 2>/dev/null | sed 's#.*/##' | sed 's/^/    /' || true
else
  echo "=== Extracting a local minimum ==="
  NMIN=$($RAYWEAVE query --len escape_result.minima < "$RESULT" 2>/dev/null || echo 0)
  if [[ "${NMIN:-0}" -ge 2 ]]; then
    IDX=1
  else
    IDX=0
  fi
  $RAYWEAVE escape extract --index "$IDX" < "$RESULT" > "$OUTDIR/${PREFIX}min1.yaml"
  echo "Written: $OUTDIR/${PREFIX}min1.yaml (minimum $IDX)"
fi
