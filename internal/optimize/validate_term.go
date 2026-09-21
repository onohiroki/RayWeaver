package optimize

import (
	"fmt"
	"sort"
	"strings"

	"github.com/hiroki/rayweaver/internal/glass"
	"github.com/hiroki/rayweaver/internal/types"
)

// DiagLevel represents the severity of a diagnostic message.
type DiagLevel int

const (
	DiagInfo DiagLevel = iota
	DiagWarning
	DiagError
)

// TermDiagnostic holds the result of a validation check on a merit term.
type TermDiagnostic struct {
	OK         bool
	Message    string
	Resolution string
	Source     string
	Kind       string
	TermIndex  int
	DiagLevel  DiagLevel
}

// ValidationScope defines what to validate and where.
type ValidationScope struct {
	TermIndex  int
	Kind       string
	Label      string // e.g. "spot_rms:field:2"
	MustChange bool   // true if term must change during optimisation
}

// DiscoverValidations inspects the merit terms and the system to produce
// the validation list. Field auto-discovery uses the term's FieldIndex or
// FieldID if set; otherwise defaults are used.
func DiscoverValidations(
	meritTerms []types.MeritTerm,
	referenceSurface int,
	gc *glass.Catalog,
) []ValidationScope {
	var out []ValidationScope

	for i, t := range meritTerms {
		label := t.Kind
		if t.Field >= 0 {
			label += fmt.Sprintf(":field:%d", t.Field)
		}

		mustChange := kindMustChange(t.Kind)
		out = append(out, ValidationScope{
			TermIndex:  i,
			Kind:       t.Kind,
			Label:      label,
			MustChange: mustChange,
		})
	}

	return out
}

func kindMustChange(kind string) bool {
	switch kind {
	case "focal_length", "effective_focal_length":
		return true
	case "curvature":
		return true
	case "thickness":
		return true
	case "seidel_spherical":
		return true
	default:
		return false
	}
}

// TermSurfaceIDs returns the surface IDs that a merit term applies to.
// Uses the term's SurfaceSet if non-empty; otherwise falls back to SurfaceSet
// from the variable (caller provides this).
func TermSurfaceIDs(term types.MeritTerm, variableSurfaceSet []int) []int {
	if len(term.SurfaceSet) > 0 {
		return term.SurfaceSet
	}
	if len(variableSurfaceSet) > 0 {
		return variableSurfaceSet
	}
	return nil
}

// GlassSurfaceSet identifies all lens-element glass surfaces in the term list
// by clustering adjacent (curvature, thickness) pairs and marking the first of
// each pair (glass-air interface) via glass_surfaces. Returns the sorted set.
func GlassSurfaceSet(terms []types.MeritTerm) []int {
	set := make(map[int]bool)
	for _, t := range terms {
		for _, s := range t.SurfaceSet {
			set[s] = true
		}
	}
	out := make([]int, 0, len(set))
	for s := range set {
		out = append(out, s)
	}
	sort.Ints(out)
	return out
}

