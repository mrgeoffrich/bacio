// Package idlepinger is the leader-gated reaper that queues "are you
// still alive?" channel pings to long-idle agent sessions and
// force-ends them if no ack arrives within model.AgentPingNoAckTimeout
// (BACI-57).
//
// Composes the existing primitives: enumerate sessions via the store,
// queue pings via client.EnsurePingDispatch (audited), and force-end
// via client.EndAgent (audited, auto-releases claims and clears
// assignees). No new infrastructure.
//
// BACI-148 added a per-tick busy-work gate in front of both branches:
// if any fresh non-probe inbound dispatch is in flight for the session
// the reaper leaves it alone, because the matcher's bind is itself
// proof the session was healthy moments ago.
//
// Mirrors internal/dispatcher's shape: a Backend interface declaring
// the minimal store surface needed, a Client interface declaring the
// audited mutation surface, a Pinger struct, and a Tick that one
// per-tick helper (controller.PingIfLeader) calls under the
// ui_leader gate.
package idlepinger

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/mrgeoffrich/bacio/internal/cli/inputs"
	"github.com/mrgeoffrich/bacio/internal/model"
	"github.com/mrgeoffrich/bacio/internal/store"
)

// Backend is the minimal store surface the pinger needs. *store.Store
// satisfies it directly.
type Backend interface {
	ListAgentSessions(store.AgentSessionFilter) ([]*model.AgentSession, error)
	ListDispatches(store.DispatchFilter) ([]*model.AgentDispatch, error)
	GetRepoByID(int64) (*model.Repo, error)
	// OpenClaimsForSession is the BACI-159 graduated-cutoff lookup: a
	// session holding at least one open claim is "mid-job" and gets
	// double the slack before the reaper fires. Single indexed
	// session-keyed query; the pinger's session list is small enough
	// that the per-tick cost is dwarfed by the existing ListDispatches
	// calls.
	OpenClaimsForSession(sessionID string) ([]*model.AgentClaim, error)
}

// Client is the audited mutation surface — both methods wrap a store
// call with a history row + (for EndAgent) assignee-change tracking.
// *client.localClient satisfies it via the borrowed-store wrapper.
type Client interface {
	EnsurePingDispatch(ctx context.Context, sess *model.AgentSession) (*model.AgentDispatch, error)
	EndAgent(ctx context.Context, repo *model.Repo, in inputs.AgentEndInput, dryRun bool) (*model.AgentSession, error)
}

// Pinger is the per-tick reaper. Stateless except for the optional
// `now` clock override (test affordance). Safe to construct once per
// process and call Tick from a timer.
type Pinger struct {
	b   Backend
	c   Client
	log *slog.Logger
	now func() time.Time
}

// New returns a Pinger backed by b + c. logger may be nil — Tick falls
// back to slog.Default() for per-session warnings. The caller is
// responsible for running Tick on a timer (and for gating on the
// ui_leader lease via controller.PingIfLeader so only one reaper runs
// across the cluster).
func New(b Backend, c Client, logger *slog.Logger) *Pinger {
	return &Pinger{b: b, c: c, log: logger}
}

// withClock returns a Pinger using clock as its now() source. Test-only
// affordance — callers in main code leave the default time.Now.
func (p *Pinger) withClock(clock func() time.Time) *Pinger {
	cp := *p
	cp.now = clock
	return &cp
}

