#!/bin/bash
set -uo pipefail

# =============================================================================
# glass-attraction-demo.bash — base vs glass-attraction comparison
#
# Purpose: quantify how glass_attraction affects the optimised glass nd/vd
# values on a deliberately swapped flint/crown Cooke triplet.
#
# The base run keeps `glass_attraction` configured but at zero weight, so it
# is numerically identical to no attraction yet still reports the nearest
# real-glass distances; the attraction run ramps the weight up.
#
# Steps
#   1. optimize (base)          : attraction configured, weight 0
#   2. optimize (attraction)    : attraction weight ramped to 15
#   3. distance table           : nearest real glass per surface, base vs attr
#   4. chief                    : spot RMS per field, base vs attraction
#   5. gates                    : roles preserved + glasses near real catalog
#
# Dependencies: rayweave + POSIX shell utilities (sed, grep, tee) only.
# =============================================================================

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

CLEAN=false
while [[ $# -gt 0 ]]; do
  case "$1" in
    --clean) CLEAN=true; shift ;;
    *) echo "Unknown option: $1"; exit 1 ;;
  esac
done

YAML="$SCRIPT_DIR/glass-attraction-demo.yaml"
OUTDIR="$SCRIPT_DIR"
BASE_RESULT="$OUTDIR/glass-attraction-base-result.yaml"
ATTR_RESULT="$OUTDIR/glass-attraction-attr-result.yaml"
BASE_LOG="$OUTDIR/glass-attraction-base-log.jsonl"
ATTR_LOG="$OUTDIR/glass-attraction-attr-log.jsonl"
RESULT_FILE="$OUTDIR/glass-attraction-demo-result.txt"
BASE_YAML="$OUTDIR/.glass-attraction-base.yaml"

# Clean-only mode
if [ "$CLEAN" = true ]; then
  echo "=== Cleaning up generated files ==="
  rm -f "$BASE_RESULT" "$ATTR_RESULT" "$BASE_LOG" "$ATTR_LOG" "$BASE_YAML"
  rm -f "$OUTDIR"/glass-attraction-base-stderr.txt "$OUTDIR"/glass-attraction-attr-stderr.txt
  rm -f "$OUTDIR"/glass-attraction-init.png "$OUTDIR"/glass-attraction-base.png "$OUTDIR"/glass-attraction-attr.png
  rm -f "$RESULT_FILE"
  echo "  Removed generated files"
  exit 0
fi

# Locate the rayweave binary
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

# Base input: keep glass_attraction enabled (so diagnostics are reported) but
# zero its weight. The `weight_from`/`weight_to` keys are unique to the
# glass_attraction block in this input.
make_base_yaml() {
  sed -e 's/^    weight_from:.*/    weight_from: 0.0/' \
      -e 's/^    weight_to:.*/    weight_to: 0.0/' "$YAML"
}

echo "=== Glass attraction demo: base vs attraction ==="
echo
echo "Optical system: the US2645157 Cooke triplet, glasses SWAPPED:"
echo "  Surface 3-4: negative-power element, crown (nd=1.63854, vd=55.42)"
echo "  Surface 6-7: positive-power element, flint (nd=1.64831, vd=33.84)"
echo "  Inline real-glass catalog: 12 glasses (N-BK7, N-SK16, SK18, ...)"
echo
echo "  Base:       glass_attraction configured, weight 0 (no bias)"
echo "  Attraction: glass_attraction weight ramped 1 -> 15 (distance kernel,"
echo "              per-glass vd sensitivity weighting)"
echo

# ── Base run (zero-weight attraction) ──
echo "=== Step 1: DLS optimization (base, zero-weight attraction) ==="
make_base_yaml > "$BASE_YAML"
BASE_STDERR="$OUTDIR/glass-attraction-base-stderr.txt"
$RAYWEAVE optimize --verbose --log "$BASE_LOG" < "$BASE_YAML" > "$BASE_RESULT" 2> "$BASE_STDERR"
rm -f "$BASE_YAML"
echo "  Done. Log: $BASE_LOG"

