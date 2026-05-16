package tui

import (
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/mrgeoffrich/bacio/internal/model"
	"github.com/mrgeoffrich/bacio/internal/store"
	"github.com/mrgeoffrich/bacio/internal/version"
)

// agentCardHeight is the fixed row budget for one agent card (4 content
// rows + rounded border, no vertical padding).
const agentCardHeight = 6

// agentsView is the supervision panel: a card per agent session live
// against this repo, with `enter` drilling into one agent's open claims
// and dispatches. Read-only — a window onto the local agent registry,
// refreshed with `r`.
type agentsView struct {
	store *store.Store
	repo  *model.Repo

	sessions   []*model.AgentSession
	claims     map[int64][]*model.AgentClaim // session PK -> open claims
	dispatches []*model.AgentDispatch        // every dispatch in the repo
	pending    map[int64]int                 // session PK -> open dispatch count

	cursor    int  // selected card
	scroll    int  // index of the first visible card
	detail    bool // drill-down overlay open for sessions[cursor]
	detailRow int  // scroll offset within the detail overlay
	showAll   bool // when false (default), hide SessionStart stubs that never registered
	err       error
}

func newAgentsView(s *store.Store, repo *model.Repo) *agentsView {
	a := &agentsView{store: s, repo: repo, claims: map[int64][]*model.AgentClaim{}}
	a.reload()
	return a
}

func (a *agentsView) reload() {
	a.pending = map[int64]int{}
	sessions, err := a.store.ListAgentSessions(store.AgentSessionFilter{
		RepoID:         &a.repo.ID,
		RegisteredOnly: !a.showAll,
	})
	if err != nil {
		a.err = err
		return
	}
	// Open claims for every alive session in the repo, in one query —
	// ended sessions hold no open claims, so they're correctly absent.
	a.claims, err = a.store.OpenClaimsBySession(a.repo.ID)
	if err != nil {
		a.err = err
		return
	}
	dispatches, err := a.store.ListDispatches(store.DispatchFilter{RepoID: &a.repo.ID})
	if err != nil {
		a.err = err
		return
	}
	// Count open (pending/delivered) dispatches per session — matched by
	// the bare session id or the agent identity behind it, mirroring the
	// inbox drain query.
	for _, d := range dispatches {
		if d.Status != model.DispatchPending && d.Status != model.DispatchDelivered {
			continue
		}
		for _, s := range sessions {
			if dispatchTargetsSession(d, s) {
				a.pending[s.ID]++
			}
		}
	}
	a.err = nil
	a.sessions = sessions
	a.dispatches = dispatches
	if a.cursor >= len(sessions) {
		a.cursor = max(0, len(sessions)-1)
	}
}

// dispatchTargetsSession reports whether a dispatch is aimed at this
// session — either by its bare session id or via the agent identity.
func dispatchTargetsSession(d *model.AgentDispatch, s *model.AgentSession) bool {
	if d.TargetSessionID != "" && d.TargetSessionID == s.SessionID {
		return true
	}
	if d.TargetAgentID != nil && s.AgentID != nil && *d.TargetAgentID == *s.AgentID {
		return true
	}
	return false
}

func (a *agentsView) Init() tea.Cmd  { return nil }
func (a *agentsView) Status() string { return "" }

func (a *agentsView) HasOverlay() bool    { return a.detail }
func (a *agentsView) CloseOverlay()       { a.detail = false }
func (a *agentsView) CapturesInput() bool { return false }

func (a *agentsView) Breadcrumb() string {
	if !a.detail || a.cursor >= len(a.sessions) {
		return ""
	}
	return agentLabel(a.sessions[a.cursor])
}

func (a *agentsView) Help() string {
	if a.detail {
		return "j/k scroll · esc back · r reload · q quit"
	}
	suffix := "a show stubs"
	if a.showAll {
		suffix = "a hide stubs"
	}
	return "j/k move · enter detail · g/G top/bottom · r reload · " + suffix + " · q quit"
}

func (a *agentsView) Update(msg tea.Msg) tea.Cmd {
	key, ok := msg.(tea.KeyMsg)
	if !ok {
		return nil
	}
	if a.detail {
		switch key.String() {
		case "esc":
			a.detail = false
		case "j", "down":
			a.detailRow++
		case "k", "up":
			if a.detailRow > 0 {
				a.detailRow--
			}
		case "g", "home":
			a.detailRow = 0
		case "r":
			a.reload()
		}
		return nil
	}
	switch key.String() {
	case "j", "down":
		if a.cursor < len(a.sessions)-1 {
			a.cursor++
		}
	case "k", "up":
		if a.cursor > 0 {
			a.cursor--
		}
	case "g", "home":
		a.cursor = 0
	case "G", "end":
		if len(a.sessions) > 0 {
			a.cursor = len(a.sessions) - 1
		}
	case "enter":
		if len(a.sessions) > 0 {
			a.detail = true
			a.detailRow = 0
		}
	case "r":
		a.reload()
	case "a":
		// Toggle SessionStart-stub visibility. Off by default — stubs
		// are noise unless you're debugging why an agent never
		// completed register.
		a.showAll = !a.showAll
		a.cursor = 0
		a.scroll = 0
		a.reload()
	}
	return nil
}

