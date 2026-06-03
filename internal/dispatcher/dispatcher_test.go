package dispatcher

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/mrgeoffrich/bacio/internal/model"
	"github.com/mrgeoffrich/bacio/internal/store"
)

// fakeBackend is an in-memory Backend stand-in for Matcher.Tick tests.
// Only the fields the matcher reads are populated; the rest of the
// store surface is left out.
type fakeBackend struct {
	repos        []*model.Repo
	templates    []*store.PromptTemplate
	queuedByRM   map[queueKey][]*model.AgentDispatch
	inFlightByM  map[queueKey]int
	inFlightByMB map[branchKey]int // BACI-227 per-(repo, mode, branch) counts
	sessions     map[int64][]*model.AgentSession
	openClaims   map[int64]map[int64][]*model.AgentClaim
	dispatches   map[int64][]*model.AgentDispatch
	agents       map[string]*model.Agent

	binds          []bindCall          // recorded in BindQueuedDispatch order
	bindErr        map[int64]error     // per-dispatch-id override (e.g. ErrNotFound)
	listQueuedHits map[queueKey]int    // call counts for ListQueuedByRepoMode
	countBaseHits  map[branchKey]int   // BACI-227: CountInFlightByModeBase call counts
	pickCalls      [][]AgentCandidate  // captures the slice passed to AutoPickFreeAgent
}

type queueKey struct {
	repoID int64
	mode   model.DispatchMode
}

// branchKey (BACI-227) is the composite key for the per-branch
// in-flight cache the fake backend serves CountInFlightByModeBase
// from. Mirrors store.InflightKey in shape, scoped to the fake.
type branchKey struct {
	repoID int64
	mode   model.DispatchMode
	branch string
}

type bindCall struct {
	dispatchID int64
	agentID    int64
}

func newFakeBackend() *fakeBackend {
	return &fakeBackend{
		queuedByRM:     map[queueKey][]*model.AgentDispatch{},
		inFlightByM:    map[queueKey]int{},
		inFlightByMB:   map[branchKey]int{},
		sessions:       map[int64][]*model.AgentSession{},
		openClaims:     map[int64]map[int64][]*model.AgentClaim{},
		dispatches:     map[int64][]*model.AgentDispatch{},
		agents:         map[string]*model.Agent{},
		bindErr:        map[int64]error{},
		listQueuedHits: map[queueKey]int{},
		countBaseHits:  map[branchKey]int{},
	}
}

func (f *fakeBackend) ListRepos() ([]*model.Repo, error) { return f.repos, nil }

func (f *fakeBackend) ListPromptTemplates() ([]*store.PromptTemplate, error) {
	return f.templates, nil
}

func (f *fakeBackend) ListQueuedModesByRepo(repoID int64) ([]model.DispatchMode, error) {
	seen := map[model.DispatchMode]bool{}
	var out []model.DispatchMode
	for k := range f.queuedByRM {
		if k.repoID != repoID || seen[k.mode] {
			continue
		}
		if len(f.queuedByRM[k]) == 0 {
			continue
		}
		seen[k.mode] = true
		out = append(out, k.mode)
	}
	// Stable mode order (slug ASC) to match the production helper.
	for i := 0; i < len(out); i++ {
		for j := i + 1; j < len(out); j++ {
			if string(out[i]) > string(out[j]) {
				out[i], out[j] = out[j], out[i]
			}
		}
	}
	return out, nil
}

func (f *fakeBackend) ListQueuedByRepoMode(repoID int64, mode model.DispatchMode) ([]*model.AgentDispatch, error) {
	k := queueKey{repoID: repoID, mode: mode}
	f.listQueuedHits[k]++
	return append([]*model.AgentDispatch(nil), f.queuedByRM[k]...), nil
}

func (f *fakeBackend) CountInFlightByMode(repoID int64, mode model.DispatchMode) (int, error) {
	return f.inFlightByM[queueKey{repoID: repoID, mode: mode}], nil
}

