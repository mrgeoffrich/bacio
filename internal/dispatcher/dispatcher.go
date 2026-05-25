package dispatcher

import (
	"errors"
	"fmt"
	"time"

	"github.com/mrgeoffrich/bacio/internal/model"
	"github.com/mrgeoffrich/bacio/internal/store"
)

// Backend is the minimal store surface the matcher and picker need.
// *store.Store satisfies it directly — same shape as
// internal/leader.Backend, so the dispatcher mirrors the elector's
// patterns (a small backend interface that's easy to fake in tests).
type Backend interface {
	ListRepos() ([]*model.Repo, error)
	ListPromptTemplates() ([]*store.PromptTemplate, error)
	ListQueuedModesByRepo(repoID int64) ([]model.DispatchMode, error)
	ListQueuedByRepoMode(repoID int64, mode model.DispatchMode) ([]*model.AgentDispatch, error)
	CountInFlightByMode(repoID int64, mode model.DispatchMode) (int, error)
	ListAgentSessions(store.AgentSessionFilter) ([]*model.AgentSession, error)
	OpenClaimsBySession(repoID int64) (map[int64][]*model.AgentClaim, error)
	ListDispatches(store.DispatchFilter) ([]*model.AgentDispatch, error)
	GetAgentByName(name string) (*model.Agent, error)
	BindQueuedDispatch(id int64, agentID int64) (*model.AgentDispatch, error)
}

// Matcher is the per-(repo, mode) FIFO queue matcher. Stateless — every
// tick reads fresh state from the backend and writes binds back. Safe
// to construct once per process and call Tick from a timer.
type Matcher struct {
	b Backend
}

// Bind records one successful queued→pending transition the matcher
// committed during a tick. The audit-writing caller (controller.
// MatchIfLeader) consumes this slice to emit one `agent.bind` history
// row per entry, attributing it to the matcher subsystem (BACI-160).
// Dispatch is the post-bind row (Status=pending, TargetAgentID set);
// AgentName is the candidate name the picker chose, supplied alongside
// the dispatch row because BindQueuedDispatch's returned row doesn't
// always carry the agent's display name in scanDispatch.
type Bind struct {
	Dispatch  *model.AgentDispatch
	AgentName string
}

// New returns a Matcher backed by b. The caller is responsible for
// running Tick on a timer (and for gating on the ui_leader lease so only
// one process matches at a time across the cluster).
func New(b Backend) *Matcher {
	return &Matcher{b: b}
}

// Tick walks every (repo, mode) group with queued dispatches and binds
// the oldest queued item per group to a free agent, subject to the
// template's concurrency limit. Returns the number of successful binds
// and the first matcher-side error (read failures stop the tick; per-
// group failures from BindQueuedDispatch are logged into the count
// only — a race with another binder is fine).
//
// Thin compatibility shim around TickDetailed for callers that only
// need the count. New audit-emitting callers (controller.MatchIfLeader,
// BACI-160) use TickDetailed to get the per-bind detail they need to
// write `agent.bind` history rows.
func (m *Matcher) Tick() (int, error) {
	binds, err := m.TickDetailed()
	return len(binds), err
}

// TickDetailed is Tick with the per-bind detail caller-visible. Returns
// the slice of successful binds in the order they were committed; an
// empty slice means nothing was bindable this tick (concurrency cap,
// empty queue, or no free agent — none of which are errors). On the
// first read failure the partially-built slice is returned alongside
// the error so an audit-writing caller can still record the binds it
// did see before bailing.
func (m *Matcher) TickDetailed() ([]Bind, error) {
	if m == nil || m.b == nil {
		return nil, nil
	}
	templates, err := m.b.ListPromptTemplates()
	if err != nil {
		return nil, fmt.Errorf("matcher: list templates: %w", err)
	}
	limits := make(map[string]int, len(templates))
	for _, t := range templates {
		limits[t.Slug] = t.ConcurrencyLimit
	}

	repos, err := m.b.ListRepos()
	if err != nil {
		return nil, fmt.Errorf("matcher: list repos: %w", err)
	}
	now := time.Now()
	var binds []Bind
	for _, repo := range repos {
		if repo == nil {
			continue
		}
		repoBinds, err := m.tickRepo(repo.ID, limits, now)
		if err != nil {
			return binds, err
		}
		binds = append(binds, repoBinds...)
	}
	return binds, nil
}

