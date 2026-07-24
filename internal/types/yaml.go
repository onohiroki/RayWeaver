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
	Coating      string         `yaml:"coating,omitempty"`
	Role         string         `yaml:"role,omitempty"`
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
	}
	if s.radiusUsed {
		raw.Radius = s.Radius()
	} else {
		raw.Curvature = s.Curvature
	}
	return raw, nil
}
