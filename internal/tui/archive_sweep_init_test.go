//go:build !js

package tui

import (
	"path/filepath"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/mrgeoffrich/bacio/internal/leader"
	"github.com/mrgeoffrich/bacio/internal/store"
)

// TestInitFiresArchiveSweepImmediately (BACI-175) pins the sweep-on-
// startup pass for the TUI surface. The Init() Cmd batch must contain
// an immediate archiveSweepTickMsg producer alongside the existing
// archiveSweepTick() cadence seed. Without the immediate fire, a
// short-lived TUI peek would never trigger a sweep before exiting —
// the exact failure mode BACI-175 was filed for.
//
// We don't run the tea.Tick cadence seed (it would block for the full
// ArchiveSweepInterval). Instead we collect every Cmd in the Init
// batch and only execute the ones that return synchronously, then
// assert at least one of those returns an archiveSweepTickMsg.
func TestInitFiresArchiveSweepImmediately(t *testing.T) {
	dir := t.TempDir()
	s, err := store.Open(filepath.Join(dir, "db.sqlite"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	repo, err := s.CreateRepo("MINI", "mini", t.TempDir(), "")
	if err != nil {
		t.Fatalf("create repo: %v", err)
	}

	// A real elector is required — the leader-gated seeds (including
	// the new immediate archive sweep fire) are only appended when
	// m.elector != nil. The backend doesn't need to grant the lease;
	// the assertion is purely about the Cmd graph shape.
	el := leader.New(s, "test")
	t.Cleanup(el.Release)

	m, err := NewModel(s, repo, el, "", nil)
	if err != nil {
		t.Fatalf("NewModel: %v", err)
	}

	cmd := m.Init()
	if cmd == nil {
		t.Fatal("Init returned nil — leader-gated seeds were never appended")
	}

	if !batchProducesMsg[archiveSweepTickMsg](cmd) {
		t.Fatal("Init batch did not contain an immediate archiveSweepTickMsg producer — BACI-175 sweep-on-startup regression")
	}
}

// batchProducesMsg drains the top-level Cmd's BatchMsg and returns
// true iff at least one inner Cmd, when executed with a tight
// deadline, returns a message of type M. tea.Tick-backed Cmds block
// for their full interval and are skipped (they show up as a
// deadline-exceeded path); immediate `func() tea.Msg` Cmds return
// synchronously and are inspected.
func batchProducesMsg[M tea.Msg](cmd tea.Cmd) bool {
	msg := cmd()
	batch, ok := msg.(tea.BatchMsg)
	if !ok {
		// A single-cmd Init wouldn't be wrapped in Batch — handle the
		// edge case rather than failing on the type assertion.
		_, hit := msg.(M)
		return hit
	}
	for _, inner := range batch {
		if inner == nil {
			continue
		}
		done := make(chan tea.Msg, 1)
		go func(c tea.Cmd) {
			defer func() {
				// A panicking Cmd shouldn't bring the test down. Recover
				// and treat it as "no message".
				_ = recover()
			}()
			done <- c()
		}(inner)
		select {
		case got := <-done:
			if _, hit := got.(M); hit {
				return true
			}
		case <-time.After(50 * time.Millisecond):
			// tea.Tick blocks for its interval. Skip.
		}
	}
	return false
}
