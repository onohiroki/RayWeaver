package asphere

import (
	"testing"
	"github.com/hiroki/rayweaver/internal/types"
)

func TestCenterFieldBestZ(t *testing.T) {
	tests := []struct {
		name    string
		fields  []FieldFocusResult
		wantZ   float64
		wantOk  bool
	}{
		{
			name: "both fans present",
			fields: []FieldFocusResult{
				{
					Angle: 0,
					Tangential: FocusFanResult{BestZ: 10, Samples: make([]types.AsphereFocusSample, 1)},
					Sagittal:   FocusFanResult{BestZ: 20, Samples: make([]types.AsphereFocusSample, 1)},
				},
			},
			wantZ: 15,
			wantOk: true,
		},
		{
			name: "only tangential",
			fields: []FieldFocusResult{
				{
					Angle: 0,
					Tangential: FocusFanResult{BestZ: 10, Samples: make([]types.AsphereFocusSample, 1)},
					Sagittal:   FocusFanResult{BestZ: 20, Samples: nil},
				},
			},
			wantZ: 10,
			wantOk: true,
		},
		{
			name: "only sagittal",
			fields: []FieldFocusResult{
				{
					Angle: 0,
					Tangential: FocusFanResult{BestZ: 10, Samples: nil},
					Sagittal:   FocusFanResult{BestZ: 20, Samples: make([]types.AsphereFocusSample, 1)},
				},
			},
			wantZ: 20,
			wantOk: true,
		},
		{
			name: "no fans",
			fields: []FieldFocusResult{
				{
					Angle: 0,
					Tangential: FocusFanResult{BestZ: 10, Samples: nil},
					Sagittal:   FocusFanResult{BestZ: 20, Samples: nil},
				},
			},
			wantZ: 0,
			wantOk: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotZ, gotOk := centerFieldBestZ(tt.fields)
			if gotOk != tt.wantOk {
				t.Fatalf("centerFieldBestZ() ok = %v, want %v", gotOk, tt.wantOk)
			}
			if gotZ != tt.wantZ {
				t.Fatalf("centerFieldBestZ() z = %v, want %v", gotZ, tt.wantZ)
			}
		})
	}
}
