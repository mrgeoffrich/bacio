// Package controller is the shared leader-gated background loop that the
// desktop app, the `bacio api` server, and the TUI all run. Before this
// package existed, each of those three UIs hand-rolled the same three
// loops (heartbeat / live-list prune / BACI-51 queue matcher) inline,
// which is exactly how the api shipped without a matcher goroutine —
// drift between three near-identical copies. Centralising the loops here
// makes that class of bug impossible.
//
// Two surfaces:
//
//   - [Controller] wraps the elector + matcher + store and runs the
//     six loops as goroutines under a single done channel. Used by
//     long-lived processes that have no other event loop to drive ticks
//     off (the desktop's Wails lifecycle and the api's http.Server).
//   - [MatchIfLeader] and [PruneIfLeader] are per-tick helpers that
//     callers drive themselves. Used by the TUI, where bubbletea owns
//     the timer model and per-tick work runs inside Update handlers.
//
// Both surfaces gate on the elector's cached AmLeader state so exactly
// one process across the cluster runs the prune / matcher work at a
// time — the matcher's BindQueuedDispatch is conditional-UPDATE-safe,
// but two prune passes racing on the same DELETE would just waste
// cycles.
package controller

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/mrgeoffrich/bacio/internal/dispatcher"
	"github.com/mrgeoffrich/bacio/internal/idlepinger"
	"github.com/mrgeoffrich/bacio/internal/leader"
	"github.com/mrgeoffrich/bacio/internal/model"
	"github.com/mrgeoffrich/bacio/internal/store"
	bsync "github.com/mrgeoffrich/bacio/internal/sync"
)

// MatchIfLeader runs one [dispatcher.Matcher] tick if el holds the
// lease, then writes one `agent.bind` audit row
// (Actor=[model.MatcherActor], BACI-160) per successful queued→pending
// transition the tick committed. Read errors from the matcher are
// logged at warn level and never returned — a transient DB blip must
// not propagate up to the caller's tick handler. A nil matcher or
// elector is a no-op; a nil store skips the audit write but still runs
// the tick (the caller is expected to pass the store the matcher's
// Backend was built from so the audit row goes to the same DB the bind
// committed against).
func MatchIfLeader(m *dispatcher.Matcher, el *leader.Elector, s *store.Store, log *slog.Logger) {
	if m == nil || el == nil || !el.CurrentState().AmLeader {
		return
	}
	binds, err := m.TickDetailed()
	if err != nil {
		loggerOrDefault(log).Warn("bacio: queue match failed", "err", err)
		// Fall through — TickDetailed returns the partial slice on
		// error so the audit log still records the binds the tick
		// did commit before bailing.
	}
	if s == nil {
		return
	}
	for _, b := range binds {
		recordBindAudit(s, b, log)
	}
}

// recordBindAudit writes one `agent.bind` row per successful matcher
// bind (BACI-160 gap 1). Failures are logged at warn level and never
// propagate — losing one audit row is better than rolling back the
// bind the matcher already committed.
func recordBindAudit(s *store.Store, b dispatcher.Bind, log *slog.Logger) {
	if s == nil || b.Dispatch == nil {
		return
	}
	d := b.Dispatch
	id := d.ID
	repoID := d.RepoID
	entry := model.HistoryEntry{
		Actor:       model.MatcherActor,
		Op:          "agent.bind",
		Kind:        "agent",
		RepoID:      &repoID,
		RepoPrefix:  d.RepoPrefix,
		TargetID:    &id,
		TargetLabel: bindTargetLabel(b),
		Details:     bindDetails(b),
	}
	if err := s.RecordHistory(entry); err != nil {
		loggerOrDefault(log).Warn("bacio: failed to record agent.bind audit",
			"dispatch_id", id, "agent", b.AgentName, "err", err)
	}
}

