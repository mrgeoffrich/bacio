// BACI-199 store-layer tests for the feature auto-completion pass
// added to ArchiveSweep. The pass promotes `active` features whose
// every child issue is in a terminal state to `done` (if at least one
// child is `done`) or `cancelled` (if every child is `cancelled`),
// with the manual sticky-bit blocking sweep-driven promotion. Sibling
// to archive_test.go which covers the BACI-68 archive cascade.
package store

import (
	"testing"

	"github.com/mrgeoffrich/bacio/internal/model"
)

// TestArchiveSweep_FeatureAutoStatePromotesAllDone: a feature with two
// children, both `done`, promotes to `done` on a single sweep tick.
func TestArchiveSweep_FeatureAutoStatePromotesAllDone(t *testing.T) {
	s := newTestStore(t)
	repo, _ := s.CreateRepo("TST", "test", t.TempDir(), "")
	feat, _ := s.CreateFeature(repo.ID, "demo", "Demo", "", "", "")
	a, _ := s.CreateIssue(repo.ID, &feat.ID, "a", "", model.StateDone, nil, "", "")
	b, _ := s.CreateIssue(repo.ID, &feat.ID, "b", "", model.StateDone, nil, "", "")
	_ = a
	_ = b

	res, err := s.ArchiveSweep(false)
	if err != nil {
		t.Fatalf("sweep: %v", err)
	}
	if res.FeaturesAutoStated != 1 {
		t.Fatalf("FeaturesAutoStated = %d, want 1", res.FeaturesAutoStated)
	}
	got, _ := s.GetFeatureByID(feat.ID)
	if got.State != model.FeatureStateDone {
		t.Fatalf("state = %q, want done", got.State)
	}
	if got.StateManual {
		t.Fatalf("state_manual = true after sweep promotion; sweep self-pinned")
	}
}

// TestArchiveSweep_FeatureAutoStateAllCancelledPromotesCancelled: every
// child `cancelled` → feature `cancelled`.
func TestArchiveSweep_FeatureAutoStateAllCancelledPromotesCancelled(t *testing.T) {
	s := newTestStore(t)
	repo, _ := s.CreateRepo("TST", "test", t.TempDir(), "")
	feat, _ := s.CreateFeature(repo.ID, "demo", "Demo", "", "", "")
	_, _ = s.CreateIssue(repo.ID, &feat.ID, "a", "", model.StateCancelled, nil, "", "")
	_, _ = s.CreateIssue(repo.ID, &feat.ID, "b", "", model.StateCancelled, nil, "", "")

	res, err := s.ArchiveSweep(false)
	if err != nil {
		t.Fatalf("sweep: %v", err)
	}
	if res.FeaturesAutoStated != 1 {
		t.Fatalf("FeaturesAutoStated = %d, want 1", res.FeaturesAutoStated)
	}
	got, _ := s.GetFeatureByID(feat.ID)
	if got.State != model.FeatureStateCancelled {
		t.Fatalf("state = %q, want cancelled", got.State)
	}
}

// TestArchiveSweep_FeatureAutoStateMixedPicksDone: at least one child
// `done` and the rest `cancelled` → feature `done`. `done` wins.
func TestArchiveSweep_FeatureAutoStateMixedPicksDone(t *testing.T) {
	s := newTestStore(t)
	repo, _ := s.CreateRepo("TST", "test", t.TempDir(), "")
	feat, _ := s.CreateFeature(repo.ID, "demo", "Demo", "", "", "")
	_, _ = s.CreateIssue(repo.ID, &feat.ID, "a", "", model.StateDone, nil, "", "")
	_, _ = s.CreateIssue(repo.ID, &feat.ID, "b", "", model.StateCancelled, nil, "", "")

	res, err := s.ArchiveSweep(false)
	if err != nil {
		t.Fatalf("sweep: %v", err)
	}
	if res.FeaturesAutoStated != 1 {
		t.Fatalf("FeaturesAutoStated = %d, want 1", res.FeaturesAutoStated)
	}
	got, _ := s.GetFeatureByID(feat.ID)
	if got.State != model.FeatureStateDone {
		t.Fatalf("state = %q, want done (mixed should pick done)", got.State)
	}
}

