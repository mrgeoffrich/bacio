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

	t.Run("rejects_dispatch_preamble", func(t *testing.T) {
		// BACI-294 decision 5: the reserved _dispatch_preamble slug leads
		// BuiltinTemplateSlugs() and was wrongly accepted as a stage mode.
		// It is the wrapper prepended to every dispatch, never a chain stage.
		if _, err := ProcessFromStages([]string{BuiltinTemplatePreamble}); err == nil {
			t.Error("expected error for _dispatch_preamble as a stage mode")
		}
		if _, err := ProcessFromStages([]string{BuiltinTemplatePlan, BuiltinTemplatePreamble}); err == nil {
			t.Error("expected error for _dispatch_preamble in a chain")
		}
	})

	t.Run("shelve_last", func(t *testing.T) {
		// BACI-332: shelve is a final-only sentinel like ship, and renders
		// "Shelve" via the sentinel-label lookup (not the raw mode).
		p, err := ProcessFromStages([]string{BuiltinTemplateScope, ShelveJobMode})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(p.Stages) != 2 || p.Stages[1] != ShelveJobMode {
			t.Fatalf("Stages = %v, want [scope shelve]", p.Stages)
		}
		if p.Name != "Scope → Shelve" {
			t.Errorf("Name = %q, want %q", p.Name, "Scope → Shelve")
		}
	})

	t.Run("shelve_not_last", func(t *testing.T) {
		if _, err := ProcessFromStages([]string{ShelveJobMode, BuiltinTemplateImplement}); err == nil {
			t.Error("expected error for shelve before the final stage")
		}
	})

	t.Run("two_finals_rejected", func(t *testing.T) {
		// Ship and Shelve are each final-only, so a chain ending in both
		// fails the position rule — at most one terminal sentinel.
		if _, err := ProcessFromStages([]string{BuiltinTemplateImplement, ShipJobMode, ShelveJobMode}); err == nil {
			t.Error("expected error for two terminal sentinels (ship then shelve)")
		}
	})

	t.Run("mark_done_last", func(t *testing.T) {
		// BACI-352: mark_done is a final-only sentinel like ship/shelve, and
		// renders "Done" via the sentinel-label lookup (not the raw mode).
		p, err := ProcessFromStages([]string{BuiltinTemplateImplement, MarkDoneJobMode})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(p.Stages) != 2 || p.Stages[1] != MarkDoneJobMode {
			t.Fatalf("Stages = %v, want [implement mark_done]", p.Stages)
		}
		if p.Name != "Implement → Done" {
			t.Errorf("Name = %q, want %q", p.Name, "Implement → Done")
		}
	})

	t.Run("mark_done_not_last", func(t *testing.T) {
		if _, err := ProcessFromStages([]string{MarkDoneJobMode, BuiltinTemplateImplement}); err == nil {
			t.Error("expected error for mark_done before the final stage")
		}
	})

	t.Run("ship_then_mark_done_rejected", func(t *testing.T) {
		// At most one terminal sentinel: ship is not last, so the chain
		// ending in both ship and mark_done is rejected.
		if _, err := ProcessFromStages([]string{BuiltinTemplateImplement, ShipJobMode, MarkDoneJobMode}); err == nil {
			t.Error("expected error for two terminal sentinels (ship then mark_done)")
		}
	})
}

// TestProcessBySlugScopeShelve: the BACI-332 named preset resolves to the
// Scope → Shelve chain.
func TestProcessBySlugScopeShelve(t *testing.T) {
	p, err := ProcessBySlug("scope-shelve")
	if err != nil {
		t.Fatalf("ProcessBySlug(scope-shelve): %v", err)
	}
	want := []string{BuiltinTemplateScope, ShelveJobMode}
	if len(p.Stages) != len(want) {
		t.Fatalf("Stages = %v, want %v", p.Stages, want)
	}
	for i, s := range want {
		if p.Stages[i] != s {
			t.Fatalf("Stages[%d] = %q, want %q", i, p.Stages[i], s)
		}
	}
}

