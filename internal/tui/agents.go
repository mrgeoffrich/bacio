package tui

import (
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/mrgeoffrich/bacio/internal/client"
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
	store  *store.Store
	repo   *model.Repo
	client client.Client

	sessions    []*model.AgentSession
	claims      map[int64][]*model.AgentClaim // session PK -> open claims
	dispatches  []*model.AgentDispatch        // every dispatch in the repo
	pending     map[int64]int                 // session PK -> open dispatch count
	needsAction map[string]bool               // issue key set in state needs_action
	todos       map[int64][]model.SessionTodo // session PK -> latest TodoWrite snapshot
	// BACI-53: open ask_user_question rows per session. Surfaced as a
	// `?N` badge on the card so the user knows an agent is waiting on
	// them, and (BACI-53 follow-up) answerable via the in-TUI
	// questionOverlay reached by `?` on the focused card.
	questions map[int64][]*model.SessionQuestion // session PK -> open questions

	cursor    int              // selected card
	scroll    int              // index of the first visible card
	detail    bool             // drill-down overlay open for sessions[cursor]
	detailRow int              // scroll offset within the detail overlay
	showAll   bool             // when false (default), hide SessionStart stubs that never registered
	overlay   *questionOverlay // BACI-53 answer modal for the focused session's first open question
	err       error
}

