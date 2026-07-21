package coating

import (
	"math"
	"testing"

	"github.com/hiroki/rayweaver/internal/types"
)

func TestComputeTMMNormalIncidenceAR(t *testing.T) {
	n0 := 1.0
	ns := 1.62
	n1 := 1.38
	d1 := 99.6
	lambda := 0.00055
	result := ComputeTMM(n0, ns, []types.CoatingLayer{{Thickness: d1, N: n1}}, lambda, 0)
	Rtheory := ((n0 - ns) / (n0 + ns)) * ((n0 - ns) / (n0 + ns))
	if result.Rs >= Rtheory {
		t.Errorf("AR coating Rs = %v should be less than uncoated R = %v", result.Rs, Rtheory)
	}
}

func TestComputeTMMDielectricMirror(t *testing.T) {
	n0 := 1.0
	ns := 1.52
	layers := []types.CoatingLayer{}
	for i := 0; i < 9; i++ {
		if i%2 == 0 {
			layers = append(layers, types.CoatingLayer{Thickness: 94.2, N: 1.46})
		} else {
			layers = append(layers, types.CoatingLayer{Thickness: 58.5, N: 2.35})
		}
	}
	lambda := 0.00055
	result := ComputeTMM(n0, ns, layers, lambda, 0)
	if result.Rs < 0.8 {
		t.Errorf("9-layer mirror Rs = %v, expected > 0.8", result.Rs)
	}
	if result.Ts > 0.2 {
		t.Errorf("9-layer mirror Ts = %v, expected < 0.2", result.Ts)
	}
}

func TestComputeTMMObliqueIncidence(t *testing.T) {
	n0 := 1.0
	ns := 1.5
	lambda := 0.00055
	result := ComputeTMM(n0, ns, nil, lambda, 45.0*math.Pi/180.0)
	if math.Abs(result.Rs-result.Rp) < 1e-8 {
		t.Error("Expected Rs != Rp at oblique incidence for uncoated substrate")
	}
}

func TestComputeTMMUncoatedSubstrate(t *testing.T) {
	n0 := 1.0
	ns := 1.5
	lambda := 0.00055
	result := ComputeTMM(n0, ns, nil, lambda, 0)
	Rtheory := ((n0 - ns) / (n0 + ns)) * ((n0 - ns) / (n0 + ns))
	if math.Abs(result.Rs-Rtheory) > 1e-12 {
		t.Errorf("Uncoated Rs = %v, want %v", result.Rs, Rtheory)
	}
	if result.Rs != result.Rp {
		t.Error("Rs != Rp at normal incidence")
	}
}

func TestNewCatalog(t *testing.T) {
	c := NewCatalog()
	if c == nil {
		t.Fatal("NewCatalog returned nil")
	}
}

func TestCatalogAddAndLookup(t *testing.T) {
	c := NewCatalog()
	c.Add(types.CoatingEntry{Name: "AR", Layers: []types.CoatingLayer{{Material: "MgF2", Thickness: 99.6, N: 1.38}}})
	got, ok := c.Lookup("AR")
	if !ok {
		t.Fatal("Lookup failed")
	}
	if got.Name != "AR" {
		t.Errorf("Name = %v, want AR", got.Name)
	}
}
