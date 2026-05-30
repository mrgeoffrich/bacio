package tui

import (
	"fmt"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/mrgeoffrich/bacio/internal/boardcards"
	"github.com/mrgeoffrich/bacio/internal/client"
	"github.com/mrgeoffrich/bacio/internal/model"
	"github.com/mrgeoffrich/bacio/internal/store"
)

// boardRefreshInterval is how often the board reloads issues from the
// store while the TUI is open. Short enough to feel live, long enough
// not to thrash SQLite.
const boardRefreshInterval = 10 * time.Second

// overlayPane identifies which sub-pane of the fullscreen card overlay
// currently has the focus, so j/k and enter route to the right place
// and the matching header gets the accent treatment.
type overlayPane int

const (
	paneDescription overlayPane = iota
	paneComments
	paneAttachments
)

// boardRefreshMsg is delivered by tea.Tick to trigger a reload.
type boardRefreshMsg time.Time

func boardRefreshTick() tea.Cmd {
	return tea.Tick(boardRefreshInterval, func(t time.Time) tea.Msg {
		return boardRefreshMsg(t)
	})
}

// spinnerInterval is how fast the waiting-for-claim spinner advances a
// frame. Fast enough to read as motion, slow enough not to thrash.
const spinnerInterval = 120 * time.Millisecond

// spinnerFrames is the braille spinner cycled on a waiting-for-claim
// card's key marker.
var spinnerFrames = []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}

// spinnerTickMsg advances the waiting-for-claim spinner. The tick-chain
// only re-arms while at least one visible issue is waiting, so the
// animation costs nothing when nothing is waiting.
type spinnerTickMsg time.Time

func spinnerTick() tea.Cmd {
	return tea.Tick(spinnerInterval, func(t time.Time) tea.Msg {
		return spinnerTickMsg(t)
	})
}

// maybeStartSpinnerCmd arms the spinner tick-chain when there is at
// least one waiting issue and a chain isn't already running. Returns nil
// otherwise. Safe to call after any action that may have surfaced a
// queued/pending/delivered dispatch (e.g. a TUI-initiated dispatch).
func (b *boardView) maybeStartSpinnerCmd() tea.Cmd {
	if len(b.waitingIssues) > 0 && !b.spinnerRunning {
		b.spinnerRunning = true
		return spinnerTick()
	}
	return nil
}

// boardView is the kanban tab: one column per state, optional bottom detail
// pane, and a fullscreen card overlay opened with `enter`.
type boardView struct {
	store         *store.Store
	repo          *model.Repo
	actor         string // history actor for board-initiated mutations
	states        []model.State
	hidden        map[model.State]bool
	columns       map[model.State][]*model.Issue
	col           int
	rows          map[model.State]int
	scroll        map[model.State]int // top visible card index per column
	detailVisible bool

	// takenIssues is the repo-wide set of issues with an open agent
	// claim, keyed by issue ID. Refreshed on every reload(). Taken
	// cards are marked in renderColumn and refuse the `x` dispatch.
	takenIssues map[int64]bool

	// waitingIssues is the repo-wide set of issues with an active
	// queued/pending/delivered dispatch (BACI-255: read straight off the
	// dispatch table, no more denormalised cache). Refreshed on every
	// reload(). Waiting cards show an animated spinner marker and
	// refuse the `x` dispatch, same guard as takenIssues. `taken` wins
	// over `waiting` when (defensively) a card is somehow both.
	waitingIssues map[int64]bool
	// waitingStateByIssue (BACI-145) is the per-issue WaitingState
	// projection — same derivation the React tree uses via
	// boardcards.DeriveWaitingState. Keyed by issue ID; entries exist
	// only for waiting cards. Drives the inline label on the bottom
	// line of a waiting card, in lockstep with the kanban surface.
	waitingStateByIssue map[int64]*boardcards.WaitingState
	// spinnerFrame is the current spinner animation frame; spinnerRunning
	// guards against stacking concurrent tick-chains.
	spinnerFrame   int
	spinnerRunning bool

	selected    *model.Issue
	comments    []*model.Comment
	commentsErr error
	docLinks    []*model.DocumentLink
	prs         []*model.PullRequest
	claimants   []*model.AgentClaim // per-issue claim history (open + released)
	attachErr   error

	overlay       bool
	overlayFocus  overlayPane // which sub-pane has focus inside the card overlay
	overlayScroll int         // scroll offset for the description (top pane)
	commentsRow   int         // line scroll offset within the comments pane
	attachRow     int         // cursor index in the attachments list

	// Comment detail overlay: a fullscreen view of every comment on
	// the focused issue, opened with `enter` while the comments pane
	// is focused. Lives inside b.overlay (you can only get here from
	// the card overlay), so HasOverlay continues to gate on b.overlay.
	commentOverlay       bool
	commentOverlayScroll int

	picker    bool
	pickerRow int

	// Feature filter picker — opened with `f`. hiddenFeatures is keyed
	// by slug; the sentinel store.HiddenFeaturesUnassigned represents
	// the "no feature" group. featurePickerSlugs holds the list shown
	// in the picker (snapshotted when the picker opens so the order
	// doesn't shift while the user is navigating).
	hiddenFeatures     map[string]bool
	featurePicker      bool
	featurePickerRow   int
	featurePickerSlugs []string

	lastRefresh time.Time

	// BACI-53: per-issue-key open ask_user_question rows, populated in
	// reload() from the same OpenClaimsBySession scan that drives the
	// `taken` map. Drives the `?N` glyph on the card meta line and
	// gates the `?` keybind that opens the answer overlay.
	openQuestions map[string][]*model.SessionQuestion
	questionOlay  *questionOverlay

	// BACI-168: "+ from prompt" composer overlay. Open via `N` from the
	// board (capital — lowercase `n` keeps its archive-decline duty);
	// submit (ctrl+s) creates an issue + queues a `scope` dispatch;
	// `esc` cancels with no writes. Nil when not open. See
	// board_compose.go for the type + Update / View bodies.
	composeOverlay *composeOverlay

	// BACI-68: when `a` is pressed on a non-terminal issue we don't
	// archive immediately — we set this confirm flag and surface a
	// y/n prompt in the help footer. A subsequent `y` archives; any
	// other key clears the flag without a write. The id field gates
	// the confirmation to the exact card that triggered it so a
	// scroll-and-confirm doesn't archive a different row by mistake.
	confirmArchive   bool
	confirmArchiveID int64

	mdCache mdCache // see internal/tui/markdown.go

	// commentMD caches glamour-rendered comment bodies keyed by
	// (commentID, width). Cleared whenever the selected issue changes
	// since the cache only ever serves the focused issue's comments.
	commentMD map[commentMDKey]string

	err error
}

type commentMDKey struct {
	id    int64
	width int
}

func (b *boardView) cachedMD(id int64, src string, width int) string {
	return b.mdCache.get(id, src, width)
}

func newBoardView(s *store.Store, repo *model.Repo, actor string) (*boardView, error) {
	hidden, err := s.LoadHiddenStates(repo.ID)
	if err != nil {
		return nil, err
	}
	hiddenFeats, err := s.LoadHiddenFeatures(repo.ID)
	if err != nil {
		return nil, err
	}
	b := &boardView{
		store:          s,
		repo:           repo,
		actor:          actor,
		states:         model.BoardColumnStates(),
		hidden:         hidden,
		hiddenFeatures: hiddenFeats,
		columns:        map[model.State][]*model.Issue{},
		rows:           map[model.State]int{},
		scroll:         map[model.State]int{},
		detailVisible:  true,
	}
	b.questionOlay = newQuestionOverlay(client.NewLocalFromStore(s, actor))
	if err := b.reload(); err != nil {
		return nil, err
	}
	b.lastRefresh = time.Now()
	b.clampCol()
	b.refreshSelection()
	return b, nil
}

// visibleStates returns the states whose columns should be drawn, preserving
// canonical state order. When everything is hidden the slice is empty and the
// caller renders a placeholder.
func (b *boardView) visibleStates() []model.State {
	out := make([]model.State, 0, len(b.states))
	for _, st := range b.states {
		if !b.hidden[st] {
			out = append(out, st)
		}
	}
	return out
}

