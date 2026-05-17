// Package dispatcher runs the per-(repo, mode) FIFO matcher that binds
// BACI-51 queued dispatches to free agents. The matcher is a thin
// orchestrator over the store helpers added in BACI-51; the picker (the
// "which free agent gets this work" decision) used to live in
// internal/client/local_dispatch.go and is now centralised here so the
// matcher and the client's targeted-dispatch path can both reach it
// without circular imports.
package dispatcher

import (
	"time"

	"github.com/mrgeoffrich/bacio/internal/model"
	"github.com/mrgeoffrich/bacio/internal/store"
)

// AgentCandidate is the small projection AutoPickFreeAgent works on —
// just the bits the picker decides from. Kept structurally compatible
// with the original internal/client/local_dispatch.go form so callers
// could be migrated without renaming every field.
type AgentCandidate struct {
	AgentName       string
	Ended           bool
	Busy            bool
	HasChannel      bool
	HasOpenDispatch bool
}

// AutoPickFreeAgent returns the first candidate eligible to take auto-
// dispatched work: live, not busy, has a channel, no un-acked dispatch
// already queued against it, and a persistent identity slug
// (CreateDispatch routes by slug). Candidates are expected in
// last-seen-DESC order; the first match wins so the freshest free
// agent gets the work.
func AutoPickFreeAgent(cands []AgentCandidate) string {
	for _, c := range cands {
		if c.Ended || c.Busy || c.AgentName == "" || !c.HasChannel || c.HasOpenDispatch {
			continue
		}
		return c.AgentName
	}
	return ""
}

// BuildCandidates assembles the AgentCandidate slice from the raw
// store reads the auto-pick path needs: the per-repo session list, the
// repo-wide open-claim index, and every open dispatch (used to mark
// agents that already have un-acked work). Sessions arrive
// last-seen-DESC from ListAgentSessions; that order is preserved so
// AutoPickFreeAgent picks the freshest free agent.
func BuildCandidates(
	sessions []*model.AgentSession,
	openClaims map[int64][]*model.AgentClaim,
	dispatches []*model.AgentDispatch,
	now time.Time,
) []AgentCandidate {
	occupiedAgentID := map[int64]bool{}
	occupiedSession := map[string]bool{}
	for _, d := range dispatches {
		// Queued rows have no target yet, so they don't occupy anyone.
		// Pending and delivered are the "in-flight, not yet acked"
		// statuses that mark an agent as busy with prior work.
		if d.Status != model.DispatchPending && d.Status != model.DispatchDelivered {
			continue
		}
		if d.TargetAgentID != nil {
			occupiedAgentID[*d.TargetAgentID] = true
		}
		if d.TargetSessionID != "" {
			occupiedSession[d.TargetSessionID] = true
		}
	}
	out := make([]AgentCandidate, 0, len(sessions))
	for _, s := range sessions {
		cand := AgentCandidate{
			AgentName:  s.AgentName,
			Ended:      model.SessionLiveness(s, now) == "ended",
			HasChannel: s.ChannelSeenAt != nil,
		}
		if !cand.Ended {
			cand.Busy, _ = model.SessionBusy(openClaims[s.ID])
		}
		if s.AgentID != nil && occupiedAgentID[*s.AgentID] {
			cand.HasOpenDispatch = true
		}
		if occupiedSession[s.SessionID] {
			cand.HasOpenDispatch = true
		}
		out = append(out, cand)
	}
	return out
}

// pickCandidatesForRepo composes ListAgentSessions +
// OpenClaimsBySession + ListDispatches into a candidate slice — the
// shape AutoPickFreeAgent works on. Used by the matcher's per-tick
// per-repo work; callers outside this package generally don't need it.
func pickCandidatesForRepo(b Backend, repoID int64, now time.Time) ([]AgentCandidate, error) {
	sessions, err := b.ListAgentSessions(store.AgentSessionFilter{
		RepoID:         &repoID,
		RegisteredOnly: true,
	})
	if err != nil {
		return nil, err
	}
	openClaims, err := b.OpenClaimsBySession(repoID)
	if err != nil {
		return nil, err
	}
	dispatches, err := b.ListDispatches(store.DispatchFilter{RepoID: &repoID})
	if err != nil {
		return nil, err
	}
	return BuildCandidates(sessions, openClaims, dispatches, now), nil
}
