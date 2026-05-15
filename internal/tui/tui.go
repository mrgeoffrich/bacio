// Package tui implements the full-screen terminal UI for `bacio tui`.
//
// The shell defined here owns the alt-screen Bubble Tea program, the tab
// strip, and the top-level key bindings (quit, switch tab). Each tab is a
// `view`: a self-contained widget that handles its own input, state, and
// rendering. Views can declare an active overlay (e.g. fullscreen card or
// document viewer); when one is up the shell routes q/esc to the view
// (so esc closes the overlay rather than quitting), but digit shortcuts
// always win — switching tabs from inside an overlay closes that
// overlay so coming back lands on the base layout. Tab/shift+tab are
// view-local: the shell never grabs them, so each view can use them
// for its own purposes (e.g. cycling card-overlay panes).
package tui

import (
	"fmt"
	"os"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/mrgeoffrich/bacio/internal/leader"
	"github.com/mrgeoffrich/bacio/internal/model"
	"github.com/mrgeoffrich/bacio/internal/store"
)

// view is the contract every tab implements. Shell mutates state by handing
// messages to Update; rendering is a pure function of (width, height).
//
// Init returns an optional one-shot Cmd run at program startup — used by
// the board to kick off its 30-second refresh tick. Status returns an
// optional right-aligned chip rendered in the footer (e.g. "↻ 14:23:05"
// for the board's last refresh time); empty strings are skipped.
type view interface {
	Init() tea.Cmd
	Update(msg tea.Msg) tea.Cmd
	View(width, height int) string
	Help() string
	HasOverlay() bool
	// CloseOverlay dismisses any open overlay so the next time this tab
	// is activated it renders its base layout. Called by the shell when
	// the user switches tabs from inside an overlay.
	CloseOverlay()
	Status() string
	// Breadcrumb returns the trail beyond the tab name when a detail
	// overlay is up — e.g. "[MINI-22]" or "[MINI-22] → Comments". When
	// non-empty, the shell shows "{tabName} → {Breadcrumb}" in place of
	// the tab strip. Empty means no detail; the regular tab strip
	// renders as usual.
	Breadcrumb() string
}

type tab struct {
	name string
	v    view
}

// openDocMsg is emitted by the board's attachments pane when the user
// presses enter on a linked document. The shell intercepts it and hands
// the filename to the Documents tab, switching focus.
type openDocMsg struct {
	filename string
}

// leaderTickMsg fires on every ~10s election tick, handled exclusively by
// the shell (not broadcast to views — views receive leaderStateMsg instead).
type leaderTickMsg time.Time

// leaderStateMsg is dispatched by the shell after each election tick.
// It is broadcast to all views; boardView uses it to gate dispatch.
type leaderStateMsg struct{ state leader.State }

// leaderTick returns a Cmd that fires leaderTickMsg after UILeaderHeartbeatInterval.
func leaderTick() tea.Cmd {
	return tea.Tick(store.UILeaderHeartbeatInterval, func(t time.Time) tea.Msg {
		return leaderTickMsg(t)
	})
}

// Run boots the Bubble Tea program in alt-screen mode and blocks until
// quit. The store is owned by the caller. It constructs a leader.Elector
// for the process and releases the lease on graceful exit.
func Run(s *store.Store, repo *model.Repo) error {
	h, _ := os.Hostname()
	label := fmt.Sprintf("tui pid=%d host=%s", os.Getpid(), h)
	el := leader.New(s, label)
	defer el.Release()
	m, err := NewModel(s, repo, el)
	if err != nil {
		return err
	}
	p := tea.NewProgram(m, tea.WithAltScreen())
	_, err = p.Run()
	return err
}

// NewModel builds the four-tab TUI model wired against the given store
// and repo. Exposed so alternative entry points (the WASM demo) can
// embed the model into their own tea.Program with different program
// options — e.g. WithInput/WithOutput for an xterm.js bridge.
// Pass el as nil to disable leader election (WASM demo — always acts as leader).
func NewModel(s *store.Store, repo *model.Repo, el *leader.Elector) (*Model, error) {
	board, err := newBoardView(s, repo, tuiActor())
	if err != nil {
		return nil, err
	}
	return &Model{
		repo:    repo,
		elector: el,
		tabs: []tab{
			{"Board", board},
			{"Features", newFeaturesView(s, repo)},
			{"Documents", newDocsView(s, repo)},
			{"Agents", newAgentsView(s, repo)},
			{"History", newHistoryView(s, repo)},
		},
		returnTab: -1,
	}, nil
}