func (a *agentsView) View(width, height int) string {
	if width == 0 || height == 0 {
		return ""
	}
	if a.detail && a.cursor < len(a.sessions) {
		return a.viewDetail(width, height)
	}

	titleBar := lipgloss.NewStyle().
		Bold(true).Foreground(lipgloss.Color("231")).Background(colHeaderFocus).
		Width(width).Padding(0, 1).
		Render(fmt.Sprintf("Agents · %d session(s) · %d dispatch(es)",
			len(a.sessions), len(a.dispatches)))

	bodyRows := height - 1
	if bodyRows < 1 {
		bodyRows = 1
	}
	if a.err != nil {
		return lipgloss.JoinVertical(lipgloss.Left, titleBar,
			errorStyle.Render(a.err.Error()))
	}
	if len(a.sessions) == 0 {
		return lipgloss.JoinVertical(lipgloss.Left, titleBar,
			mutedStyle.Padding(1, 1).Render("(no agent sessions for this repo)"))
	}

	now := time.Now().UTC()
	perView := max(1, bodyRows/agentCardHeight)
	a.windowCursor(perView)
	end := min(a.scroll+perView, len(a.sessions))

	var cards []string
	for i := a.scroll; i < end; i++ {
		cards = append(cards, a.renderCard(a.sessions[i], i == a.cursor, width, now))
	}
	body := strings.Join(cards, "\n")
	body = clipLines(body, bodyRows)
	return lipgloss.JoinVertical(lipgloss.Left, titleBar, body)
}

// windowCursor nudges a.scroll so the selected card stays within the
// visible window [scroll, scroll+perView).
func (a *agentsView) windowCursor(perView int) {
	if a.cursor < a.scroll {
		a.scroll = a.cursor
	}
	if a.cursor >= a.scroll+perView {
		a.scroll = a.cursor - perView + 1
	}
	maxScroll := len(a.sessions) - perView
	if maxScroll < 0 {
		maxScroll = 0
	}
	if a.scroll > maxScroll {
		a.scroll = maxScroll
	}
	if a.scroll < 0 {
		a.scroll = 0
	}
}

// renderCard draws one agent session as a fixed-height bordered card.
func (a *agentsView) renderCard(s *model.AgentSession, selected bool, width int, now time.Time) string {
	innerW := width - 4 // border (2) + horizontal padding (2)
	if innerW < 10 {
		innerW = 10
	}

	statusLabel, pill := agentStatusPill(s, now)
	pillStr := pill.Render(statusLabel)
	// A session holding an open claim is busy — show a "busy · BACI-12"
	// badge alongside the liveness pill. Busy is orthogonal to liveness.
	var busyStr string
	if isBusy, issueKey := model.SessionBusy(a.claims[s.ID]); isBusy {
		busyStr = agentBusyBadge.Render("busy · "+issueKey) + " "
	}
	rightW := lipgloss.Width(busyStr) + lipgloss.Width(pillStr)
	nameW := innerW - rightW - 1
	if nameW < 4 {
		nameW = 4
	}
	name := boldStyle.Render(truncate(agentLabel(s), nameW))
	gap := innerW - lipgloss.Width(name) - rightW
	if gap < 1 {
		gap = 1
	}
	row1 := name + strings.Repeat(" ", gap) + busyStr + pillStr

	row2 := mutedStyle.Render(truncate(
		fmt.Sprintf("model %s · branch %s", dashIfEmpty(s.Model), dashIfEmpty(s.Branch)), innerW))
	row3 := mutedStyle.Render(truncate(
		fmt.Sprintf("last seen %s ago · actor %s", relAgo(now.Sub(s.LastSeenAt.UTC())), s.Actor), innerW))
	row4 := mutedStyle.Render(truncate(
		fmt.Sprintf("%d open claim(s) · %d pending dispatch(es)",
			len(a.claims[s.ID]), a.pending[s.ID]), innerW))

	inner := strings.Join([]string{row1, row2, row3, row4}, "\n")

	border := colBorderColor
	if selected {
		border = colFocusBorder
	}
	return lipgloss.NewStyle().
		Border(colBorder).BorderForeground(border).
		Width(width-2).Padding(0, 1).
		Render(inner)
}

