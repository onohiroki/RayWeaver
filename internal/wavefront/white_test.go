package wavefront

import (
	"math"
	"testing"
)

func TestWhiteSummaryWeights(t *testing.T) {
	results := []FieldResult{
		{FieldIndex: 0, FieldAngle: 0, Wavelength: 0.0005, Polarization: "RCP",
			Statistics: Statistics{RMS: 1, Strehl: 0.5}},
		{FieldIndex: 0, FieldAngle: 0, Wavelength: 0.0005, Polarization: "LCP",
			Statistics: Statistics{RMS: 1, Strehl: 0.5}},
		{FieldIndex: 0, FieldAngle: 0, Wavelength: 0.0006, Polarization: "RCP",
			Statistics: Statistics{RMS: 3, Strehl: 0.9}},
	}
	white := whiteSummary(results, []float64{0.0005, 0.0006}, []float64{0.5, 0.5})
	if white == nil || len(white.Fields) != 1 {
		t.Fatalf("white summary = %+v", white)
	}
	f := white.Fields[0]
	if math.Abs(f.RMS-math.Sqrt(5)) > 1e-12 {
		t.Errorf("white RMS = %v, want sqrt(5)", f.RMS)
	}
	if math.Abs(f.Strehl-0.7) > 1e-12 {
		t.Errorf("white Strehl = %v, want 0.7", f.Strehl)
	}
	if f.Samples != 2 {
		t.Errorf("samples = %d, want 2 (one per wavelength, pol deduplicated)", f.Samples)
	}
	if len(white.Wavelengths) != 2 || math.Abs(white.Wavelengths[0].Weight-0.5) > 1e-12 {
		t.Errorf("normalised wavelengths = %+v", white.Wavelengths)
	}
}