type Model struct {
	repo   *model.Repo
	tabs   []tab
	active int
	width  int
	height int
	// returnTab is set when a cross-tab jump (e.g. opening a doc
	// attachment from the board) lands the user on a tab with an open
	// overlay; closing that overlay restores the previous tab. -1 when
	// no return is pending. Cleared whenever the user navigates
	// explicitly (digit/tab/shift+tab) so a manual swap doesn't bounce
	// back later.
	returnTab int
	// elector runs the UI leader election; nil in the WASM demo.
	elector     *leader.Elector
	leaderState leader.State
}

func (m *Model) Init() tea.Cmd {
	var cmds []tea.Cmd
	for _, t := range m.tabs {
		if c := t.v.Init(); c != nil {
			cmds = append(cmds, c)
		}
	}
	if m.elector != nil {
		cmds = append(cmds, leaderTick())
	}
	if len(cmds) == 0 {
		return nil
	}
	return tea.Batch(cmds...)
}

func (m *Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		return m, nil
	case leaderTickMsg:
		// Election tick: renew if leader, acquire if standby. Handled at
		// the shell level (not broadcast to views). The resulting state is
		// dispatched as leaderStateMsg so views can update asynchronously.
		if m.elector != nil {
			m.leaderState = m.elector.Tick()
		}
		state := m.leaderState
		stateCmd := func() tea.Msg { return leaderStateMsg{state: state} }
		return m, tea.Batch(leaderTick(), stateCmd)
	case openDocMsg:
		// Cross-tab: hand the filename to the Documents tab and focus
		// it. Remember where we came from so esc on the doc overlay
		// returns the user there.
		for i, t := range m.tabs {
			if t.name != "Documents" {
				continue
			}
			if dv, ok := t.v.(*docsView); ok {
				dv.selectByFilename(msg.filename)
			}
			if m.active != i {
				m.returnTab = m.active
			}
			m.active = i
			return m, nil
		}
		return m, nil
	case tea.KeyMsg:
		// ctrl+c and q always quit, even past an overlay. esc stays
		// view-routed when an overlay is up so it closes the overlay
		// rather than the program.
		switch msg.String() {
		case "ctrl+c", "q":
			return m, tea.Quit
		}
		s := msg.String()
		hasOverlay := m.tabs[m.active].v.HasOverlay()

		// Digit shortcuts always win — the user can jump screens from
		// anywhere, including inside an overlay. Close the leaving
		// view's overlay first so coming back lands on its base layout.
		// (tab/shift+tab are intentionally NOT global: views use tab to
		// cycle their inner panes, e.g. description ↔ comments ↔
		// attachments inside the card overlay.)
		if idx, ok := digitSwitchTarget(s, len(m.tabs)); ok {
			if idx != m.active {
				m.tabs[m.active].v.CloseOverlay()
				m.active = idx
				m.returnTab = -1 // explicit nav cancels any pending auto-return
			}
			return m, nil
		}

		if !hasOverlay && s == "esc" {
			return m, tea.Quit
		}
		cmd := m.tabs[m.active].v.Update(msg)
		// If the active view just closed its overlay AND we have a
		// pending return target (set by openDocMsg), bounce back.
		if hasOverlay && !m.tabs[m.active].v.HasOverlay() && m.returnTab >= 0 {
			m.active = m.returnTab
			m.returnTab = -1
		}
		return m, cmd
	}
	// Non-key, non-windowsize messages (ticks, custom commands) get
	// broadcast to every view so a tab can receive replies to its own
	// commands even while another tab is active.
	var cmds []tea.Cmd
	for _, t := range m.tabs {
		if c := t.v.Update(msg); c != nil {
			cmds = append(cmds, c)
		}
	}
	if len(cmds) == 0 {
		return m, nil
	}
	return m, tea.Batch(cmds...)
}

func (m *Model) View() string {
	if m.width == 0 || m.height == 0 {
		return ""
	}
	header := m.renderHeader()
	footer := m.renderFooter()
	bodyHeight := m.height - lipgloss.Height(header) - lipgloss.Height(footer)
	if bodyHeight < 5 {
		bodyHeight = 5
	}
	body := m.tabs[m.active].v.View(m.width, bodyHeight)
	// Strict-clip and pad so the active view can never push the tab
	// strip / footer off-screen by overflowing its row budget.
	body = scrollLines(body, 0, bodyHeight)
	if missing := bodyHeight - totalLines(body); missing > 0 {
		body += strings.Repeat("\n", missing)
	}
	return lipgloss.JoinVertical(lipgloss.Left, header, body, footer)
}

