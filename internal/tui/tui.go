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
	"os/signal"
	"strings"
	"syscall"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/mrgeoffrich/bacio/internal/client"
	"github.com/mrgeoffrich/bacio/internal/controller"
	"github.com/mrgeoffrich/bacio/internal/dispatcher"
	"github.com/mrgeoffrich/bacio/internal/idlepinger"
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
	// CapturesInput reports whether the view is currently focused on a
	// raw text input (e.g. a bubbles/textarea) that must receive literal
	// `q` and digit keystrokes. While it returns true the shell does NOT
	// treat `q` as quit or `1`-`9` as tab-switches — they route to the
	// view instead. `ctrl+c` is unaffected: it is always a hard quit.
	// Views with no text input return false unconditionally.
	CapturesInput() bool
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

// pruneTickMsg fires on the controller-UI live-list prune cadence
// (UILeaderPruneInterval). Handled at the shell level: when this process
// holds the leader lease, the shell calls Store.PruneEndedAgentSessions
// with AgentSessionLiveListRetention; standby ticks are no-ops. The
// cadence keeps running regardless of leader state so a standby→leader
// promotion picks up the next tick on schedule.
type pruneTickMsg time.Time

// pruneTick returns a Cmd that fires pruneTickMsg after UILeaderPruneInterval.
func pruneTick() tea.Cmd {
	return tea.Tick(store.UILeaderPruneInterval, func(t time.Time) tea.Msg {
		return pruneTickMsg(t)
	})
}

// queueMatchTickMsg fires on the BACI-51 dispatcher matcher cadence
// (QueueMatchInterval). Same shell-level pattern as pruneTickMsg:
// only the leader runs the matcher; standby ticks are no-ops; the
// cadence keeps running on standby so a promotion picks up next tick.
type queueMatchTickMsg time.Time

// queueMatchTick returns a Cmd that fires queueMatchTickMsg after
// QueueMatchInterval.
func queueMatchTick() tea.Cmd {
	return tea.Tick(store.QueueMatchInterval, func(t time.Time) tea.Msg {
		return queueMatchTickMsg(t)
	})
}

// idlePingTickMsg fires on the BACI-57 idle-pinger cadence
// (IdlePingTickInterval). Same shell-level pattern as the other
// leader-gated tickers: only the leader runs the reaper; standby
// ticks are no-ops; the cadence keeps running on standby so a
// promotion picks up next tick. Must stay in lockstep with
// controller.Controller's idle-ping goroutine so the TUI honours the
// same loop set as desktop + api.
type idlePingTickMsg time.Time

// idlePingTick returns a Cmd that fires idlePingTickMsg after
// IdlePingTickInterval.
func idlePingTick() tea.Cmd {
	return tea.Tick(store.IdlePingTickInterval, func(t time.Time) tea.Msg {
		return idlePingTickMsg(t)
	})
}

// archiveSweepTickMsg fires on the BACI-68 archive-sweep cadence
// (store.ArchiveSweepInterval, currently 1h). Same leader-gated
// shell-level pattern as the other tickers so a TUI-only deployment
// still gets the auto-archive sweep that desktop / api receive from
// the controller goroutine.
type archiveSweepTickMsg time.Time

func archiveSweepTick() tea.Cmd {
	return tea.Tick(store.ArchiveSweepInterval, func(t time.Time) tea.Msg {
		return archiveSweepTickMsg(t)
	})
}

// Run boots the Bubble Tea program in alt-screen mode and blocks until
// quit. The store is owned by the caller. It constructs a leader.Elector
// for the process and releases the lease on graceful exit.
//
// SIGTERM/SIGHUP are translated into a graceful tea.Quit so the deferred
// el.Release fires and a standby UI can promote within one tick (~10s)
// rather than waiting out the 180s stale window. SIGINT is left to
// bubbletea, which already converts it into a graceful quit.
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

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGTERM, syscall.SIGHUP)
	defer signal.Stop(sigCh)
	go func() {
		if _, ok := <-sigCh; ok {
			p.Quit()
		}
	}()

	_, err = p.Run()
	return err
}