// CountInFlightByModeBase (BACI-227) returns the per-branch count.
// Records each call against countBaseHits so tests can pin the
// matcher's per-tick caching invariant — one count per (repo, mode,
// branch) per tick, no matter how many queued rows share the branch.
func (f *fakeBackend) CountInFlightByModeBase(repoID int64, mode model.DispatchMode, branch string) (int, error) {
	k := branchKey{repoID: repoID, mode: mode, branch: branch}
	f.countBaseHits[k]++
	return f.inFlightByMB[k], nil
}

func (f *fakeBackend) ListAgentSessions(filter store.AgentSessionFilter) ([]*model.AgentSession, error) {
	if filter.RepoID == nil {
		return nil, nil
	}
	return f.sessions[*filter.RepoID], nil
}

func (f *fakeBackend) OpenClaimsBySession(repoID int64) (map[int64][]*model.AgentClaim, error) {
	return f.openClaims[repoID], nil
}

func (f *fakeBackend) ListDispatches(filter store.DispatchFilter) ([]*model.AgentDispatch, error) {
	if filter.RepoID == nil {
		return nil, nil
	}
	return f.dispatches[*filter.RepoID], nil
}

func (f *fakeBackend) GetAgentByName(name string) (*model.Agent, error) {
	ag, ok := f.agents[name]
	if !ok {
		return nil, store.ErrNotFound
	}
	return ag, nil
}

func (f *fakeBackend) BindQueuedDispatch(id int64, agentID int64) (*model.AgentDispatch, error) {
	if err, ok := f.bindErr[id]; ok {
		return nil, err
	}
	f.binds = append(f.binds, bindCall{dispatchID: id, agentID: agentID})
	// Remove the dispatch from the queue so the next tick wouldn't
	// re-pick it. Find by id across all keys.
	for k, list := range f.queuedByRM {
		out := list[:0]
		for _, d := range list {
			if d.ID != id {
				out = append(out, d)
			}
		}
		f.queuedByRM[k] = out
	}
	return &model.AgentDispatch{ID: id, TargetAgentID: &agentID, Status: model.DispatchPending}, nil
}

// --- helpers for building fixtures ---

func (f *fakeBackend) addRepo(id int64, prefix string) {
	f.repos = append(f.repos, &model.Repo{ID: id, Prefix: prefix})
}

func (f *fakeBackend) addTemplate(slug string, concurrencyLimit int) {
	f.templates = append(f.templates, &store.PromptTemplate{Slug: slug, ConcurrencyLimit: concurrencyLimit})
}

func (f *fakeBackend) addQueued(repoID int64, mode model.DispatchMode, ids ...int64) {
	k := queueKey{repoID: repoID, mode: mode}
	for _, id := range ids {
		f.queuedByRM[k] = append(f.queuedByRM[k], &model.AgentDispatch{
			ID:        id,
			RepoID:    repoID,
			Mode:      mode,
			Status:    model.DispatchQueued,
			CreatedAt: time.Unix(id, 0), // FIFO order by id
		})
	}
}

// addQueuedBranch (BACI-227) is addQueued's per-branch sibling: stamps
// BaseBranch on each row so the matcher's branchOf folds to the same
// key the per-(repo, mode, branch) inflight cache is seeded with.
func (f *fakeBackend) addQueuedBranch(repoID int64, mode model.DispatchMode, branch string, ids ...int64) {
	k := queueKey{repoID: repoID, mode: mode}
	for _, id := range ids {
		f.queuedByRM[k] = append(f.queuedByRM[k], &model.AgentDispatch{
			ID:         id,
			RepoID:     repoID,
			Mode:       mode,
			Status:     model.DispatchQueued,
			CreatedAt:  time.Unix(id, 0), // FIFO order by id
			BaseBranch: branch,
		})
	}
}

func (f *fakeBackend) addFreeAgent(repoID, agentID int64, name string) {
	f.agents[name] = &model.Agent{ID: agentID, Name: name}
	now := time.Now()
	f.sessions[repoID] = append(f.sessions[repoID], &model.AgentSession{
		ID:            agentID, // use agentID as a session PK proxy
		SessionID:     name + "-session",
		RepoID:        repoID,
		AgentID:       &agentID,
		AgentName:     name,
		LastSeenAt:    now,
		ChannelSeenAt: &now,
		RegisteredAt:  &now,
	})
}