// TestArchiveSweep_FeatureAutoStateRespectsStickyBit: a feature whose
// user pinned auto-close OFF via SetFeatureAutoClose is left untouched
// even when every child is `done` — the sticky bit blocks sweep-driven
// re-promotion. (Pre-BACI-250 the bit was a side-effect of every
// SetFeatureState call; post-BACI-250 it is its own first-class verb.)
func TestArchiveSweep_FeatureAutoStateRespectsStickyBit(t *testing.T) {
	s := newTestStore(t)
	repo, _ := s.CreateRepo("TST", "test", t.TempDir(), "")
	feat, _ := s.CreateFeature(repo.ID, "demo", "Demo", "", "", "")
	_, _ = s.CreateIssue(repo.ID, &feat.ID, "a", "", model.StateDone, nil, "", "")
	_, _ = s.CreateIssue(repo.ID, &feat.ID, "b", "", model.StateDone, nil, "", "")
	if err := s.SetFeatureState(feat.ID, model.FeatureStateCancelled); err != nil {
		t.Fatalf("set state cancelled: %v", err)
	}
	if err := s.SetFeatureAutoClose(feat.ID, false); err != nil {
		t.Fatalf("auto-close off: %v", err)
	}

	res, err := s.ArchiveSweep(false)
	if err != nil {
		t.Fatalf("sweep: %v", err)
	}
	if res.FeaturesAutoStated != 0 {
		t.Fatalf("FeaturesAutoStated = %d, want 0 (sticky bit must block)", res.FeaturesAutoStated)
	}
	got, _ := s.GetFeatureByID(feat.ID)
	if got.State != model.FeatureStateCancelled {
		t.Fatalf("state = %q, want cancelled (sticky bit must hold)", got.State)
	}
}

// TestArchiveSweep_FeatureAutoStateChildlessFeatureUntouched: a feature
// with zero children must stay `active`.
func TestArchiveSweep_FeatureAutoStateChildlessFeatureUntouched(t *testing.T) {
	s := newTestStore(t)
	repo, _ := s.CreateRepo("TST", "test", t.TempDir(), "")
	feat, _ := s.CreateFeature(repo.ID, "demo", "Demo", "", "", "")

	res, err := s.ArchiveSweep(false)
	if err != nil {
		t.Fatalf("sweep: %v", err)
	}
	if res.FeaturesAutoStated != 0 {
		t.Fatalf("FeaturesAutoStated = %d, want 0 (childless must skip)", res.FeaturesAutoStated)
	}
	got, _ := s.GetFeatureByID(feat.ID)
	if got.State != model.FeatureStateActive {
		t.Fatalf("state = %q, want active", got.State)
	}
}

// TestArchiveSweep_FeatureAutoStateIdempotent: a second sweep on a DB
// where the first sweep already promoted everything writes zero rows.
func TestArchiveSweep_FeatureAutoStateIdempotent(t *testing.T) {
	s := newTestStore(t)
	repo, _ := s.CreateRepo("TST", "test", t.TempDir(), "")
	feat, _ := s.CreateFeature(repo.ID, "demo", "Demo", "", "", "")
	_, _ = s.CreateIssue(repo.ID, &feat.ID, "a", "", model.StateDone, nil, "", "")

	r1, err := s.ArchiveSweep(false)
	if err != nil {
		t.Fatalf("sweep 1: %v", err)
	}
	if r1.FeaturesAutoStated != 1 {
		t.Fatalf("sweep 1 FeaturesAutoStated = %d, want 1", r1.FeaturesAutoStated)
	}
	r2, err := s.ArchiveSweep(false)
	if err != nil {
		t.Fatalf("sweep 2: %v", err)
	}
	if r2.FeaturesAutoStated != 0 {
		t.Fatalf("sweep 2 FeaturesAutoStated = %d, want 0 (idempotent)", r2.FeaturesAutoStated)
	}
	_ = feat
}

// TestArchiveSweep_FeatureAutoStatePartialChildrenLeavesActive: a
// feature where one child is `in_progress` (not terminal) must stay
// `active`, even if siblings are `done`.
func TestArchiveSweep_FeatureAutoStatePartialChildrenLeavesActive(t *testing.T) {
	s := newTestStore(t)
	repo, _ := s.CreateRepo("TST", "test", t.TempDir(), "")
	feat, _ := s.CreateFeature(repo.ID, "demo", "Demo", "", "", "")
	_, _ = s.CreateIssue(repo.ID, &feat.ID, "a", "", model.StateDone, nil, "", "")
	_, _ = s.CreateIssue(repo.ID, &feat.ID, "b", "", model.StateInReview, nil, "", "")

	res, err := s.ArchiveSweep(false)
	if err != nil {
		t.Fatalf("sweep: %v", err)
	}
	if res.FeaturesAutoStated != 0 {
		t.Fatalf("FeaturesAutoStated = %d, want 0 (non-terminal child must block)", res.FeaturesAutoStated)
	}
	got, _ := s.GetFeatureByID(feat.ID)
	if got.State != model.FeatureStateActive {
		t.Fatalf("state = %q, want active", got.State)
	}
}