// Tick runs one sweep. For each alive registered session it walks:
//
//   - Skip if any fresh non-probe inbound dispatch exists for the
//     session — the matcher already vouches for liveness when it binds
//     a real dispatch, and the agent's eventual AckDispatch will bump
//     LastSeenAt and clear staleness naturally (BACI-148). Avoids the
//     race where the matcher delivers an implement dispatch ~seconds
//     before the reaper would otherwise force-end the session.
//   - If any pending|delivered ping older than AgentPingNoAckTimeout
//     exists for the session → EndAgent(reason=presumed_dead).
//   - Else if LastSeenAt is older than the per-session effective idle
//     cutoff AND no pending|delivered ping exists → EnsurePingDispatch.
//     The effective cutoff is the BACI-159 graduated value: a session
//     holding at least one open claim gets ClaimHolderIdlePingMultiplier
//     × AgentIdlePingThreshold (40 min) instead of the base 20 min, so
//     a long Task-spawned subagent run doesn't get force-ended while
//     its parent supervisor is still doing real work.
//   - Else if LastSeenAt is older than AgentProactiveProbeThreshold
//     (BACI-159 proactive probe, 10 min) AND no pending|delivered ping
//     exists → EnsurePingDispatch. Informational only: a claim-holder
//     that doesn't respond is fine, the graduated reap gate above is
//     what eventually decides "presumed dead", not this probe.
//   - Otherwise no-op.
//
// Returns (pinged, ended, firstError). A transient error on one
// session is logged at warn level and the tick continues — same posture
// as dispatcher.Matcher — but the first such error is also returned so
// the caller can surface it once.
func (p *Pinger) Tick(ctx context.Context) (pinged, ended int, err error) {
	if p == nil || p.b == nil || p.c == nil {
		return 0, 0, nil
	}
	now := p.clock()

	sessions, err := p.b.ListAgentSessions(store.AgentSessionFilter{
		OnlyAlive:      true,
		RegisteredOnly: true,
	})
	if err != nil {
		return 0, 0, err
	}

	pingCutoff := now.Add(-model.AgentPingNoAckTimeout)
	probeCutoff := now.Add(-model.AgentProactiveProbeThreshold)

	var firstErr error
	for _, sess := range sessions {
		if sess == nil {
			continue
		}
		// Defensive: skip sessions whose repo we can't resolve (shouldn't
		// happen for an alive registered row, but a future schema-rename
		// race shouldn't kill the sweep).
		if sess.RepoID == 0 {
			continue
		}

		// BACI-159: resolve the per-session graduated idle cutoff up
		// front — a claim-holder gets double the slack before the reap
		// gate fires. Done before the inbound-dispatch gate because the
		// gate's idleCutoff is itself derived from the effective cutoff
		// (a claim-holder also gets the wider fresh-work window).
		openClaims, err := p.b.OpenClaimsForSession(sess.SessionID)
		if err != nil {
			p.warn("list open claims", err, sess.SessionID)
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		idleCutoff := effectiveIdleCutoff(now, openClaims)

		// BACI-148 gate: if the matcher has bound a real inbound
		// dispatch to this session within the last idle window, leave
		// the session alone — the bind is itself proof of liveness and
		// the agent's eventual AckDispatch will bump LastSeenAt. Shares
		// firstErr with the ping-only query below; both are read-only
		// list calls, so a transient error on either is reported once.
		inbound, err := p.b.ListDispatches(store.DispatchFilter{
			RepoID:          &sess.RepoID,
			TargetSessionID: sess.SessionID,
			Statuses:        []model.DispatchStatus{model.DispatchPending, model.DispatchDelivered},
		})
		if err != nil {
			p.warn("list inbound dispatches", err, sess.SessionID)
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		if hasFreshNonProbeWork(inbound, idleCutoff) {
			continue
		}

		inflight, err := p.b.ListDispatches(store.DispatchFilter{
			RepoID:          &sess.RepoID,
			TargetSessionID: sess.SessionID,
			Statuses:        []model.DispatchStatus{model.DispatchPending, model.DispatchDelivered},
			CreatedBy:       model.IdlePingDispatchCreator,
		})
		if err != nil {
			p.warn("list ping dispatches", err, sess.SessionID)
			if firstErr == nil {
				firstErr = err
			}
			continue
		}

		// Find the oldest in-flight ping; if it's past the no-ack
		// window the session is presumed dead.
		var oldest *model.AgentDispatch
		for _, d := range inflight {
			if oldest == nil || d.CreatedAt.Before(oldest.CreatedAt) {
				oldest = d
			}
		}

		if oldest != nil && oldest.CreatedAt.Before(pingCutoff) {
			repo, rerr := p.b.GetRepoByID(sess.RepoID)
			if rerr != nil {
				p.warn("resolve session repo", rerr, sess.SessionID)
				if firstErr == nil {
					firstErr = rerr
				}
				continue
			}
			if _, eerr := p.c.EndAgent(ctx, repo, inputs.AgentEndInput{
				SessionID: sess.SessionID,
				Reason:    string(model.EndReasonPresumedDead),
			}, false); eerr != nil {
				// EndAgent is idempotent on already-ended sessions, so a
				// not-found / already-ended return is fine.
				if errors.Is(eerr, store.ErrNotFound) {
					continue
				}
				p.warn("force-end presumed-dead session", eerr, sess.SessionID)
				if firstErr == nil {
					firstErr = eerr
				}
				continue
			}
			ended++
			continue
		}

		// No in-flight ping yet — the queue-ping branches share the same
		// EnsurePingDispatch call and idempotency guard. Pick the
		// branch whose cutoff the session has actually crossed: the
		// per-session graduated reap cutoff (idleCutoff) takes
		// precedence over the proactive probe cutoff (probeCutoff) for
		// counting purposes, but both produce the same audited
		// `bacio-channel-ping` row.
		if oldest == nil && (sess.LastSeenAt.Before(idleCutoff) || sess.LastSeenAt.Before(probeCutoff)) {
			if _, perr := p.c.EnsurePingDispatch(ctx, sess); perr != nil {
				p.warn("queue idle-check ping", perr, sess.SessionID)
				if firstErr == nil {
					firstErr = perr
				}
				continue
			}
			pinged++
		}
	}
	return pinged, ended, firstErr
}

// effectiveIdleCutoff returns the per-session "stale enough to reap"
// threshold: now - AgentIdlePingThreshold for a session with no open
// claim, or now - (ClaimHolderIdlePingMultiplier × AgentIdlePingThreshold)
// for a session holding at least one open claim (BACI-159 graduated
// gate). A LastSeenAt before this cutoff is stale enough to trip the
// reaper's queue-ping branch.
func effectiveIdleCutoff(now time.Time, openClaims []*model.AgentClaim) time.Time {
	mult := 1
	for _, c := range openClaims {
		if c == nil || c.ReleasedAt != nil {
			continue
		}
		mult = model.ClaimHolderIdlePingMultiplier
		break
	}
	return now.Add(-time.Duration(mult) * model.AgentIdlePingThreshold)
}

func (p *Pinger) clock() time.Time {
	if p.now != nil {
		return p.now()
	}
	return time.Now()
}

func (p *Pinger) warn(what string, err error, sessionID string) {
	log := p.log
	if log == nil {
		log = slog.Default()
	}
	log.Warn("bacio idlepinger: "+what, "err", err, "session_id", sessionID)
}

// hasFreshNonProbeWork reports whether rows contains a non-probe
// inbound dispatch created after cutoff. The reaper's own ping
// (IdlePingDispatchCreator) and the channel's register-yourself nudge
// (SetupDispatchCreator) are themselves liveness probes, not work, and
// must not count — otherwise a wedged session that the reaper has
// already pinged would never be reaped (BACI-148).
func hasFreshNonProbeWork(rows []*model.AgentDispatch, cutoff time.Time) bool {
	for _, d := range rows {
		if d == nil {
			continue
		}
		if d.CreatedBy == model.IdlePingDispatchCreator {
			continue
		}
		if d.CreatedBy == model.SetupDispatchCreator {
			continue
		}
		if d.CreatedAt.After(cutoff) {
			return true
		}
	}
	return false
}