// NewModel builds the six-tab TUI model wired against the given store
// and repo. Exposed so alternative entry points (the WASM demo) can
// embed the model into their own tea.Program with different program
// options — e.g. WithInput/WithOutput for an xterm.js bridge.
// Pass el as nil to disable leader election (WASM demo — always acts as leader).
func NewModel(s *store.Store, repo *model.Repo, el *leader.Elector) (*Model, error) {
	board, err := newBoardView(s, repo, tuiActor())
	if err != nil {
		return nil, err
	}
	// Build the pinger client off a borrowed wrapper — the TUI owns the
	// store, the pinger borrows it for audited mutations. Actor is the
	// elector's label when one exists, falling back to a generic "tui"
	// so a WASM-demo run (nil elector) still attributes audit rows.
	pingerActor := "tui"
	if el != nil {
		// Sharing the elector's label keeps audit rows pointing at the
		// same identity the leader chip shows in the footer.
		h, _ := os.Hostname()
		pingerActor = fmt.Sprintf("tui pid=%d host=%s", os.Getpid(), h)
	}
	pingerClient := client.NewLocalFromStore(s, pingerActor)
	return &Model{
		repo:    repo,
		store:   s,
		elector: el,
		matcher: dispatcher.New(s),
		pinger:  idlepinger.New(s, pingerClient, nil),
		tabs: []tab{
			{"Board", board},
			{"Features", newFeaturesView(s, repo)},
			{"Documents", newDocsView(s, repo)},
			{"Agents", newAgentsView(s, repo, tuiActor())},
			{"History", newHistoryView(s, repo)},
			{"Settings", newSettingsView(s, repo)},
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
	// store is the shell-level handle the elector-gated prune + queue
	// matcher tickers use (views still hold their own copies for their
	// own queries).
	store *store.Store
	// elector runs the UI leader election; nil in the WASM demo.
	elector     *leader.Elector
	leaderState leader.State
	// matcher binds BACI-51 queued dispatches to free agents on the
	// queueMatchTick cadence. nil disables (e.g. WASM demo).
	matcher *dispatcher.Matcher
	// pinger is the BACI-57 idle-pinger reaper. Runs under the leader
	// lease via controller.PingIfLeader on the idlePingTick cadence.
	// nil disables (e.g. WASM demo).
	pinger *idlepinger.Pinger
}

func (m *Model) Init() tea.Cmd {
	var cmds []tea.Cmd
	for _, t := range m.tabs {
		if c := t.v.Init(); c != nil {
			cmds = append(cmds, c)
		}
	}
	if m.elector != nil {
		// Fire the first election tick *immediately* rather than waiting
		// out the 10s interval — otherwise a freshly-launched TUI shows no
		// "Controlling" chip for a full tick even when the previous holder
		// released gracefully (heartbeat_at = 0) and ACQUIRE would
		// succeed on the spot. The leaderTickMsg handler chains the next
		// tick via leaderTick(), so this only seeds the loop.
		cmds = append(cmds, func() tea.Msg { return leaderTickMsg(time.Now()) })
		// Seed the live-list prune cadence. We don't fire immediately:
		// store.Open() has just run the 60-day pruneAgentSessions a moment
		// ago, so there are no >4h-old ended rows newer than that pass'
		// cutoff. The first real tick fires UILeaderPruneInterval later.
		cmds = append(cmds, pruneTick())
		// Seed the BACI-51 queue-matcher cadence. Same pattern: don't
		// fire immediately (lots of UIs racing on first launch would
		// all attempt a match in the same instant); the first tick
		// runs QueueMatchInterval (~5s) after Init.
		cmds = append(cmds, queueMatchTick())
		// Seed the BACI-57 idle-pinger cadence. Same pattern: skip the
		// immediate fire (the very first reaper sweep at startup buys
		// nothing — any candidates were already candidates a moment ago).
		// The first tick runs IdlePingTickInterval (~30s) after Init.
		cmds = append(cmds, idlePingTick())
		// Seed the BACI-68 archive-sweep cadence. Hourly; first tick
		// runs ArchiveSweepInterval after Init. Same skip-the-immediate
		// pattern as the other ticks.
		cmds = append(cmds, archiveSweepTick())
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
	case pruneTickMsg:
		// Live-list prune tick: drop ended agent_sessions older than
		// AgentSessionLiveListRetention so the Agents list stays tidy.
		// Leader-gating + error handling lives in controller.PruneIfLeader
		// so this stays in lockstep with the desktop/api goroutine path.
		// The ticker keeps running on standby (the lease can flip back to
		// us mid-run).
		controller.PruneIfLeader(m.store, m.elector, nil)
		if m.elector == nil {
			return m, nil
		}
		return m, pruneTick()
	case queueMatchTickMsg:
		// BACI-51 queue-matcher tick: bind queued dispatches to free
		// agents. Leader-gating + error handling lives in
		// controller.MatchIfLeader — same helper the desktop and api
		// goroutines call, so all three UIs run the matcher identically.
		controller.MatchIfLeader(m.matcher, m.elector, nil)
		if m.elector == nil {
			return m, nil
		}
		return m, queueMatchTick()
	case idlePingTickMsg:
		// BACI-57 idle-pinger tick: queue "are you alive?" pings to
		// long-idle sessions, force-end the ones that don't ack
		// within AgentPingNoAckTimeout. Leader-gating + error
		// handling lives in controller.PingIfLeader — same helper the
		// desktop and api goroutines call, so all three UIs run the
		// reaper identically.
		controller.PingIfLeader(m.pinger, m.elector, nil)
		if m.elector == nil {
			return m, nil
		}
		return m, idlePingTick()
	case archiveSweepTickMsg:
		// BACI-68 archive-sweep tick: runs the three-pass sweep
		// (terminal-issue-age → empty-feature → orphan-document) on
		// the leader. Same leader-gating helper the controller
		// goroutine uses so TUI and desktop/api stay in lockstep.
		controller.ArchiveSweepIfLeader(m.store, m.elector, nil)
		if m.elector == nil {
			return m, nil
		}
		return m, archiveSweepTick()
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
		s := msg.String()
		// captures is true when the active view is focused on a raw text
		// input (e.g. the Settings tab's template-body textarea). While
		// it's set the shell yields `q` and the digit keys to the view so
		// they can be typed literally. `ctrl+c` is never yielded — it is
		// the universal hard quit.
		captures := m.tabs[m.active].v.CapturesInput()

		// ctrl+c always quits. q quits too — except while a view is
		// capturing raw input, where it must reach the text editor. esc
		// stays view-routed when an overlay is up so it closes the
		// overlay rather than the program.
		if s == "ctrl+c" {
			return m, tea.Quit
		}
		if s == "q" && !captures {
			return m, tea.Quit
		}
		hasOverlay := m.tabs[m.active].v.HasOverlay()

		// Digit shortcuts switch tabs from anywhere, including inside an
		// overlay — but not while a view is capturing raw input (a digit
		// must be typeable into the textarea). Close the leaving view's
		// overlay first so coming back lands on its base layout.
		// (tab/shift+tab are intentionally NOT global: views use tab to
		// cycle their inner panes, e.g. description ↔ comments ↔
		// attachments inside the card overlay.)
		if !captures {
			if idx, ok := digitSwitchTarget(s, len(m.tabs)); ok {
				if idx != m.active {
					m.tabs[m.active].v.CloseOverlay()
					m.active = idx
					m.returnTab = -1 // explicit nav cancels any pending auto-return
				}
				return m, nil
			}
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

	// Leader chip: only shown when this process actually holds the lease
	// (not on standby, not in the WASM demo where there's no elector).
	var leaderChip string
	if m.elector != nil && m.leaderState.AmLeader {
		leaderChip = leaderLeadStyle.Render("● ctrl")
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
