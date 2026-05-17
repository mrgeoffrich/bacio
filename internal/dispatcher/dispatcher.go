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
func (m *Matcher) Tick() (int, error) {
	if m == nil || m.b == nil {
		return 0, nil
	}
	templates, err := m.b.ListPromptTemplates()
	if err != nil {
		return 0, fmt.Errorf("matcher: list templates: %w", err)
	}
	limits := make(map[string]int, len(templates))
	for _, t := range templates {
		limits[t.Slug] = t.ConcurrencyLimit
	}

	repos, err := m.b.ListRepos()
	if err != nil {
		return 0, fmt.Errorf("matcher: list repos: %w", err)
	}
	now := time.Now()
	binds := 0
	for _, repo := range repos {
		if repo == nil {
			continue
		}
		n, err := m.tickRepo(repo.ID, limits, now)
		if err != nil {
			return binds, err
		}
		binds += n
	}
	return binds, nil
}

// tickRepo processes one repo's queued dispatches. Per (repo, mode) it
// checks the concurrency cap, picks a free agent, and binds the oldest
// queued row. Each mode is independent — a stuck `ship` queue doesn't
// hold up `plan` even if the ship-it template has concurrency_limit=1
// and the slot is full.
func (m *Matcher) tickRepo(repoID int64, limits map[string]int, now time.Time) (int, error) {
	modes, err := m.b.ListQueuedModesByRepo(repoID)
	if err != nil {
		return 0, fmt.Errorf("matcher: list queued modes for repo %d: %w", repoID, err)
	}
	if len(modes) == 0 {
		return 0, nil
	}
	// Build the candidate slice once per (repo, tick) — it's the same
	// agent pool every mode draws from, and BindQueuedDispatch's
	// transactional WHERE guards against a concurrent bind grabbing the
	// same agent. The picker re-runs per mode (cheap) so a successful
	// bind for `plan` doesn't make the `ship` matcher think the agent
	// is still free — but for one tick that's a tolerable approximation
	// (next tick re-reads fresh state).
	cands, err := pickCandidatesForRepo(m.b, repoID, now)
	if err != nil {
		return 0, fmt.Errorf("matcher: build candidates for repo %d: %w", repoID, err)
	}
	binds := 0
	for _, mode := range modes {
		bound, err := m.tickMode(repoID, mode, limits, cands)
		if err != nil {
			return binds, err
		}
		if bound {
			binds++
		}
	}
	return binds, nil
}

// tickMode processes one (repo, mode) group: enforce the concurrency
// limit, take the oldest queued row, pick a free agent, bind. Returns
// true iff a bind succeeded. A no-eligible-agent or empty-queue case
// returns (false, nil) — quiet skip, next tick retries.
func (m *Matcher) tickMode(
	repoID int64,
	mode model.DispatchMode,
	limits map[string]int,
	cands []AgentCandidate,
) (bool, error) {
	limit := limits[string(mode)]
	if limit > 0 {
		inflight, err := m.b.CountInFlightByMode(repoID, mode)
		if err != nil {
			return false, fmt.Errorf("matcher: count in-flight (%d, %s): %w", repoID, mode, err)
		}
		if inflight >= limit {
			return false, nil
		}
	}
	queue, err := m.b.ListQueuedByRepoMode(repoID, mode)
	if err != nil {
		return false, fmt.Errorf("matcher: list queued (%d, %s): %w", repoID, mode, err)
	}
	if len(queue) == 0 {
		return false, nil
	}
	agentName := AutoPickFreeAgent(cands)
	if agentName == "" {
		return false, nil
	}
	ag, err := m.b.GetAgentByName(agentName)
	if err != nil {
		// An identity that was in the candidate list but isn't in the
		// agents table is a defensive impossibility — bail this mode
		// (skip the bind) and let the next tick re-read.
		if errors.Is(err, store.ErrNotFound) {
			return false, nil
		}
		return false, fmt.Errorf("matcher: resolve agent %q: %w", agentName, err)
	}
	oldest := queue[0]
	if _, err := m.b.BindQueuedDispatch(oldest.ID, ag.ID); err != nil {
		// A concurrent process beat us to this row — fine, no-op and
		// move on. The next tick will pick up whatever is still queued.
		if errors.Is(err, store.ErrNotFound) {
			return false, nil
		}
		return false, fmt.Errorf("matcher: bind dispatch %d: %w", oldest.ID, err)
	}
	return true, nil
}