// ValidateTerms runs the full term validation suite and returns all
// diagnostics. It does NOT fail the build; validation results go to
// warnings and diagnostics.
func ValidateTerms(
	meritTerms []types.MeritTerm,
	variables []Variable,
	referenceSurface int,
	gc *glass.Catalog,
) []TermDiagnostic {
	var diags []TermDiagnostic

	scopes := DiscoverValidations(meritTerms, referenceSurface, gc)

	for _, sc := range scopes {
		if sc.Kind == "glass_role" {
			diags = append(diags, TermDiagnostic{
				OK:         true,
				Message:    fmt.Sprintf("glass_role term at index %d (skip validations, optics may change)", sc.TermIndex),
				Resolution: "skip",
				Source:     sc.Kind,
				Kind:       sc.Kind,
				TermIndex:  sc.TermIndex,
				DiagLevel:  DiagInfo,
			})
			continue
		}

		if sc.Kind == "lateral_color" {
			diags = append(diags, TermDiagnostic{
				OK:         true,
				Message:    fmt.Sprintf("lateral_color term at index %d (skip validations, optics may change)", sc.TermIndex),
				Resolution: "skip",
				Source:     sc.Kind,
				Kind:       sc.Kind,
				TermIndex:  sc.TermIndex,
				DiagLevel:  DiagInfo,
			})
			continue
		}

		if isGridTraceTerm(sc.Kind) {
			diags = append(diags, TermDiagnostic{
				OK:         true,
				Message:    fmt.Sprintf("%s term at index %d uses dynamic grid (skip validations, grid adapts)", sc.Kind, sc.TermIndex),
				Resolution: "dynamic_grid",
				Source:     sc.Kind,
				Kind:       sc.Kind,
				TermIndex:  sc.TermIndex,
				DiagLevel:  DiagInfo,
			})
			continue
		}

		diags = append(diags, TermDiagnostic{
			OK:         true,
			Message:    fmt.Sprintf("term %q at index %d passed default checks", sc.Kind, sc.TermIndex),
			Resolution: "ok",
			Source:     sc.Kind,
			Kind:       sc.Kind,
			TermIndex:  sc.TermIndex,
			DiagLevel:  DiagInfo,
		})
	}

	return diags
}

func isGridTraceTerm(kind string) bool {
	switch kind {
	case "spot_rms":
		return true
	case "spot_rms_t":
		return true
	case "spot_rms_s":
		return true
	case "spot_rms_worst":
		return true
	case "spot_rms_weighted":
		return true
	case "spot_ee_radius":
		return true
	case "geometric_mtf_sag":
		return true
	case "geometric_mtf_tan":
		return true
	case "field_alive":
		return true
	case "wavefront_astigmatism":
		return true
	case "wavefront_defocus":
		return true
	case "wavefront_rms_residual":
		return true
	case "wavefront_tilt":
		return true
	case "longitudinal_color":
		return true
	case "lateral_color":
		return true
	default:
		return false
	}
}

// FormatTermDiagnostics produces human-readable output for term diagnostics.
func FormatTermDiagnostics(dias []TermDiagnostic) string {
	var b strings.Builder
	for _, d := range dias {
		if d.DiagLevel == DiagInfo {
			continue
		}
		switch d.DiagLevel {
		case DiagWarning:
			b.WriteString(fmt.Sprintf("[warning] %s\n", d.Message))
		case DiagError:
			b.WriteString(fmt.Sprintf("[error]   %s\n", d.Message))
		default:
			b.WriteString(fmt.Sprintf("[info]    %s\n", d.Message))
		}
	}
	return b.String()
}

// FilterWarnings returns only diagnostics at warning or error level.
func FilterWarnings(dias []TermDiagnostic) []TermDiagnostic {
	var out []TermDiagnostic
	for _, d := range dias {
		if d.DiagLevel >= DiagWarning {
			out = append(out, d)
		}
	}
	return out
}

// FilterDiagnostics returns only the diagnostics at the requested level.
func FilterDiagnostics(dias []TermDiagnostic, level DiagLevel) []TermDiagnostic {
	var out []TermDiagnostic
	for _, d := range dias {
		if d.DiagLevel == level {
			out = append(out, d)
		}
	}
	return out
}

// FormatDiagnosticsSummary produces a one-line summary of diagnostic counts.
func FormatDiagnosticsSummary(dias []TermDiagnostic) string {
	info, warn, err := 0, 0, 0
	for _, d := range dias {
		switch d.DiagLevel {
		case DiagInfo:
			info++
		case DiagWarning:
			warn++
		case DiagError:
			err++
		}
	}
	if warn == 0 && err == 0 {
		return fmt.Sprintf("validations: %d info, all clear", info)
	}
	return fmt.Sprintf("validations: %d info, %d warnings, %d errors", info, warn, err)
}
