package spectral

import (
	"fmt"
	"math"
	"strings"

	"github.com/hiroki/rayweaver/internal/types"
)

// Wavelength-set modes understood by a Resolver.
const (
	ModeDesign   = "design"
	ModeSpectral = "spectral"
	ModeCustom   = "custom"
)

// Resolver holds the two document-level wavelength sets and the reference
// wavelength. Evaluation commands use Spectral (the 5 nm grid); the optimiser
// uses SpectralOptimize (the 10 nm grid). Both are normalised to Σw = 1.
type Resolver struct {
	Design           []types.WavelengthSample
	Spectral         []types.WavelengthSample
	SpectralOptimize []types.WavelengthSample
	Reference        float64
}

// NewResolver resolves the design set from a config's wavelength table and the
// continuous spectrum from the document. A missing/invalid spectrum leaves the
// Spectral sets empty; requesting them then reports an error instead of
// silently falling back to the design lines.
func NewResolver(wavelengths []types.WavelengthItem, spectrum *types.SpectralConfig, reference float64) Resolver {
	if reference <= 0 {
		reference = types.DefaultWavelength
	}
	r := Resolver{
		Design:    DesignSamples(wavelengths),
		Reference: reference,
	}
	if spectrum != nil {
		if s, err := SpectralSamples(spectrum, spectrumStep(spectrum, false)); err == nil {
			r.Spectral = s
		}
		if s, err := SpectralSamples(spectrum, spectrumStep(spectrum, true)); err == nil {
			r.SpectralOptimize = s
		}
	}
	return r
}

func spectrumStep(cfg *types.SpectralConfig, optimize bool) float64 {
	if optimize {
		if cfg.OptimizeStep > 0 {
			return cfg.OptimizeStep
		}
		return 10
	}
	if cfg.Step > 0 {
		return cfg.Step
	}
	return 5
}

// DesignSamples converts a config wavelength table into a normalised set. The
// declaration order is preserved, duplicates within 1e-12 mm keep the first
// entry, and a non-positive weight excludes an entry. When every weight is
// non-positive the legacy convention applies: all entries weigh 1.
func DesignSamples(items []types.WavelengthItem) []types.WavelengthSample {
	out := make([]types.WavelengthSample, 0, len(items))
	positive := false
	for _, w := range items {
		if w.Weight > 0 {
			positive = true
			break
		}
	}
	for _, w := range items {
		if w.Value <= 0 {
			continue
		}
		raw := w.Weight
		if !positive {
			raw = 1
		}
		if raw <= 0 {
			continue
		}
		if hasValue(out, w.Value) {
			continue
		}
		out = append(out, types.WavelengthSample{
			Value: w.Value, Label: w.Label, RawWeight: raw,
		})
	}
	return normalize(out)
}

// CustomSamples converts an explicit term-level list into a normalised set.
func CustomSamples(items []types.WavelengthSample) ([]types.WavelengthSample, error) {
	out := make([]types.WavelengthSample, 0, len(items))
	for _, w := range items {
		if w.Value <= 0 {
			continue
		}
		raw := w.Weight
		if raw <= 0 {
			raw = 1
		}
		if hasValue(out, w.Value) {
			continue
		}
		out = append(out, types.WavelengthSample{
			Value: w.Value, Label: w.Label, RawWeight: raw,
		})
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("wavelength set %q is empty", ModeCustom)
	}
	return normalize(out), nil
}