# ── Attraction run ──
echo
echo "=== Step 2: DLS optimization (with glass attraction) ==="
ATTR_STDERR="$OUTDIR/glass-attraction-attr-stderr.txt"
$RAYWEAVE optimize --verbose --log "$ATTR_LOG" < "$YAML" > "$ATTR_RESULT" 2> "$ATTR_STDERR"
echo "  Done. Log: $ATTR_LOG"

# ── Nearest-real-glass distance table (from the opt_results diagnostics) ──
# Pair order is deterministic (sorted by surface): pairs[0] = S3, pairs[1] = S6.
echo
echo "--- Distance to nearest real catalog glass (normalised nd/vd) ---"
surface_glass() { # <result-yaml> <surface-id> <nd|vd>
  $RAYWEAVE query -r "configs[0].surfaces[id=$2].material.$3" < "$1"
}
pair_field() { # <result-yaml> <pair-index> <field>
  $RAYWEAVE query -r "opt_results.glass_attraction.pairs[$2].$3" < "$1"
}
{
  printf "  %-4s %-11s %9s %8s   %-10s %7s\n" "Surf" "phase" "nd" "vd" "nearest" "dist"
  i=0
  for sid in 3 6; do
    for spec in "base:$BASE_RESULT" "attraction:$ATTR_RESULT"; do
      label="${spec%%:*}"; file="${spec#*:}"
      nd=$(surface_glass "$file" "$sid" nd)
      vd=$(surface_glass "$file" "$sid" vd)
      near=$(pair_field "$file" "$i" nearest_key)
      dist=$(pair_field "$file" "$i" distance)
      printf "  S%-3s %-11s %9.5f %8.3f   %-10s %7.4f\n" "$sid" "$label" "$nd" "$vd" "$near" "$dist"
    done
    i=$((i + 1))
  done
} | tee "$RESULT_FILE"
echo

# ── Spot RMS comparison ──
echo "--- Spot RMS comparison (primary λ=587.6nm) ---"
BASE_CHIEF=$($RAYWEAVE chief < "$BASE_RESULT" 2>/dev/null)
ATTR_CHIEF=$($RAYWEAVE chief < "$ATTR_RESULT" 2>/dev/null)
INIT_CHIEF=$($RAYWEAVE chief < "$YAML" 2>/dev/null)
rms_field() {
  local chief="$1" fi="$2"
  echo "$chief" | $RAYWEAVE query -r "chief_rays[$fi].spot_stats.rms_r"
}
RMS_ONAXIS_BASE=$(rms_field "$BASE_CHIEF" 0)
RMS_ONAXIS_ATTR=$(rms_field "$ATTR_CHIEF" 0)
{
  printf "  %-6s %10s  %10s  %10s\n" "Field" "Initial" "Base" "Attraction"
  printf "  %-6s %10s  %10s  %10s\n" "-----" "-------" "----" "----------"
  for fi in 0 1 2; do
    rms_i=$(rms_field "$INIT_CHIEF" "$fi")
    rms_b=$(rms_field "$BASE_CHIEF" "$fi")
    rms_a=$(rms_field "$ATTR_CHIEF" "$fi")
    printf "  %-6s %10.4f  %10.4f  %10.4f\n" "f$fi" "$rms_i" "$rms_b" "$rms_a"
  done
  echo
} | tee -a "$RESULT_FILE"

# ── Glass attraction diagnostics ──
echo "--- Glass attraction diagnostics (attraction run) ---"
echo "  Final weight: $($RAYWEAVE query -r 'opt_results.glass_attraction.weight' < "$ATTR_RESULT")"
$RAYWEAVE list optimization --format table < "$ATTR_RESULT" 2>/dev/null | grep -A 20 "Glass Attraction" || true
echo

