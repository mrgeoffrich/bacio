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
	settings := newSettingsView(s, repo)

	m := &Model{
		repo: repo,
		tabs: []tab{
			{"Board", board},
			{"Features", features},
			{"Documents", docs},
			{"Agents", agents},
			{"History", hist},
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
	case "settings":
		m.active = 5
	case "settings-editor":
		m.active = 5
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
		return fmt.Errorf("unknown snapshot target %q (try board, features, docs, agents, agent-detail, history, settings, settings-editor, card-overlay, doc-overlay, feature-overlay, picker, feature-picker, dispatch-picker)", opts.Target)
	}

	out := m.View()
	if !strings.HasSuffix(out, "\n") {
		out += "\n"
	}
	_, err = os.Stdout.WriteString(out)
	return err
}

// focusIssue repositions the board so the issue with the given key (e.g.
// "MINI-1") becomes the selected card. Returns an error if the key is set
// but doesn't match any visible issue. A blank key is a no-op.
func focusIssue(b *boardView, key string) error {
	key = strings.TrimSpace(key)
	if key == "" {
		return nil
	}
	visible := b.visibleStates()
	for ci, st := range visible {
		for ri, iss := range b.columns[st] {
			if strings.EqualFold(iss.Key, key) {
				b.col = ci
				b.rows[st] = ri
				b.refreshSelection()
				return nil
			}
		}
	}
	return fmt.Errorf("issue %q not found in any visible column", key)
}
