package tui

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/mrgeoffrich/bacio/internal/store"
)

// TestFeaturesView_HToggleCollectHandoffs (BACI-333): pressing `h` flips
// the focused feature's collect_handoffs column and records a
// feature.handoffs audit row. A second `h` flips it back.
func TestFeaturesView_HToggleCollectHandoffs(t *testing.T) {
	s, err := store.OpenMemory()
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	repo, err := s.CreateRepo("MINI", "mini", t.TempDir(), "")
	if err != nil {
		t.Fatalf("create repo: %v", err)
	}
	feat, err := s.CreateFeature(repo.ID, "maintenance", "Maintenance", "", "", "")
	if err != nil {
		t.Fatalf("create feature: %v", err)
	}
	if !feat.CollectHandoffs {
		t.Fatal("default CollectHandoffs = false, want true")
	}

	fv := newFeaturesView(s, repo)

	// h #1 → OFF.
	fv.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("h")})
	off, err := s.GetFeatureByID(feat.ID)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if off.CollectHandoffs {
		t.Fatal("after h #1 CollectHandoffs = true, want false")
	}
	if !hasFeaturesHistoryOp(t, s, "feature.handoffs") {
		t.Fatal("expected a feature.handoffs audit row")
	}

	// h #2 → back ON.
	fv.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("h")})
	on, err := s.GetFeatureByID(feat.ID)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if !on.CollectHandoffs {
		t.Fatal("after h #2 CollectHandoffs = false, want true")
	}
}

func hasFeaturesHistoryOp(t *testing.T, s *store.Store, op string) bool {
	t.Helper()
	rows, err := s.ListHistory(store.HistoryFilter{Op: op, Limit: 100})
	if err != nil {
		t.Fatalf("list history: %v", err)
	}
	return len(rows) > 0
}
