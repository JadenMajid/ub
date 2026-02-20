package graph

import (
	"testing"

	"ub/internal/formula"
)

func TestBuildPlanLayers(t *testing.T) {
	formulas := map[string]formula.Formula{
		"a": {Name: "a", Version: "1.0.0"},
		"b": {Name: "b", Version: "1.0.0", Deps: []string{"a"}},
		"c": {Name: "c", Version: "1.0.0", Deps: []string{"a"}},
		"d": {Name: "d", Version: "1.0.0", Deps: []string{"b", "c"}},
	}

	plan, err := BuildPlan(formulas)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(plan.Layers) != 3 {
		t.Fatalf("expected 3 layers, got %d", len(plan.Layers))
	}

	if plan.Layers[0][0] != "a" {
		t.Fatalf("expected first layer to contain a, got %v", plan.Layers[0])
	}
}

func TestBuildPlanCycle(t *testing.T) {
	formulas := map[string]formula.Formula{
		"a": {Name: "a", Version: "1.0.0", Deps: []string{"b"}},
		"b": {Name: "b", Version: "1.0.0", Deps: []string{"a"}},
	}

	if _, err := BuildPlan(formulas); err == nil {
		t.Fatal("expected cycle detection error")
	}
}