// clampCol pulls b.col back into range whenever the visible set changes.
func (b *boardView) clampCol() {
	v := b.visibleStates()
	if len(v) == 0 {
		b.col = 0
		return
	}
	if b.col >= len(v) {
		b.col = len(v) - 1
	}
	if b.col < 0 {
		b.col = 0
	}
}

// carryRow copies the focused column's cursor row into the column at
// dst (an index into visible), clamped to that column's card count so
// horizontal navigation feels like moving across a grid rather than
// between independent lists. Empty target columns clamp to 0.
func (b *boardView) carryRow(visible []model.State, dst int) {
	if b.col < 0 || b.col >= len(visible) || dst < 0 || dst >= len(visible) {
		return
	}
	row := b.rows[visible[b.col]]
	if n := len(b.columns[visible[dst]]); row > n-1 {
		row = n - 1
	}
	if row < 0 {
		row = 0
	}
	b.rows[visible[dst]] = row
}

// persistHidden writes the current hidden set to disk. Errors are stored on
// the model so the user sees them in the footer instead of crashing the UI.
func (b *boardView) persistHidden() {
	if err := b.store.SaveHiddenStates(b.repo.ID, b.hidden); err != nil {
		b.err = err
	}
}

func (b *boardView) reload() error {
	// BACI-68: respect the global display.show_archived toggle so an
	// archived row only surfaces here when the user has explicitly
	// asked to see them. Default-off keeps the column the user knows.
	includeArchived, _ := b.store.GetDisplayShowArchived()
	issues, err := b.store.ListIssues(store.IssueFilter{
		RepoID:          &b.repo.ID,
		IncludeArchived: includeArchived,
	})
	if err != nil {
		return err
	}
	// Repo-wide set of issues with an open agent claim — one query, used
	// to mark taken cards and gate the `x` dispatch.
	openClaims, err := b.store.OpenClaimsBySession(b.repo.ID)
	if err != nil {
		return err
	}
	taken := map[int64]bool{}
	sessIDSet := map[string]bool{}
	for _, claims := range openClaims {
		for _, c := range claims {
			taken[c.IssueID] = true
			if c.SessionID != "" {
				sessIDSet[c.SessionID] = true
			}
		}
	}
	b.takenIssues = taken
	// BACI-53: bulk-read open ask_user_question rows for every
	// claiming session and bucket by issue_key so the card render +
	// `?` keybind can look up by the focused issue.
	b.openQuestions = map[string][]*model.SessionQuestion{}
	if len(sessIDSet) > 0 {
		sessIDList := make([]string, 0, len(sessIDSet))
		for id := range sessIDSet {
			sessIDList = append(sessIDList, id)
		}
		byPK, qerr := b.store.ListOpenQuestionsBySessions(sessIDList)
		if qerr == nil {
			for _, qs := range byPK {
				for _, q := range qs {
					if q.IssueKey == "" {
						continue
					}
					b.openQuestions[q.IssueKey] = append(b.openQuestions[q.IssueKey], q)
				}
			}
		}
	}
	// BACI-255: derive the per-repo "waiting" set straight off the
	// dispatch table — the same query the IssueLockBanner / kanban
	// React surfaces use. Pre-BACI-255 this consulted a denormalised
	// issues.waiting_for_claim column that could drift; reading the
	// dispatch rows directly is the single source of truth. A failed
	// ListDispatches degrades to "nothing is waiting" — same fallback
	// as before, no worse than a freshly-loaded board.
	waiting := map[int64]bool{}
	b.waitingStateByIssue = map[int64]*boardcards.WaitingState{}
	allDispatches, derr := b.store.ListDispatches(store.DispatchFilter{RepoID: &b.repo.ID})
	if derr == nil {
		activeByIssue := map[int64]*model.AgentDispatch{}
		for _, d := range allDispatches {
			if d == nil || d.IssueID == nil {
				continue
			}
			id := *d.IssueID
			if _, ok := activeByIssue[id]; ok {
				continue
			}
			// BACI-209 / BACI-217: skip dormant follow-ons — same
			// rule activeDispatchByIssueID applies in the React
			// pipeline. A queued row with a dormant gate is waiting
			// for the gate, not for the matcher; the chip is its
			// surface, not the spinner.
			if d.Status == model.DispatchQueued && (d.QueuedAfterDispatchID != nil || d.QueuedUntilBlockersClear) {
				continue
			}
			switch d.Status {
			case model.DispatchQueued, model.DispatchPending, model.DispatchDelivered:
				activeByIssue[id] = d
				waiting[id] = true
			}
		}
		// BACI-145: per-issue WaitingState — same derivation the
		// kanban uses, so the inline "why is this card waiting?"
		// label stays in lockstep with the React surface. BACI-227:
		// per-(mode, branch) in-flight grouping so the inline label
		// tracks the matcher's per-branch concurrency gate exactly.
		if len(activeByIssue) > 0 {
			inflight, ierr := b.store.InflightByModeBaseForRepo(b.repo.ID)
			if ierr != nil {
				inflight = map[store.InflightKey]int{}
			}
			templates, terr := b.store.ListPromptTemplates()
			if terr != nil {
				templates = nil
			}
			for _, iss := range issues {
				active := activeByIssue[iss.ID]
				if active == nil {
					continue
				}
				ws := boardcards.DeriveWaitingState(iss, active, inflight, templates)
				if ws != nil {
					b.waitingStateByIssue[iss.ID] = ws
				}
			}
		}
	}
	b.waitingIssues = waiting
	for _, st := range b.states {
		b.columns[st] = nil
	}
	for _, iss := range issues {
		if b.featureHidden(iss.FeatureSlug) {
			continue
		}
		b.columns[iss.State] = append(b.columns[iss.State], iss)
	}
	// BACI-101: Done and Cancelled render newest-completed first. Other
	// columns keep their creation order. Shares the comparator with
	// boardcards.Assemble so the TUI and desktop/web stay consistent.
	for _, st := range []model.State{model.StateDone, model.StateCancelled} {
		col := b.columns[st]
		sort.SliceStable(col, func(i, j int) bool {
			return boardcards.CompletionSortKey(col[i]).After(
				boardcards.CompletionSortKey(col[j]))
		})
	}
	// Issue descriptions may have changed under us — start the cache
	// fresh so stale renders aren't served until a new render replaces
	// them.
	b.mdCache.reset()
	return nil
}

// featureHidden reports whether the given feature slug is currently
// filtered out via the feature picker. The empty slug ("") maps to
// store.HiddenFeaturesUnassigned so "(unassigned)" can be toggled like
// any other group.
func (b *boardView) featureHidden(slug string) bool {
	key := slug
	if key == "" {
		key = store.HiddenFeaturesUnassigned
	}
	return b.hiddenFeatures[key]
}

// cancelWaitingOnSelection is the BACI-51 X-key handler: finds the
// selected card's active queued/pending/delivered dispatch and cancels
// it. A no-op on a card that isn't waiting (no spurious audit row), so
// hitting X on the wrong card just does nothing instead of erroring.
// Triggers an immediate reload so the spinner disappears on the next
// repaint without waiting for the next refresh tick.
func (b *boardView) cancelWaitingOnSelection() error {
	iss := b.currentIssue()
	if iss == nil || !b.waitingIssues[iss.ID] {
		return nil
	}
	dsp, err := b.store.WaitingDispatchForIssue(b.repo.ID, iss.ID)
	if err != nil {
		return err
	}
	if dsp == nil {
		// Race: the matcher bound it (or another UI cancelled) between
		// the spinner render and the key press. Nothing to do.
		return nil
	}
	// BACI-130: cancel-after-delivery is rejected at the store boundary.
	// Short-circuit here so the X key on a delivered card is a quiet
	// no-op instead of a TUI error toast — the card is still in flight,
	// the worker has the Task, and the right way to stop them is
	// interrupting the agent itself. Defence in depth: if this guard is
	// missed, the store still rejects.
	if dsp.Status == model.DispatchDelivered {
		return nil
	}
	if _, err := b.store.CancelDispatch(dsp.ID); err != nil {
		return err
	}
	recordTUIOp(b.store, model.HistoryEntry{
		RepoID: &b.repo.ID, RepoPrefix: b.repo.Prefix,
		Op: "agent.cancel", Kind: "agent", Actor: b.actor,
		TargetID: &dsp.ID, TargetLabel: iss.Key,
		Details: "issue=" + iss.Key + ",status=cancelled,via=tui-spinner",
	})
	if err := b.reload(); err != nil {
		return err
	}
	return nil
}