// tickRepo processes one repo's queued dispatches. Per (repo, mode) it
// checks the concurrency cap, picks a free agent, and binds the oldest
// queued row. Each mode is independent — a stuck `ship` queue doesn't
// hold up `plan` even if the ship-it template has concurrency_limit=1
// and the slot is full.
//
// Within a single tick, an agent bound to one mode is marked busy in
// the local candidate slice before the next mode runs, so a multi-mode
// queue (e.g. 1 ship + 1 plan + 1 free agent) never over-assigns that
// agent. The next tick re-reads fresh state from the backend.
func (m *Matcher) tickRepo(repoID int64, limits map[string]int, now time.Time) ([]Bind, error) {
	modes, err := m.b.ListQueuedModesByRepo(repoID)
	if err != nil {
		return nil, fmt.Errorf("matcher: list queued modes for repo %d: %w", repoID, err)
	}
	if len(modes) == 0 {
		return nil, nil
	}
	// Build the candidate slice once per (repo, tick) — it's the same
	// agent pool every mode draws from. After a successful bind we
	// mark the bound agent as busy in this slice (see
	// markCandidateBusy) so the next mode's pick skips them — without
	// this guard a mixed-mode queue could double-bind a single agent
	// within one tick, defeating the per-template concurrency cap.
	cands, err := pickCandidatesForRepo(m.b, repoID, now)
	if err != nil {
		return nil, fmt.Errorf("matcher: build candidates for repo %d: %w", repoID, err)
	}
	var binds []Bind
	for _, mode := range modes {
		bind, err := m.tickMode(repoID, mode, limits, cands)
		if err != nil {
			return binds, err
		}
		if bind != nil {
			binds = append(binds, *bind)
			markCandidateBusy(cands, bind.AgentName)
		}
	}
	return binds, nil
}

// tickMode processes one (repo, mode) group: enforce the concurrency
// limit, take the oldest queued row, pick a free agent, bind. Returns
// the populated Bind on success (nil on an empty queue, concurrency
// cap hit, no free agent, or a swallowed bind race).
func (m *Matcher) tickMode(
	repoID int64,
	mode model.DispatchMode,
	limits map[string]int,
	cands []AgentCandidate,
) (*Bind, error) {
	limit := limits[string(mode)]
	if limit > 0 {
		inflight, err := m.b.CountInFlightByMode(repoID, mode)
		if err != nil {
			return nil, fmt.Errorf("matcher: count in-flight (%d, %s): %w", repoID, mode, err)
		}
		if inflight >= limit {
			return nil, nil
		}
	}
	queue, err := m.b.ListQueuedByRepoMode(repoID, mode)
	if err != nil {
		return nil, fmt.Errorf("matcher: list queued (%d, %s): %w", repoID, mode, err)
	}
	if len(queue) == 0 {
		return nil, nil
	}
	agentName := AutoPickFreeAgent(cands)
	if agentName == "" {
		return nil, nil
	}
	ag, err := m.b.GetAgentByName(agentName)
	if err != nil {
		// An identity that was in the candidate list but isn't in the
		// agents table is a defensive impossibility — bail this mode
		// (skip the bind) and let the next tick re-read.
		if errors.Is(err, store.ErrNotFound) {
			return nil, nil
		}
		return nil, fmt.Errorf("matcher: resolve agent %q: %w", agentName, err)
	}
	oldest := queue[0]
	bound, err := m.b.BindQueuedDispatch(oldest.ID, ag.ID)
	if err != nil {
		// A concurrent process beat us to this row — fine, no-op and
		// move on. The next tick will pick up whatever is still queued.
		if errors.Is(err, store.ErrNotFound) {
			return nil, nil
		}
		return nil, fmt.Errorf("matcher: bind dispatch %d: %w", oldest.ID, err)
	}
	return &Bind{Dispatch: bound, AgentName: agentName}, nil
}

// markCandidateBusy flips the HasOpenDispatch flag on the candidate
// with the given AgentName so the next AutoPickFreeAgent call within
// the same tick skips them. A name we didn't recognise is a no-op
// (defensive — the next tick reads fresh state regardless).
func markCandidateBusy(cands []AgentCandidate, agentName string) {
	for i := range cands {
		if cands[i].AgentName == agentName {
			cands[i].HasOpenDispatch = true
			return
		}
	}
}