// bindTargetLabel picks the most specific label for the bind audit
// row: the bound agent's name (always present on a successful bind
// because the matcher selected it from the candidate pool). Mirrors
// dispatchTargetLabel's "agent first, session id fallback" rule, but
// the matcher path always has an agent.
func bindTargetLabel(b dispatcher.Bind) string {
	if b.AgentName != "" {
		return b.AgentName
	}
	if b.Dispatch != nil && b.Dispatch.TargetAgentName != "" {
		return b.Dispatch.TargetAgentName
	}
	return ""
}

// bindDetails composes the audit-log Details string for an agent.bind
// row. Mirrors the dispatch-audit Details convention — pick out the
// high-signal fields (dispatch id, issue, mode, agent) without dumping
// the full payload. A reader of `bacio history --op agent.bind` cares
// most about "which dispatch got handed to which agent for which
// issue".
func bindDetails(b dispatcher.Bind) string {
	d := b.Dispatch
	if d == nil {
		return ""
	}
	parts := []string{fmt.Sprintf("dispatch_id=%d", d.ID)}
	if d.IssueKey != "" {
		parts = append(parts, "issue="+d.IssueKey)
	}
	if d.Mode != "" {
		parts = append(parts, "mode="+string(d.Mode))
	}
	if b.AgentName != "" {
		parts = append(parts, "agent="+b.AgentName)
	}
	return strings.Join(parts, ",")
}

// PruneIfLeader runs one live-list prune of ended agent_sessions if el
// holds the lease. Drops rows older than
// [store.AgentSessionLiveListRetention] (the 4h "agents tab tidy"
// retention, not the 60-day store-open retention — that one runs on
// every Store.Open). Errors are logged at warn level and never
// returned. A nil store or elector is a no-op.
func PruneIfLeader(s *store.Store, el *leader.Elector, log *slog.Logger) {
	if s == nil || el == nil || !el.CurrentState().AmLeader {
		return
	}
	if _, err := s.PruneEndedAgentSessions(store.AgentSessionLiveListRetention); err != nil {
		loggerOrDefault(log).Warn("bacio: live agent-session prune failed", "err", err)
	}
}

// PingIfLeader runs one [idlepinger.Pinger] sweep if el holds the
// lease (BACI-57). Errors are logged at warn level and never returned —
// the reaper is mechanical janitor work (its audit rows give the
// forensic trail), and a transient DB blip must not propagate up to
// the caller's tick handler. A nil pinger or elector is a no-op.
func PingIfLeader(p *idlepinger.Pinger, el *leader.Elector, log *slog.Logger) {
	if p == nil || el == nil || !el.CurrentState().AmLeader {
		return
	}
	if _, _, err := p.Tick(context.Background()); err != nil {
		loggerOrDefault(log).Warn("bacio: idle ping sweep failed", "err", err)
	}
}

// ArchiveSweepIfLeader runs one Store.ArchiveSweep pass if el holds
// the lease (BACI-68). Same logged-and-swallowed error contract as
// the other …IfLeader helpers — auto-archive is mechanical janitor
// work and a transient DB blip must not propagate up to the caller's
// tick handler. A nil store or elector is a no-op. The post-sweep log
// line and audit row only fire when at least one row was archived, so
// a quiet DB doesn't generate hourly noise.
//
// BACI-160 gap 2: writes one `archive.sweep` history row per non-empty
// sweep with Actor=[model.ControllerActor], mirroring the manual
// `bacio archive sweep` verb's audit row. Without this the leader-
// driven hourly tick was the dominant caller of `ArchiveSweep` (24x
// more sweeps per day than the manual verb) but produced no audit
// trail — `bacio history --op archive.sweep` only saw the rare manual
// run.
func ArchiveSweepIfLeader(s *store.Store, el *leader.Elector, log *slog.Logger) {
	if s == nil || el == nil || !el.CurrentState().AmLeader {
		return
	}
	res, err := s.ArchiveSweep()
	if err != nil {
		loggerOrDefault(log).Warn("bacio: archive sweep failed", "err", err)
		return
	}
	if res.Total() == 0 {
		return
	}
	loggerOrDefault(log).Info("bacio: archive sweep stamped rows",
		"issues", res.IssuesArchived,
		"features", res.FeaturesArchived,
		"documents", res.DocumentsArchived,
	)
	entry := model.HistoryEntry{
		Actor: model.ControllerActor,
		Op:    "archive.sweep",
		Kind:  "sweep",
		// Compact, parseable details — mirrors detailsForSweep in
		// internal/client/local_archive.go so a reader sees identical
		// shape regardless of which surface kicked the sweep.
		Details: fmt.Sprintf(`{"issues":%d,"features":%d,"documents":%d}`,
			res.IssuesArchived, res.FeaturesArchived, res.DocumentsArchived),
	}
	if err := s.RecordHistory(entry); err != nil {
		loggerOrDefault(log).Warn("bacio: failed to record archive.sweep audit", "err", err)
	}
}

