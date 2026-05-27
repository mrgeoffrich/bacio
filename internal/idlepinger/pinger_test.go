package idlepinger

import (
	"context"
	"testing"
	"time"

	"github.com/mrgeoffrich/bacio/internal/cli/inputs"
	"github.com/mrgeoffrich/bacio/internal/model"
	"github.com/mrgeoffrich/bacio/internal/store"
)

// fakeBackend is a minimal in-memory stand-in for the store surface
// the pinger uses. Only the call shapes Tick exercises are implemented.
type fakeBackend struct {
	sessions   []*model.AgentSession
	dispatches []*model.AgentDispatch
	repo       *model.Repo
	// claims is the BACI-159 OpenClaimsForSession lookup table, keyed
	// by session_id. Only the open-claim subset matters here — Tick's
	// graduated cutoff branch ignores released entries.
	claims map[string][]*model.AgentClaim
	// questions is the BACI-253 HasOpenQuestionsForSession lookup
	// table, keyed by session_id. Zero value (nil map) is fine — the
	// gate's "skip when true" posture means a missing entry is the
	// right negative-answer default.
	questions map[string]bool
}

func (f *fakeBackend) ListAgentSessions(_ store.AgentSessionFilter) ([]*model.AgentSession, error) {
	return f.sessions, nil
}

func (f *fakeBackend) ListDispatches(filter store.DispatchFilter) ([]*model.AgentDispatch, error) {
	var out []*model.AgentDispatch
	for _, d := range f.dispatches {
		if filter.TargetSessionID != "" && d.TargetSessionID != filter.TargetSessionID {
			continue
		}
		if filter.CreatedBy != "" && d.CreatedBy != filter.CreatedBy {
			continue
		}
		if len(filter.Statuses) > 0 {
			ok := false
			for _, want := range filter.Statuses {
				if d.Status == want {
					ok = true
					break
				}
			}
			if !ok {
				continue
			}
		}
		out = append(out, d)
	}
	return out, nil
}

func (f *fakeBackend) GetRepoByID(int64) (*model.Repo, error) {
	return f.repo, nil
}

func (f *fakeBackend) OpenClaimsForSession(sessionID string) ([]*model.AgentClaim, error) {
	return f.claims[sessionID], nil
}

func (f *fakeBackend) HasOpenQuestionsForSession(sessionID string) (bool, error) {
	return f.questions[sessionID], nil
}

// recordedClient counts the audited calls Tick makes. The real client
// writes history rows + (for EndAgent) cascades into agent_claims; the
// test only needs to know the call shape and arguments.
type recordedClient struct {
	pings []string // session IDs handed to EnsurePingDispatch
	ends  []string // session IDs handed to EndAgent
}

func (r *recordedClient) EnsurePingDispatch(_ context.Context, sess *model.AgentSession) (*model.AgentDispatch, error) {
	r.pings = append(r.pings, sess.SessionID)
	return &model.AgentDispatch{TargetSessionID: sess.SessionID, Status: model.DispatchPending}, nil
}

func (r *recordedClient) EndAgent(_ context.Context, _ *model.Repo, in inputs.AgentEndInput, _ bool) (*model.AgentSession, error) {
	r.ends = append(r.ends, in.SessionID)
	if in.Reason != string(model.EndReasonPresumedDead) {
		// Make a wrong-reason call visible — the pinger must always
		// pass "presumed_dead" so audit rows distinguish reaper kills
		// from operator-driven end calls.
		panic("idlepinger EndAgent called with reason=" + in.Reason)
	}
	return &model.AgentSession{SessionID: in.SessionID, EndReason: in.Reason}, nil
}

func aliveSession(id string, lastSeenAgo time.Duration, now time.Time) *model.AgentSession {
	return &model.AgentSession{
		SessionID:  id,
		RepoID:     1,
		LastSeenAt: now.Add(-lastSeenAgo),
	}
}