// focusOnKey repositions the cursor so the issue with the given key
// becomes the selected card. Returns an error if the key is non-empty
// but doesn't match any visible issue. A blank key is a no-op. Lifted
// out of the snapshot-only helper so production code (the compose
// overlay's post-submit cursor jump) can share a single implementation.
func (b *boardView) focusOnKey(key string) error {
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

func (b *boardView) currentIssue() *model.Issue {
	v := b.visibleStates()
	if b.col < 0 || b.col >= len(v) {
		return nil
	}
	st := v[b.col]
	issues := b.columns[st]
	if len(issues) == 0 {
		return nil
	}
	r := b.rows[st]
	if r >= len(issues) {
		r = len(issues) - 1
	}
	if r < 0 {
		r = 0
	}
	return issues[r]
}

// refreshSelection re-fetches comments and attachments only when the
// selected issue changes — keeps navigation snappy without a separate
// cache.
func (b *boardView) refreshSelection() {
	iss := b.currentIssue()
	if iss == nil {
		b.selected = nil
		b.comments = nil
		b.commentsErr = nil
		b.docLinks = nil
		b.prs = nil
		b.claimants = nil
		b.attachErr = nil
		return
	}
	// ListIssues omits the heavy description column; re-fetch the full row
	// so the preview pane and overlay can render it.
	full, err := b.store.GetIssueByID(iss.ID)
	if err != nil {
		full = iss
	}
	if b.selected != nil && b.selected.ID == iss.ID {
		b.selected = full
		return
	}
	b.selected = full
	b.commentMD = nil
	b.commentsRow = 0
	b.commentOverlay = false
	b.commentOverlayScroll = 0
	cs, err := b.store.ListComments(iss.ID)
	b.comments = cs
	b.commentsErr = err
	docs, derr := b.store.ListDocumentsLinkedToIssue(iss.ID)
	b.docLinks = docs
	prs, perr := b.store.ListPRs(iss.ID)
	b.prs = prs
	claimants, cerr := b.store.ListClaimsForIssue(iss.ID)
	b.claimants = claimants
	switch {
	case derr != nil:
		b.attachErr = derr
	case perr != nil:
		b.attachErr = perr
	case cerr != nil:
		b.attachErr = cerr
	default:
		b.attachErr = nil
	}
	b.overlayScroll = 0
}

func (b *boardView) Init() tea.Cmd { return boardRefreshTick() }

func (b *boardView) Status() string {
	if b.err != nil {
		return b.err.Error()
	}
	if b.lastRefresh.IsZero() {
		return ""
	}
	return "↻ " + b.lastRefresh.Format("15:04:05")
}

func (b *boardView) HasOverlay() bool {
	return b.overlay || b.picker || b.featurePicker ||
		(b.questionOlay != nil && b.questionOlay.open) ||
		b.composeOverlay != nil
}

func (b *boardView) CloseOverlay() {
	b.overlay = false
	b.picker = false
	b.featurePicker = false
	b.commentOverlay = false
	if b.questionOlay != nil {
		b.questionOlay.close()
	}
	b.composeOverlay = nil
}

func (b *boardView) Breadcrumb() string {
	switch {
	case b.composeOverlay != nil:
		return "New issue"
	case b.commentOverlay && b.selected != nil:
		return "[" + b.selected.Key + "] → Comments"
	case b.overlay && b.selected != nil:
		return "[" + b.selected.Key + "]"
	case b.picker:
		return "Columns"
	case b.featurePicker:
		return "Features"
	}
	return ""
}

// CapturesInput returns true while the BACI-168 compose overlay is
// open so the shell yields `q` and the digit keys 1-9 to the textarea
// instead of treating them as quit / tab-switch. The other overlays
// (picker, etc.) are bounded captures handled in-view; only the
// composer is a free-text editor that needs the shell-level
// CapturesInput contract.
func (b *boardView) CapturesInput() bool { return b.composeOverlay != nil }

func (b *boardView) Help() string {
	switch {
	case b.composeOverlay != nil:
		return "type to describe · ctrl+s create · esc cancel"
	case b.questionOlay != nil && b.questionOlay.open:
		return "j/k move · space toggle · tab next q · enter submit · d dismiss · esc close"
	case b.picker:
		return "j/k move · space toggle · a all · n none · esc close"
	case b.featurePicker:
		return "j/k move · space toggle · a all · n none · esc close"
	case b.commentOverlay:
		return "j/k scroll · g/G top/bottom · esc back"
	case b.overlay:
		switch b.overlayFocus {
		case paneComments:
			return "tab next pane · j/k scroll · enter open all · esc close"
		case paneAttachments:
			return "tab next pane · j/k select · enter open · esc close"
		}
		return "tab next pane · j/k scroll · g/G top/bottom · esc close"
	}
	if b.confirmArchive && b.selected != nil && b.selected.ID == b.confirmArchiveID {
		return fmt.Sprintf("archive %s (%s)? y to confirm · n to cancel", b.selected.Key, b.selected.State)
	}
	return "h/l cols · j/k cards · enter open · N new · x send · X cancel · a archive · A unarchive · ? answer · c columns · f features · H hide col · d detail · r reload · q quit"
}

func (b *boardView) Update(msg tea.Msg) tea.Cmd {
	if t, ok := msg.(boardRefreshMsg); ok {
		// Periodic background reload. We pull fresh issues, refresh the
		// selected card's comments, and schedule the next tick. Errors
		// surface in the footer rather than crashing the loop.
		//
		// Skip the actual reload while an overlay is open — reloading
		// underneath it can shuffle the issue list and selection out
		// from under the user. The tick still re-arms unconditionally
		// so refreshes resume the moment the overlay closes.
		if !b.HasOverlay() {
			if err := b.reload(); err != nil {
				b.err = err
			} else {
				b.err = nil
			}
			b.lastRefresh = time.Time(t)
			b.clampCol()
			b.refreshSelection()
		}
		// Start the spinner tick-chain if a reload turned up waiting
		// cards and one isn't already running. The chain stops itself
		// (spinnerRunning = false) once nothing is waiting.
		if spin := b.maybeStartSpinnerCmd(); spin != nil {
			return tea.Batch(boardRefreshTick(), spin)
		}
		return boardRefreshTick()
	}
	if _, ok := msg.(spinnerTickMsg); ok {
		// Advance the spinner. Re-arm only while something is still
		// waiting; otherwise let the chain die so we don't tick forever.
		if len(b.waitingIssues) == 0 {
			b.spinnerRunning = false
			return nil
		}
		b.spinnerFrame = (b.spinnerFrame + 1) % len(spinnerFrames)
		return spinnerTick()
	}
	// BACI-53: question overlay submit/cancel results land here.
	if rmsg, ok := msg.(questionResultMsg); ok {
		if b.questionOlay != nil {
			b.questionOlay.handleResult(rmsg)
			if !b.questionOlay.open {
				if err := b.reload(); err != nil {
					b.err = err
				}
			}
		}
		return nil
	}
	key, ok := msg.(tea.KeyMsg)
	if !ok {
		return nil
	}
	if b.questionOlay != nil && b.questionOlay.open {
		return b.questionOlay.update(key)
	}
	if b.composeOverlay != nil {
		// BACI-168: while the "+ from prompt" composer is open, every
		// key is the composer's — ctrl+s submits, esc cancels, every
		// other key flows to the textarea (including q and digits,
		// gated at the shell via CapturesInput above).
		return b.composeOverlay.Update(b, key)
	}
	if b.picker {
		b.updatePicker(key)
		return nil
	}
	if b.featurePicker {
		b.updateFeaturePicker(key)
		return nil
	}
	if b.commentOverlay {
		b.updateCommentOverlay(key)
		return nil
	}
	if b.overlay {
		return b.updateOverlay(key)
	}
	visible := b.visibleStates()
	switch key.String() {
	case "h", "left":
		if b.col > 0 {
			b.carryRow(visible, b.col-1)
			b.col--
		}
	case "l", "right":
		if b.col < len(visible)-1 {
			b.carryRow(visible, b.col+1)
			b.col++
		}
	case "j", "down":
		if len(visible) == 0 {
			break
		}
		st := visible[b.col]
		if b.rows[st] < len(b.columns[st])-1 {
			b.rows[st]++
		}
	case "k", "up":
		if len(visible) == 0 {
			break
		}
		st := visible[b.col]
		if b.rows[st] > 0 {
			b.rows[st]--
		}
	case "g", "home":
		if len(visible) > 0 {
			b.rows[visible[b.col]] = 0
		}
	case "G", "end":
		if len(visible) == 0 {
			break
		}
		st := visible[b.col]
		if n := len(b.columns[st]); n > 0 {
			b.rows[st] = n - 1
		}
	case "r":
		if err := b.reload(); err != nil {
			b.err = err
		} else {
			b.err = nil
		}
		b.lastRefresh = time.Now()
	case "d":
		b.detailVisible = !b.detailVisible
	case "enter":
		if b.selected != nil {
			b.overlay = true
			b.overlayFocus = paneDescription
			b.overlayScroll = 0
			b.commentsRow = 0
			b.attachRow = 0
		}
	case "c":
		b.openPicker()
	case "f":
		b.openFeaturePicker()
	case "X":
		// BACI-51: cancel a queued / pending / delivered dispatch on
		// the selected waiting card. No-op on cards that aren't
		// waiting — the spinner is the affordance, capital X is the
		// keystroke.
		if err := b.cancelWaitingOnSelection(); err != nil {
			b.err = err
		}
	case "H":
		// Quick power-user hide of the focused column. We refuse to hide
		// the last visible column so the board never goes empty by accident.
		if len(visible) <= 1 {
			break
		}
		st := visible[b.col]
		b.hidden[st] = true
		b.persistHidden()
		b.clampCol()
	case "?":
		// BACI-53: open the answer overlay for the focused card's
		// first open ask_user_question row. No-op when the card's
		// claiming session hasn't asked anything.
		if b.selected != nil && b.questionOlay != nil {
			qs := b.openQuestions[b.selected.Key]
			if len(qs) > 0 {
				b.questionOlay.reset(qs[0])
			}
		}
	case "a":
		// BACI-68: archive the focused card. Already-archived rows
		// are a no-op (unarchive via `A`). Non-terminal issues (todo /
		// in_progress / in_review) arm the confirm prompt so a
		// stray keystroke can't silently hide active work.
		if b.selected != nil {
			b.tryArchive(b.selected)
		}
	case "y", "Y":
		if b.confirmArchive && b.selected != nil && b.selected.ID == b.confirmArchiveID {
			b.confirmArchive = false
			b.confirmArchiveID = 0
			b.doArchive(b.selected)
		}
	case "n":
		// Decline a pending archive confirm. Lowercase only — capital
		// `N` opens the BACI-168 compose overlay (below) so we don't
		// want it doing double duty here.
		if b.confirmArchive {
			b.confirmArchive = false
			b.confirmArchiveID = 0
		}
	case "N":
		// BACI-168: open the "+ from prompt" composer overlay. Gated on
		// no other overlay being open so a stray N mid-confirm /
		// mid-picker doesn't surprise the user with a half-armed prompt
		// underneath a fresh composer.
		if !b.confirmArchive && !b.HasOverlay() {
			return b.openComposeOverlay()
		}
	case "A":
		// BACI-68: unarchive the focused card. No-op when the card
		// isn't archived.
		if b.selected != nil && b.selected.ArchivedAt != nil {
			b.doUnarchive(b.selected)
		}
	}
	// Any other key cancels a pending archive confirm. `N` is no longer
	// on the don't-cancel list — capital N opens the BACI-168 composer
	// (handled by the case "N" arm above, which is gated on
	// !b.confirmArchive), so a user who hits `a` then `N` clearly
	// wants the confirm gone whether the composer opens or not.
	if b.confirmArchive {
		s := key.String()
		if s != "a" && s != "y" && s != "Y" && s != "n" {
			b.confirmArchive = false
			b.confirmArchiveID = 0
		}
	}
	b.refreshSelection()
	return nil
}

// tryArchive routes the `a` keystroke: already-archived → no-op,
// terminal state (done/cancelled) → archive immediately, non-terminal
// → arm the confirm prompt. The reviewer-asked-for guard so a stray
// `a` on an in_progress card doesn't silently hide active work.
func (b *boardView) tryArchive(iss *model.Issue) {
	if iss.ArchivedAt != nil {
		return
	}
	if iss.State == model.StateDone || iss.State == model.StateCancelled {
		b.doArchive(iss)
		return
	}
	b.confirmArchive = true
	b.confirmArchiveID = iss.ID
}

func (b *boardView) doArchive(iss *model.Issue) {
	if err := b.store.SetIssueArchived(iss.ID, true); err != nil {
		b.err = err
		return
	}
	b.recordArchiveOp(iss, "issue.archive")
	if err := b.reload(); err != nil {
		b.err = err
		return
	}
	b.err = nil
}

func (b *boardView) doUnarchive(iss *model.Issue) {
	if err := b.store.SetIssueArchived(iss.ID, false); err != nil {
		b.err = err
		return
	}
	b.recordArchiveOp(iss, "issue.unarchive")
	if err := b.reload(); err != nil {
		b.err = err
		return
	}
	b.err = nil
}

func (b *boardView) recordArchiveOp(iss *model.Issue, op string) {
	recordTUIOp(b.store, model.HistoryEntry{
		Actor:       b.actor,
		RepoID:      &iss.RepoID,
		RepoPrefix:  b.repo.Prefix,
		Op:          op,
		Kind:        "issue",
		TargetID:    &iss.ID,
		TargetLabel: iss.Key,
	})
}

// updateOverlay handles input while the fullscreen card overlay is up.
// Tab cycles focus through description / comments / attachments; j/k/g/G
// route to the focused pane only.
func (b *boardView) updateOverlay(key tea.KeyMsg) tea.Cmd {
	switch key.String() {
	case "esc":
		b.overlay = false
		return nil
	case "tab":
		b.overlayFocus = (b.overlayFocus + 1) % 3
		return nil
	case "shift+tab":
		b.overlayFocus = (b.overlayFocus + 2) % 3
		return nil
	}

	switch b.overlayFocus {
	case paneDescription:
		switch key.String() {
		case "enter":
			b.overlay = false
		case "j", "down":
			b.overlayScroll++
		case "k", "up":
			if b.overlayScroll > 0 {
				b.overlayScroll--
			}
		case "g", "home":
			b.overlayScroll = 0
		case "G", "end":
			b.overlayScroll = 1 << 30
		case "pgdown", " ":
			b.overlayScroll += 10
		case "pgup":
			b.overlayScroll -= 10
			if b.overlayScroll < 0 {
				b.overlayScroll = 0
			}
		}
	case paneComments:
		switch key.String() {
		case "enter":
			// Open every comment in a fullscreen scrollable view.
			if len(b.comments) > 0 {
				b.commentOverlay = true
				b.commentOverlayScroll = 0
			}
		case "j", "down":
			b.commentsRow++
		case "k", "up":
			if b.commentsRow > 0 {
				b.commentsRow--
			}
		case "g", "home":
			b.commentsRow = 0
		case "G", "end":
			b.commentsRow = 1 << 30
		case "pgdown", " ":
			b.commentsRow += 10
		case "pgup":
			b.commentsRow -= 10
			if b.commentsRow < 0 {
				b.commentsRow = 0
			}
		}
	case paneAttachments:
		total := len(b.docLinks) + len(b.prs)
		switch key.String() {
		case "enter":
			// Enter on an attachment is more useful than "close
			// overlay" — the user can still close via esc.
			return b.openSelectedAttachment()
		case "j", "down":
			if b.attachRow < total-1 {
				b.attachRow++
			}
		case "k", "up":
			if b.attachRow > 0 {
				b.attachRow--
			}
		case "g", "home":
			b.attachRow = 0
		case "G", "end":
			if total > 0 {
				b.attachRow = total - 1
			}
		}
	}
	return nil
}

// boardColWidths splits the board's total width across n columns,
// handing the focused column a modest extra slice so it reads as
// emphasised alongside the colFocusBorder highlight. When there isn't
// room for every column to clear the 16-col minimum the bonus is
// dropped and every column is clamped to 16 (the board overflows, same
// as before this nicety existed).
func boardColWidths(width, n, focus int) []int {
	widths := make([]int, n)
	base := width / n
	if base < 16 {
		for i := range widths {
			widths[i] = 16
		}
		return widths
	}
	// Start equal, then hand the width%n remainder to the first few
	// columns so the row tiles the full width with no gap.
	rem := width % n
	for i := range widths {
		widths[i] = base
		if i < rem {
			widths[i]++
		}
	}
	if n < 2 || focus < 0 || focus >= n {
		return widths
	}
	// Shave a modest bonus off the non-focused columns round-robin,
	// never letting any drop below 16, and give whatever was actually
	// freed to the focused column — so the total stays exactly width.
	const focusBonus = 6
	freed := 0
	for freed < focusBonus {
		moved := false
		for i := range widths {
			if i == focus || widths[i] <= 16 {
				continue
			}
			widths[i]--
			freed++
			moved = true
			if freed == focusBonus {
				break
			}
		}
		if !moved {
			break
		}
	}
	widths[focus] += freed
	return widths
}

func (b *boardView) View(width, height int) string {
	if width == 0 || height == 0 {
		return ""
	}
	if b.composeOverlay != nil {
		return b.viewComposeOverlay(width, height)
	}
	if b.questionOlay != nil && b.questionOlay.open {
		return b.questionOlay.view(width, height)
	}
	if b.picker {
		return b.viewPicker(width, height)
	}
	if b.featurePicker {
		return b.viewFeaturePicker(width, height)
	}
	if b.overlay {
		return b.viewOverlay(width, height)
	}

	// Detail pane sized at ~1/3 of body, clamped 8–14 rows; suppressed on
	// short terminals.
	detailHeight := 0
	if b.detailVisible && height >= 20 {
		detailHeight = height / 3
		if detailHeight < 8 {
			detailHeight = 8
		}
		if detailHeight > 14 {
			detailHeight = 14
		}
	}
	colsHeight := height - detailHeight
	if colsHeight < 5 {
		colsHeight = 5
	}

	visible := b.visibleStates()
	if len(visible) == 0 {
		empty := lipgloss.NewStyle().
			Width(width).Height(colsHeight).
			Align(lipgloss.Center, lipgloss.Center).
			Foreground(mutedColor).
			Render("All columns hidden — press c to choose visible columns.")
		return empty
	}

	n := len(visible)
	widths := boardColWidths(width, n, b.col)

	cols := make([]string, n)
	for i, st := range visible {
		cols[i] = b.renderColumn(st, i == b.col, widths[i], colsHeight)
	}
	board := lipgloss.JoinHorizontal(lipgloss.Top, cols...)

	parts := []string{board}
	if detailHeight > 0 {
		parts = append(parts, b.renderDetail(width, detailHeight))
	}
	return lipgloss.JoinVertical(lipgloss.Left, parts...)
}

func (b *boardView) renderColumn(st model.State, focused bool, width, height int) string {
	issues := b.columns[st]
	sel := b.rows[st]
	if sel >= len(issues) {
		sel = max(0, len(issues)-1)
	}

	border := colBorderColor
	if focused {
		border = colFocusBorder
	}

	innerWidth := width - 2
	if innerWidth < 4 {
		innerWidth = 4
	}

	// Each card occupies exactly cardHeight lines: the first carries the
	// issue key and the start of the title, subsequent lines are wrapped
	// title continuations indented to align under the title text. The
	// column title ("In Progress · 100") is no longer a row of its own
	// inside the box — it rides in the top border via post-processing,
	// which gives the body one extra row for actual cards.
	const cardHeight = 3

	// Compute how many cards fit in the body, then nudge the per-column
	// scroll so the cursor stays in view.
	bodyRows := height - 2 // -2 for top/bottom borders
	if bodyRows < cardHeight {
		bodyRows = cardHeight
	}
	cardsPerCol := bodyRows / cardHeight
	if cardsPerCol < 1 {
		cardsPerCol = 1
	}
	scroll := b.scroll[st]
	if scroll > sel {
		scroll = sel
	}
	if sel >= scroll+cardsPerCol {
		scroll = sel - cardsPerCol + 1
	}
	maxScroll := len(issues) - cardsPerCol
	if maxScroll < 0 {
		maxScroll = 0
	}
	if scroll > maxScroll {
		scroll = maxScroll
	}
	if scroll < 0 {
		scroll = 0
	}
	b.scroll[st] = scroll

	end := scroll + cardsPerCol
	if end > len(issues) {
		end = len(issues)
	}
	moreAbove := scroll > 0
	moreBelow := end < len(issues)

	// The column title sits in the top border, the count + scroll
	// arrow share the bottom border. Splitting them this way frees up
	// the title from any ellipsis from the count digits — at 14-col
	// columns "In Progress" survives intact instead of getting clipped
	// to "In … · 100". Each region gets a 1-space gutter on each side
	// plus a 1-char ─ minimum so it floats inside the border rather
	// than abutting the corners.
	const sidePad = 1   // " region " — one space each side
	const minDashes = 1 // ─ each side after the spaces
	regionBudget := innerWidth - 2*(sidePad+minDashes)

	var title string
	if regionBudget >= 1 {
		title = truncate(stateLabel(st), regionBudget)
	}

	var arrowSuffix string
	switch {
	case moreAbove && moreBelow:
		arrowSuffix = " ↕"
	case moreAbove:
		arrowSuffix = " ↑"
	case moreBelow:
		arrowSuffix = " ↓"
	}
	footer := fmt.Sprintf("%d%s", len(issues), arrowSuffix)
	if regionBudget >= 1 && utf8.RuneCountInString(footer) > regionBudget {
		footer = truncate(footer, regionBudget)
	}
	if regionBudget < 1 {
		footer = ""
	}

	// No reserved gutter on the LEFT of the card any more — the
	// feature-colour signal now rides on the square brackets around
	// the issue number (see keyRender below) rather than a dedicated
	// ▌ stripe column, so we reclaim that 1 col for title text.
	contentW := innerWidth
	if contentW < 3 {
		contentW = 3
	}
	cardStyle := lipgloss.NewStyle().Width(contentW)
	selStyle := lipgloss.NewStyle().Width(contentW).
		Background(cardSelectedBG).Foreground(lipgloss.Color("231"))

	var lines []string
	if len(issues) == 0 {
		lines = append(lines, mutedStyle.Width(innerWidth).Padding(1, 1).Render("— empty —"))
	}
	for i := scroll; i < end; i++ {
		iss := issues[i]
		// On the kanban cards we drop the repo prefix (it's already in
		// the tab strip's title chip in the top-right) and show only
		// the issue number, space-padded to a minimum of 2 chars so a
		// column of mixed-width numbers stays visually aligned:
		// [ 1] [ 2] [10] [11] [120] [1234]. The detail / overlay views
		// elsewhere still use the full iss.Key.
		bracketed := fmt.Sprintf("[%2d]", iss.Number)
		fullW := contentW

		isSel := i == sel && focused
		// A taken card (an agent holds an open claim on it) renders bold
		// so a human can see at a glance not to touch it — the `x`
		// dispatch is also refused on it. Bold survives the selected
		// state (selStyle.Bold) so the signal doesn't vanish on focus.
		isTaken := b.takenIssues[iss.ID]
		// BACI-68: an archived card rendered as Faint so it reads as
		// muted background context. The toggle has to be on for this
		// branch to even reach reload (reload's IncludeArchived filter
		// drops archived rows otherwise) — once visible, the styling
		// makes the lifecycle state legible at a glance.
		isArchived := iss.ArchivedAt != nil
		// A waiting card (dispatch queued, not yet claimed) shows an
		// animated spinner where the opening bracket would be. `taken`
		// wins: once an agent claims, the claim is established in the
		// same transaction as the dispatch ack, but render defensively
		// in case they overlap.
		isWaiting := b.waitingIssues[iss.ID] && !isTaken
		styler := cardStyle
		if isTaken {
			styler = cardStyle.Bold(true)
		}
		if isArchived {
			styler = styler.Faint(true)
		}
		// keyRender colours the [ and ] in the feature colour while
		// leaving the number itself in keyStyle's lavender. That's the
		// only place the feature signal lives now — the old ▌ stripe
		// is gone. Selected cards are rendered as plain text (no
		// per-rune styling) because nested lipgloss resets punch holes
		// in selStyle's background; the colour is sacrificed in the
		// selected state, same tradeoff the original code made for the
		// whole key. A taken card overrides the bracket colour with the
		// amber takenColor so the marker reads even against a feature hue.
		var keyRender string
		if isSel {
			styler = selStyle
			if isTaken {
				styler = selStyle.Bold(true)
			}
			if isWaiting {
				// Swap the opening bracket for the spinner frame; plain
				// text because nested styling punches holes in selStyle.
				keyRender = spinnerFrames[b.spinnerFrame] + fmt.Sprintf("%2d]", iss.Number)
			} else {
				keyRender = bracketed
			}
		} else {
			bracketColor := featureColor(iss.FeatureSlug)
			numStyle := keyStyle
			if isTaken {
				bracketColor = takenColor
				numStyle = keyStyle.Foreground(takenColor).Bold(true)
			}
			bracketStyle := lipgloss.NewStyle().Foreground(bracketColor).Bold(isTaken)
			openMark := bracketStyle.Render("[")
			if isWaiting {
				// Spinner glyph in place of the opening bracket, tinted
				// in the distinct waitingColor so it reads as motion
				// without being confused with the amber taken marker.
				openMark = lipgloss.NewStyle().Foreground(waitingColor).Render(spinnerFrames[b.spinnerFrame])
			}
			keyRender = openMark +
				numStyle.Render(fmt.Sprintf("%2d", iss.Number)) +
				bracketStyle.Render("]")
		}

		emit := func(content string) {
			lines = append(lines, styler.Render(content))
		}

		// Always use the packed layout: [KEY] + one-space gutter + the
		// start of the title on line 0, continuations on subsequent
		// lines. Every card is exactly cardHeight=3 lines, and the
		// title sits flush against the key rather than dropping to
		// line 1. Short titles like "Banana" fit fully on line 0 and
		// pad the remaining rows with whitespace; longer titles fill
		// more rows.
		//
		// `firstW < 1` is the one edge case where packing isn't
		// possible (bracketed key alone fills the content width); we
		// fall back to "key alone on line 0, title wrap on line 1" so
		// we still emit exactly cardHeight lines.
		prefixW := len(bracketed) + 1 // [KEY] + single-space gutter
		firstW := contentW - prefixW
		if firstW < 1 {
			emit(keyRender)
			fallback := wrapLines(iss.Title, contentW, 1)
			if len(fallback) > 0 {
				emit(fallback[0])
			} else {
				emit("")
			}
			continue
		}
		// BACI-53: prefix the title with "? " when this card's claiming
		// session has an open ask_user_question row tied to this issue.
		// Two-column prefix consumes title width but keeps card height
		// fixed — the badge has to live inline since the layout is a
		// 3-row budget per card.
		titleText := iss.Title
		if len(b.openQuestions[iss.Key]) > 0 {
			titleText = "? " + titleText
		}
		// BACI-145: on a waiting card the bottom (third) row carries the
		// inline "why is this card waiting?" label, so the title's wrap
		// budget shrinks from 3 rows to 2 (cardHeight - 1). A long
		// title's last wrap line gets dropped rather than collide with
		// the label — acceptable because waiting cards are short-lived
		// (seconds-to-minutes) and the title is still readable on rows
		// 1-2. Non-waiting cards keep the full 3-row budget.
		titleBudget := cardHeight
		var waitingLabel string
		if isWaiting {
			if ws := b.waitingStateByIssue[iss.ID]; ws != nil {
				waitingLabel = boardcards.WaitingStateLabel(ws)
			}
			if waitingLabel != "" {
				titleBudget = cardHeight - 1
				if titleBudget < 1 {
					titleBudget = 1
				}
			}
		}
		titleLines := wrapLinesAt(titleText, func(line int) int {
			if line == 0 {
				return firstW
			}
			return fullW
		}, titleBudget)

		for j := 0; j < cardHeight; j++ {
			var content string
			switch {
			case waitingLabel != "" && j == cardHeight-1:
				// Bottom row: render the waiting label in waitingColor so
				// it reads as motion-adjacent (matches the spinner's
				// colour), truncated to the column width.
				content = lipgloss.NewStyle().Foreground(waitingColor).
					Render(truncate(waitingLabel, fullW))
			case j == 0:
				if len(titleLines) > 0 {
					content = keyRender + " " + titleLines[0]
				} else {
					content = keyRender
				}
			case j < len(titleLines):
				content = titleLines[j]
			default:
				content = ""
			}
			emit(content)
		}
	}

	content := strings.Join(lines, "\n")

	rendered := lipgloss.NewStyle().
		Border(colBorder).
		BorderForeground(border).
		Width(innerWidth).
		Height(height - 2).
		MaxHeight(height).
		Render(content)

	// Slot the title into the top border and the count+arrow into the
	// bottom border. Both regions are centered inside their border's
	// horizontal run; the title is bold so it reads as a heading, and
	// the bottom footer stays in the border's plain colour so it sits
	// quietly as secondary info.
	if title != "" {
		rendered = overlayCenteredInBorder(rendered, title, '╭', '╮', innerWidth, true, true)
	}
	if footer != "" {
		rendered = overlayCenteredInBorder(rendered, footer, '╰', '╯', innerWidth, false, false)
	}
	return rendered
}

// overlayCenteredInBorder splices `text` into the middle of the top
// or bottom border line of a lipgloss-rendered box, wrapped in single
// spaces so it visually floats inside the ─ run:
//
//	╭─── Todo ───────╮       (top, top=true)
//	╰─── 60 ↓ ───────╯       (bottom, top=false)
//
// `leftCorner` / `rightCorner` are the expected corner glyphs for the
// target border (╭/╮ for top, ╰/╯ for bottom); if the line doesn't
// match (e.g. lipgloss switched to a different border style) the line
// is returned unchanged rather than corrupted. When `bold` is true the
// text is wrapped in SGR 1 / 22 so it stands out from the plain border
// chars; the border's foreground ANSI escape continues to apply so
// the bolded text inherits the border colour rather than going white.
//
// Implementation note: we post-process the rendered string rather
// than passing a custom Border to lipgloss because lipgloss cycles
// the Border.Top / Border.Bottom character across the full width —
// there's no hook for "put this glyph at the middle of the edge".
func overlayCenteredInBorder(rendered, text string, leftCorner, rightCorner rune, innerWidth int, top, bold bool) string {
	lines := strings.Split(rendered, "\n")
	if len(lines) == 0 {
		return rendered
	}
	idx := 0
	if !top {
		idx = len(lines) - 1
	}
	lines[idx] = spliceCenteredIntoBorder(lines[idx], text, leftCorner, rightCorner, innerWidth, bold)
	return strings.Join(lines, "\n")
}

func spliceCenteredIntoBorder(line, text string, leftCorner, rightCorner rune, innerWidth int, bold bool) string {
	region := " " + text + " "
	regionW := utf8.RuneCountInString(region)
	if regionW > innerWidth {
		return line
	}

	type cell struct {
		bytePos  int
		byteSize int
		r        rune
	}
	var cells []cell

	i := 0
	for i < len(line) {
		if line[i] == 0x1b && i+1 < len(line) && line[i+1] == '[' {
			// Skip CSI: ESC [ <params> <final>. Final byte is 0x40-0x7E.
			j := i + 2
			for j < len(line) {
				b := line[j]
				j++
				if b >= 0x40 && b <= 0x7E {
					break
				}
			}
			i = j
			continue
		}
		r, sz := utf8.DecodeRuneInString(line[i:])
		cells = append(cells, cell{bytePos: i, byteSize: sz, r: r})
		i += sz
	}

	// Expected layout: cells[0] = leftCorner, cells[1..N] = ─, cells[N+1] = rightCorner.
	if len(cells) < 3 || cells[0].r != leftCorner || cells[len(cells)-1].r != rightCorner {
		return line
	}

	dashSpan := innerWidth - regionW
	leftDashes := dashSpan / 2
	startIdx := 1 + leftDashes
	endIdx := startIdx + regionW - 1
	if endIdx >= len(cells)-1 {
		return line
	}

	startByte := cells[startIdx].bytePos
	endByte := cells[endIdx].bytePos + cells[endIdx].byteSize

	open, close := "", ""
	if bold {
		// SGR 1 turns bold on, 22 turns it back off. We deliberately
		// don't reset the foreground colour — the border's surrounding
		// ANSI is still active, so the text inherits the border colour
		// and only the boldness toggles.
		open, close = "\x1b[1m", "\x1b[22m"
	}
	return line[:startByte] + open + region + close + line[endByte:]
}

// fitHeader builds the per-column kanban header text ("Todo · 12 ↓")
// so the whole thing fits in innerWidth runes. The count and arrow are
// the live data; the label ("In Progress") is the most expendable
// piece, so when truncation is forced we shrink the label and keep
// the count + arrow visible. Result for "In Progress · 100 ↓" at
// innerWidth=16: "In Prog… · 100 ↓" instead of "In Progress ·… ↓".
func fitHeader(label string, count int, indicator string, innerWidth int) string {
	tail := fmt.Sprintf(" · %d%s", count, indicator)
	full := label + tail
	if utf8.RuneCountInString(full) <= innerWidth {
		return full
	}
	labelBudget := innerWidth - utf8.RuneCountInString(tail)
	if labelBudget < 1 {
		// Even the count+indicator alone exceed innerWidth — column
		// is absurdly narrow. Drop everything but enough chars to
		// keep the render inside the box.
		return truncate(full, innerWidth)
	}
	return truncate(label, labelBudget) + tail
}

func (b *boardView) renderDetail(width, height int) string {
	innerWidth := width - 4
	if innerWidth < 10 {
		innerWidth = 10
	}
	innerHeight := height - 2
	if innerHeight < 3 {
		innerHeight = 3
	}

	box := lipgloss.NewStyle().
		Border(colBorder).
		BorderForeground(colBorderColor).
		Width(width-2).
		Height(height-2).
		Padding(0, 1)

	if b.selected == nil {
		return box.Foreground(mutedColor).Render("No issue selected.")
	}

	iss := b.selected
	bracketedKey := "[" + iss.Key + "]"
	previewTag := mutedStyle.Italic(true).Render("preview")
	previewW := lipgloss.Width(previewTag)
	// Reserve room on the right for the "preview" label plus a 2-col gap.
	titleSpace := innerWidth - len(bracketedKey) - 2 - previewW - 2
	if titleSpace < 4 {
		titleSpace = 4
	}
	titleContent := keyStyle.Bold(true).Render(bracketedKey) + "  " +
		boldStyle.Render(truncate(iss.Title, titleSpace))
	gap := innerWidth - lipgloss.Width(titleContent) - previewW
	if gap < 1 {
		gap = 1
	}
	titleLine := lipgloss.JoinHorizontal(lipgloss.Top,
		titleContent,
		lipgloss.NewStyle().Width(gap).Render(""),
		previewTag,
	)

	metaParts := []string{"state: " + stateLabel(iss.State)}
	if iss.FeatureSlug != "" {
		metaParts = append(metaParts, "feature: ["+iss.FeatureSlug+"]")
	}
	if len(iss.Tags) > 0 {
		metaParts = append(metaParts, "tags: "+strings.Join(iss.Tags, ", "))
	}
	metaParts = append(metaParts, "updated: "+iss.UpdatedAt.Format("2006-01-02 15:04"))
	meta := mutedStyle.Render(truncate(strings.Join(metaParts, " · "), innerWidth))

	var commentLine string
	switch {
	case b.commentsErr != nil:
		commentLine = lipgloss.NewStyle().Foreground(lipgloss.Color("196")).
			Render("comments error: " + b.commentsErr.Error())
	case len(b.comments) == 0:
		commentLine = mutedStyle.Render("0 comments — press enter for full view")
	default:
		c := b.comments[len(b.comments)-1]
		preview := fmt.Sprintf("%d comments · last by %s: %s",
			len(b.comments), c.Author, oneLine(c.Body))
		commentLine = mutedStyle.Render(truncate(preview, innerWidth))
	}

	descRows := innerHeight - 4
	if descRows < 1 {
		descRows = 1
	}
	var desc string
	if iss.Description == "" {
		desc = mutedStyle.Italic(true).Render("(no description — enter for full view)")
	} else {
		desc = b.cachedMD(iss.ID, iss.Description, innerWidth)
		desc = clipLines(desc, descRows)
	}

	content := lipgloss.JoinVertical(lipgloss.Left,
		titleLine,
		meta,
		"",
		desc,
		commentLine,
	)
	return box.Render(content)
}

// viewOverlay renders the fullscreen card view as four content
// regions stitched together inside a single shared rounded frame.
// From top to bottom:
//   - header (fixed 2 content rows) — id, title, metadata.
//   - description (~60% of the remainder, focusable, scrollable).
//   - bottom row (~40% of the remainder): comments | attachments,
//     each focusable, sharing a vertical divider.
//
// Borders are drawn once by renderOverlayFrame; focus is signalled by
// the in-panel "▸ Heading" accent rather than by border colour, since
// the frame is a single shared chrome.
func (b *boardView) viewOverlay(width, height int) string {
	iss := b.selected
	if iss == nil {
		return panelBox(width, height, "No issue selected.", true)
	}
	if b.commentOverlay {
		return b.viewCommentOverlay(width, height)
	}
	if width < 20 || height < 12 {
		return panelBox(width, height, "Terminal too small.", true)
	}

	// 4 horizontal border rows (top, header/desc divider, desc/bottom
	// divider, bottom) eat 4 rows; the rest is content.
	contentRows := height - 4
	headerH := 2
	if headerH > contentRows-6 {
		headerH = max(1, contentRows-6)
	}
	remaining := contentRows - headerH
	descH := remaining * 6 / 10
	if descH < 3 {
		descH = 3
	}
	bottomH := remaining - descH
	if bottomH < 3 {
		bottomH = 3
		descH = remaining - bottomH
	}

	innerW := width - 2
	leftW := innerW / 2
	rightW := innerW - 1 - leftW

	headerLines := b.renderOverlayHeaderLines(iss, innerW, headerH)
	descLines := b.renderOverlayDescriptionLines(iss, innerW, descH)
	leftLines := b.renderCommentsLines(leftW, bottomH)
	rightLines := b.renderAttachmentsLines(rightW, bottomH)

	return renderOverlayFrame(width, headerLines, descLines, leftLines, rightLines, leftW)
}

// renderOverlayHeaderLines returns h lines (each cellW visible cols)
// of the issue-overlay header: title row, then a single meta row.
// Pads with empty rows if h > 2.
func (b *boardView) renderOverlayHeaderLines(iss *model.Issue, cellW, h int) []string {
	titleLine := keyStyle.Bold(true).Render("["+iss.Key+"]") + "  " +
		boldStyle.Render(iss.Title)

	metaItems := []string{stateLabel(iss.State)}
	if iss.FeatureSlug != "" {
		metaItems = append(metaItems, "["+iss.FeatureSlug+"]")
	}
	if len(iss.Tags) > 0 {
		metaItems = append(metaItems, strings.Join(iss.Tags, ", "))
	}
	metaItems = append(metaItems,
		"created "+iss.CreatedAt.Format("2006-01-02 15:04"),
		"updated "+iss.UpdatedAt.Format("2006-01-02 15:04"),
	)
	metaLine := mutedStyle.Render(strings.Join(metaItems, " · "))

	lines := padCell(strings.Join([]string{titleLine, metaLine}, "\n"), cellW)
	for len(lines) < h {
		lines = append(lines, strings.Repeat(" ", cellW))
	}
	return lines[:h]
}

// renderOverlayDescriptionLines returns h lines (each cellW visible
// cols) of the description panel: heading + scrollable markdown body
// + scrollbar, all already padded to cellW.
func (b *boardView) renderOverlayDescriptionLines(iss *model.Issue, cellW, h int) []string {
	focused := b.overlayFocus == paneDescription
	contentW := cellW - 4 // 2 cols horizontal padding on each side
	if contentW < 12 {
		contentW = 12
	}
	mdW := contentW - 1 // 1 col reserved for scrollbar inside scrollableBlock
	if mdW < 10 {
		mdW = 10
	}

	descHeader := paneHeading("Description", focused)
	var desc string
	if iss.Description == "" {
		desc = mutedStyle.Italic(true).Render("(none)")
	} else {
		desc = b.cachedMD(iss.ID, iss.Description, mdW)
	}
	body := strings.Join([]string{descHeader, "", desc}, "\n")
	inner := scrollableBlock(contentW, h, body, &b.overlayScroll, focused)
	return padCell(inner, cellW)
}

// paneHeading returns a section header. When focused, a 🟣 sits to the
// left of the label as the focus indicator. The unfocused variant uses
// 3 leading spaces so the label's x-position doesn't jump on focus
// change (🟣 is 2 cells + 1 space = 3 cells of indent).
func paneHeading(label string, focused bool) string {
	if focused {
		return lipgloss.NewStyle().Bold(true).Foreground(colFocusBorder).
			Render("🟣 " + label)
	}
	return boldStyle.Render("   " + label)
}

// renderCommentsLines returns h lines (each cellW visible cols) of
// the comments column: heading + scrollable comment list + scrollbar,
// padded to cellW so the caller can drop them directly into
// renderOverlayFrame. j/k scrolls line-by-line via b.commentsRow;
// `enter` (handled in updateOverlay) opens the fullscreen detail view.
func (b *boardView) renderCommentsLines(cellW, h int) []string {
	focused := b.overlayFocus == paneComments
	contentW := cellW - 4 // 2 cols horizontal padding each side
	if contentW < 12 {
		contentW = 12
	}
	bodyW := contentW - 1 // scrollbar reservation inside paneScrollFrame
	if bodyW < 10 {
		bodyW = 10
	}

	header := paneHeading(fmt.Sprintf("Comments · %d", len(b.comments)), focused)
	var body []string
	switch {
	case b.commentsErr != nil:
		body = append(body, errorStyle.Render(b.commentsErr.Error()))
	case len(b.comments) == 0:
		body = append(body, mutedStyle.Italic(true).Render("(no comments yet)"))
	default:
		for _, c := range b.comments {
			head := keyStyle.Render(c.Author) +
				mutedStyle.Render("  "+c.CreatedAt.Format("2006-01-02 15:04"))
			body = append(body, head)
			body = append(body, strings.Split(b.cachedCommentMD(c, bodyW), "\n")...)
			body = append(body, "")
		}
	}
	inner := paneScrollFrame(header, body, contentW, h, b.commentsRow, false, -1, true, focused)
	return padCell(inner, cellW)
}

// renderAttachmentsLines returns h lines (each cellW visible cols) of
// the attachments column: heading + selectable item list, padded to
// cellW. Each item has a title (filename / PR URL) and optionally a
// subtitle (italic muted) on the next line. The selected item's title
// renders in the focus colour when this pane is focused; other titles
// render plain white. Selection auto-scrolls into view.
func (b *boardView) renderAttachmentsLines(cellW, h int) []string {
	focused := b.overlayFocus == paneAttachments
	contentW := cellW - 4
	if contentW < 12 {
		contentW = 12
	}

	count := len(b.docLinks) + len(b.prs)
	header := paneHeading(fmt.Sprintf("Attachments · %d", count), focused)

	if focused && count > 0 && b.attachRow >= count {
		b.attachRow = count - 1
	}

	titleStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("231"))
	selectedStyle := lipgloss.NewStyle().Foreground(colFocusBorder).Bold(true)
	subtitleStyle := mutedStyle.Italic(true)

	// titleSlot is the body-slot index of the currently selected item's
	// title row. paneScrollFrame uses it to scroll-track without
	// painting a highlight bar (we colour the title text directly).
	titleSlot := -1

	var body []string
	switch {
	case b.attachErr != nil:
		body = append(body, errorStyle.Render(b.attachErr.Error()))
	case count == 0:
		body = append(body, mutedStyle.Italic(true).Render("(none)"))
	default:
		for i, l := range b.docLinks {
			style := titleStyle
			if focused && b.attachRow == i {
				style = selectedStyle
				titleSlot = len(body)
			}
			body = append(body, style.Render("📄 "+truncate(l.DocumentFilename, contentW-3)))
			if l.Description != "" {
				body = append(body, subtitleStyle.Render("   "+truncate(l.Description, contentW-3)))
			}
		}
		docCount := len(b.docLinks)
		for i, pr := range b.prs {
			style := titleStyle
			if focused && b.attachRow == docCount+i {
				style = selectedStyle
				titleSlot = len(body)
			}
			body = append(body, style.Render("🔀 "+truncate(pr.URL, contentW-3)))
		}
	}

	// Claimants block — the per-issue agent-claim history. Display-only
	// (not part of the selectable attachment list), appended below the
	// docs/PRs so a reader can see who has worked the issue and why.
	if len(b.claimants) > 0 {
		if len(body) > 0 {
			body = append(body, "")
		}
		taken := model.AnyOpenClaim(b.claimants)
		label := "Claimed by"
		if taken {
			label += " · taken"
		}
		body = append(body, boldStyle.Render(label))
		for _, c := range b.claimants {
			who := c.AgentName
			if who == "" {
				who = truncate(c.SessionID, 12)
			}
			marker := "●"
			if c.ReleasedAt != nil {
				marker = "○"
			}
			body = append(body, titleStyle.Render(truncate(marker+" "+who, contentW)))
			if c.Prompt != "" {
				body = append(body, subtitleStyle.Render("   "+truncate(oneLine(c.Prompt), contentW-3)))
			}
		}
	}

	inner := paneScrollFrame(header, body, contentW, h, 0, false, titleSlot, false, focused)
	return padCell(inner, cellW)
}

