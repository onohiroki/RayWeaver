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
#   1. escape                  : global search -> <prefix>result.yaml
#   2. DLS refinement          : the escape best is refined with a
#                                wavefront_astigmatism merit that lifts the
#                                16° middle field to Strehl >= 0.5 while a
#                                24° wavefront_astigmatism term (w24=6500)
#                                plus the 24° spot anchor (w24=0.11) keep the
#                                outer field healthy -> <prefix>refined.yaml
#   3. PSF verification        : per-field Strehl at best focus (RCP+LCP, d
#                                line) -> table; the demo gates on every field
#                                reaching >= 0.5
#   4. doublegauss --save/--log: every discovered minimum written to a clean
#                                <prefix>min1.yaml, <prefix>min2.yaml, ... as
#                                it is found (interrupt/kill safe), progress
#                                streamed to <prefix>progress.jsonl
#      triplet                 : escape extract --index N pulls one full
#                                local-minimum system out
#   5. plot                    : diagrams of the initial and refined systems
#   6. element-powers chart    : a PNG showing every local minimum's
#                                element_powers offset from a merit baseline
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
    LENS_NAME="6-element double-Gauss (f/2.8 50 mm)"
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
  rm -f "$OUTDIR"/escape-demo-best.yaml "$OUTDIR"/escape-demo-refine-in.yaml "$OUTDIR"/escape-demo-refined.yaml "$OUTDIR"/escape-demo-psf.yaml
  rm -f "$OUTDIR"/escape-demo-doublegauss-result.yaml "$OUTDIR"/escape-demo-doublegauss-result.txt
  rm -f "$OUTDIR"/escape-demo-doublegauss-progress.jsonl
  rm -f "$OUTDIR"/escape-demo-doublegauss-min*.yaml
  rm -f "$OUTDIR"/escape-demo-doublegauss-init.png "$OUTDIR"/escape-demo-doublegauss-best.png "$OUTDIR"/escape-demo-doublegauss-min1.png
  rm -f "$OUTDIR"/escape-demo-doublegauss-best.yaml "$OUTDIR"/escape-demo-doublegauss-refine-in.yaml "$OUTDIR"/escape-demo-doublegauss-refined.yaml "$OUTDIR"/escape-demo-doublegauss-psf.yaml
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
- The escape best is refined with a wavefront_astigmatism merit
  (${PREFIX}refined.yaml) so every field reaches a best-focus Strehl >= 0.5;
  the per-field numbers and the pass/fail gate are printed in the PSF
  verification table above.
- If the best merit is only marginally better than a plain local DLS run,
  the merit landscape is effectively single-modal and escape is unnecessary.
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
  $RAYWEAVE query --each 'escape_result.minima[]:index,merit' --printf '%d %.6e' < "$RESULT" \
    | while read -r idx merit; do
        mark=" "
        [ "$idx" = "$BEST_IDX" ] && mark="*"
        powers=$($RAYWEAVE query --each "escape_result.minima[$idx].features[0].element_powers[]" \
          --printf '%.4g' < "$RESULT" | paste -sd ',' -)
        if [[ -n "$SAVE_BASE" ]]; then
          file=$(basename "$($RAYWEAVE query -r "escape_result.minima[$idx].file" < "$RESULT")")
          printf "  %s[%s] merit=%s  file=%s  element_powers=%s\n" "$mark" "$idx" "$merit" "$file" "$powers"
        else
          printf "  %s[%s] merit=%s  element_powers=%s\n" "$mark" "$idx" "$merit" "$powers"
        fi
      done
} | tee "$RESULT_FILE"
echo