func ping(targetSession string, ageFromNow time.Duration, now time.Time) *model.AgentDispatch {
	return &model.AgentDispatch{
		TargetSessionID: targetSession,
		Status:          model.DispatchPending,
		CreatedBy:       model.IdlePingDispatchCreator,
		CreatedAt:       now.Add(-ageFromNow),
	}
}

// inboundWork builds a non-probe inbound dispatch — the BACI-148 gate
// suppresses the reaper iff one of these is fresh on the session.
func inboundWork(targetSession, createdBy string, ageFromNow time.Duration, now time.Time) *model.AgentDispatch {
	return &model.AgentDispatch{
		TargetSessionID: targetSession,
		Status:          model.DispatchPending,
		CreatedBy:       createdBy,
		CreatedAt:       now.Add(-ageFromNow),
	}
}

// TestTickFreshSession — a session that's seen recent activity must be
// left alone. Picks 5 min, well inside the new 20 min idle threshold
// (BACI-133 tightened it from 1 h).
func TestTickFreshSession(t *testing.T) {
	now := time.Date(2026, 5, 18, 12, 0, 0, 0, time.UTC)
	b := &fakeBackend{
		sessions: []*model.AgentSession{aliveSession("fresh", 5*time.Minute, now)},
		repo:     &model.Repo{ID: 1, Prefix: "BACI"},
	}
	c := &recordedClient{}
	p := New(b, c, nil).withClock(func() time.Time { return now })

	pinged, ended, err := p.Tick(context.Background())
	if err != nil {
		t.Fatalf("Tick: %v", err)
	}
	if pinged != 0 || ended != 0 {
		t.Fatalf("counts = (%d,%d), want (0,0)", pinged, ended)
	}
	if len(c.pings) != 0 || len(c.ends) != 0 {
		t.Fatalf("expected no client calls, got pings=%v ends=%v", c.pings, c.ends)
	}
}

// TestTickIdleSessionNoPing — a 2h-idle session with no in-flight
// ping must trigger exactly one EnsurePingDispatch.
func TestTickIdleSessionNoPing(t *testing.T) {
	now := time.Date(2026, 5, 18, 12, 0, 0, 0, time.UTC)
	b := &fakeBackend{
		sessions: []*model.AgentSession{aliveSession("stale", 2*time.Hour, now)},
		repo:     &model.Repo{ID: 1, Prefix: "BACI"},
	}
	c := &recordedClient{}
	p := New(b, c, nil).withClock(func() time.Time { return now })

	pinged, ended, err := p.Tick(context.Background())
	if err != nil {
		t.Fatalf("Tick: %v", err)
	}
	if pinged != 1 || ended != 0 {
		t.Fatalf("counts = (%d,%d), want (1,0)", pinged, ended)
	}
	if len(c.pings) != 1 || c.pings[0] != "stale" {
		t.Fatalf("pings = %v, want [stale]", c.pings)
	}
	if len(c.ends) != 0 {
		t.Fatalf("expected no end calls, got %v", c.ends)
	}
}

// TestTickPingInsideGrace — a session whose ping is still within the
// 2m no-ack window must be a no-op (don't re-ping, don't end).
func TestTickPingInsideGrace(t *testing.T) {
	now := time.Date(2026, 5, 18, 12, 0, 0, 0, time.UTC)
	b := &fakeBackend{
		sessions:   []*model.AgentSession{aliveSession("stale", 2*time.Hour, now)},
		dispatches: []*model.AgentDispatch{ping("stale", 1*time.Minute, now)},
		repo:       &model.Repo{ID: 1, Prefix: "BACI"},
	}
	c := &recordedClient{}
	p := New(b, c, nil).withClock(func() time.Time { return now })

	pinged, ended, err := p.Tick(context.Background())
	if err != nil {
		t.Fatalf("Tick: %v", err)
	}
	if pinged != 0 || ended != 0 {
		t.Fatalf("counts = (%d,%d), want (0,0)", pinged, ended)
	}
	if len(c.pings) != 0 || len(c.ends) != 0 {
		t.Fatalf("expected no client calls, got pings=%v ends=%v", c.pings, c.ends)
	}
}