// paneScrollFrame composes a focused-pane: a sticky header row, a
// blank spacer row, then a (height-2)-row body window that the caller
// can scroll (offset) and/or highlight a single row in (cursorIdx).
// When highlightCursor is false the row offset is treated as a pure
// scroll position. When withScrollbar is true a 1-col scrollbar is
// reserved on the right edge of the body rows; the column is reserved
// unconditionally so toggling scrollable / not doesn't shift content.
func paneScrollFrame(header string, body []string, width, height, offset int, highlightCursor bool, cursorIdx int, withScrollbar, focused bool) string {
	rowsAvailable := height - 2 // 1 header row + 1 spacer below it
	if rowsAvailable < 1 {
		rowsAvailable = 1
	}
	bodyWidth := width
	if withScrollbar {
		bodyWidth = width - 1
		if bodyWidth < 1 {
			bodyWidth = 1
		}
	}

	// Auto-scroll so the cursor (if any) stays in view. Independent of
	// highlightCursor — callers can ask for "scroll-tracking only" by
	// passing cursorIdx>=0 with highlightCursor=false.
	if cursorIdx >= 0 {
		if cursorIdx < offset {
			offset = cursorIdx
		}
		if cursorIdx >= offset+rowsAvailable {
			offset = cursorIdx - rowsAvailable + 1
		}
	}
	maxOffset := len(body) - rowsAvailable
	if maxOffset < 0 {
		maxOffset = 0
	}
	if offset > maxOffset {
		offset = maxOffset
	}
	if offset < 0 {
		offset = 0
	}

	end := offset + rowsAvailable
	if end > len(body) {
		end = len(body)
	}
	visible := append([]string(nil), body[offset:end]...)

	if highlightCursor && cursorIdx >= offset && cursorIdx < end {
		i := cursorIdx - offset
		visible[i] = lipgloss.NewStyle().Width(bodyWidth).
			Background(cardSelectedBG).Foreground(lipgloss.Color("231")).
			Render(visible[i])
	}

	for len(visible) < rowsAvailable {
		visible = append(visible, "")
	}

	headerLine := lipgloss.NewStyle().Width(width).Render(header)
	spacer := lipgloss.NewStyle().Width(width).Render("")
	bodyBlock := lipgloss.NewStyle().Width(bodyWidth).Render(strings.Join(visible, "\n"))
	if withScrollbar {
		bar := renderVerticalScrollbar(rowsAvailable, len(body), offset, focused)
		bodyBlock = lipgloss.JoinHorizontal(lipgloss.Top, bodyBlock, bar)
	}
	return lipgloss.JoinVertical(lipgloss.Left, headerLine, spacer, bodyBlock)
}

func stateLabel(st model.State) string {
	switch st {
	case model.StateTodo:
		return "Todo"
	case model.StateInReview:
		return "In Review"
	case model.StateDone:
		return "Done"
	case model.StateCancelled:
		return "Cancelled"
	case model.StateInPipeline:
		return "In Pipeline"
	case model.StateToBeShipped:
		return "To Be Shipped"
	}
	return string(st)
}
