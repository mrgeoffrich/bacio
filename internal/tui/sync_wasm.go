//go:build js

package tui

import (
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/mrgeoffrich/bacio/internal/model"
	"github.com/mrgeoffrich/bacio/internal/store"
)

// syncView (wasm build) is a placeholder for the Sync tab. The native
// build (sync.go) reads internal/sync's BuildRegistry, but
// internal/sync pulls in flock-based file locking that has no js/wasm
// build — so the wasm demo can't show the same surface. A short
// "(not available in the web demo)" panel keeps the tab strip intact
// and the snapshot harness happy.
type syncView struct {
	store *store.Store
	repo  *model.Repo
}

func newSyncView(s *store.Store, repo *model.Repo, _ string) *syncView {
	return &syncView{store: s, repo: repo}
}

func (v *syncView) Init() tea.Cmd                  { return nil }
func (v *syncView) Status() string                 { return "" }
func (v *syncView) HasOverlay() bool               { return false }
func (v *syncView) CloseOverlay()                  {}
func (v *syncView) CapturesInput() bool            { return false }
func (v *syncView) Breadcrumb() string             { return "" }
func (v *syncView) Help() string                   { return "q quit (sync surface not available in the web demo)" }
func (v *syncView) Update(_ tea.Msg) tea.Cmd       { return nil }

func (v *syncView) View(width, height int) string {
	if width == 0 || height == 0 {
		return ""
	}
	innerWidth := width - 2
	if innerWidth < 40 {
		innerWidth = 40
	}
	innerHeight := height - 2
	if innerHeight < 5 {
		innerHeight = 5
	}
	box := lipgloss.NewStyle().
		Border(colBorder).BorderForeground(colFocusBorder).
		Width(innerWidth).Height(innerHeight)
	return box.Render(mutedStyle.Padding(1, 2).Render(
		"Sync surface is not available in the web demo (the registry helper needs flock-based locking)."))
}