// renderFooter lays out left-aligned help and a right-aligned status chip
// (e.g. the board's auto-refresh timestamp). Both come from the active view.
func (m *Model) renderFooter() string {
	helpText := footerStyle.Render(" " + m.tabs[m.active].v.Help() + " ")
	status := m.tabs[m.active].v.Status()
	if status == "" {
		return helpText
	}
	statusText := footerStyle.Render(status + " ")
	gap := m.width - lipgloss.Width(helpText) - lipgloss.Width(statusText)
	if gap < 1 {
		gap = 1
	}
	spacer := lipgloss.NewStyle().Width(gap).Render("")
	return lipgloss.JoinHorizontal(lipgloss.Top, helpText, spacer, statusText)
}

func (m *Model) renderHeader() string {
	displayName := m.repo.Name
	if owner := parseRepoOwner(m.repo.RemoteURL); owner != "" {
		displayName = owner + "/" + m.repo.Name
	}
	repoTag := titleStyle.Render(fmt.Sprintf("%s — %s %s", m.repo.Prefix, repoGlyph, displayName))

	var left string
	if crumb := m.tabs[m.active].v.Breadcrumb(); crumb != "" {
		// On a detail page: tab strip is replaced by a breadcrumb.
		// Tab name in muted, separator in muted, leaf in active style.
		left = lipgloss.JoinHorizontal(lipgloss.Top,
			tabInactive.Render(m.tabs[m.active].name),
			mutedStyle.Render(" → "),
			tabActive.Render(crumb),
		)
	} else {
		var tabParts []string
		for i, t := range m.tabs {
			label := fmt.Sprintf("%d %s", i+1, t.name)
			if i == m.active {
				tabParts = append(tabParts, tabActive.Render(label))
			} else {
				tabParts = append(tabParts, tabInactive.Render(label))
			}
		}
		left = lipgloss.JoinHorizontal(lipgloss.Top, tabParts...)
	}

	// Leader chip: only shown when the elector is active (not the WASM demo).
	var leaderChip string
	if m.elector != nil {
		if m.leaderState.AmLeader {
			leaderChip = leaderLeadStyle.Render("● ctrl")
		} else {
			leaderChip = leaderStandbyStyle.Render("○ stby")
		}
	}
	right := lipgloss.JoinHorizontal(lipgloss.Top, leaderChip, repoTag)

	gap := m.width - lipgloss.Width(right) - lipgloss.Width(left)
	if gap < 1 {
		gap = 1
	}
	spacer := lipgloss.NewStyle().Width(gap).Render("")
	return lipgloss.JoinHorizontal(lipgloss.Top, left, spacer, right)
}

// digitSwitchTarget maps a "1"–"9" key to a 0-based tab index. Returns
// (idx, true) only when the digit names a valid tab; out-of-range
// digits fall through to the view rather than being silently swallowed.
func digitSwitchTarget(key string, n int) (int, bool) {
	if len(key) != 1 || key[0] < '1' || key[0] > '9' {
		return 0, false
	}
	idx := int(key[0] - '1')
	if idx >= n {
		return 0, false
	}
	return idx, true
}

// parseRepoOwner extracts the owner/org segment from a git remote URL. It
// understands both SSH ("git@host:owner/repo.git") and HTTPS-style remotes
// ("https://host/owner/repo.git"), and preserves multi-level paths so
// GitLab subgroups ("group/subgroup/repo") still surface correctly. Returns
// "" when the URL is empty or unparseable, and the caller falls back to
// just the repo name.
func parseRepoOwner(remoteURL string) string {
	s := strings.TrimSpace(remoteURL)
	s = strings.TrimSuffix(s, ".git")
	s = strings.TrimSuffix(s, "/")
	if s == "" {
		return ""
	}

	switch {
	case strings.Contains(s, "://"):
		// scheme://[user@]host/owner/repo
		s = s[strings.Index(s, "://")+3:]
		if at := strings.Index(s, "@"); at >= 0 {
			s = s[at+1:]
		}
		slash := strings.Index(s, "/")
		if slash < 0 {
			return ""
		}
		s = s[slash+1:]
	case strings.Contains(s, "@") && strings.Contains(s, ":"):
		// SCP-style: user@host:owner/repo
		s = s[strings.Index(s, "@")+1:]
		colon := strings.Index(s, ":")
		if colon < 0 {
			return ""
		}
		s = s[colon+1:]
	}

	parts := strings.Split(s, "/")
	if len(parts) < 2 {
		return ""
	}
	return strings.Join(parts[:len(parts)-1], "/")
}
