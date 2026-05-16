//go:build js

package tui

import (
	tea "github.com/charmbracelet/bubbletea"

	"github.com/mrgeoffrich/bacio/internal/model"
	"github.com/mrgeoffrich/bacio/internal/store"
)

// settingsView (wasm build) is a read-only template list. The native
// build (settings.go) adds an editor + add/rename/delete affordances
// on top of this, backed by bubbles/textarea — but textarea
// transitively imports atotto/clipboard, which has no js/wasm build.
// The wasm demo is read-only by design (cmd/bacio-wasm has no save
// loop), so a Settings tab that only *shows* the templates is the
// correct behaviour here, not a degraded one.
type settingsView struct {
	store  *store.Store
	repo   *model.Repo
	stages []stageRow
	cursor int
	err    error
}

func newSettingsView(s *store.Store, repo *model.Repo) *settingsView {
	v := &settingsView{store: s, repo: repo}
	v.reload()
	return v
}

func (s *settingsView) reload() {
	tmpls, err := s.store.ListPromptTemplates()
	if err != nil {
		s.err = err
		return
	}
	s.err = nil
	s.stages = loadStageRowsFromTemplates(tmpls)
	if s.cursor >= len(s.stages) {
		s.cursor = max(0, len(s.stages)-1)
	}
}

func (s *settingsView) Init() tea.Cmd       { return nil }
func (s *settingsView) Status() string      { return "" }
func (s *settingsView) HasOverlay() bool    { return false }
func (s *settingsView) CloseOverlay()       {}
func (s *settingsView) CapturesInput() bool { return false }
func (s *settingsView) Breadcrumb() string  { return "" }

func (s *settingsView) Help() string {
	return "j/k move · r reload · q quit (read-only in the web demo)"
}

func (s *settingsView) Update(msg tea.Msg) tea.Cmd {
	key, ok := msg.(tea.KeyMsg)
	if !ok {
		return nil
	}
	switch key.String() {
	case "j", "down":
		if s.cursor < len(s.stages)-1 {
			s.cursor++
		}
	case "k", "up":
		if s.cursor > 0 {
			s.cursor--
		}
	case "g", "home":
		s.cursor = 0
	case "G", "end":
		if len(s.stages) > 0 {
			s.cursor = len(s.stages) - 1
		}
	case "r":
		s.reload()
	}
	return nil
}

// openEditor is a no-op on the wasm build — the read-only demo has no
// editor. It exists so the platform-neutral snapshot harness
// (snapshot.go) compiles for js as well as native.
func (s *settingsView) openEditor(int) tea.Cmd { return nil }

func (s *settingsView) View(width, height int) string {
	if width == 0 || height == 0 {
		return ""
	}
	return renderSettingsList(width, height, s.stages, s.cursor, s.err)
}