func (f *fakeBackend) setInFlight(repoID int64, mode model.DispatchMode, n int) {
	f.inFlightByM[queueKey{repoID: repoID, mode: mode}] = n
}

// setInFlightBranch (BACI-227) is setInFlight's per-branch sibling:
// seeds the matcher's CountInFlightByModeBase return for one
// (repo, mode, branch) triple.
func (f *fakeBackend) setInFlightBranch(repoID int64, mode model.DispatchMode, branch string, n int) {
	f.inFlightByMB[branchKey{repoID: repoID, mode: mode, branch: branch}] = n
}

// ---- tests ----

func TestMatcherTick_EmptyQueue(t *testing.T) {
	b := newFakeBackend()
	b.addRepo(1, "MINI")
	b.addTemplate("plan", 0)

	n, err := New(b).Tick()
	if err != nil {
		t.Fatalf("Tick: %v", err)
	}
	if n != 0 {
		t.Fatalf("bind count = %d, want 0", n)
	}
	if len(b.binds) != 0 {
		t.Fatalf("unexpected binds: %+v", b.binds)
	}
}

func TestMatcherTick_BindsFreeAgentToQueuedDispatch(t *testing.T) {
	b := newFakeBackend()
	b.addRepo(1, "MINI")
	b.addTemplate("plan", 0)
	b.addFreeAgent(1, 10, "otter")
	b.addQueued(1, model.DispatchMode("plan"), 42)

	n, err := New(b).Tick()
	if err != nil {
		t.Fatalf("Tick: %v", err)
	}
	if n != 1 {
		t.Fatalf("bind count = %d, want 1", n)
	}
	if len(b.binds) != 1 || b.binds[0].dispatchID != 42 || b.binds[0].agentID != 10 {
		t.Fatalf("binds = %+v, want [{42 10}]", b.binds)
	}
}

func TestMatcherTick_ConcurrencyCapHoldsBackSecondShip(t *testing.T) {
	b := newFakeBackend()
	b.addRepo(1, "MINI")
	b.addTemplate("ship", 1)
	b.addFreeAgent(1, 10, "otter")
	b.addQueued(1, model.DispatchMode("ship"), 1)
	// One ship-it already in flight on main: matcher should refuse to
	// bind a second on the same branch. BACI-227: the queued row has
	// no BaseBranch, so branchOf folds to "main" — seed the cap on
	// that branch to reproduce today's per-mode-cap behaviour.
	b.setInFlightBranch(1, model.DispatchMode("ship"), model.DefaultBaseBranch, 1)

	n, err := New(b).Tick()
	if err != nil {
		t.Fatalf("Tick: %v", err)
	}
	if n != 0 {
		t.Fatalf("bind count = %d, want 0 (cap should hold)", n)
	}
	if len(b.binds) != 0 {
		t.Fatalf("expected no bind, got %+v", b.binds)
	}
}

func TestMatcherTick_MultiModeDoesNotOverAssignOneAgent(t *testing.T) {
	// BACI-51 review finding #1: with one free agent and queued items
	// across two distinct modes, the matcher used to pick the same
	// agent twice in a single tick because cands was stale. After the
	// fix, the second mode sees the freshly-bound agent as occupied
	// and skips (no other free agent → no bind).
	b := newFakeBackend()
	b.addRepo(1, "MINI")
	b.addTemplate("plan", 0)
	b.addTemplate("ship", 0)
	b.addFreeAgent(1, 10, "otter")
	b.addQueued(1, model.DispatchMode("plan"), 100)
	b.addQueued(1, model.DispatchMode("ship"), 200)

	n, err := New(b).Tick()
	if err != nil {
		t.Fatalf("Tick: %v", err)
	}
	if n != 1 {
		t.Fatalf("bind count = %d, want 1 (one agent ⇒ one bind per tick)", n)
	}
	if len(b.binds) != 1 {
		t.Fatalf("binds = %+v, want exactly 1", b.binds)
	}
	if b.binds[0].agentID != 10 {
		t.Fatalf("bound agent id = %d, want 10", b.binds[0].agentID)
	}
	// Confirm the *other* mode's queue stays untouched — the dispatch
	// id we didn't bind is still queued for the next tick.
	other := int64(100)
	if b.binds[0].dispatchID == 100 {
		other = 200
	}
	stillQueued := false
	for _, list := range b.queuedByRM {
		for _, d := range list {
			if d.ID == other {
				stillQueued = true
			}
		}
	}
	if !stillQueued {
		t.Fatalf("expected dispatch %d to remain queued for next tick", other)
	}
}