// FollowOnSweepIfLeader (BACI-179) runs one combined follow-on
// lifecycle pass if el holds the lease. Orphan-cancel first, then
// promote — design §4 / §6 picks this single-helper / single-
// goroutine shape over two parallel sweeps so an issue that's both
// "terminal" and "predecessor settled" cancels before it can promote
// (avoiding a wasted matcher bind one tick later). One audit row per
// affected dispatch is written; same logged-and-swallowed contract as
// the other …IfLeader helpers — sweeping is mechanical janitor work,
// and a transient DB blip must not propagate up to the caller's tick
// handler. A nil store or elector is a no-op.
//
// The cancel sweep is attributed to ControllerActor on the orphan
// path; the user-driven cancel from CancelFollowOnDispatch writes its
// own `agent.followon.cancel` row at client time stamped with the
// requesting actor, so `bacio history --op agent.followon.cancel`
// distinguishes the two paths by Actor (`bacio-controller` vs the
// user / agent slug).
func FollowOnSweepIfLeader(s *store.Store, el *leader.Elector, log *slog.Logger) {
	if s == nil || el == nil || !el.CurrentState().AmLeader {
		return
	}
	cancelled, err := s.CancelOrphanedFollowOns()
	if err != nil {
		loggerOrDefault(log).Warn("bacio: orphan-cancel follow-on sweep failed", "err", err)
		// Fall through — a partial slice is still worth auditing, and the
		// promote pass below is independent of the cancel pass.
	}
	for _, d := range cancelled {
		recordFollowOnCancelAudit(s, d, log)
	}
	// BACI-195: load the per-mode state-gates so PromoteReadyFollowOns
	// can re-evaluate them at fire time. A gate-failed dormant row is
	// cancelled in the same transaction as a passing promotion, so the
	// matcher never sees an out-of-state queued row.
	gates, err := s.AllPromptStates()
	if err != nil {
		loggerOrDefault(log).Warn("bacio: load prompt state-gates for follow-on sweep failed", "err", err)
		// Fall through with nil gates; PromoteReadyFollowOns will
		// gate-fail every ready row. Marking them cancelled with a
		// gate-fail audit beats silently re-trying every tick — the
		// gate-load failure itself surfaces via the warn log above.
	}
	promoted, gateFailed, err := s.PromoteReadyFollowOns(gates)
	if err != nil {
		loggerOrDefault(log).Warn("bacio: promote follow-on sweep failed", "err", err)
	}
	for _, d := range promoted {
		recordFollowOnPromoteAudit(s, d, log)
	}
	for _, d := range gateFailed {
		recordFollowOnGateFailAudit(s, d, log)
	}
}

