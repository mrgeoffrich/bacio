package tui

import (
	"fmt"
	"os"
	"strings"

	"github.com/mrgeoffrich/bacio/internal/model"
	"github.com/mrgeoffrich/bacio/internal/store"
)

// SnapshotOpts controls non-interactive rendering for layout debugging.
type SnapshotOpts struct {
	Target string // tab or overlay name (case-insensitive)
	Issue  string // optional issue key (e.g. MINI-1) to focus on board/card-overlay
	Width  int
	Height int
}

// Snapshot builds the same Model the TUI uses and renders one view to
// stdout, sized to the given width/height. Lets us inspect layouts in CI,
// reproduce visual bugs, and run snapshot tests without a real terminal.
func Snapshot(s *store.Store, repo *model.Repo, opts SnapshotOpts) error {
	if opts.Width <= 0 {
		opts.Width = 120
	}
	if opts.Height <= 0 {
		opts.Height = 40
	}

	board, err := newBoardView(s, repo, "snapshot")
	if err != nil {
		return err
	}
	features := newFeaturesView(s, repo)
	docs := newDocsView(s, repo)
	agents := newAgentsView(s, repo, "snapshot")
	hist := newHistoryView(s, repo)
	sync := newSyncView(s, repo, "snapshot")
	settings := newSettingsView(s, repo)

	m := &Model{
		repo: repo,
		tabs: []tab{
			{"Board", board},
			{"Features", features},
			{"Documents", docs},
			{"Agents", agents},
			{"History", hist},
			{"Sync", sync},
			{"Settings", settings},
		},
		width:  opts.Width,
		height: opts.Height,
	}

	target := strings.ToLower(strings.TrimSpace(opts.Target))
	switch target {
	case "board":
		m.active = 0
		if err := focusIssue(board, opts.Issue); err != nil {
			return err
		}
	case "features":
		m.active = 1
	case "documents", "docs":
		m.active = 2
	case "agents":
		m.active = 3
	case "agent-detail":
		m.active = 3
		if len(agents.sessions) > 0 {
			agents.detail = true
		}
	case "history":
		m.active = 4
	case "sync":
		m.active = 5
	case "settings":
		m.active = 6
	case "settings-editor":
		m.active = 6
		if len(settings.stages) > 0 {
			settings.openEditor(0)
		}
	case "card-overlay", "card":
		m.active = 0
		if err := focusIssue(board, opts.Issue); err != nil {
			return err
		}
		board.overlay = true
	case "picker":
		m.active = 0
		board.openPicker()
	case "feature-picker":
		m.active = 0
		board.openFeaturePicker()
	case "dispatch-picker":
		m.active = 0
		if err := focusIssue(board, opts.Issue); err != nil {
			return err
		}
		board.openDispatchPicker()
	case "composer":
		// BACI-168 "+ from prompt" overlay. The wasm build's
		// openComposeOverlay is a no-op (no textarea), so this target
		// renders a plain board on the wasm snapshot binary; on native
		// it renders the composer chrome centred over the board.
		m.active = 0
		board.openComposeOverlay()
	case "doc-overlay":
		m.active = 2
		if docs.loaded != nil {
			docs.overlay = true
		}
	case "feature-overlay":
		m.active = 1
		if features.selected != nil {
			features.overlay = true
		}
	default:
		return fmt.Errorf("unknown snapshot target %q (try board, features, docs, agents, agent-detail, history, sync, settings, settings-editor, card-overlay, doc-overlay, feature-overlay, picker, feature-picker, dispatch-picker, composer)", opts.Target)
	}

	out := m.View()
	if !strings.HasSuffix(out, "\n") {
		out += "\n"
	}
	_, err = os.Stdout.WriteString(out)
	return err
}

// focusIssue is a snapshot-side alias for boardView.focusOnKey. Kept so
// the existing snapshot call sites and tests continue to compile; the
// real implementation lives on boardView so production code (the
// compose overlay's post-submit cursor jump) can share it.
func focusIssue(b *boardView, key string) error {
	return b.focusOnKey(key)
}
