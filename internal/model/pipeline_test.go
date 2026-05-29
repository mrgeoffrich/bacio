package model

import "testing"

// TestProcessFromStages covers the BACI-283 explicit-stage-list
// constructor behind the desktop cumulative-stepper picker: the happy
// path plus each rejection at the model validation boundary.
func TestProcessFromStages(t *testing.T) {
	t.Run("happy_full_chain", func(t *testing.T) {
		p, err := ProcessFromStages([]string{BuiltinTemplateDesign, BuiltinTemplatePlanLarge, BuiltinTemplateImplement, ShipJobMode})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		want := []string{BuiltinTemplateDesign, BuiltinTemplatePlanLarge, BuiltinTemplateImplement, ShipJobMode}
		if len(p.Stages) != len(want) {
			t.Fatalf("Stages = %v, want %v", p.Stages, want)
		}
		for i, s := range want {
			if p.Stages[i] != s {
				t.Fatalf("Stages[%d] = %q, want %q", i, p.Stages[i], s)
			}
		}
		if p.Slug != "design-plan_large-implement-ship" {
			t.Errorf("Slug = %q, want %q", p.Slug, "design-plan_large-implement-ship")
		}
		if p.Name != "Design → Plan (large) → Implement → Ship" {
			t.Errorf("Name = %q, want %q", p.Name, "Design → Plan (large) → Implement → Ship")
		}
	})

	t.Run("trims_and_lowercases", func(t *testing.T) {
		p, err := ProcessFromStages([]string{" Plan ", "", "IMPLEMENT"})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(p.Stages) != 2 || p.Stages[0] != BuiltinTemplatePlan || p.Stages[1] != BuiltinTemplateImplement {
			t.Fatalf("Stages = %v, want [plan implement]", p.Stages)
		}
	})

	t.Run("empty", func(t *testing.T) {
		if _, err := ProcessFromStages(nil); err == nil {
			t.Error("expected error for empty stage list")
		}
		if _, err := ProcessFromStages([]string{"", "  "}); err == nil {
			t.Error("expected error for all-blank stage list")
		}
	})

	t.Run("unknown_mode", func(t *testing.T) {
		if _, err := ProcessFromStages([]string{BuiltinTemplatePlan, "frobnicate"}); err == nil {
			t.Error("expected error for unknown job mode")
		}
	})

	t.Run("duplicate_non_ship", func(t *testing.T) {
		if _, err := ProcessFromStages([]string{BuiltinTemplatePlan, BuiltinTemplatePlan}); err == nil {
			t.Error("expected error for duplicate non-ship mode")
		}
	})

	t.Run("ship_not_last", func(t *testing.T) {
		if _, err := ProcessFromStages([]string{ShipJobMode, BuiltinTemplateImplement}); err == nil {
			t.Error("expected error for ship before the final stage")
		}
	})

	t.Run("ship_only", func(t *testing.T) {
		// A lone Ship hand-off is a valid one-stage chain.
		p, err := ProcessFromStages([]string{ShipJobMode})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(p.Stages) != 1 || p.Stages[0] != ShipJobMode {
			t.Fatalf("Stages = %v, want [ship]", p.Stages)
		}
	})
}

// TestPipelineProcessesLockstep guards the preset table: every stage of
// every preset must be a known builtin template slug or the Ship
// sentinel, so a typo'd new preset fails the unit suite rather than at
// runtime. ProcessFromStages is the validator that enforces the same
// rules on the free-form path.
func TestPipelineProcessesLockstep(t *testing.T) {
	for _, p := range PipelineProcesses() {
		if _, err := ProcessFromStages(p.Stages); err != nil {
			t.Errorf("preset %q has invalid stages %v: %v", p.Slug, p.Stages, err)
		}
	}
}