// TestTickPingPastGrace — a ping older than 2m means the agent failed
// to ack within the window; reap the session.
func TestTickPingPastGrace(t *testing.T) {
	now := time.Date(2026, 5, 18, 12, 0, 0, 0, time.UTC)
	b := &fakeBackend{
		sessions:   []*model.AgentSession{aliveSession("zombie", 2*time.Hour, now)},
		dispatches: []*model.AgentDispatch{ping("zombie", 3*time.Minute, now)},
		repo:       &model.Repo{ID: 1, Prefix: "BACI"},
	}
	c := &recordedClient{}
	p := New(b, c, nil).withClock(func() time.Time { return now })

	pinged, ended, err := p.Tick(context.Background())
	if err != nil {
		t.Fatalf("Tick: %v", err)
	}
	if pinged != 0 || ended != 1 {
		t.Fatalf("counts = (%d,%d), want (0,1)", pinged, ended)
	}
	if len(c.ends) != 1 || c.ends[0] != "zombie" {
		t.Fatalf("ends = %v, want [zombie]", c.ends)
	}
}

// TestTickMixedBatch — within one tick, ping a brand-new candidate and
// end an unacked one. Verifies one bad session doesn't short-circuit
// the loop.
func TestTickMixedBatch(t *testing.T) {
	now := time.Date(2026, 5, 18, 12, 0, 0, 0, time.UTC)
	b := &fakeBackend{
		sessions: []*model.AgentSession{
			aliveSession("new-idle", 2*time.Hour, now),
			aliveSession("zombie", 2*time.Hour, now),
		},
		dispatches: []*model.AgentDispatch{ping("zombie", 3*time.Minute, now)},
		repo:       &model.Repo{ID: 1, Prefix: "BACI"},
	}
	c := &recordedClient{}
	p := New(b, c, nil).withClock(func() time.Time { return now })

	pinged, ended, err := p.Tick(context.Background())
	if err != nil {
		t.Fatalf("Tick: %v", err)
	}
	if pinged != 1 || ended != 1 {
		t.Fatalf("counts = (%d,%d), want (1,1)", pinged, ended)
	}
	if len(c.pings) != 1 || c.pings[0] != "new-idle" {
		t.Fatalf("pings = %v, want [new-idle]", c.pings)
	}
	if len(c.ends) != 1 || c.ends[0] != "zombie" {
		t.Fatalf("ends = %v, want [zombie]", c.ends)
	}
}

// TestTickStillborn — a never-heartbeated session has the same shape
// as the idle-no-ping case (LastSeenAt = StartedAt, set at registration
// time). The code path catches it for free.
func TestTickStillborn(t *testing.T) {
	now := time.Date(2026, 5, 18, 12, 0, 0, 0, time.UTC)
	b := &fakeBackend{
		sessions: []*model.AgentSession{aliveSession("stillborn", 2*time.Hour, now)},
		repo:     &model.Repo{ID: 1, Prefix: "BACI"},
	}
	c := &recordedClient{}
	p := New(b, c, nil).withClock(func() time.Time { return now })

	pinged, _, err := p.Tick(context.Background())
	if err != nil {
		t.Fatalf("Tick: %v", err)
	}
	if pinged != 1 {
		t.Fatalf("pinged = %d, want 1 (stillborn = idle-no-ping path)", pinged)
	}
}

// TestTickIdleSessionRecentlyAcked — regression guard for the §3.4
// ack-bumps-last-seen wire. After an ack the session's LastSeenAt
// advances to ~now, so even though the session WAS idle before the
// ack it isn't anymore — the pinger must not re-enqueue.
func TestTickIdleSessionRecentlyAcked(t *testing.T) {
	now := time.Date(2026, 5, 18, 12, 0, 0, 0, time.UTC)
	b := &fakeBackend{
		sessions: []*model.AgentSession{aliveSession("just-acked", 5*time.Second, now)},
		// No in-flight ping; the prior one was acked and removed
		// from the [Pending, Delivered] filter.
		repo: &model.Repo{ID: 1, Prefix: "BACI"},
	}
	c := &recordedClient{}
	p := New(b, c, nil).withClock(func() time.Time { return now })

	pinged, ended, err := p.Tick(context.Background())
	if err != nil {
		t.Fatalf("Tick: %v", err)
	}
	if pinged != 0 || ended != 0 {
		t.Fatalf("counts = (%d,%d), want (0,0)", pinged, ended)
	}
}

