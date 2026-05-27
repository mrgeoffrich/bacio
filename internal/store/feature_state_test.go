package store

import (
	"testing"

	"github.com/mrgeoffrich/bacio/internal/model"
)

// TestCreateFeature_DefaultsToActive confirms that the BACI-199 column
// defaults seed `state = 'active'` / `state_manual = 0` on a fresh
// insert, matching the brief's "existing features default to active
// with no manual intervention required" criterion.
func TestCreateFeature_DefaultsToActive(t *testing.T) {
	s := newTestStore(t)
	repo, err := s.CreateRepo("FS", "fs", t.TempDir(), "")
	if err != nil {
		t.Fatalf("create repo: %v", err)
	}
	feat, err := s.CreateFeature(repo.ID, "demo", "Demo", "", "", "")
	if err != nil {
		t.Fatalf("create feature: %v", err)
	}
	if feat.State != model.FeatureStateActive {
		t.Fatalf("created state = %q, want %q", feat.State, model.FeatureStateActive)
	}
	if feat.StateManual {
		t.Fatalf("created state_manual = true, want false")
	}
}

// TestSetFeatureState_LeavesManualBitUntouched (BACI-250): state writes
// and auto-close pinning are decoupled. SetFeatureState writes only
// the `state` column — `state_manual` carries whatever the row already
// had. The old BACI-199 coupling (every state write stamped
// state_manual = 1) is gone; pinning now goes through
// SetFeatureAutoClose.
func TestSetFeatureState_LeavesManualBitUntouched(t *testing.T) {
	s := newTestStore(t)
	repo, err := s.CreateRepo("FS", "fs", t.TempDir(), "")
	if err != nil {
		t.Fatalf("create repo: %v", err)
	}
	feat, err := s.CreateFeature(repo.ID, "demo", "Demo", "", "", "")
	if err != nil {
		t.Fatalf("create feature: %v", err)
	}

	// State write on a fresh row (state_manual = 0) must NOT set the bit.
	if err := s.SetFeatureState(feat.ID, model.FeatureStateDone); err != nil {
		t.Fatalf("set state done: %v", err)
	}
	got, err := s.GetFeatureByID(feat.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.State != model.FeatureStateDone {
		t.Fatalf("state = %q, want %q", got.State, model.FeatureStateDone)
	}
	if got.StateManual {
		t.Fatalf("state_manual = true after state write; state writes must not touch the bit")
	}

	// Pre-seed state_manual = 1 via SetFeatureAutoClose, then a state
	// write must leave it set — state writes do not clear the bit either.
	if err := s.SetFeatureAutoClose(feat.ID, false); err != nil {
		t.Fatalf("auto-close off: %v", err)
	}
	if err := s.SetFeatureState(feat.ID, model.FeatureStateCancelled); err != nil {
		t.Fatalf("set state cancelled: %v", err)
	}
	got, err = s.GetFeatureByID(feat.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.State != model.FeatureStateCancelled {
		t.Fatalf("state = %q, want %q", got.State, model.FeatureStateCancelled)
	}
	if !got.StateManual {
		t.Fatalf("state_manual cleared by state write; state writes must not touch the bit")
	}
}

// TestSetFeatureAutoClose_FlipsStickyBit covers the new BACI-250 helper:
// enabled=false sets state_manual=1, enabled=true clears it. The state
// column is untouched.
func TestSetFeatureAutoClose_FlipsStickyBit(t *testing.T) {
	s := newTestStore(t)
	repo, err := s.CreateRepo("FS", "fs", t.TempDir(), "")
	if err != nil {
		t.Fatalf("create repo: %v", err)
	}
	feat, err := s.CreateFeature(repo.ID, "demo", "Demo", "", "", "")
	if err != nil {
		t.Fatalf("create feature: %v", err)
	}

	// Turn auto-close OFF — sticky bit goes on.
	if err := s.SetFeatureAutoClose(feat.ID, false); err != nil {
		t.Fatalf("auto-close off: %v", err)
	}
	got, err := s.GetFeatureByID(feat.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if !got.StateManual {
		t.Fatalf("state_manual = false after auto-close off, want true")
	}
	if got.State != model.FeatureStateActive {
		t.Fatalf("state = %q after auto-close off, want active (auto-close must not touch state)", got.State)
	}

	// Turn auto-close ON — sticky bit clears.
	if err := s.SetFeatureAutoClose(feat.ID, true); err != nil {
		t.Fatalf("auto-close on: %v", err)
	}
	got, err = s.GetFeatureByID(feat.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.StateManual {
		t.Fatalf("state_manual = true after auto-close on, want false")
	}
	if got.State != model.FeatureStateActive {
		t.Fatalf("state = %q after auto-close on, want active", got.State)
	}
}

// TestSetFeatureAutoClose_BumpsUpdatedAt confirms that flipping the
// auto-close bit bumps updated_at, so the BACI-5 LWW sync gate sees
// the change. Mirrors TestSetFeatureArchivedBumpsUpdatedAtOnEveryFlip
// — backdate the row via SQL so the SQLite CURRENT_TIMESTAMP (second
// resolution) is unambiguously newer.
func TestSetFeatureAutoClose_BumpsUpdatedAt(t *testing.T) {
	s := newTestStore(t)
	repo, err := s.CreateRepo("FS", "fs", t.TempDir(), "")
	if err != nil {
		t.Fatalf("create repo: %v", err)
	}
	feat, err := s.CreateFeature(repo.ID, "demo", "Demo", "", "", "")
	if err != nil {
		t.Fatalf("create feature: %v", err)
	}

	if _, err := s.DB.Exec(
		`UPDATE features SET updated_at = '2026-01-01 00:00:00' WHERE id = ?`, feat.ID,
	); err != nil {
		t.Fatalf("backdate: %v", err)
	}
	before, err := s.GetFeatureByID(feat.ID)
	if err != nil {
		t.Fatalf("get before: %v", err)
	}

	if err := s.SetFeatureAutoClose(feat.ID, false); err != nil {
		t.Fatalf("auto-close off: %v", err)
	}
	after, err := s.GetFeatureByID(feat.ID)
	if err != nil {
		t.Fatalf("get after: %v", err)
	}
	if !after.UpdatedAt.After(before.UpdatedAt) {
		t.Fatalf("updated_at did not advance: before=%s after=%s", before.UpdatedAt, after.UpdatedAt)
	}
}