func TestMatcherTick_MultiAgentMultiModeBindsBoth(t *testing.T) {
	// Inverse of the over-assign test: two free agents + two modes
	// should bind both within one tick. Proves markCandidateBusy
	// doesn't over-correct and leave an eligible agent on the bench.
	b := newFakeBackend()
	b.addRepo(1, "MINI")
	b.addTemplate("plan", 0)
	b.addTemplate("ship", 0)
	b.addFreeAgent(1, 10, "otter")
	b.addFreeAgent(1, 11, "lynx")
	b.addQueued(1, model.DispatchMode("plan"), 100)
	b.addQueued(1, model.DispatchMode("ship"), 200)

	n, err := New(b).Tick()
	if err != nil {
		t.Fatalf("Tick: %v", err)
	}
	if n != 2 {
		t.Fatalf("bind count = %d, want 2 (one per mode, distinct agents)", n)
	}
	if len(b.binds) != 2 {
		t.Fatalf("binds = %+v, want exactly 2", b.binds)
	}
	if b.binds[0].agentID == b.binds[1].agentID {
		t.Fatalf("both binds went to the same agent: %+v", b.binds)
	}
}

func TestMatcherTick_FIFOOldestFirst(t *testing.T) {
	// Both rows queued for the same (repo, mode); the smaller-id one
	// was added first (created_at = time.Unix(id, 0) in the fixture),
	// so the matcher's ListQueuedByRepoMode should hand it back first
	// — bind hits the older row.
	b := newFakeBackend()
	b.addRepo(1, "MINI")
	b.addTemplate("plan", 0)
	b.addFreeAgent(1, 10, "otter")
	b.addQueued(1, model.DispatchMode("plan"), 5, 9) // 5 is older

	n, err := New(b).Tick()
	if err != nil {
		t.Fatalf("Tick: %v", err)
	}
	if n != 1 {
		t.Fatalf("bind count = %d, want 1", n)
	}
	if b.binds[0].dispatchID != 5 {
		t.Fatalf("bound dispatch id = %d, want 5 (older first)", b.binds[0].dispatchID)
	}
}

func TestMatcherTick_NoFreeAgentIsQuietSkip(t *testing.T) {
	// Queue has work but no agent is eligible — Tick returns (0, nil),
	// not an error.
	b := newFakeBackend()
	b.addRepo(1, "MINI")
	b.addTemplate("plan", 0)
	b.addQueued(1, model.DispatchMode("plan"), 1)

	n, err := New(b).Tick()
	if err != nil {
		t.Fatalf("Tick: %v", err)
	}
	if n != 0 {
		t.Fatalf("bind count = %d, want 0", n)
	}
	if len(b.binds) != 0 {
		t.Fatalf("expected no binds, got %+v", b.binds)
	}
}

func TestMatcherTick_BindRaceIsSwallowed(t *testing.T) {
	// A concurrent process beat us to a queued row → BindQueuedDispatch
	// returns store.ErrNotFound. Tick should swallow it and continue
	// (the next tick re-reads fresh state). No error propagated.
	b := newFakeBackend()
	b.addRepo(1, "MINI")
	b.addTemplate("plan", 0)
	b.addFreeAgent(1, 10, "otter")
	b.addQueued(1, model.DispatchMode("plan"), 7)
	b.bindErr[7] = store.ErrNotFound

	n, err := New(b).Tick()
	if err != nil {
		t.Fatalf("Tick: %v", err)
	}
	if n != 0 {
		t.Fatalf("bind count = %d, want 0 (race swallowed)", n)
	}
}

