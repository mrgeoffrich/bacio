package dispatcher

import (
	"testing"
	"time"

	"github.com/mrgeoffrich/bacio/internal/model"
	"github.com/mrgeoffrich/bacio/internal/store"
)

// fakeBackend is an in-memory Backend stand-in for Matcher.Tick tests.
// Only the fields the matcher reads are populated; the rest of the
// store surface is left out.
type fakeBackend struct {
	repos       []*model.Repo
	templates   []*store.PromptTemplate
	queuedByRM  map[queueKey][]*model.AgentDispatch
	inFlightByM map[queueKey]int
	sessions    map[int64][]*model.AgentSession
	openClaims  map[int64]map[int64][]*model.AgentClaim
	dispatches  map[int64][]*model.AgentDispatch
	agents      map[string]*model.Agent

	binds          []bindCall          // recorded in BindQueuedDispatch order
	bindErr        map[int64]error     // per-dispatch-id override (e.g. ErrNotFound)
	listQueuedHits map[queueKey]int    // call counts for ListQueuedByRepoMode
	pickCalls      [][]AgentCandidate  // captures the slice passed to AutoPickFreeAgent
}

type queueKey struct {
	repoID int64
	mode   model.DispatchMode
}

type bindCall struct {
	dispatchID int64
	agentID    int64
}

func newFakeBackend() *fakeBackend {
	return &fakeBackend{
		queuedByRM:     map[queueKey][]*model.AgentDispatch{},
		inFlightByM:    map[queueKey]int{},
		sessions:       map[int64][]*model.AgentSession{},
		openClaims:     map[int64]map[int64][]*model.AgentClaim{},
		dispatches:     map[int64][]*model.AgentDispatch{},
		agents:         map[string]*model.Agent{},
		bindErr:        map[int64]error{},
		listQueuedHits: map[queueKey]int{},
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
	// One ship-it already in flight: matcher should refuse to bind a second.
	b.setInFlight(1, model.DispatchMode("ship"), 1)

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
