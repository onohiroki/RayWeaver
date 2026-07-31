package types

import (
	"errors"
	"fmt"

	"gopkg.in/yaml.v3"
)

func (v *Vec3) UnmarshalYAML(node *yaml.Node) error {
	if node.Kind != yaml.SequenceNode {
		return errors.New("Vec3 must be a YAML sequence")
	}
	if len(node.Content) != 3 {
		return errors.New("Vec3 requires exactly 3 elements")
	}
	if err := node.Content[0].Decode(&v.X); err != nil {
		return fmt.Errorf("Vec3.X: %w", err)
	}
	if err := node.Content[1].Decode(&v.Y); err != nil {
		return fmt.Errorf("Vec3.Y: %w", err)
	}
	if err := node.Content[2].Decode(&v.Z); err != nil {
		return fmt.Errorf("Vec3.Z: %w", err)
	}
	return nil
}

func (v Vec3) MarshalYAML() (interface{}, error) {
	return []float64{v.X, v.Y, v.Z}, nil
}

func (j *JonesVector) UnmarshalYAML(node *yaml.Node) error {
	if node.Kind != yaml.SequenceNode {
		return errors.New("JonesVector must be a YAML sequence of 4 floats: [Re(Ex), Im(Ex), Re(Ey), Im(Ey)]")
	}
	if len(node.Content) != 4 {
		return errors.New("JonesVector requires exactly 4 elements")
	}
	var reEx, imEx, reEy, imEy float64
	if err := node.Content[0].Decode(&reEx); err != nil {
		return fmt.Errorf("JonesVector Re(Ex): %w", err)
	}
	if err := node.Content[1].Decode(&imEx); err != nil {
		return fmt.Errorf("JonesVector Im(Ex): %w", err)
	}
	if err := node.Content[2].Decode(&reEy); err != nil {
		return fmt.Errorf("JonesVector Re(Ey): %w", err)
	}
	if err := node.Content[3].Decode(&imEy); err != nil {
		return fmt.Errorf("JonesVector Im(Ey): %w", err)
	}
	j.Ex = complex(reEx, imEx)
	j.Ey = complex(reEy, imEy)
	return nil
}

func (j JonesVector) MarshalYAML() (interface{}, error) {
	return []float64{real(j.Ex), imag(j.Ex), real(j.Ey), imag(j.Ey)}, nil
}

type surfaceYAML struct {
	ID           int             `yaml:"id"`
	Type         SurfaceType    `yaml:"type"`
	Radius       float64        `yaml:"radius,omitempty"`
	Curvature    float64        `yaml:"curvature,omitempty"`
	Conic        float64        `yaml:"conic"`
	Thickness    float64        `yaml:"thickness"`
	Material     string         `yaml:"material"`
	Diameter     float64        `yaml:"diameter,omitempty"`
	Coefficients []float64      `yaml:"coefficients,omitempty"`
	NormRadius   float64        `yaml:"norm_radius,omitempty"`
	Decenter     []DecenterStep `yaml:"decenter,omitempty"`
	Coating        string         `yaml:"coating,omitempty"`
	Role           string         `yaml:"role,omitempty"`
	AutoAperture   bool           `yaml:"auto_aperture,omitempty"`
	MinGlassPath   float64        `yaml:"min_glass_path,omitempty"`
	MaxGlassPath   float64        `yaml:"max_glass_path,omitempty"`
}

func (s *Surface) UnmarshalYAML(node *yaml.Node) error {
	var raw surfaceYAML
	if err := node.Decode(&raw); err != nil {
		return err
	}
	s.ID = raw.ID
	s.Type = raw.Type
	s.Conic = raw.Conic
	s.Thickness = raw.Thickness
	s.Material = raw.Material
	s.Diameter = raw.Diameter
	s.Coefficients = raw.Coefficients
	s.NormRadius = raw.NormRadius
	s.Decenter = raw.Decenter
	s.Coating = raw.Coating
	s.Role = raw.Role
	s.AutoAperture = raw.AutoAperture
	s.MinGlassPath = raw.MinGlassPath
	s.MaxGlassPath = raw.MaxGlassPath

	if raw.Curvature != 0 {
		s.Curvature = raw.Curvature
		s.radiusUsed = false
	} else {
		s.SetRadius(raw.Radius)
		s.radiusUsed = true
	}
	return nil
}

