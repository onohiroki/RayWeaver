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