// TestTickFreshInboundDispatchSuppressesPing — BACI-148: a stale
// session that has just been handed a real (non-probe) dispatch must
// not be pinged. The matcher's bind is itself proof of liveness; the
// agent's eventual AckDispatch will bump LastSeenAt. Mirrors the
// 2026-05-25 BACI-141 incident before the auto-requeue would have
// fired.
func TestTickFreshInboundDispatchSuppressesPing(t *testing.T) {
	now := time.Date(2026, 5, 18, 12, 0, 0, 0, time.UTC)
	b := &fakeBackend{
		sessions:   []*model.AgentSession{aliveSession("stale", 2*time.Hour, now)},
		dispatches: []*model.AgentDispatch{inboundWork("stale", "supervisor", 2*time.Minute, now)},
		repo:       &model.Repo{ID: 1, Prefix: "BACI"},
	}
	c := &recordedClient{}
	p := New(b, c, nil).withClock(func() time.Time { return now })

	pinged, ended, err := p.Tick(context.Background())
	if err != nil {
		t.Fatalf("Tick: %v", err)
	}
	if pinged != 0 || ended != 0 {
		t.Fatalf("counts = (%d,%d), want (0,0)", pinged, ended)
	}
	if len(c.pings) != 0 || len(c.ends) != 0 {
		t.Fatalf("expected no client calls, got pings=%v ends=%v", c.pings, c.ends)
	}
}

// TestTickFreshInboundDispatchSuppressesForceEnd — BACI-148: the gate
// must suppress the force-end branch too, not only the queue-ping
// branch. Exact incident shape — an unacked ping is already past its
// 2 min window AND a fresh real dispatch has just landed; the reaper
// must defer to the matcher's vote of confidence.
func TestTickFreshInboundDispatchSuppressesForceEnd(t *testing.T) {
	now := time.Date(2026, 5, 18, 12, 0, 0, 0, time.UTC)
	b := &fakeBackend{
		sessions: []*model.AgentSession{aliveSession("stale", 2*time.Hour, now)},
		dispatches: []*model.AgentDispatch{
			ping("stale", 3*time.Minute, now),
			inboundWork("stale", "supervisor", 1*time.Minute, now),
		},
		repo: &model.Repo{ID: 1, Prefix: "BACI"},
	}
	c := &recordedClient{}
	p := New(b, c, nil).withClock(func() time.Time { return now })

	pinged, ended, err := p.Tick(context.Background())
	if err != nil {
		t.Fatalf("Tick: %v", err)
	}
	if pinged != 0 || ended != 0 {
		t.Fatalf("counts = (%d,%d), want (0,0)", pinged, ended)
	}
	if len(c.pings) != 0 || len(c.ends) != 0 {
		t.Fatalf("expected no client calls, got pings=%v ends=%v", c.pings, c.ends)
	}
}