# ── Pass gates ──
echo "=== Pass gates ==="
FAIL=0
check_gate() {
  local desc="$1"; shift
  if "$@"; then echo "  PASS  $desc"; else echo "  FAIL  $desc"; FAIL=1; fi
}

VD3_ATTR=$(surface_glass "$ATTR_RESULT" 3 vd)
VD6_ATTR=$(surface_glass "$ATTR_RESULT" 6 vd)
DIST_BASE_S6=$(pair_field "$BASE_RESULT" 1 distance)
DIST_S3=$(pair_field "$ATTR_RESULT" 0 distance)
DIST_S6=$(pair_field "$ATTR_RESULT" 1 distance)

gate_base_far() { $RAYWEAVE query --gate "d > 0.1" --set d="$DIST_BASE_S6" < /dev/null > /dev/null 2>&1; }
gate_attr_near() { $RAYWEAVE query --gate "d < 0.05" --set d="$DIST_S6" < /dev/null > /dev/null 2>&1; }
gate_s3_flint() { $RAYWEAVE query --gate "vd < 45" --set vd="$VD3_ATTR" < /dev/null > /dev/null 2>&1; }
gate_s6_crown() { $RAYWEAVE query --gate "vd > 45" --set vd="$VD6_ATTR" < /dev/null > /dev/null 2>&1; }
gate_all_near() { $RAYWEAVE query --gate "d < 0.1" --set d="$DIST_S3" < /dev/null > /dev/null 2>&1 && \
                  $RAYWEAVE query --gate "d < 0.1" --set d="$DIST_S6" < /dev/null > /dev/null 2>&1; }
gate_rms() { $RAYWEAVE query --gate "a < b" --set a="$RMS_ONAXIS_ATTR" --set b=0.05 < /dev/null > /dev/null 2>&1; }

check_gate "Base S6 is a floating (non-physical) glass (dist=$DIST_BASE_S6 > 0.1)" gate_base_far
check_gate "Attraction S6 snapped onto a real glass (dist=$DIST_S6 < 0.05)" gate_attr_near
check_gate "Attraction S3 is a flint (vd=$VD3_ATTR < 45)" gate_s3_flint
check_gate "Attraction S6 is a crown (vd=$VD6_ATTR > 45)" gate_s6_crown
check_gate "Both attracted glasses near real catalog (d3=$DIST_S3, d6=$DIST_S6 < 0.1)" gate_all_near
check_gate "Attraction still well-corrected on axis (RMS=$RMS_ONAXIS_ATTR < 0.05 mm)" gate_rms

if [ "$FAIL" = 1 ]; then
  echo "  >>> Demo failed one or more gates" | tee -a "$RESULT_FILE"
  exit 1
fi
echo "  >>> All gates passed" | tee -a "$RESULT_FILE"
echo

echo "=== PNG diagrams ==="
$RAYWEAVE chief --clear-aperture --ray-fan < "$YAML" 2>/dev/null \
  | $RAYWEAVE trace 2>/dev/null \
  | $RAYWEAVE plot -o "$OUTDIR/glass-attraction-init.png" > /dev/null 2>/dev/null || true
echo "Written: $OUTDIR/glass-attraction-init.png"

$RAYWEAVE chief --clear-aperture --ray-fan < "$BASE_RESULT" 2>/dev/null \
  | $RAYWEAVE trace 2>/dev/null \
  | $RAYWEAVE plot -o "$OUTDIR/glass-attraction-base.png" > /dev/null 2>/dev/null || true
echo "Written: $OUTDIR/glass-attraction-base.png"

$RAYWEAVE chief --clear-aperture --ray-fan < "$ATTR_RESULT" 2>/dev/null \
  | $RAYWEAVE trace 2>/dev/null \
  | $RAYWEAVE plot -o "$OUTDIR/glass-attraction-attr.png" > /dev/null 2>/dev/null || true
echo "Written: $OUTDIR/glass-attraction-attr.png"
echo

echo "=== Logs saved: $BASE_LOG, $ATTR_LOG ==="
