package main

import (
	"testing"

	"github.com/hiroki/rayweaver/internal/pso"
	"github.com/hiroki/rayweaver/internal/types"
)

// TestBuildPSOConfigDefaults verifies that an empty PSOConfig keeps the
// pso.Config defaults (including the inertia sentinel 0, which the explorer
// turns into the built-in 0.9 -> 0.4 decay).
func TestBuildPSOConfigDefaults(t *testing.T) {
	c := buildPSOConfig(&types.PSOConfig{})
	d := pso.DefaultConfig()
	if c.SwarmSize != d.SwarmSize {
		t.Errorf("SwarmSize = %d, want %d", c.SwarmSize, d.SwarmSize)
	}
	if c.PsoIterations != d.PsoIterations {
		t.Errorf("PsoIterations = %d, want %d", c.PsoIterations, d.PsoIterations)
	}
	if c.Inertia != 0 {
		t.Errorf("Inertia = %v, want 0 (built-in decay sentinel)", c.Inertia)
	}
	if c.StallWindowFrac != d.StallWindowFrac {
		t.Errorf("StallWindowFrac = %v, want %v", c.StallWindowFrac, d.StallWindowFrac)
	}
	if c.StallRelTol != d.StallRelTol {
		t.Errorf("StallRelTol = %v, want %v", c.StallRelTol, d.StallRelTol)
	}
}

// TestBuildPSOConfigFromYAML verifies that PSO-specific and embedded escape
// fields (swarm, iterations, inertia, stall) are taken from the YAML config.
func TestBuildPSOConfigFromYAML(t *testing.T) {
	cfg := &types.PSOConfig{
		SwarmSize:         40,
		PsoIterations:     60,
		Inertia:           0.9,
		Cognitive:         1.6,
		Social:            1.3,
		VelocityClamp:     0.25,
		InitSpread:        0.15,
		ConstraintPenalty: 500,
	}
	cfg.StallWindowFrac = 0.35
	cfg.StallRelTol = 1e-3

	c := buildPSOConfig(cfg)
	if c.SwarmSize != 40 {
		t.Errorf("SwarmSize = %d, want 40", c.SwarmSize)
	}
	if c.PsoIterations != 60 {
		t.Errorf("PsoIterations = %d, want 60", c.PsoIterations)
	}
	if c.Inertia != 0.9 {
		t.Errorf("Inertia = %v, want 0.9", c.Inertia)
	}
	if c.Cognitive != 1.6 {
		t.Errorf("Cognitive = %v, want 1.6", c.Cognitive)
	}
	if c.Social != 1.3 {
		t.Errorf("Social = %v, want 1.3", c.Social)
	}
	if c.VelocityClamp != 0.25 {
		t.Errorf("VelocityClamp = %v, want 0.25", c.VelocityClamp)
	}
	if c.InitSpread != 0.15 {
		t.Errorf("InitSpread = %v, want 0.15", c.InitSpread)
	}
	if c.ConstraintPenalty != 500 {
		t.Errorf("ConstraintPenalty = %v, want 500", c.ConstraintPenalty)
	}
	if c.StallWindowFrac != 0.35 {
		t.Errorf("StallWindowFrac = %v, want 0.35 (YAML must be honored)", c.StallWindowFrac)
	}
	if c.StallRelTol != 1e-3 {
		t.Errorf("StallRelTol = %v, want 1e-3 (YAML must be honored)", c.StallRelTol)
	}
}
