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
	feat, err := s.CreateFeature(repo.ID, "demo", "Demo", "", "")
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

// TestSetFeatureState_FlipsStateAndManualBit covers the manual path:
// SetFeatureState(..., manual=true) writes the new state AND sets the
// sticky bit so the auto-completion sweep leaves the row alone. A
// subsequent SetFeatureState(..., manual=false) (the sweep path) MUST
// NOT clear the sticky bit — once a user pins state, the pin is
// permanent until they pin a new value.
func TestSetFeatureState_FlipsStateAndManualBit(t *testing.T) {
	s := newTestStore(t)
	repo, err := s.CreateRepo("FS", "fs", t.TempDir(), "")
	if err != nil {
		t.Fatalf("create repo: %v", err)
	}
	feat, err := s.CreateFeature(repo.ID, "demo", "Demo", "", "")
	if err != nil {
		t.Fatalf("create feature: %v", err)
	}

	// Manual flip to done — sticky bit on.
	if err := s.SetFeatureState(feat.ID, model.FeatureStateDone, true); err != nil {
		t.Fatalf("set state done (manual): %v", err)
	}
	got, err := s.GetFeatureByID(feat.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.State != model.FeatureStateDone {
		t.Fatalf("state = %q, want %q", got.State, model.FeatureStateDone)
	}
	if !got.StateManual {
		t.Fatalf("state_manual = false, want true after manual write")
	}

	// A sweep-path call (manual=false) must not clear the sticky bit.
	if err := s.SetFeatureState(feat.ID, model.FeatureStateCancelled, false); err != nil {
		t.Fatalf("set state cancelled (sweep): %v", err)
	}
	got, err = s.GetFeatureByID(feat.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.State != model.FeatureStateCancelled {
		t.Fatalf("state = %q, want %q", got.State, model.FeatureStateCancelled)
	}
	if !got.StateManual {
		t.Fatalf("state_manual flipped back to false after sweep write")
	}
}

// TestSetFeatureState_SweepLeavesManualBitClear is the inverse: a
// sweep-driven SetFeatureState call (manual=false) on a row whose
// sticky bit was never set MUST NOT turn the bit on. Otherwise the
// sweep would self-pin and never re-promote a feature whose children
// later change state.
func TestSetFeatureState_SweepLeavesManualBitClear(t *testing.T) {
	s := newTestStore(t)
	repo, err := s.CreateRepo("FS", "fs", t.TempDir(), "")
	if err != nil {
		t.Fatalf("create repo: %v", err)
	}
	feat, err := s.CreateFeature(repo.ID, "demo", "Demo", "", "")
	if err != nil {
		t.Fatalf("create feature: %v", err)
	}
	if err := s.SetFeatureState(feat.ID, model.FeatureStateDone, false); err != nil {
		t.Fatalf("set state done (sweep): %v", err)
	}
	got, err := s.GetFeatureByID(feat.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.State != model.FeatureStateDone {
		t.Fatalf("state = %q, want %q", got.State, model.FeatureStateDone)
	}
	if got.StateManual {
		t.Fatalf("state_manual = true after sweep write; sweep self-pinned")
	}
}