// SpectralSamples samples a continuous spectrum on a grid (or on the explicit
// entries) and normalises the weights. Entries are explicit samples whose
// relative values are the raw weights; otherwise the curve is evaluated at
// every grid point and weighted by the trapezoidal Δλ.
func SpectralSamples(cfg *types.SpectralConfig, step float64) ([]types.WavelengthSample, error) {
	if cfg == nil {
		return nil, fmt.Errorf("wavelength set %q needs a document spectrum", ModeSpectral)
	}
	if strings.EqualFold(strings.TrimSpace(cfg.Curve), "custom") && len(cfg.Entries) == 0 {
		return nil, fmt.Errorf("spectrum curve %q needs entries", cfg.Curve)
	}
	if len(cfg.Entries) > 0 {
		out := make([]types.WavelengthSample, 0, len(cfg.Entries))
		for _, e := range cfg.Entries {
			if e.Wavelength <= 0 || e.Relative < 0 {
				continue
			}
			out = append(out, types.WavelengthSample{
				Value:     e.Wavelength * 1e-6,
				RawWeight: e.Relative,
			})
		}
		if len(out) == 0 {
			return nil, fmt.Errorf("spectrum entries are empty or invalid")
		}
		return normalize(out), nil
	}
	lo, hi := 400.0, 700.0
	if len(cfg.Range) >= 2 && cfg.Range[0] > 0 && cfg.Range[1] > cfg.Range[0] {
		lo, hi = cfg.Range[0], cfg.Range[1]
	}
	if step <= 0 {
		step = cfg.Step
	}
	if step <= 0 {
		step = 5
	}
	n := int(math.Floor((hi-lo)/step+1e-9)) + 1
	if n < 2 {
		n = 2
	}
	nm := make([]float64, n)
	for i := range nm {
		nm[i] = lo + float64(i)*step
		if nm[i] > hi {
			nm[i] = hi
		}
	}
	curve, err := curveFor(cfg.Curve, lo, hi)
	if err != nil {
		return nil, err
	}
	out := make([]types.WavelengthSample, 0, n)
	for i, l := range nm {
		d := 0.0
		switch {
		case n == 1:
			d = 1
		case i == 0:
			d = (nm[1] - nm[0]) / 2
		case i == n-1:
			d = (nm[n-1] - nm[n-2]) / 2
		default:
			d = (nm[i+1] - nm[i-1]) / 2
		}
		rel := 1.0
		if curve != nil {
			rel = curve.Weight(l * 1e-6)
		}
		out = append(out, types.WavelengthSample{
			Value:     l * 1e-6,
			RawWeight: rel * d,
		})
	}
	return normalize(out), nil
}

func curveFor(name string, lo, hi float64) (*Curve, error) {
	switch strings.ToUpper(strings.TrimSpace(name)) {
	case "", "D65":
		return D65(), nil
	case "FLAT":
		return Flat(lo, hi), nil
	case "CUSTOM":
		return nil, nil
	default:
		return nil, fmt.Errorf("unknown spectrum curve %q (D65 | FLAT | custom)", name)
	}
}

// NormalizeSet returns a copy normalised to Σw = 1 (a no-op for an empty set).
func NormalizeSet(samples []types.WavelengthSample) []types.WavelengthSample {
	return normalize(append([]types.WavelengthSample(nil), samples...))
}

func normalize(samples []types.WavelengthSample) []types.WavelengthSample {
	var sum float64
	for _, s := range samples {
		if s.RawWeight > 0 {
			sum += s.RawWeight
		}
	}
	if sum <= 0 {
		for i := range samples {
			samples[i].Weight = 0
		}
		return samples
	}
	for i := range samples {
		if samples[i].RawWeight > 0 {
			samples[i].Weight = samples[i].RawWeight / sum
		}
	}
	return samples
}

func hasValue(samples []types.WavelengthSample, value float64) bool {
	for _, s := range samples {
		if math.Abs(s.Value-value) <= 1e-12 {
			return true
		}
	}
	return false
}

// For returns the resolved set a term's wavelength_set selects.
func (r Resolver) For(mode string, term types.MeritTerm, optimize bool) ([]types.WavelengthSample, error) {
	switch strings.ToLower(strings.TrimSpace(mode)) {
	case ModeDesign:
		if len(r.Design) == 0 {
			return nil, fmt.Errorf("wavelength set %q has no config wavelengths", ModeDesign)
		}
		return r.Design, nil
	case ModeSpectral:
		set := r.Spectral
		if optimize {
			set = r.SpectralOptimize
		}
		if len(set) == 0 {
			return nil, fmt.Errorf("wavelength set %q has no usable document spectrum", ModeSpectral)
		}
		return set, nil
	case ModeCustom:
		return CustomSamples(term.Wavelengths)
	default:
		return nil, fmt.Errorf("unknown wavelength_set %q (design | spectral | custom)", mode)
	}
}