// TestMatcherTick_UnblocksPastStaleOrphan (BACI-58 §A end-to-end) wires
// the real *store.Store as the matcher's backend and proves the
// integrated fix: a delivered dispatch to a long-dead agent no longer
// strands the queue. Without the BACI-58 staleness gate the matcher's
// CountInFlightByMode would return 1 (=ship's concurrency_limit) and
// the fresh queued dispatch would sit forever.
func TestMatcherTick_UnblocksPastStaleOrphan(t *testing.T) {
	s, err := store.Open(filepath.Join(t.TempDir(), "matcher.sqlite"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })

	repo, err := s.CreateRepo("STND", "stranded", t.TempDir(), "")
	if err != nil {
		t.Fatalf("create repo: %v", err)
	}
	iss, err := s.CreateIssue(repo.ID, nil, "stranded ship", "", model.StateInReview, nil, "", "")
	if err != nil {
		t.Fatalf("create issue: %v", err)
	}

	// Force ship's concurrency_limit to 1 — same setup as the
	// real-world repro the issue cites.
	if _, err := s.SetPromptTemplateConcurrencyLimit(string(model.DispatchModeShip), 1); err != nil {
		t.Fatalf("set ship concurrency: %v", err)
	}

	// Dead agent: registered, then its session forced stale beyond
	// the AgentIdlePingThreshold. Its delivered ship dispatch would
	// historically have permanently held the only ship slot.
	deadAgent, _, err := s.UpsertAgent("dead-otter@claude.dead", true)
	if err != nil {
		t.Fatalf("upsert dead agent: %v", err)
	}
	deadSess, err := s.UpsertAgentSession(store.UpsertAgentSessionIn{
		SessionID: "sess-dead", RepoID: repo.ID, AgentID: &deadAgent.ID, Actor: "agent-claude",
		MarkRegistered: true,
	})
	if err != nil {
		t.Fatalf("upsert dead session: %v", err)
	}
	deadDispatch, err := s.AddDispatch(store.AddDispatchIn{
		RepoID:        repo.ID,
		TargetAgentID: &deadAgent.ID,
		IssueID:       &iss.ID,
		Mode:          model.DispatchModeShip,
		Payload:       "ship orphan",
		CreatedBy:     "supervisor",
	})
	if err != nil {
		t.Fatalf("add dead dispatch: %v", err)
	}
	if _, err := s.MarkDispatchDelivered(deadDispatch.ID); err != nil {
		t.Fatalf("mark delivered: %v", err)
	}
	// Backdate the dead session's last_seen_at past the staleness
	// window so the BACI-58 gate excludes its dispatch from the count.
	if _, err := s.DB.Exec(
		`UPDATE agent_sessions SET last_seen_at = datetime('now','-3 hours') WHERE session_id = ?`,
		deadSess.SessionID,
	); err != nil {
		t.Fatalf("force-stale dead session: %v", err)
	}

	// Live agent + a fresh queued ship dispatch the matcher should bind.
	liveAgent, _, err := s.UpsertAgent("live-otter@claude.alive", true)
	if err != nil {
		t.Fatalf("upsert live agent: %v", err)
	}
	if _, err := s.UpsertAgentSession(store.UpsertAgentSessionIn{
		SessionID: "sess-live", RepoID: repo.ID, AgentID: &liveAgent.ID, Actor: "agent-claude",
		MarkRegistered: true,
	}); err != nil {
		t.Fatalf("upsert live session: %v", err)
	}
	// Mark the live session as channel-connected. The picker only
	// considers candidates whose session has a `channel_seen_at` —
	// without this the live agent looks no better than the dead one.
	if _, err := s.DB.Exec(
		`UPDATE agent_sessions SET channel_seen_at = CURRENT_TIMESTAMP WHERE session_id = ?`,
		"sess-live",
	); err != nil {
		t.Fatalf("mark live session channel-connected: %v", err)
	}
	freshIss, err := s.CreateIssue(repo.ID, nil, "fresh ship", "", model.StateInReview, nil, "", "")
	if err != nil {
		t.Fatalf("create fresh issue: %v", err)
	}
	queuedDispatch, err := s.AddDispatch(store.AddDispatchIn{
		RepoID:        repo.ID,
		IssueID:       &freshIss.ID,
		Mode:          model.DispatchModeShip,
		Payload:       "ship fresh",
		CreatedBy:     "supervisor",
		InitialStatus: model.DispatchQueued,
	})
	if err != nil {
		t.Fatalf("add queued dispatch: %v", err)
	}

	bound, err := New(s).Tick()
	if err != nil {
		t.Fatalf("Tick: %v", err)
	}
	if bound != 1 {
		t.Fatalf("matcher bound %d dispatches, want 1 (stale orphan should not have held the slot)", bound)
	}
	got, err := s.GetDispatch(queuedDispatch.ID)
	if err != nil {
		t.Fatalf("get queued dispatch: %v", err)
	}
	if got.Status != model.DispatchPending {
		t.Fatalf("queued dispatch status after Tick = %q, want pending (matcher should have bound it)", got.Status)
	}
	if got.TargetAgentID == nil || *got.TargetAgentID != liveAgent.ID {
		t.Fatalf("queued dispatch target agent = %v, want %d (live agent)", got.TargetAgentID, liveAgent.ID)
	}
}