echo "--- DLS refinement of the escape best (wavefront-astigmatism merit) ---"
# The escape's spot merit robustly finds the well-corrected landscape (the 24°
# outer field is already >= 0.5), but leaves the 16° middle field weak. A short
# DLS refinement with a wavefront_astigmatism merit lifts the middle field to
# Strehl >= 0.5; a second 24° wavefront_astigmatism term (w=6500) plus the 24°
# spot anchor (w=0.11) keeps the outer field healthy. The result is the demo's
# final lens.
BEST_IDX=$($RAYWEAVE query -r escape_result.best_index < "$RESULT")
$RAYWEAVE escape extract --index "$BEST_IDX" < "$RESULT" > "$OUTDIR/${PREFIX}best.yaml"
awk '/^      merit:/{exit} {print}' "$OUTDIR/${PREFIX}best.yaml" > "$OUTDIR/${PREFIX}refine-in.yaml"
case "$LENS" in
  triplet)
    cat >> "$OUTDIR/${PREFIX}refine-in.yaml" <<'MERIT'
      merit:
        type: weighted_sum
        terms:
            - kind: spot_rms
              field: 0
              wavelength: 0.0004861
              weight: 2.0
            - kind: spot_rms
              field: 0
              wavelength: 0.0005876
              weight: 2.0
            - kind: spot_rms
              field: 0
              wavelength: 0.0006563
              weight: 2.0
            - kind: spot_rms
              field: 1
              wavelength: 0.0004861
              weight: 1.0
            - kind: spot_rms_t
              field: 1
              wavelength: 0.0004861
              weight: 0.5
            - kind: spot_rms_s
              field: 1
              wavelength: 0.0004861
              weight: 0.5
            - kind: spot_rms_worst
              field: 1
              wavelength: 0.0004861
              weight: 1.0
            - kind: wavefront_astigmatism
              field: 1
              wavelength: 0.0004861
              weight: 14000.0
              target: 0
            - kind: spot_rms
              field: 1
              wavelength: 0.0005876
              weight: 1.0
            - kind: spot_rms_t
              field: 1
              wavelength: 0.0005876
              weight: 0.5
            - kind: spot_rms_s
              field: 1
              wavelength: 0.0005876
              weight: 0.5
            - kind: spot_rms_worst
              field: 1
              wavelength: 0.0005876
              weight: 1.0
            - kind: wavefront_astigmatism
              field: 1
              wavelength: 0.0005876
              weight: 14000.0
              target: 0
            - kind: spot_rms
              field: 1
              wavelength: 0.0006563
              weight: 1.0
            - kind: spot_rms_t
              field: 1
              wavelength: 0.0006563
              weight: 0.5
            - kind: spot_rms_s
              field: 1
              wavelength: 0.0006563
              weight: 0.5
            - kind: spot_rms_worst
              field: 1
              wavelength: 0.0006563
              weight: 1.0
            - kind: wavefront_astigmatism
              field: 1
              wavelength: 0.0006563
              weight: 14000.0
              target: 0
            - kind: wavefront_astigmatism
              field: 2
              wavelength: 0.0004861
              weight: 6500.0
              target: 0
            - kind: wavefront_astigmatism
              field: 2
              wavelength: 0.0005876
              weight: 6500.0
              target: 0
            - kind: wavefront_astigmatism
              field: 2
              wavelength: 0.0006563
              weight: 6500.0
              target: 0
            - kind: spot_rms_worst
              field: 2
              wavelength: 0.0004861
              weight: 0.11
            - kind: spot_rms_weighted
              field: 2
              wavelength: 0.0004861
              weight: 0.11
            - kind: spot_ee_radius
              field: 2
              wavelength: 0.0004861
              weight: 0.11
              fraction: 0.8
            - kind: spot_rms_worst
              field: 2
              wavelength: 0.0005876
              weight: 0.11
            - kind: spot_rms_weighted
              field: 2
              wavelength: 0.0005876
              weight: 0.11
            - kind: spot_ee_radius
              field: 2
              wavelength: 0.0005876
              weight: 0.11
              fraction: 0.8
            - kind: spot_rms_worst
              field: 2
              wavelength: 0.0006563
              weight: 0.11
            - kind: spot_rms_weighted
              field: 2
              wavelength: 0.0006563
              weight: 0.11
            - kind: spot_ee_radius
              field: 2
              wavelength: 0.0006563
              weight: 0.11
              fraction: 0.8
MERIT
    ;;
  doublegauss)
    cat >> "$OUTDIR/${PREFIX}refine-in.yaml" <<'MERIT'
      merit:
        type: weighted_sum
        terms:
            - kind: spot_rms
              field: 0
              wavelength: 0.0005876
              weight: 3.0
            - kind: spot_rms_worst
              field: 1
              wavelength: 0.0005876
              weight: 1.0
            - kind: spot_rms_worst
              field: 2
              wavelength: 0.0005876
              weight: 1.0
            - kind: spot_rms_worst
              field: 3
              wavelength: 0.0005876
              weight: 0.5
MERIT
    ;;
esac
awk '/^chief:/{p=1} p' "$OUTDIR/${PREFIX}best.yaml" >> "$OUTDIR/${PREFIX}refine-in.yaml"
$RAYWEAVE optimize < "$OUTDIR/${PREFIX}refine-in.yaml" > "$OUTDIR/${PREFIX}refined.yaml"
echo "Written: $OUTDIR/${PREFIX}refined.yaml"
echo

echo "--- PSF verification (all fields Strehl >= 0.5) ---"
$RAYWEAVE psf --polarization RCP+LCP --wavelengths 0.0005876 --best-focus \
  < "$OUTDIR/${PREFIX}refined.yaml" > "$OUTDIR/${PREFIX}psf.yaml"
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
  echo "  => Some fields are below Strehl 0.5 (the refinement merit needs retuning)."
fi
echo

echo "--- PNG diagrams ---"
echo "=== Initial diagram ==="
$RAYWEAVE chief --clear-aperture --ray-fan < "$YAML" | $RAYWEAVE trace \
  | $RAYWEAVE plot -o "$OUTDIR/${PREFIX}init.png" >/dev/null
echo "Written: $OUTDIR/${PREFIX}init.png"

echo "=== Best-solution diagram ==="
$RAYWEAVE chief --clear-aperture --ray-fan < "$OUTDIR/${PREFIX}refined.yaml" | $RAYWEAVE trace \
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