func (s Surface) MarshalYAML() (interface{}, error) {
	raw := surfaceYAML{
		ID:           s.ID,
		Type:         s.Type,
		Conic:        s.Conic,
		Thickness:    s.Thickness,
		Material:     s.Material,
		Diameter:     s.Diameter,
		Coefficients: s.Coefficients,
		NormRadius:   s.NormRadius,
		Decenter:     s.Decenter,
		Coating:      s.Coating,
		Role:         s.Role,
		AutoAperture: s.AutoAperture,
		MinGlassPath: s.MinGlassPath,
		MaxGlassPath: s.MaxGlassPath,
	}
	if s.radiusUsed {
		raw.Radius = s.Radius()
	} else {
		raw.Curvature = s.Curvature
	}
	return raw, nil
}

type glassYAML struct {
	Type              GlassType              `yaml:"type,omitempty"`
	Key               string                 `yaml:"key,omitempty"`
	Name              string                 `yaml:"name,omitempty"`
	Label             string                 `yaml:"label,omitempty"`
	Manufacturer      string                 `yaml:"manufacturer,omitempty"`
	DispersionFormula DispersionFormula      `yaml:"dispersion_formula,omitempty"`
	ND                float64                `yaml:"nd,omitempty"`
	VD                float64                `yaml:"vd,omitempty"`
	Coefficients      []float64              `yaml:"coefficients,omitempty"`
	WavelengthMin     float64                `yaml:"wavelength_range_min,omitempty"`
	WavelengthMax     float64                `yaml:"wavelength_range_max,omitempty"`
	Aliases           []string               `yaml:"aliases,omitempty"`
	RefractiveIndices RefractiveIndexTable `yaml:"refractive_indices,omitempty"`
}

func ResolveGlassKey(g Glass) string {
	switch g.Type {
	case GlassTypeCatalog:
		return g.Name
	case GlassTypeTabulated:
		return g.Label
	case GlassTypeModel:
		if g.Label != "" {
			return g.Label
		}
		return fmt.Sprintf("%.5f:%.2f", g.ND, g.VD)
	default:
		return g.Name
	}
}

func (g *Glass) UnmarshalYAML(node *yaml.Node) error {
	var raw glassYAML
	if err := node.Decode(&raw); err != nil {
		return err
	}

	g.Type = raw.Type
	g.Key = raw.Key
	g.Name = raw.Name
	g.Label = raw.Label
	g.Manufacturer = raw.Manufacturer
	g.DispersionFormula = raw.DispersionFormula
	g.ND = raw.ND
	g.VD = raw.VD
	g.Coefficients = raw.Coefficients
	g.WavelengthMin = raw.WavelengthMin
	g.WavelengthMax = raw.WavelengthMax
	g.Aliases = raw.Aliases
	g.RefractiveIndices = raw.RefractiveIndices

	if g.Type == "" {
		switch {
		case g.DispersionFormula != "":
			g.Type = GlassTypeCatalog
		case len(g.RefractiveIndices) > 0:
			g.Type = GlassTypeTabulated
		default:
			g.Type = GlassTypeModel
		}
	}

	if g.Type == GlassTypeTabulated && g.Label == "" {
		return fmt.Errorf("tabulated glass requires a label")
	}

	return nil
}

func (g Glass) MarshalYAML() (interface{}, error) {
	key := g.Key
	if key == "" {
		key = ResolveGlassKey(g)
	}
	raw := glassYAML{
		Type:              g.Type,
		Key:               key,
		Name:              g.Name,
		Label:             g.Label,
		Manufacturer:      g.Manufacturer,
		DispersionFormula: g.DispersionFormula,
		ND:                g.ND,
		VD:                g.VD,
		Coefficients:      g.Coefficients,
		WavelengthMin:     g.WavelengthMin,
		WavelengthMax:     g.WavelengthMax,
		Aliases:           g.Aliases,
		RefractiveIndices: g.RefractiveIndices,
	}
	return raw, nil
}