// TestMatcherTickDetailed_ReturnsBindDetail (BACI-160 gap 1) locks in
// TickDetailed's contract: one Bind entry per successful queued→pending
// transition with the bound agent's name + dispatch id surfaced so an
// audit-writing caller (controller.MatchIfLeader) has enough context
// to record the agent.bind row.
func TestMatcherTickDetailed_ReturnsBindDetail(t *testing.T) {
	b := newFakeBackend()
	b.addRepo(1, "MINI")
	b.addTemplate("plan", 0)
	b.addFreeAgent(1, 10, "otter")
	b.addQueued(1, model.DispatchMode("plan"), 42)

	binds, err := New(b).TickDetailed()
	if err != nil {
		t.Fatalf("TickDetailed: %v", err)
	}
	if len(binds) != 1 {
		t.Fatalf("binds = %d, want 1", len(binds))
	}
	if binds[0].AgentName != "otter" {
		t.Fatalf("bind AgentName = %q, want otter", binds[0].AgentName)
	}
	if binds[0].Dispatch == nil || binds[0].Dispatch.ID != 42 {
		t.Fatalf("bind Dispatch = %+v, want dispatch id 42", binds[0].Dispatch)
	}
}

// TestMatcherTickDetailed_EmptyOnNoBind: a tick that doesn't bind
// anything (no free agent) returns an empty slice and nil error —
// the audit caller's loop must safely no-op on that shape.
func TestMatcherTickDetailed_EmptyOnNoBind(t *testing.T) {
	b := newFakeBackend()
	b.addRepo(1, "MINI")
	b.addTemplate("plan", 0)
	b.addQueued(1, model.DispatchMode("plan"), 1) // no free agent

	binds, err := New(b).TickDetailed()
	if err != nil {
		t.Fatalf("TickDetailed: %v", err)
	}
	if len(binds) != 0 {
		t.Fatalf("binds = %+v, want empty slice", binds)
	}
}