// ExpandTerms resolves every term's wavelength_set. Ordinary kinds expand into
// one term per wavelength (the aggregate polychromatic kinds and
// secondary_spectrum instead carry the resolved set). normalize is "sum"
// (default) or "none". Non-expandable kinds report an error.
func ExpandTerms(terms []types.MeritTerm, r Resolver, normalization string) ([]types.MeritTerm, error) {
	norm := strings.ToLower(strings.TrimSpace(normalization))
	if norm == "" {
		norm = "sum"
	}
	if norm != "sum" && norm != "none" {
		return nil, fmt.Errorf("unknown wavelength_normalization %q (sum | none)", normalization)
	}
	out := make([]types.MeritTerm, 0, len(terms))
	for _, t := range terms {
		mode := strings.TrimSpace(t.WavelengthSet)
		if mode == "" {
			if t.Wavelength <= 0 {
				t.Wavelength = r.Reference
			}
			out = append(out, t)
			continue
		}
		if t.Wavelength > 0 && !isAggregateKind(t.Kind) {
			return nil, fmt.Errorf("merit term %q sets both wavelength and wavelength_set", t.Kind)
		}
		if isNonExpandable(t.Kind) {
			return nil, fmt.Errorf("merit kind %q does not support wavelength_set", t.Kind)
		}
		set, err := r.For(mode, t, true)
		if err != nil {
			return nil, err
		}
		if isAggregateKind(t.Kind) {
			// secondary_spectrum keeps its F/C correction pair in
			// wavelength/comparison_wavelength; the polychromatic kinds use
			// the reference only for normalisation.
			if t.Kind != "secondary_spectrum" || t.Wavelength <= 0 {
				t.Wavelength = r.Reference
			}
			// The aggregate computes its own normalised combination; the
			// outer per-wavelength weight must stay 1 or the reference line's
			// table weight would scale the whole metric.
			t.WavelengthWeight = 1
			t.ResolvedWavelengths = set
			t.WavelengthSet = strings.ToLower(mode)
			out = append(out, t)
			continue
		}
		for _, s := range set {
			c := t
			c.WavelengthSet = ""
			c.Wavelengths = nil
			c.ResolvedWavelengths = nil
			c.Wavelength = s.Value
			if norm == "none" {
				c.WavelengthWeight = s.RawWeight
			} else {
				c.WavelengthWeight = s.Weight
			}
			out = append(out, c)
		}
	}
	return out, nil
}

// HasSpectralSet reports whether any term asks for the continuous spectrum.
// Callers use it to warn when a spectral set expands into a large term list.
func HasSpectralSet(terms []types.MeritTerm) bool {
	for _, t := range terms {
		if strings.EqualFold(strings.TrimSpace(t.WavelengthSet), ModeSpectral) {
			return true
		}
	}
	return false
}

// HasDesignSet reports whether any term asks for the config wavelength table.
func HasDesignSet(terms []types.MeritTerm) bool {
	for _, t := range terms {
		if strings.EqualFold(strings.TrimSpace(t.WavelengthSet), ModeDesign) {
			return true
		}
	}
	return false
}

// SetContains reports whether a resolved set contains value (1e-12 mm).
func SetContains(samples []types.WavelengthSample, value float64) bool {
	for _, s := range samples {
		if math.Abs(s.Value-value) <= 1e-12 {
			return true
		}
	}
	return false
}

// ExcludedDesignValues lists table values excluded by a non-positive weight.
// It returns nil when every weight is non-positive (the legacy all-equal case).
func ExcludedDesignValues(items []types.WavelengthItem) []float64 {
	positive := false
	for _, w := range items {
		if w.Weight > 0 {
			positive = true
			break
		}
	}
	if !positive {
		return nil
	}
	var out []float64
	for _, w := range items {
		if w.Value > 0 && w.Weight <= 0 {
			out = append(out, w.Value)
		}
	}
	return out
}

func isAggregateKind(kind string) bool {
	switch kind {
	case "polychromatic_spot_rms", "polychromatic_ee_radius",
		"polychromatic_strehl", "polychromatic_mtf_sag", "polychromatic_mtf_tan",
		"secondary_spectrum":
		return true
	}
	return false
}

func isNonExpandable(kind string) bool {
	switch kind {
	case "lateral_color", "longitudinal_color", "axial_color", "focal_length", "glass_role":
		return true
	}
	return false
}