// TestProcessFromStagesWithPrefix covers the BACI-294 edit-path
// constructor: it validates the combined locked-prefix + edited-tail
// chain (no duplicate rule, ship-last across the whole chain) and returns
// the tail only.
func TestProcessFromStagesWithPrefix(t *testing.T) {
	t.Run("happy_combined", func(t *testing.T) {
		// Locked prefix [plan complete, implement running]; new tail
		// [review, ship]. The returned Stages is the tail only.
		p, err := ProcessFromStagesWithPrefix(
			[]string{BuiltinTemplatePlan, BuiltinTemplateImplement},
			[]string{BuiltinTemplateReview, ShipJobMode},
		)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		want := []string{BuiltinTemplateReview, ShipJobMode}
		if len(p.Stages) != len(want) {
			t.Fatalf("Stages = %v, want %v (tail only)", p.Stages, want)
		}
		for i, s := range want {
			if p.Stages[i] != s {
				t.Fatalf("Stages[%d] = %q, want %q", i, p.Stages[i], s)
			}
		}
		// Slug/Name describe the whole chain.
		if p.Slug != "plan-implement-review-ship" {
			t.Errorf("Slug = %q, want whole-chain slug", p.Slug)
		}
	})

	t.Run("duplicate_mode_allowed", func(t *testing.T) {
		// Decision 1: a re-loop (Implement → Review → Implement) is
		// expressible — duplicates are NOT rejected on the edit path.
		p, err := ProcessFromStagesWithPrefix(
			[]string{BuiltinTemplateImplement},
			[]string{BuiltinTemplateReview, BuiltinTemplateImplement},
		)
		if err != nil {
			t.Fatalf("unexpected error for a re-loop chain: %v", err)
		}
		if len(p.Stages) != 2 {
			t.Fatalf("Stages = %v, want the 2-stage tail", p.Stages)
		}
	})

	t.Run("empty_prefix_replaces_all", func(t *testing.T) {
		// Every job pending → the whole chain is the tail; an empty prefix
		// is fine as long as the tail is non-empty.
		p, err := ProcessFromStagesWithPrefix(nil, []string{BuiltinTemplatePlan, BuiltinTemplateImplement})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(p.Stages) != 2 {
			t.Fatalf("Stages = %v, want [plan implement]", p.Stages)
		}
	})

	t.Run("both_empty", func(t *testing.T) {
		if _, err := ProcessFromStagesWithPrefix(nil, nil); err == nil {
			t.Error("expected error when prefix and tail are both empty")
		}
	})

	t.Run("ship_in_tail_not_last", func(t *testing.T) {
		if _, err := ProcessFromStagesWithPrefix(
			[]string{BuiltinTemplatePlan},
			[]string{ShipJobMode, BuiltinTemplateImplement},
		); err == nil {
			t.Error("expected error for ship before the final stage of the tail")
		}
	})

	t.Run("ship_in_prefix_with_tail", func(t *testing.T) {
		// Ship must be the last stage of the WHOLE chain — a ship in the
		// locked prefix followed by a non-ship tail is rejected.
		if _, err := ProcessFromStagesWithPrefix(
			[]string{BuiltinTemplatePlan, ShipJobMode},
			[]string{BuiltinTemplateImplement},
		); err == nil {
			t.Error("expected error for ship in the prefix with a non-empty tail")
		}
	})

	t.Run("unknown_tail_mode", func(t *testing.T) {
		if _, err := ProcessFromStagesWithPrefix(
			[]string{BuiltinTemplatePlan},
			[]string{"frobnicate"},
		); err == nil {
			t.Error("expected error for unknown tail mode")
		}
	})

	t.Run("rejects_dispatch_preamble", func(t *testing.T) {
		if _, err := ProcessFromStagesWithPrefix(
			[]string{BuiltinTemplatePlan},
			[]string{BuiltinTemplatePreamble},
		); err == nil {
			t.Error("expected error for _dispatch_preamble in the tail")
		}
	})

	t.Run("mark_done_in_tail_last", func(t *testing.T) {
		// BACI-352: mark_done is final-only across the whole chain, same as
		// ship/shelve on the edit path.
		p, err := ProcessFromStagesWithPrefix(
			[]string{BuiltinTemplateImplement},
			[]string{BuiltinTemplateReview, MarkDoneJobMode},
		)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(p.Stages) != 2 || p.Stages[1] != MarkDoneJobMode {
			t.Fatalf("Stages = %v, want [review mark_done] (tail only)", p.Stages)
		}
	})

	t.Run("mark_done_in_tail_not_last", func(t *testing.T) {
		if _, err := ProcessFromStagesWithPrefix(
			[]string{BuiltinTemplatePlan},
			[]string{MarkDoneJobMode, BuiltinTemplateImplement},
		); err == nil {
			t.Error("expected error for mark_done before the final stage of the tail")
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