// TestMatcherTick_TwoBranchesShipConcurrently (BACI-227) — the
// headline win: a `ship` cap of 1 used to serialise every branch
// behind one slot; now feat/A and feat/B each get their own slot.
// Two queued ships on two different feature branches with two free
// agents must both bind within one tick.
func TestMatcherTick_TwoBranchesShipConcurrently(t *testing.T) {
	b := newFakeBackend()
	b.addRepo(1, "MINI")
	b.addTemplate("ship", 1)
	b.addFreeAgent(1, 10, "otter")
	b.addFreeAgent(1, 11, "lynx")
	b.addQueuedBranch(1, model.DispatchMode("ship"), "feat/A", 1)
	b.addQueuedBranch(1, model.DispatchMode("ship"), "feat/B", 2)

	n, err := New(b).Tick()
	if err != nil {
		t.Fatalf("Tick: %v", err)
	}
	if n != 1 {
		// Per the tickRepo contract one bind per (repo, mode) per
		// tick is the invariant — a second eligible row for the same
		// mode but a different branch waits for the next tick.
		t.Fatalf("bind count = %d, want 1 (one bind per tickMode call; second branch picks up next tick)", n)
	}
	// Run a second tick — the other branch's row should bind now.
	n2, err := New(b).Tick()
	if err != nil {
		t.Fatalf("Tick 2: %v", err)
	}
	if n2 != 1 {
		t.Fatalf("second tick bind count = %d, want 1 (the other branch's queued row)", n2)
	}
	if len(b.binds) != 2 {
		t.Fatalf("total binds = %+v, want 2 (one per branch across two ticks)", b.binds)
	}
	boundIDs := map[int64]bool{b.binds[0].dispatchID: true, b.binds[1].dispatchID: true}
	if !boundIDs[1] || !boundIDs[2] {
		t.Fatalf("expected both dispatch ids 1 and 2 bound, got %+v", b.binds)
	}
}

// TestMatcherTick_FeatureAndMainShipConcurrently (BACI-227) — a
// feature branch and main are independent slots too. A `ship` cap of
// 1 with one row each on feat/A and main + two free agents must bind
// both across consecutive ticks.
func TestMatcherTick_FeatureAndMainShipConcurrently(t *testing.T) {
	b := newFakeBackend()
	b.addRepo(1, "MINI")
	b.addTemplate("ship", 1)
	b.addFreeAgent(1, 10, "otter")
	b.addFreeAgent(1, 11, "lynx")
	// Mix of explicit "main" and a feature branch — branchOf must
	// fold both to distinct branch keys so they don't share a slot.
	b.addQueuedBranch(1, model.DispatchMode("ship"), "feat/A", 1)
	b.addQueuedBranch(1, model.DispatchMode("ship"), "main", 2)

	matcher := New(b)
	if _, err := matcher.Tick(); err != nil {
		t.Fatalf("Tick 1: %v", err)
	}
	if _, err := matcher.Tick(); err != nil {
		t.Fatalf("Tick 2: %v", err)
	}
	if len(b.binds) != 2 {
		t.Fatalf("total binds = %+v, want 2 (one per branch across two ticks)", b.binds)
	}
}

// TestMatcherTick_SameBranchTwoShipsStillSerialised (BACI-227) — the
// per-tick branch cache must hold: two queued ships on the same
// branch with two free agents bind exactly one in this tick. The
// second row sees branchCounts["feat/A"]=1 after the bind bumps it
// and stays queued for the next tick. Locks in that the cache is
// updated post-bind so a second eligible row for the same branch is
// correctly held.
func TestMatcherTick_SameBranchTwoShipsStillSerialised(t *testing.T) {
	b := newFakeBackend()
	b.addRepo(1, "MINI")
	b.addTemplate("ship", 1)
	b.addFreeAgent(1, 10, "otter")
	b.addFreeAgent(1, 11, "lynx")
	b.addQueuedBranch(1, model.DispatchMode("ship"), "feat/A", 1, 2)

	n, err := New(b).Tick()
	if err != nil {
		t.Fatalf("Tick: %v", err)
	}
	if n != 1 {
		t.Fatalf("bind count = %d, want 1 (same-branch second row must wait for next tick)", n)
	}
	if b.binds[0].dispatchID != 1 {
		t.Fatalf("bound dispatch id = %d, want 1 (older first)", b.binds[0].dispatchID)
	}
	// Row 2 still queued for the next tick.
	stillQueued := false
	for _, list := range b.queuedByRM {
		for _, d := range list {
			if d.ID == 2 {
				stillQueued = true
			}
		}
	}
	if !stillQueued {
		t.Fatalf("expected dispatch 2 to remain queued (cap should hold inside one tick)")
	}
}