func newAgentsView(s *store.Store, repo *model.Repo, actor string) *agentsView {
	c := client.NewLocalFromStore(s, actor)
	a := &agentsView{
		store:  s,
		repo:   repo,
		client: c,
		claims: map[int64][]*model.AgentClaim{},
		overlay: newQuestionOverlay(c),
	}
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
	// Issue keys currently in state needs_action — the Stop hook
	// auto-flips a claimed in_progress issue here on idle, so an open
	// claim landing in this set is the "agent parked, waiting on the
	// user" signal driving the waiting badge on each card.
	needs, err := a.store.ListIssues(store.IssueFilter{
		RepoID: &a.repo.ID,
		States: []model.State{model.StateNeedsAction},
	})
	if err != nil {
		a.err = err
		return
	}
	a.needsAction = make(map[string]bool, len(needs))
	for _, iss := range needs {
		a.needsAction[iss.Key] = true
	}
	// Bulk-read each live session's TodoWrite mirror in one query,
	// scoped to the (session, newest-open-claim-issue, active-dispatch)
	// triple so a session that has worked multiple dispatches only
	// flows the active dispatch's rows onto its card (BACI-62 + BACI-132).
	// Same one-trip pattern the claims map uses, so the card row +
	// detail pane render n/m progress without N+1 lookups.
	sessionIDs := make([]string, 0, len(sessions))
	pairs := make([]store.SessionIssuePair, 0, len(sessions))
	for _, s := range sessions {
		sessionIDs = append(sessionIDs, s.SessionID)
		_, issueKey := model.SessionBusy(a.claims[s.ID])
		dispatchID := pickActiveDispatchID(dispatches, s, issueKey)
		if dispatchID == nil {
			// No in-flight dispatch — skip the pair so the card
			// shows no tasks. Matches the kanban + Agents-view
			// surfaces and what the activity pill does today
			// when pickActiveMode returns "".
			continue
		}
		pairs = append(pairs, store.SessionIssuePair{
			SessionID:  s.SessionID,
			IssueKey:   issueKey,
			DispatchID: dispatchID,
		})
	}
	a.todos, err = a.store.ListTodosBySessionsAndIssue(pairs)
	if err != nil {
		a.err = err
		return
	}
	// BACI-53: bulk-read open ask_user_question rows. Same one-trip
	// pattern as todos — used to render the per-card "?N" badge.
	a.questions, err = a.store.ListOpenQuestionsBySessions(sessionIDs)
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

// pickActiveDispatchID returns the id of the newest non-cancelled
// dispatch targeting (session, issue) — BACI-132's per-dispatch
// scope for the kanban Tasks pill and the agent card's todo list.
// Mirrors boardcards.pickActiveDispatchID and the one in
// internal/agentcards/cards.go; kept duplicated rather than hoisted
// to avoid an import-cycle into internal/boardcards (same reasoning
// the existing dispatchTargetsSession copies use).
func pickActiveDispatchID(dispatches []*model.AgentDispatch, s *model.AgentSession, issueKey string) *int64 {
	if s == nil || issueKey == "" {
		return nil
	}
	var best *model.AgentDispatch
	for _, d := range dispatches {
		if d == nil || d.IssueKey != issueKey {
			continue
		}
		if d.Status == model.DispatchCancelled {
			continue
		}
		if !dispatchTargetsSession(d, s) {
			continue
		}
		if best == nil || d.CreatedAt.After(best.CreatedAt) {
			best = d
		}
	}
	if best == nil {
		return nil
	}
	id := best.ID
	return &id
}

func (a *agentsView) Init() tea.Cmd  { return nil }
func (a *agentsView) Status() string { return "" }

func (a *agentsView) HasOverlay() bool    { return a.detail || (a.overlay != nil && a.overlay.open) }
func (a *agentsView) CloseOverlay()       { a.detail = false; if a.overlay != nil { a.overlay.close() } }
func (a *agentsView) CapturesInput() bool { return false }

func (a *agentsView) Breadcrumb() string {
	if !a.detail || a.cursor >= len(a.sessions) {
		return ""
	}
	return agentLabel(a.sessions[a.cursor])
}

func (a *agentsView) Help() string {
	if a.overlay != nil && a.overlay.open {
		return "j/k move · space toggle · tab next q · enter submit · d dismiss · esc close"
	}
	if a.detail {
		return "j/k scroll · esc back · r reload · q quit"
	}
	suffix := "a show stubs"
	if a.showAll {
		suffix = "a hide stubs"
	}
	return "j/k move · enter detail · ? answer · g/G top/bottom · r reload · " + suffix + " · q quit"
}

func (a *agentsView) Update(msg tea.Msg) tea.Cmd {
	// BACI-53 question-overlay results land here as a typed msg the
	// overlay returned from its submit/cancel Cmd. Apply, then refresh
	// the agent cards so the `?N` badge clears.
	if rmsg, ok := msg.(questionResultMsg); ok {
		if a.overlay != nil {
			a.overlay.handleResult(rmsg)
			if !a.overlay.open {
				a.reload()
			}
		}
		return nil
	}
	key, ok := msg.(tea.KeyMsg)
	if !ok {
		return nil
	}
	if a.overlay != nil && a.overlay.open {
		return a.overlay.update(key)
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
	case "?":
		// BACI-53: open the answer overlay for the focused card's
		// first open question. No-op when the card has none.
		if a.cursor < len(a.sessions) && a.overlay != nil {
			sess := a.sessions[a.cursor]
			qs := a.questions[sess.ID]
			if len(qs) > 0 {
				a.overlay.reset(qs[0])
			}
		}
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
	// BACI-53: when the answer overlay is open, render it on top of
	// whatever underlying view is current (cards or detail). Mirrors
	// the picker overlays in board.go.
	if a.overlay != nil && a.overlay.open {
		return a.overlay.view(width, height)
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
	// A session holding an open claim on a needs_action issue is parked
	// — render "waiting · BACI-12" in amber instead of the blue busy
	// pill, because waiting is the actionable state and showing both
	// for the same issue is noise. Otherwise fall back to "busy" when
	// the session just holds an open claim.
	var badgeStr string
	if isWaiting, waitKey := model.SessionWaiting(a.claims[s.ID], a.needsAction); isWaiting {
		badgeStr = agentWaitingBadge.Render("waiting · "+waitKey) + " "
	} else if isBusy, issueKey := model.SessionBusy(a.claims[s.ID]); isBusy {
		badgeStr = agentBusyBadge.Render("busy · "+issueKey) + " "
	}
	rightW := lipgloss.Width(badgeStr) + lipgloss.Width(pillStr)
	nameW := innerW - rightW - 1
	if nameW < 4 {
		nameW = 4
	}
	name := boldStyle.Render(truncate(agentLabel(s), nameW))
	gap := innerW - lipgloss.Width(name) - rightW
	if gap < 1 {
		gap = 1
	}
	row1 := name + strings.Repeat(" ", gap) + badgeStr + pillStr

	row2 := mutedStyle.Render(truncate(
		fmt.Sprintf("model %s · branch %s", dashIfEmpty(s.Model), dashIfEmpty(s.Branch)), innerW))
	row3 := mutedStyle.Render(truncate(
		fmt.Sprintf("last seen %s ago · actor %s", relAgo(now.Sub(s.LastSeenAt.UTC())), s.Actor), innerW))
	// row4 widens to carry the todos progress badge inline alongside
	// the claims + dispatches counts. The whole row is truncated to
	// innerW, so a narrow window drops the rightmost field cleanly
	// without breaking the fixed 4-row card budget.
	done, total := todoProgress(a.todos[s.ID])
	row4Text := fmt.Sprintf("%d open claim(s) · %d pending dispatch(es)",
		len(a.claims[s.ID]), a.pending[s.ID])
	if total > 0 {
		row4Text += fmt.Sprintf(" · todos %d/%d", done, total)
	}
	if n := len(a.questions[s.ID]); n > 0 {
		// BACI-53: pending ask_user_question rows. Inline tag in row4
		// for now; the answer overlay is a follow-up.
		row4Text += fmt.Sprintf(" · ?%d", n)
	}
	row4 := mutedStyle.Render(truncate(row4Text, innerW))

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
	if isWaiting, waitKey := model.SessionWaiting(a.claims[s.ID], a.needsAction); isWaiting {
		header += " " + agentWaitingBadge.Render("waiting · "+waitKey)
	} else if isBusy, issueKey := model.SessionBusy(a.claims[s.ID]); isBusy {
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
		if a.needsAction[c.IssueKey] {
			line += " (needs action)"
		}
		if c.Prompt != "" {
			line += " — " + oneLine(c.Prompt)
		}
		lines = append(lines, truncate(line, innerW))
	}

	// Todos section sits between claims and dispatches — same vocabulary
	// (Bold header + indent + glyph), reuses scrollLines for overflow.
	todos := a.todos[s.ID]
	tdDone, tdTotal := todoProgress(todos)
	lines = append(lines, "", boldStyle.Render(fmt.Sprintf("Todos (%d/%d)", tdDone, tdTotal)))
	if len(todos) == 0 {
		lines = append(lines, mutedStyle.Render("  (none)"))
	}
	for _, td := range todos {
		glyph, style := todoGlyphAndStyle(td.Status)
		line := truncate("  "+glyph+" "+td.Content, innerW)
		if style != nil {
			line = style.Render(line)
		}
		lines = append(lines, line)
	}

	// BACI-53: list open ask_user_question rows so the user can see
	// what they're being asked. Answering happens via the desktop /
	// web modal or `bacio agent questions answer --json '...'`
	// (full TUI answer overlay is a follow-up).
	qs := a.questions[s.ID]
	if len(qs) > 0 {
		lines = append(lines, "", boldStyle.Render(fmt.Sprintf("User input needed (%d)", len(qs))))
		for _, q := range qs {
			header := q.RequestUUID
			text := "(payload unavailable)"
			if len(q.Payload.Questions) > 0 {
				header = q.Payload.Questions[0].Header
				text = q.Payload.Questions[0].Question
			}
			line := truncate(fmt.Sprintf("  #%d %s — %s", q.ID, header, oneLine(text)), innerW)
			lines = append(lines, line)
		}
		lines = append(lines, mutedStyle.Render(truncate(
			"  Answer via the desktop/web modal or `bacio agent questions answer --json '...'`", innerW)))
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

// todoProgress counts the completed-vs-total todos for the n/m
// progress badge. Empty/nil snapshot returns (0, 0) — the caller hides
// the badge in that case.
func todoProgress(todos []model.SessionTodo) (done, total int) {
	for _, t := range todos {
		if t.Status == model.TodoCompleted {
			done++
		}
	}
	return done, len(todos)
}

// todoActiveStyle emphasises the in-progress row in the per-agent
// drill-down — bold, same colour as the rest of the body. Completed
// rows render muted (re-uses mutedStyle). Pending rows use the
// default body style.
var todoActiveStyle = lipgloss.NewStyle().Bold(true)

// todoGlyphAndStyle returns the per-status glyph and (optionally) a
// lipgloss style to apply to the whole row. nil style = render with
// the surrounding body style.
func todoGlyphAndStyle(st model.TodoStatus) (string, *lipgloss.Style) {
	switch st {
	case model.TodoCompleted:
		return "●", &mutedStyle
	case model.TodoInProgress:
		return "◐", &todoActiveStyle
	default:
		return "○", nil
	}
}

// agentBusyBadge styles the "busy · ISSUE-KEY" badge shown on agent
// cards when a session is holding an open claim.
var agentBusyBadge = lipgloss.NewStyle().
	Background(lipgloss.Color("33")).Foreground(lipgloss.Color("231")).Padding(0, 1)

// agentWaitingBadge styles the "waiting · ISSUE-KEY" badge shown when
// the session holds an open claim on a needs_action issue (auto-flipped
// by the Stop hook on idle). Amber matches the needs_action column on
// the kanban board so the badge and the column read as the same thing.
var agentWaitingBadge = lipgloss.NewStyle().
	Background(lipgloss.Color("220")).Foreground(lipgloss.Color("232")).Padding(0, 1)

// agentStatusPill maps a session's liveness to a label + pill style.
func agentStatusPill(s *model.AgentSession, now time.Time) (string, lipgloss.Style) {
	switch model.SessionLiveness(s, now) {
	case "active":
		return "active", lipgloss.NewStyle().
			Background(lipgloss.Color("76")).Foreground(lipgloss.Color("231")).Padding(0, 1)
	case "idle":
		return "idle", lipgloss.NewStyle().
			Background(lipgloss.Color("220")).Foreground(lipgloss.Color("232")).Padding(0, 1)
	case "errored":
		// Red — the session took an Anthropic API failure (BACI-296).
		// Surface the error class on the pill so the failure mode is
		// readable at a glance.
		label := "errored"
		if s != nil && s.ErrorType != "" {
			label = "errored:" + s.ErrorType
		}
		return label, lipgloss.NewStyle().
			Background(lipgloss.Color("196")).Foreground(lipgloss.Color("231")).Padding(0, 1)
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