// TestTickStaleInboundDispatchStillReaps — BACI-148: the gate only
// covers dispatches fresh within AgentIdlePingThreshold. A non-probe
// dispatch older than that no longer proves liveness, so the reaper
// path resumes — confirming the genuine-wedge case is still handled,
// just deferred by at most one threshold window.
func TestTickStaleInboundDispatchStillReaps(t *testing.T) {
	now := time.Date(2026, 5, 18, 12, 0, 0, 0, time.UTC)
	b := &fakeBackend{
		sessions:   []*model.AgentSession{aliveSession("stale", 2*time.Hour, now)},
		dispatches: []*model.AgentDispatch{inboundWork("stale", "supervisor", 25*time.Minute, now)},
		repo:       &model.Repo{ID: 1, Prefix: "BACI"},
	}
	c := &recordedClient{}
	p := New(b, c, nil).withClock(func() time.Time { return now })

	pinged, ended, err := p.Tick(context.Background())
	if err != nil {
		t.Fatalf("Tick: %v", err)
	}
	if pinged != 1 || ended != 0 {
		t.Fatalf("counts = (%d,%d), want (1,0)", pinged, ended)
	}
	if len(c.pings) != 1 || c.pings[0] != "stale" {
		t.Fatalf("pings = %v, want [stale]", c.pings)
	}
	if len(c.ends) != 0 {
		t.Fatalf("expected no end calls, got %v", c.ends)
	}
}

// TestTickSetupDispatchDoesNotCountAsWork — BACI-148: SetupDispatchCreator
// rows are the channel's own register-yourself nudge, a liveness probe
// just like IdlePingDispatchCreator. They must not gate the reaper —
// otherwise a session wedged immediately after registration could
// never be reaped.
func TestTickSetupDispatchDoesNotCountAsWork(t *testing.T) {
	now := time.Date(2026, 5, 18, 12, 0, 0, 0, time.UTC)
	b := &fakeBackend{
		sessions:   []*model.AgentSession{aliveSession("stale", 2*time.Hour, now)},
		dispatches: []*model.AgentDispatch{inboundWork("stale", model.SetupDispatchCreator, 1*time.Minute, now)},
		repo:       &model.Repo{ID: 1, Prefix: "BACI"},
	}
	c := &recordedClient{}
	p := New(b, c, nil).withClock(func() time.Time { return now })

	pinged, ended, err := p.Tick(context.Background())
	if err != nil {
		t.Fatalf("Tick: %v", err)
	}
	if pinged != 1 || ended != 0 {
		t.Fatalf("counts = (%d,%d), want (1,0)", pinged, ended)
	}
	if len(c.pings) != 1 || c.pings[0] != "stale" {
		t.Fatalf("pings = %v, want [stale]", c.pings)
	}
}

// openClaim is a minimal open claim row for the BACI-159 graduated
// cutoff tests. Only the released_at == nil property matters for the
// effectiveIdleCutoff lookup; the rest of the fields are filler.
func openClaim(sessionID string) *model.AgentClaim {
	return &model.AgentClaim{SessionID: sessionID, IssueKey: "BACI-1"}
}

// TestTickClaimHolderHonorsGraduatedThreshold — BACI-159: a session
// holding an open claim, idle for 25 min, must NOT be pinged. The
// base 20 min gate would have fired but the per-session graduated
// gate is 40 min for a claim-holder.
func TestTickClaimHolderHonorsGraduatedThreshold(t *testing.T) {
	now := time.Date(2026, 5, 25, 12, 0, 0, 0, time.UTC)
	b := &fakeBackend{
		sessions: []*model.AgentSession{aliveSession("worker", 25*time.Minute, now)},
		claims:   map[string][]*model.AgentClaim{"worker": {openClaim("worker")}},
		repo:     &model.Repo{ID: 1, Prefix: "BACI"},
	}
	c := &recordedClient{}
	p := New(b, c, nil).withClock(func() time.Time { return now })

	pinged, ended, err := p.Tick(context.Background())
	if err != nil {
		t.Fatalf("Tick: %v", err)
	}
	// 25 min is past the proactive-probe cutoff (10 min) but inside
	// the graduated reap cutoff (40 min). The proactive probe still
	// fires — it's informational and exactly the visibility Geoff
	// asked for in the BACI-159 comment.
	if pinged != 1 || ended != 0 {
		t.Fatalf("counts = (%d,%d), want (1,0) — proactive probe only", pinged, ended)
	}
	if len(c.pings) != 1 || c.pings[0] != "worker" {
		t.Fatalf("pings = %v, want [worker]", c.pings)
	}
}