// TestMatcherTick_PerIssueOverridePullsBackToMain (BACI-227) — a
// per-issue base_branch override wins over the parent feature for
// cap grouping. Issue A inherits feat/X from its feature; issue B
// has its own "main" override. With one ship already in flight on
// main, the matcher binds A (feat/X slot is free) but skips B
// (main slot is taken). Proves the resolver value on the dispatch
// row — not the feature's branch_name — drives the cap.
func TestMatcherTick_PerIssueOverridePullsBackToMain(t *testing.T) {
	b := newFakeBackend()
	b.addRepo(1, "MINI")
	b.addTemplate("ship", 1)
	b.addFreeAgent(1, 10, "otter")
	b.addFreeAgent(1, 11, "lynx")

	// Queue (oldest first): A on feat/X, then B on main (override).
	b.addQueuedBranch(1, model.DispatchMode("ship"), "feat/X", 1)
	b.addQueuedBranch(1, model.DispatchMode("ship"), "main", 2)

	// Inflight: one ship on main, holding the main slot.
	b.setInFlightBranch(1, model.DispatchMode("ship"), "main", 1)

	n, err := New(b).Tick()
	if err != nil {
		t.Fatalf("Tick: %v", err)
	}
	if n != 1 {
		t.Fatalf("bind count = %d, want 1 (only the feat/X row should bind)", n)
	}
	if b.binds[0].dispatchID != 1 {
		t.Fatalf("bound dispatch id = %d, want 1 (feat/X row, main slot is taken)", b.binds[0].dispatchID)
	}
	// Row 2 (on main) stays queued — the main slot is full.
	stillQueued := false
	for _, list := range b.queuedByRM {
		for _, d := range list {
			if d.ID == 2 {
				stillQueued = true
			}
		}
	}
	if !stillQueued {
		t.Fatalf("expected dispatch 2 (main) to remain queued — main slot was taken")
	}
}

// TestMatcherTick_CappedHeadDoesNotBlockUncappedTail (BACI-227) —
// the queue-walk semantic: the FIFO head's branch must not lock out
// younger rows for other branches. Inflight: one ship on feat/A
// holding feat/A's slot. Queue (oldest first): feat/A (capped),
// feat/B (free). The matcher must skip the head and bind the tail.
func TestMatcherTick_CappedHeadDoesNotBlockUncappedTail(t *testing.T) {
	b := newFakeBackend()
	b.addRepo(1, "MINI")
	b.addTemplate("ship", 1)
	b.addFreeAgent(1, 10, "otter")

	b.addQueuedBranch(1, model.DispatchMode("ship"), "feat/A", 1)
	b.addQueuedBranch(1, model.DispatchMode("ship"), "feat/B", 2)
	b.setInFlightBranch(1, model.DispatchMode("ship"), "feat/A", 1)

	n, err := New(b).Tick()
	if err != nil {
		t.Fatalf("Tick: %v", err)
	}
	if n != 1 {
		t.Fatalf("bind count = %d, want 1 (feat/B should bind past capped feat/A head)", n)
	}
	if b.binds[0].dispatchID != 2 {
		t.Fatalf("bound dispatch id = %d, want 2 (feat/B — head feat/A is capped)", b.binds[0].dispatchID)
	}
	// Head row stays queued for the next tick — feat/A is still at cap.
	stillQueued := false
	for _, list := range b.queuedByRM {
		for _, d := range list {
			if d.ID == 1 {
				stillQueued = true
			}
		}
	}
	if !stillQueued {
		t.Fatalf("expected dispatch 1 (capped feat/A head) to remain queued")
	}

	// Per-tick caching invariant — feat/A counted exactly once,
	// regardless of how many feat/A rows are in the queue.
	k := branchKey{repoID: 1, mode: model.DispatchMode("ship"), branch: "feat/A"}
	if got := b.countBaseHits[k]; got != 1 {
		t.Errorf("feat/A counted %d times in one tick, want 1 (per-tick cache must dedupe)", got)
	}
}