// recordFollowOnPromoteAudit writes one `agent.followon.promote` row
// per row the promote sweep cleared. Mirrors recordBindAudit's
// per-row shape: TargetID = the cleared follow-on (not the parent),
// TargetLabel = issue key. Failures are logged at warn level and
// never propagate — losing one audit row is better than rolling back
// the column-clear the sweep already committed.
func recordFollowOnPromoteAudit(s *store.Store, d *model.AgentDispatch, log *slog.Logger) {
	if s == nil || d == nil {
		return
	}
	id := d.ID
	repoID := d.RepoID
	entry := model.HistoryEntry{
		Actor:       model.ControllerActor,
		Op:          "agent.followon.promote",
		Kind:        "agent",
		RepoID:      &repoID,
		RepoPrefix:  d.RepoPrefix,
		TargetID:    &id,
		TargetLabel: d.IssueKey,
		Details:     followOnDetails(d),
	}
	if err := s.RecordHistory(entry); err != nil {
		loggerOrDefault(log).Warn("bacio: failed to record agent.followon.promote audit",
			"dispatch_id", id, "err", err)
	}
}

// recordFollowOnCancelAudit writes one `agent.followon.cancel` row
// per row the orphan-cancel sweep cancelled. Same shape as the
// promote helper; the Actor distinguishes the sweep
// (`bacio-controller`) from the user-driven cancel path which lands
// the same op with the requesting actor stamped instead.
func recordFollowOnCancelAudit(s *store.Store, d *model.AgentDispatch, log *slog.Logger) {
	if s == nil || d == nil {
		return
	}
	id := d.ID
	repoID := d.RepoID
	entry := model.HistoryEntry{
		Actor:       model.ControllerActor,
		Op:          "agent.followon.cancel",
		Kind:        "agent",
		RepoID:      &repoID,
		RepoPrefix:  d.RepoPrefix,
		TargetID:    &id,
		TargetLabel: d.IssueKey,
		Details:     followOnDetails(d),
	}
	if err := s.RecordHistory(entry); err != nil {
		loggerOrDefault(log).Warn("bacio: failed to record agent.followon.cancel audit",
			"dispatch_id", id, "err", err)
	}
}

// recordFollowOnGateFailAudit (BACI-195) writes one
// `agent.followon.gate_fail` row per dormant follow-on the promote
// sweep cancelled because the issue's post-release state didn't admit
// the follow-on's mode. Distinguished from the `agent.followon.cancel`
// op so `bacio history --op agent.followon.gate_fail` surfaces a
// coherent ledger of "follow-on the user queued, then the actual
// post-release state didn't match" — distinct from orphan-cancel
// (issue landed in done/cancelled) and user-cancel (chip removed).
// Same per-row attribution pattern as the sibling helpers.
func recordFollowOnGateFailAudit(s *store.Store, d *model.AgentDispatch, log *slog.Logger) {
	if s == nil || d == nil {
		return
	}
	id := d.ID
	repoID := d.RepoID
	entry := model.HistoryEntry{
		Actor:       model.ControllerActor,
		Op:          "agent.followon.gate_fail",
		Kind:        "agent",
		RepoID:      &repoID,
		RepoPrefix:  d.RepoPrefix,
		TargetID:    &id,
		TargetLabel: d.IssueKey,
		Details:     followOnDetails(d),
	}
	if err := s.RecordHistory(entry); err != nil {
		loggerOrDefault(log).Warn("bacio: failed to record agent.followon.gate_fail audit",
			"dispatch_id", id, "err", err)
	}
}

// followOnDetails composes the audit-log Details string for the
// follow-on ops (promote / cancel / queue). Mirrors bindDetails's
// comma-separated k=v convention; carries dispatch id, issue key,
// mode, and the parent dispatch id (when still set — a promote sweep
// clears the parent FK before this is read on the post-sweep row, so
// promote audit rows naturally omit the parent_dispatch_id field).
// Empty fields are omitted, not stamped as `=`.
func followOnDetails(d *model.AgentDispatch) string {
	if d == nil {
		return ""
	}
	parts := []string{fmt.Sprintf("dispatch_id=%d", d.ID)}
	if d.IssueKey != "" {
		parts = append(parts, "issue="+d.IssueKey)
	}
	if d.Mode != "" {
		parts = append(parts, "mode="+string(d.Mode))
	}
	if d.QueuedAfterDispatchID != nil {
		parts = append(parts, fmt.Sprintf("parent_dispatch_id=%d", *d.QueuedAfterDispatchID))
	}
	return strings.Join(parts, ",")
}