// viewDetail renders the drill-down for the selected agent: its open
// claims and the dispatches aimed at it.
func (a *agentsView) viewDetail(width, height int) string {
	s := a.sessions[a.cursor]
	now := time.Now().UTC()
	innerW := width - 6 // border (2) + padding (4)
	if innerW < 10 {
		innerW = 10
	}

	statusLabel, pill := agentStatusPill(s, now)
	var lines []string
	header := boldStyle.Render(truncate(agentLabel(s), innerW)) + " " + pill.Render(statusLabel)
	if isBusy, issueKey := model.SessionBusy(a.claims[s.ID]); isBusy {
		header += " " + agentBusyBadge.Render("busy · "+issueKey)
	}
	lines = append(lines, header)
	lines = append(lines, mutedStyle.Render(truncate("session "+s.SessionID, innerW)))
	lines = append(lines, mutedStyle.Render(truncate(fmt.Sprintf(
		"model %s · branch %s · actor %s", dashIfEmpty(s.Model), dashIfEmpty(s.Branch), s.Actor), innerW)))
	bv := dashIfEmpty(s.ChannelVersion)
	current := version.String()
	if s.ChannelVersion != "" && current != "dev" && s.ChannelVersion != current {
		bv = s.ChannelVersion + " (stale, running binary is " + current + ")"
	}
	lines = append(lines, mutedStyle.Render(truncate("bacio version "+bv, innerW)))
	lines = append(lines, mutedStyle.Render(truncate(fmt.Sprintf(
		"started %s ago · last seen %s ago",
		relAgo(now.Sub(s.StartedAt.UTC())), relAgo(now.Sub(s.LastSeenAt.UTC()))), innerW)))

	claims := a.claims[s.ID]
	lines = append(lines, "", boldStyle.Render(fmt.Sprintf("Open claims (%d)", len(claims))))
	if len(claims) == 0 {
		lines = append(lines, mutedStyle.Render("  (none)"))
	}
	for _, c := range claims {
		line := "  " + c.IssueKey
		if c.Prompt != "" {
			line += " — " + oneLine(c.Prompt)
		}
		lines = append(lines, truncate(line, innerW))
	}

	var ds []*model.AgentDispatch
	for _, d := range a.dispatches {
		if dispatchTargetsSession(d, s) {
			ds = append(ds, d)
		}
	}
	lines = append(lines, "", boldStyle.Render(fmt.Sprintf("Dispatches (%d)", len(ds))))
	if len(ds) == 0 {
		lines = append(lines, mutedStyle.Render("  (none)"))
	}
	for _, d := range ds {
		mode := string(d.Mode)
		if mode == "" {
			mode = "-"
		}
		issue := d.IssueKey
		if issue == "" {
			issue = "-"
		}
		line := truncate(fmt.Sprintf("  #%-4d %-10s %-10s %-12s %s",
			d.ID, d.Status, mode, issue, oneLine(d.Payload)), innerW)
		if d.Status == model.DispatchAcked || d.Status == model.DispatchCancelled {
			line = mutedStyle.Render(line)
		}
		lines = append(lines, line)
	}

	body := scrollLines(strings.Join(lines, "\n"), a.detailRow, height-4)
	return lipgloss.NewStyle().
		Border(colBorder).BorderForeground(colFocusBorder).
		Width(width-2).Height(height-2).Padding(1, 2).
		Render(body)
}

// agentBusyBadge styles the "busy · ISSUE-KEY" badge shown on agent
// cards when a session is holding an open claim.
var agentBusyBadge = lipgloss.NewStyle().
	Background(lipgloss.Color("33")).Foreground(lipgloss.Color("231")).Padding(0, 1)

// agentStatusPill maps a session's liveness to a label + pill style.
func agentStatusPill(s *model.AgentSession, now time.Time) (string, lipgloss.Style) {
	switch model.SessionLiveness(s, now) {
	case "active":
		return "active", lipgloss.NewStyle().
			Background(lipgloss.Color("76")).Foreground(lipgloss.Color("231")).Padding(0, 1)
	case "idle":
		return "idle", lipgloss.NewStyle().
			Background(lipgloss.Color("220")).Foreground(lipgloss.Color("232")).Padding(0, 1)
	default:
		label := "ended"
		if s != nil && s.EndReason != "" {
			label = "ended:" + s.EndReason
		}
		return label, lipgloss.NewStyle().
			Background(colBorderColor).Foreground(lipgloss.Color("231")).Padding(0, 1)
	}
}

// agentLabel is the human label for a session: its agent identity slug
// when it has one, else a short form of the raw session id.
func agentLabel(s *model.AgentSession) string {
	if s.AgentName != "" {
		return s.AgentName
	}
	return truncate(s.SessionID, 12)
}

// relAgo renders a compact "time since" for the last-seen column.
func relAgo(d time.Duration) string {
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%ds", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh", int(d.Hours()))
	default:
		return fmt.Sprintf("%dd", int(d.Hours()/24))
	}
}

// dashIfEmpty renders "-" for an empty optional field.
func dashIfEmpty(s string) string {
	if s == "" {
		return "-"
	}
	return s
}
