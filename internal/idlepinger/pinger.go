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
//   - If any pending|delivered ping older than AgentPingNoAckTimeout
//     exists for the session → EndAgent(reason=presumed_dead).
//   - Else if LastSeenAt is older than AgentIdlePingThreshold AND no
//     pending|delivered ping exists → EnsurePingDispatch.
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

	idleCutoff := now.Add(-model.AgentIdlePingThreshold)
	pingCutoff := now.Add(-model.AgentPingNoAckTimeout)

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

		// No in-flight ping yet, and last_seen_at is stale — queue one.
		if oldest == nil && sess.LastSeenAt.Before(idleCutoff) {
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