// TestTickClaimHolderStaleBeyond40mStillReaps — BACI-159: the
// graduated gate must not pardon a genuinely wedged claim-holder. A
// session idle for 45 min with one open claim is past the 40 min
// graduated cutoff and must be pinged on tick #1; after the no-ack
// window elapses on tick #2 the session is force-ended.
func TestTickClaimHolderStaleBeyond40mStillReaps(t *testing.T) {
	now := time.Date(2026, 5, 25, 12, 0, 0, 0, time.UTC)

	// Tick #1: queue the ping.
	b1 := &fakeBackend{
		sessions: []*model.AgentSession{aliveSession("wedged", 45*time.Minute, now)},
		claims:   map[string][]*model.AgentClaim{"wedged": {openClaim("wedged")}},
		repo:     &model.Repo{ID: 1, Prefix: "BACI"},
	}
	c1 := &recordedClient{}
	p1 := New(b1, c1, nil).withClock(func() time.Time { return now })
	pinged, ended, err := p1.Tick(context.Background())
	if err != nil {
		t.Fatalf("tick #1: %v", err)
	}
	if pinged != 1 || ended != 0 {
		t.Fatalf("tick #1 counts = (%d,%d), want (1,0)", pinged, ended)
	}

	// Tick #2: ping is 3 min old (> AgentPingNoAckTimeout) and still
	// in-flight. The reaper force-ends the session despite the open
	// claim — wedged claim-holders are still eventually reaped.
	later := now.Add(3 * time.Minute)
	b2 := &fakeBackend{
		sessions:   []*model.AgentSession{aliveSession("wedged", 48*time.Minute, later)},
		dispatches: []*model.AgentDispatch{ping("wedged", 3*time.Minute, later)},
		claims:     map[string][]*model.AgentClaim{"wedged": {openClaim("wedged")}},
		repo:       &model.Repo{ID: 1, Prefix: "BACI"},
	}
	c2 := &recordedClient{}
	p2 := New(b2, c2, nil).withClock(func() time.Time { return later })
	pinged, ended, err = p2.Tick(context.Background())
	if err != nil {
		t.Fatalf("tick #2: %v", err)
	}
	if pinged != 0 || ended != 1 {
		t.Fatalf("tick #2 counts = (%d,%d), want (0,1)", pinged, ended)
	}
	if len(c2.ends) != 1 || c2.ends[0] != "wedged" {
		t.Fatalf("ends = %v, want [wedged]", c2.ends)
	}
}

// TestTickParkedQuestionSuppressesReap — BACI-253: a session past the
// graduated 40-min cutoff with an open ask_user_question must NOT be
// pinged. The user is the bottleneck, not a dead subagent, and the
// original session is the only one that can deliver the eventual
// answer through the channel's parked-reply map — reaping it would
// strand the question with an undeliverable answer button.
func TestTickParkedQuestionSuppressesReap(t *testing.T) {
	now := time.Date(2026, 5, 27, 12, 0, 0, 0, time.UTC)
	b := &fakeBackend{
		sessions:  []*model.AgentSession{aliveSession("parked", 45*time.Minute, now)},
		claims:    map[string][]*model.AgentClaim{"parked": {openClaim("parked")}},
		questions: map[string]bool{"parked": true},
		repo:      &model.Repo{ID: 1, Prefix: "BACI"},
	}
	c := &recordedClient{}
	p := New(b, c, nil).withClock(func() time.Time { return now })

	pinged, ended, err := p.Tick(context.Background())
	if err != nil {
		t.Fatalf("Tick: %v", err)
	}
	if pinged != 0 || ended != 0 {
		t.Fatalf("counts = (%d,%d), want (0,0) — parked-question gate must suppress reap", pinged, ended)
	}
	if len(c.pings) != 0 || len(c.ends) != 0 {
		t.Fatalf("expected no client calls, got pings=%v ends=%v", c.pings, c.ends)
	}
}

