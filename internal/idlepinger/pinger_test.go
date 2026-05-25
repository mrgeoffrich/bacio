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
