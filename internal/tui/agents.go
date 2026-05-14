package tui

import (
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/mrgeoffrich/bacio/internal/model"
	"github.com/mrgeoffrich/bacio/internal/store"
)

// agentsView is the supervision panel: which agent sessions are live
// against this repo, what each one has claimed, and the repo's
// dispatch queue. Read-only — it's a window onto the local agent
// registry, refreshed with `r`.
type agentsView struct {
	store *store.Store
	repo  *model.Repo

	sessions   []*model.AgentSession
	claims     map[int64][]*model.AgentClaim // session PK -> open claims
	dispatches []*model.AgentDispatch

	scroll int
	err    error
}

func newAgentsView(s *store.Store, repo *model.Repo) *agentsView {
	a := &agentsView{store: s, repo: repo, claims: map[int64][]*model.AgentClaim{}}
	a.reload()
	return a
}

func (a *agentsView) reload() {
	a.claims = map[int64][]*model.AgentClaim{}
	sessions, err := a.store.ListAgentSessions(store.AgentSessionFilter{RepoID: &a.repo.ID})
	if err != nil {
		a.err = err
		return
	}
	for _, s := range sessions {
		cl, err := a.store.ListAgentClaims(s.ID)
		if err != nil {
			a.err = err
			return
		}
		var open []*model.AgentClaim
		for _, c := range cl {
			if c.ReleasedAt == nil {
				open = append(open, c)
			}
		}
		a.claims[s.ID] = open
	}
	dispatches, err := a.store.ListDispatches(store.DispatchFilter{RepoID: &a.repo.ID})
	if err != nil {
		a.err = err
		return
	}
	a.err = nil
	a.sessions = sessions
	a.dispatches = dispatches
}

func (a *agentsView) Init() tea.Cmd      { return nil }
func (a *agentsView) Status() string     { return "" }
func (a *agentsView) HasOverlay() bool   { return false }
func (a *agentsView) CloseOverlay()      {}
func (a *agentsView) Breadcrumb() string { return "" }

func (a *agentsView) Help() string {
	return "j/k scroll · g/G top/bottom · r reload · q quit"
}

func (a *agentsView) Update(msg tea.Msg) tea.Cmd {
	key, ok := msg.(tea.KeyMsg)
	if !ok {
		return nil
	}
	switch key.String() {
	case "j", "down":
		a.scroll++
	case "k", "up":
		if a.scroll > 0 {
			a.scroll--
		}
	case "g", "home":
		a.scroll = 0
	case "G", "end":
		a.scroll = 1 << 30 // clamped against content length in View
	case "pgdown", " ":
		a.scroll += 10
	case "pgup":
		a.scroll -= 10
		if a.scroll < 0 {
			a.scroll = 0
		}
	case "r":
		a.reload()
	}
	return nil
}

func (a *agentsView) View(width, height int) string {
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

	if a.err != nil {
		return box.Render(errorStyle.Render(a.err.Error()))
	}

	titleBar := lipgloss.NewStyle().
		Bold(true).Foreground(lipgloss.Color("231")).Background(colHeaderFocus).
		Width(innerWidth).Padding(0, 1).
		Render(fmt.Sprintf("Agents · %d session(s) · %d dispatch(es)",
			len(a.sessions), len(a.dispatches)))

	lines := a.buildLines(innerWidth)
	rowsHeight := innerHeight - 1 // title bar
	if rowsHeight < 1 {
		rowsHeight = 1
	}
	maxScroll := len(lines) - rowsHeight
	if maxScroll < 0 {
		maxScroll = 0
	}
	if a.scroll > maxScroll {
		a.scroll = maxScroll
	}
	end := min(a.scroll+rowsHeight, len(lines))
	body := strings.Join(lines[a.scroll:end], "\n")

	return box.Render(lipgloss.JoinVertical(lipgloss.Left, titleBar, body))
}

// buildLines flattens the registry into a scrollable line buffer:
// a Sessions section (each session followed by its open claims) and a
// Dispatches section.
func (a *agentsView) buildLines(width int) []string {
	now := time.Now().UTC()
	var lines []string

	lines = append(lines, boldStyle.Render("Sessions"))
	if len(a.sessions) == 0 {
		lines = append(lines, mutedStyle.Render("  (no agent sessions)"))
	}
	for _, s := range a.sessions {
		status := "alive"
		if s.EndedAt != nil {
			status = "ended:" + s.EndReason
		}
		who := s.AgentName
		if who == "" {
			who = truncate(s.SessionID, 12)
		}
		// Truncate the plain line first, then style — styling before
		// truncation would let the cut land mid-ANSI-escape.
		line := truncate(fmt.Sprintf("  %-22s %-20s %-14s %-8s %s",
			truncate(who, 22),
			truncate(dashIfEmpty(s.Model), 20),
			truncate(dashIfEmpty(s.Branch), 14),
			relAgo(now.Sub(s.LastSeenAt.UTC())),
			status,
		), width)
		if s.EndedAt != nil {
			line = mutedStyle.Render(line)
		}
		lines = append(lines, line)
		for _, c := range a.claims[s.ID] {
			lines = append(lines, mutedStyle.Render(truncate("    claim: "+c.IssueKey, width)))
		}
	}

	lines = append(lines, "")
	lines = append(lines, boldStyle.Render("Dispatches"))
	if len(a.dispatches) == 0 {
		lines = append(lines, mutedStyle.Render("  (no dispatches)"))
	}
	for _, d := range a.dispatches {
		target := d.TargetAgentName
		if target == "" {
			target = truncate(d.TargetSessionID, 12)
		}
		issue := d.IssueKey
		if issue == "" {
			issue = "-"
		}
		line := truncate(fmt.Sprintf("  #%-4d %-10s ->%-20s %-12s %s",
			d.ID, d.Status, truncate(target, 20), issue, oneLine(d.Payload)), width)
		if d.Status == model.DispatchAcked || d.Status == model.DispatchCancelled {
			line = mutedStyle.Render(line)
		}
		lines = append(lines, line)
	}
	return lines
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