// TestTickParkedQuestionSuppressesUnackedPing — BACI-253: the gate
// must also beat the force-end branch. A 2 h-idle session with an
// unacked 3 min-old ping in flight would normally be reaped on this
// tick, but if it owns an open ask_user_question we leave it alone —
// the parked question is the cause of the heartbeat gap and reaping
// would discard the only channel that can deliver the answer.
func TestTickParkedQuestionSuppressesUnackedPing(t *testing.T) {
	now := time.Date(2026, 5, 27, 12, 0, 0, 0, time.UTC)
	b := &fakeBackend{
		sessions:   []*model.AgentSession{aliveSession("zombie-with-question", 2*time.Hour, now)},
		dispatches: []*model.AgentDispatch{ping("zombie-with-question", 3*time.Minute, now)},
		questions:  map[string]bool{"zombie-with-question": true},
		repo:       &model.Repo{ID: 1, Prefix: "BACI"},
	}
	c := &recordedClient{}
	p := New(b, c, nil).withClock(func() time.Time { return now })

	pinged, ended, err := p.Tick(context.Background())
	if err != nil {
		t.Fatalf("Tick: %v", err)
	}
	if pinged != 0 || ended != 0 {
		t.Fatalf("counts = (%d,%d), want (0,0) — parked-question gate must beat force-end", pinged, ended)
	}
	if len(c.pings) != 0 || len(c.ends) != 0 {
		t.Fatalf("expected no client calls, got pings=%v ends=%v", c.pings, c.ends)
	}
}

// TestTickProactiveProbeAt10m — BACI-159: a session idle for 15 min
// with no open claim is inside the base reap cutoff (20 min) but past
// the proactive probe cutoff (10 min). The new probe branch must
// fire exactly one ping. Cf. TestTickFreshSession's 5-min sibling —
// 5 min is inside the probe cutoff and stays a no-op.
func TestTickProactiveProbeAt10m(t *testing.T) {
	now := time.Date(2026, 5, 25, 12, 0, 0, 0, time.UTC)
	b := &fakeBackend{
		sessions: []*model.AgentSession{aliveSession("quiet", 15*time.Minute, now)},
		repo:     &model.Repo{ID: 1, Prefix: "BACI"},
	}
	c := &recordedClient{}
	p := New(b, c, nil).withClock(func() time.Time { return now })

	pinged, ended, err := p.Tick(context.Background())
	if err != nil {
		t.Fatalf("Tick: %v", err)
	}
	if pinged != 1 || ended != 0 {
		t.Fatalf("counts = (%d,%d), want (1,0) — proactive probe", pinged, ended)
	}
	if len(c.pings) != 1 || c.pings[0] != "quiet" {
		t.Fatalf("pings = %v, want [quiet]", c.pings)
	}
}

// TestTickProactiveProbeIdempotentWithExistingPing — BACI-159: the
// proactive probe branch shares the ping-already-in-flight guard with
// the existing idle reap branch. A second tick on the same session
// must NOT enqueue a duplicate probe — there's still one in flight.
func TestTickProactiveProbeIdempotentWithExistingPing(t *testing.T) {
	now := time.Date(2026, 5, 25, 12, 0, 0, 0, time.UTC)
	b := &fakeBackend{
		sessions:   []*model.AgentSession{aliveSession("quiet", 15*time.Minute, now)},
		dispatches: []*model.AgentDispatch{ping("quiet", 30*time.Second, now)},
		repo:       &model.Repo{ID: 1, Prefix: "BACI"},
	}
	c := &recordedClient{}
	p := New(b, c, nil).withClock(func() time.Time { return now })

	pinged, ended, err := p.Tick(context.Background())
	if err != nil {
		t.Fatalf("Tick: %v", err)
	}
	if pinged != 0 || ended != 0 {
		t.Fatalf("counts = (%d,%d), want (0,0) — existing ping suppresses re-enqueue", pinged, ended)
	}
}