// SyncIfLeader runs one [bsync.BackgroundRunner] tick if el holds the
// lease (BACI-89). Same logged-and-swallowed error contract as the
// other …IfLeader helpers — continual git-sync is best-effort mirror
// work and a transient git/DB blip must not propagate up to the
// caller's tick handler. A nil runner or elector is a no-op. The
// runner self-gates on the sync.background_enabled toggle and on
// whether any repo is sync-enabled, so this is cheap on a non-sync DB.
func SyncIfLeader(r *bsync.BackgroundRunner, el *leader.Elector, log *slog.Logger) {
	if r == nil || el == nil || !el.CurrentState().AmLeader {
		return
	}
	if err := r.Tick(context.Background()); err != nil {
		loggerOrDefault(log).Warn("bacio: background sync tick failed", "err", err)
	}
}

// Controller owns the background goroutines (heartbeat, prune,
// matcher, idle-pinger, archive sweep, background sync) for desktop +
// api. The TUI does not use it —
// it drives the per-tick work itself from bubbletea Update handlers
// using the package-level helpers above — so this type intentionally
// has no bubbletea coupling.
type Controller struct {
	st         *store.Store
	el         *leader.Elector
	matcher    *dispatcher.Matcher
	pinger     *idlepinger.Pinger
	syncRunner *bsync.BackgroundRunner
	log        *slog.Logger

	done chan struct{}
	wg   sync.WaitGroup
}

// New builds a Controller backed by an already-constructed elector,
// matcher, pinger, and background-sync runner. The Controller takes
// ownership of the elector for shutdown (Stop calls Release); the
// store is not closed by the Controller — the caller still owns the
// *store.Store handle's lifecycle. matcher, pinger, and syncRunner
// may be nil to disable their loops (e.g. tests that exercise only
// the heartbeat path).
//
// log may be nil; helpers fall back to slog.Default().
func New(s *store.Store, el *leader.Elector, m *dispatcher.Matcher, p *idlepinger.Pinger, sr *bsync.BackgroundRunner, log *slog.Logger) *Controller {
	return &Controller{st: s, el: el, matcher: m, pinger: p, syncRunner: sr, log: log}
}

// Start fires the heartbeat synchronously once (so the caller sees a
// non-zero leader state before Start returns), then spins six
// goroutines: heartbeat on UILeaderHeartbeatInterval, prune on
// UILeaderPruneInterval, matcher on QueueMatchInterval, idle-pinger
// on IdlePingTickInterval (BACI-57), archive sweep on
// ArchiveSweepInterval (BACI-68), and background git-sync on
// SyncTickInterval (BACI-89). If emit is non-nil it is called with
// every heartbeat result (including the synchronous startup tick) —
// used by the desktop to push leader state to the Wails frontend.
//
// Start is not safe to call twice on the same Controller; pair each
// Start with exactly one Stop.
func (c *Controller) Start(emit func(leader.State)) {
	c.done = make(chan struct{})
	if c.el != nil {
		st := c.el.Tick()
		if emit != nil {
			emit(st)
		}
	}
	// BACI-175: sweep-on-startup. Short-lived processes (a 30s `bacio
	// web --no-open` smoke run, a one-minute desktop peek, a dispatch
	// worker's controller spin-up) never live long enough for the
	// ArchiveSweepInterval ticker to fire. Running one sweep after the
	// synchronous initial heartbeat (above) catches any overdue rows
	// even if the process exits seconds later. Leader-gated via the
	// helper, so a standby controller is a no-op; idempotent on a
	// quiet DB so an over-fire across surfaces is harmless.
	ArchiveSweepIfLeader(c.st, c.el, c.log)
	// Capture done into each goroutine's local before the for-loop —
	// Stop sets c.done = nil after closing it, and re-reading c.done
	// after a ticker wake would turn <-c.done into <-nil and park the
	// goroutine forever.
	done := c.done

	c.wg.Add(1)
	go func() {
		defer c.wg.Done()
		ticker := time.NewTicker(store.UILeaderHeartbeatInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				if c.el == nil {
					continue
				}
				st := c.el.Tick()
				if emit != nil {
					emit(st)
				}
			case <-done:
				return
			}
		}
	}()

	c.wg.Add(1)
	go func() {
		defer c.wg.Done()
		ticker := time.NewTicker(store.UILeaderPruneInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				PruneIfLeader(c.st, c.el, c.log)
			case <-done:
				return
			}
		}
	}()

	c.wg.Add(1)
	go func() {
		defer c.wg.Done()
		ticker := time.NewTicker(store.QueueMatchInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				MatchIfLeader(c.matcher, c.el, c.st, c.log)
			case <-done:
				return
			}
		}
	}()

	c.wg.Add(1)
	go func() {
		defer c.wg.Done()
		ticker := time.NewTicker(store.IdlePingTickInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				PingIfLeader(c.pinger, c.el, c.log)
			case <-done:
				return
			}
		}
	}()

	// BACI-68: archive auto-sweep. Hourly, leader-gated, mechanical
	// janitor work — three SQL passes in one transaction inside
	// Store.ArchiveSweep that stamps archived_at on issues whose
	// terminal_at is older than the configured retention window
	// (BACI-162; default 7 days, settable via `bacio settings archive`
	// or the desktop Settings panel), then on features whose every
	// child issue is archived, then on docs whose every linked parent
	// is archived. Pattern matches the prune/matcher/pinger goroutines
	// above; same nil-elector tolerance via …IfLeader.
	c.wg.Add(1)
	go func() {
		defer c.wg.Done()
		ticker := time.NewTicker(store.ArchiveSweepInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				ArchiveSweepIfLeader(c.st, c.el, c.log)
			case <-done:
				return
			}
		}
	}()

	// BACI-179: follow-on dispatch lifecycle sweep. Rides
	// QueueMatchInterval (5s) — same cadence as the matcher so a
	// dormant row promotes on tick N and binds on tick N+1, keeping
	// the end-to-end "release the claim → next worker picks up the
	// follow-on" latency tight (~6s typical, ~10s worst-case). One
	// helper, one goroutine — design §6 picks this over two parallel
	// sweeps so an issue that's both terminal and "predecessor
	// settled" cancels before it can promote.
	c.wg.Add(1)
	go func() {
		defer c.wg.Done()
		ticker := time.NewTicker(store.QueueMatchInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				FollowOnSweepIfLeader(c.st, c.el, c.log)
			case <-done:
				return
			}
		}
	}()

	// BACI-89: background git-sync. store.SyncTickInterval (5m),
	// leader-gated. The runner mirrors every sync-enabled tracked
	// repo with the same pull → import → export → commit → push
	// pipeline a manual `bacio sync` runs, self-gating on the
	// sync.background_enabled toggle and an overlap guard. git
	// network I/O is intentionally confined to this goroutine.
	c.wg.Add(1)
	go func() {
		defer c.wg.Done()
		ticker := time.NewTicker(store.SyncTickInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				SyncIfLeader(c.syncRunner, c.el, c.log)
			case <-done:
				return
			}
		}
	}()
}

// Stop closes the done channel, waits for the goroutines to exit, and
// releases the elector's lease so a standby UI can promote within one
// tick (~10s) rather than waiting out the 180s stale window. Safe to
// call on a Controller whose Start was never called: it just releases
// the elector.
func (c *Controller) Stop() {
	if c.done != nil {
		close(c.done)
		c.done = nil
	}
	c.wg.Wait()
	if c.el != nil {
		c.el.Release()
	}
}

// loggerOrDefault returns log if non-nil, else slog.Default(). The
// helpers want a guaranteed-non-nil destination so a forgotten logger
// in a caller doesn't crash the tick goroutine.
func loggerOrDefault(log *slog.Logger) *slog.Logger {
	if log != nil {
		return log
	}
	return slog.Default()
}