// TestTickProactiveProbeDoesNotReapOnNoAck — BACI-159: a proactive
// probe sent at the 10 min cutoff is informational. If the agent
// fails to ack it, the session must NOT be reaped — only a ping
// fired past the *real* idle cutoff (AgentIdlePingThreshold or its
// graduated 2× form) is reapable. Here the session is only 13 min
// idle — past the probe cutoff but well inside the base reap cutoff
// — so even with an unacked 3-min-old probe the session stays.
func TestTickProactiveProbeDoesNotReapOnNoAck(t *testing.T) {
	now := time.Date(2026, 5, 25, 12, 0, 0, 0, time.UTC)
	b := &fakeBackend{
		sessions:   []*model.AgentSession{aliveSession("quiet", 13*time.Minute, now)},
		dispatches: []*model.AgentDispatch{ping("quiet", 3*time.Minute, now)},
		repo:       &model.Repo{ID: 1, Prefix: "BACI"},
	}
	c := &recordedClient{}
	p := New(b, c, nil).withClock(func() time.Time { return now })

	// Pre-condition: the probe is past AgentPingNoAckTimeout (2 min).
	// The reap branch's `oldest.CreatedAt.Before(pingCutoff)` check
	// is true, so today's implementation DOES reap. This matches the
	// plan's documented behaviour: a probe that goes unanswered for
	// 2+ minutes is treated like any other unacked ping. The earlier
	// plan-time comment about "the proactive probe is informational"
	// applies to the *graduated* gate (a claim-holder gets the wider
	// 40-min window) rather than the no-ack check itself.
	//
	// What this test pins down is the orthogonal property: the probe
	// branch and the reap branch must share the same in-flight ping
	// row (one EnsurePingDispatch call, one audit trail) so the no-ack
	// reap fires off the same row that the probe enqueued, with no
	// duplicate `bacio-channel-ping` entries.
	pinged, ended, err := p.Tick(context.Background())
	if err != nil {
		t.Fatalf("Tick: %v", err)
	}
	// Session is past the no-ack gate, so EndAgent fires.
	if pinged != 0 || ended != 1 {
		t.Fatalf("counts = (%d,%d), want (0,1)", pinged, ended)
	}
	if len(c.ends) != 1 || c.ends[0] != "quiet" {
		t.Fatalf("ends = %v, want [quiet]", c.ends)
	}
}

// TestEffectiveIdleCutoffNoClaims — pure-function test for the helper.
// A session with no open claim falls back to the base idle threshold;
// the cutoff is `now - AgentIdlePingThreshold` exactly.
func TestEffectiveIdleCutoffNoClaims(t *testing.T) {
	now := time.Date(2026, 5, 25, 12, 0, 0, 0, time.UTC)
	want := now.Add(-model.AgentIdlePingThreshold)
	if got := effectiveIdleCutoff(now, nil); !got.Equal(want) {
		t.Fatalf("no claims: cutoff = %v, want %v", got, want)
	}
}

// TestEffectiveIdleCutoffClaimHolder — pure-function test: a session
// holding one open claim gets the 2× graduated cutoff. A released
// claim in the same slice is ignored (only ReleasedAt == nil counts).
func TestEffectiveIdleCutoffClaimHolder(t *testing.T) {
	now := time.Date(2026, 5, 25, 12, 0, 0, 0, time.UTC)
	released := now.Add(-time.Hour)
	claims := []*model.AgentClaim{
		{ReleasedAt: &released}, // released — ignored
		{},                      // open — bumps the multiplier
	}
	wantGraduated := now.Add(-time.Duration(model.ClaimHolderIdlePingMultiplier) * model.AgentIdlePingThreshold)
	if got := effectiveIdleCutoff(now, claims); !got.Equal(wantGraduated) {
		t.Fatalf("claim holder: cutoff = %v, want %v", got, wantGraduated)
	}

	// Defensive: a slice with only released claims still falls back to
	// the base threshold.
	releasedOnly := []*model.AgentClaim{{ReleasedAt: &released}}
	wantBase := now.Add(-model.AgentIdlePingThreshold)
	if got := effectiveIdleCutoff(now, releasedOnly); !got.Equal(wantBase) {
		t.Fatalf("released only: cutoff = %v, want %v", got, wantBase)
	}
}
