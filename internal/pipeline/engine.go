// Package pipeline holds the controller's job-engine: the leader-gated
// loop that advances an in_pipeline card's chain of dispatch jobs. It is
// the seam that replaces worker-driven `release --state` progression —
// the engine owns issue state for Pipeline cards (see the store's
// engine-governed-state guard), queuing ordinary dispatches the BACI-51
// matcher then binds, and detecting completion when a job's dispatch
// acks.
//
// The package mirrors internal/dispatcher: a thin type over the store
// with a Tick the controller drives on a timer (gated on the ui_leader
// lease so exactly one process advances chains at a time). Tick returns
// the transitions it committed so the controller can write audit rows,
// the same shape dispatcher.Matcher uses for binds.
package pipeline

import (
	"errors"
	"fmt"
	"log/slog"
	"strings"

	"github.com/mrgeoffrich/bacio/internal/model"
	"github.com/mrgeoffrich/bacio/internal/store"
)

// ErrJobNotCancelled is returned by RerunJob when the targeted job isn't
// in the cancelled (aborted) state — re-run is an abort-recovery
// affordance only, never a way to re-run a complete or running job.
var ErrJobNotCancelled = errors.New("job is not cancelled; only an aborted job can be re-run")

// ErrJobRunning is returned by ResetProcess when a job is currently running
// — Reset wipes the whole chain (including the running job's history),
// which would orphan the live worker + dispatch. The user must Stop the
// running job first (Stop cancels the job/dispatch and steers the worker).
// The HTTP twin maps this to 409 so the CLI/remote report a clean
// "stop the running job first" rather than a generic 500.
var ErrJobRunning = errors.New("a job is running; stop it before resetting the process")

// stopWorkerSteerBody is the canned wind-down note StopRunning enqueues as
// a BACI-286 steer message at the running worker's session. Delivery is
// turn-boundary only (the channel can't interrupt a live tool call), so
// the wording asks the worker to stop at its next turn rather than
// promising a hard kill — see docs/agent-dispatch.md "User→agent steer
// messages".
const stopWorkerSteerBody = "Stop requested: the user pressed Stop on this pipeline job. Please wind down now — finish the in-flight tool call if one is running, then stop work on this issue. The engine has already cancelled the job, so there's nothing to hand off; do not open a PR or continue."

// Engine advances in_pipeline job chains. Stateless across ticks — every
// Tick reads fresh state from the store. Safe to construct once and call
// Tick from a timer.
type Engine struct {
	st  *store.Store
	log *slog.Logger
}

// New returns an Engine backed by s.
func New(s *store.Store) *Engine { return &Engine{st: s} }

// WithLogger wires a structured logger (nil routes through slog.Default).
func (e *Engine) WithLogger(log *slog.Logger) *Engine {
	if e != nil {
		e.log = log
	}
	return e
}

func (e *Engine) logger() *slog.Logger {
	if e == nil || e.log == nil {
		return slog.Default()
	}
	return e.log
}

// Advance records one engine-committed transition for the audit-writing
// caller (mirrors dispatcher.Bind). Kind is a short verb
// (job.start / job.complete / job.cancelled / handoff); Detail is the
// compact k=v string the controller stamps into the history row.
type Advance struct {
	IssueKey   string
	IssueID    int64
	RepoID     int64
	RepoPrefix string
	Kind       string
	Detail     string
}

// Tick runs one pass over every in_pipeline card across all repos:
// reconcile the running job (complete on ack; halt on an open question),
// then — only in Auto mode — advance the chain (queue the next job, or
// hand the card off to to_be_shipped at the ship sentinel; a no-Ship
// process finishes in place once its last stage completes).
// Read failures stop the tick; a single card's per-card failure is
// logged and skipped so one bad row doesn't stall the rest. Returns the
// committed transitions in commit order.
func (e *Engine) Tick() ([]Advance, error) {
	if e == nil || e.st == nil {
		return nil, nil
	}
	issues, err := e.st.ListIssues(store.IssueFilter{
		States:   []model.State{model.StateInPipeline},
		AllRepos: true,
	})
	if err != nil {
		return nil, fmt.Errorf("engine: list in_pipeline: %w", err)
	}
	var advances []Advance
	for _, iss := range issues {
		adv, err := e.tickIssue(iss)
		if err != nil {
			e.logger().Warn("bacio engine: tick issue failed", "issue", iss.Key, "err", err)
			continue
		}
		advances = append(advances, adv...)
	}
	return advances, nil
}

// AutoShipTick runs one pass over the Shipping column for every repo.
// Two responsibilities, deliberately split by the per-repo auto-ship
// toggle — and by how wide a slice of the column each one touches:
//
//   - Advance-on-ack runs ALWAYS (toggle or not) and sweeps ALL
//     to_be_shipped cards, not just the top: any card whose ship
//     dispatch has acked moves to done. This is the only path that
//     completes a ship — whether the dispatch was queued by auto-ship or
//     by a manual SHIP click — so it must not be gated on the toggle (a
//     manual ship with auto-ship off would otherwise strand the card in
//     to_be_shipped forever). Sweeping the whole column matters because
//     advancing an already-merged + acked card has no merge-serialisation
//     constraint, so a wedged head card (its ship cancelled / in-flight)
//     must not strand acked cards queued behind it (BACI-297).
//   - Auto-dispatch runs only when auto-ship is on and stays top-card-
//     only: if no ship has run for the top card yet, queue one. Keeping
//     it top-only preserves merge serialisation — we only ever auto-fire
//     the next-to-ship card, never two ships at once. With auto-ship off
//     the user initiates the ship themselves via the SHIP button (§4).
//
// A deliberately-cancelled ship is left alone (no retry loop); the next
// card becomes top once the current one ships. Leader-gated like Tick.
func (e *Engine) AutoShipTick() ([]Advance, error) {
	if e == nil || e.st == nil {
		return nil, nil
	}
	repos, err := e.st.ListRepos()
	if err != nil {
		return nil, fmt.Errorf("engine: list repos: %w", err)
	}
	var advances []Advance
	for _, repo := range repos {
		if repo == nil {
			continue
		}
		settings, err := e.st.GetRepoSettings(repo.ID)
		if err != nil {
			e.logger().Warn("bacio engine: auto-ship repo settings failed", "repo", repo.Prefix, "err", err)
			continue
		}
		// Advance pass runs unconditionally; only the dispatch pass sees the
		// auto-ship toggle. Warn-and-continue per repo so one bad repo
		// doesn't stall the rest.
		adv, err := e.advanceShippedRepo(repo.ID)
		if err != nil {
			e.logger().Warn("bacio engine: ship advance failed", "repo", repo.Prefix, "err", err)
			continue
		}
		advances = append(advances, adv...)
		adv, err = e.autoDispatchShipRepo(repo.ID, settings.AutoShip)
		if err != nil {
			e.logger().Warn("bacio engine: auto-ship dispatch failed", "repo", repo.Prefix, "err", err)
			continue
		}
		advances = append(advances, adv...)
	}
	return advances, nil
}

// advanceShippedRepo sweeps EVERY to_be_shipped card in the repo and
// advances any whose latest ship dispatch has acked to done. Runs always
// (no auto-ship gate) — a manual SHIP and an auto-ship both complete
// here. Unlike the dispatch pass it is not top-card-only: advancing an
// already-merged + acked card carries no merge-serialisation constraint,
// so a wedged head card (ship cancelled / in-flight) must not strand
// acked cards behind it (BACI-297). Cards whose ship is in-flight /
// cancelled / never-run are skipped (no advance). Idempotent: once the
// ship worker (or a prior tick) moves a card to done it drops out of the
// to_be_shipped list.
func (e *Engine) advanceShippedRepo(repoID int64) ([]Advance, error) {
	cards, err := e.st.ListIssues(store.IssueFilter{
		RepoID: &repoID,
		States: []model.State{model.StateToBeShipped},
	})
	if err != nil {
		return nil, err
	}
	var advances []Advance
	for _, card := range cards {
		latest, err := e.st.LatestDispatchForIssueMode(card.ID, model.DispatchModeShip)
		if err != nil {
			return advances, err
		}
		if latest == nil || latest.Status != model.DispatchAcked {
			continue
		}
		// SetIssueState permits to_be_shipped → done (done is not a
		// processing state).
		if err := e.st.SetIssueState(card.ID, model.StateDone); err != nil {
			return advances, err
		}
		advances = append(advances, e.advance(card, "ship.done", "ship complete"))
	}
	return advances, nil
}

// autoDispatchShipRepo queues a ship for the Shipping column's top card
// when auto-ship is on and no ship has run for it yet. Stays top-card-
// only to preserve merge serialisation (the ship template's
// concurrency_limit plus FIFO position): we only ever auto-fire the
// next-to-ship card, never two ships at once. A no-op when auto-ship is
// off, when there is no top card, or when the top card's ship is already
// in flight / cancelled (a deliberately-cancelled ship parks — re-arming
// it is deferred per BACI-297; the advance pass handles the acked case).
func (e *Engine) autoDispatchShipRepo(repoID int64, autoShip bool) ([]Advance, error) {
	if !autoShip {
		return nil, nil
	}
	top, err := e.st.TopShippingIssue(repoID)
	if err != nil {
		return nil, err
	}
	if top == nil {
		return nil, nil
	}
	latest, err := e.st.LatestDispatchForIssueMode(top.ID, model.DispatchModeShip)
	if err != nil {
		return nil, err
	}
	if latest != nil {
		// A ship dispatch already exists (in flight, acked, or cancelled) —
		// don't double-dispatch. The advance pass completes an acked one; a
		// cancelled one parks until a fresh SHIP re-arms it.
		return nil, nil
	}
	// No ship has run yet. Queue one (the matcher binds it; the ship
	// template's concurrency_limit serialises merges).
	if _, err := e.st.AddDispatch(store.AddDispatchIn{
		RepoID:        repoID,
		IssueID:       &top.ID,
		Mode:          model.DispatchModeShip,
		CreatedBy:     model.ControllerActor,
		InitialStatus: model.DispatchQueued,
	}); err != nil {
		return nil, err
	}
	return []Advance{e.advance(top, "ship.dispatch", "auto-ship queued")}, nil
}

func (e *Engine) tickIssue(iss *model.Issue) ([]Advance, error) {
	jobs, err := e.st.ListPipelineJobs(iss.ID)
	if err != nil {
		return nil, err
	}
	if len(jobs) == 0 {
		// No process chosen yet — waiting for the user to pick one.
		return nil, nil
	}
	var advances []Advance

	// 1) Reconcile the running job (any engine mode): complete on ack,
	//    halt on cancel, set/clear the pause reason while in flight.
	if running := firstByStatus(jobs, model.JobRunning); running != nil {
		completed, adv, err := e.reconcileRunning(iss, running)
		if err != nil {
			return advances, err
		}
		advances = append(advances, adv...)
		if !completed {
			// Still in flight, paused, or cancelled — nothing to advance.
			return advances, nil
		}
		// The running job completed; re-read so the advance step sees it.
		jobs, err = e.st.ListPipelineJobs(iss.ID)
		if err != nil {
			return advances, err
		}
	}

	// 2) Auto-advance only. In manual (off) mode the user drives the next
	//    step via the Start / Ship controls (the API's manual verbs).
	if iss.EngineMode != model.EngineAuto {
		return advances, nil
	}
	adv, err := e.advanceChain(iss, jobs)
	return append(advances, adv...), err
}

// advanceChain advances the card one step: start the next pending agent
// job, or hand off to to_be_shipped when the next stage is the ship
// sentinel (§6). When the chain is exhausted with no ship sentinel left
// the card finishes in place — it stays in_pipeline with every job
// complete and the engine goes idle. A no-Ship process (plan-implement,
// plan, implement) therefore never auto-hands-off to Shipping; only an
// explicit ship sentinel triggers the hand-off (BACI-277). Assumes no job
// is currently running — the caller guarantees that. Shared by the Auto
// tick and the manual StartNext.
func (e *Engine) advanceChain(iss *model.Issue, jobs []*model.PipelineJob) ([]Advance, error) {
	next := firstByStatus(jobs, model.JobPending)
	if next == nil {
		// Chain exhausted with no Ship stage left — the card finishes in
		// place (stays in_pipeline, every job complete). A no-Ship process
		// never hands off to Shipping automatically (BACI-277).
		return nil, nil
	}
	if next.IsShipHandoff() {
		return e.handoff(iss)
	}
	if next.IsShelveHandoff() {
		return e.shelve(iss)
	}
	return e.startJob(iss, next)
}

// StartNext is the manual "Start" control: advance the card one step
// regardless of engine mode — start the next pending agent job, or hand
// off to Shipping at the ship sentinel. A no-op (nil advance) when a job
// is already running, when the card isn't in_pipeline, or when a no-Ship
// chain is exhausted (the card finishes in place — same as Auto).
// Shares advanceChain with the Auto tick so manual and auto behave
// identically.
func (e *Engine) StartNext(issueID int64) ([]Advance, error) {
	if e == nil || e.st == nil {
		return nil, nil
	}
	iss, err := e.st.GetIssueByID(issueID)
	if err != nil {
		return nil, err
	}
	if iss.State != model.StateInPipeline {
		return nil, nil
	}
	jobs, err := e.st.ListPipelineJobs(issueID)
	if err != nil {
		return nil, err
	}
	if firstByStatus(jobs, model.JobRunning) != nil {
		return nil, nil // a job is in flight — can't start another
	}
	return e.advanceChain(iss, jobs)
}

// RerunJob is the per-job "Re-run" control on an aborted step (BACI-291):
// reset a specific cancelled job back to pending and immediately
// re-dispatch it, so a Stopped step can be retried in one click. A no-op
// (nil advance, no error) when the card isn't in_pipeline or another job
// is already running (can't run two at once). The target job must be
// cancelled — a complete or running job returns ErrJobNotCancelled, since
// re-run is an abort-recovery affordance, not a re-run of arbitrary
// stages. Re-run targets one job by id; an unrelated complete job earlier
// or later in the chain is left untouched.
func (e *Engine) RerunJob(issueID, jobID int64) ([]Advance, error) {
	if e == nil || e.st == nil {
		return nil, nil
	}
	iss, err := e.st.GetIssueByID(issueID)
	if err != nil {
		return nil, err
	}
	if iss.State != model.StateInPipeline {
		return nil, nil
	}
	jobs, err := e.st.ListPipelineJobs(issueID)
	if err != nil {
		return nil, err
	}
	if firstByStatus(jobs, model.JobRunning) != nil {
		return nil, nil // a job is in flight — can't re-run another
	}
	var target *model.PipelineJob
	for _, j := range jobs {
		if j.ID == jobID {
			target = j
			break
		}
	}
	if target == nil {
		return nil, store.ErrNotFound
	}
	if target.Status != model.JobCancelled {
		return nil, ErrJobNotCancelled
	}
	if err := e.st.ResetPipelineJobToPending(target.ID); err != nil {
		return nil, err
	}
	target.Status = model.JobPending
	target.DispatchID = nil
	target.StartedAt = nil
	target.CompletedAt = nil
	return e.startJob(iss, target)
}

// StopRunning is the manual "Stop / Cancel" control: cancel the card's
// running job (and its dispatch), signal the running worker to wind down,
// and halt Auto. A no-op when no job is running. If the dispatch was
// already delivered to a worker, the row can't be cancelled (BACI-130) —
// the job is still marked cancelled and Auto halted; the worker's eventual
// ack is then ignored because the job is already terminal.
//
// Because a delivered worker keeps running its Task subagent in the
// background, engine bookkeeping alone doesn't actually stop the work. So
// Stop also enqueues a canned BACI-286 steer message at the worker's open
// claim session, asking it to wind down at its next turn boundary (the
// only signal an MCP server can deliver — there is no hard interrupt). The
// steer is best-effort and never fails the Stop: a missing session (the
// dispatch was never delivered, or the worker already released) just skips
// the message.
func (e *Engine) StopRunning(issueID int64) ([]Advance, error) {
	if e == nil || e.st == nil {
		return nil, nil
	}
	iss, err := e.st.GetIssueByID(issueID)
	if err != nil {
		return nil, err
	}
	jobs, err := e.st.ListPipelineJobs(issueID)
	if err != nil {
		return nil, err
	}
	running := firstByStatus(jobs, model.JobRunning)
	if running == nil {
		return nil, nil
	}
	if running.DispatchID != nil {
		if _, err := e.st.CancelDispatch(*running.DispatchID); err != nil {
			e.logger().Warn("bacio engine: stop could not cancel dispatch (likely already delivered)",
				"issue", iss.Key, "dispatch", *running.DispatchID, "err", err)
		}
	}
	if err := e.st.SetPipelineJobStatus(running.ID, model.JobCancelled); err != nil {
		return nil, err
	}
	if err := e.st.SetIssueEngineMode(issueID, model.EngineOff); err != nil {
		return nil, err
	}
	e.setPause(iss, false)
	e.steerWorkerToStop(iss)
	return []Advance{e.advance(iss, "job.cancelled", fmt.Sprintf("seq=%d mode=%s (stopped)", running.Sequence, running.Mode))}, nil
}

// ResetProcess is the manual "Reset" control (BACI-314): wipe the card's
// ENTIRE job chain — including completed / cancelled history — so the card
// drops back to the from-scratch "Pick a process" picker. Unlike
// SetIssueProcess (which refuses once a job has started, to keep history
// immutable) Reset deliberately discards that history. It refuses with
// ErrJobRunning while a job is running — Reset would orphan the live worker
// + dispatch, so the user must Stop first (StopRunning cancels the job and
// steers the worker). After clearing the rows it disarms Auto and clears
// any pause reason so a freshly-emptied card can't silently resume on the
// next tick — the same teardown StopRunning applies. A no-op (nil, nil)
// when the card isn't in_pipeline.
func (e *Engine) ResetProcess(issueID int64) error {
	if e == nil || e.st == nil {
		return nil
	}
	iss, err := e.st.GetIssueByID(issueID)
	if err != nil {
		return err
	}
	if iss.State != model.StateInPipeline {
		return nil
	}
	jobs, err := e.st.ListPipelineJobs(issueID)
	if err != nil {
		return err
	}
	if firstByStatus(jobs, model.JobRunning) != nil {
		return ErrJobRunning
	}
	if err := e.st.ClearIssueProcess(issueID); err != nil {
		return err
	}
	if err := e.st.SetIssueEngineMode(issueID, model.EngineOff); err != nil {
		return err
	}
	return e.st.SetIssueEnginePauseReason(issueID, "")
}

// steerWorkerToStop pushes the canned wind-down note at the newest open
// claim session for the issue (the worker running the job). Best-effort:
// no open claim (never delivered, or already released) is a silent skip,
// and a write error is logged-and-continued — the engine never fails a
// Stop on the steer signal. The claim list is claimed_at DESC, so the
// first un-released claim is the newest, mirroring how boardcards derives
// the card's RunningSessionID.
func (e *Engine) steerWorkerToStop(iss *model.Issue) {
	claims, err := e.st.ListClaimsForIssue(iss.ID)
	if err != nil {
		e.logger().Warn("bacio engine: stop steer could not list claims", "issue", iss.Key, "err", err)
		return
	}
	var sessionID string
	for _, c := range claims {
		if c != nil && c.ReleasedAt == nil && c.SessionID != "" {
			sessionID = c.SessionID
			break
		}
	}
	if sessionID == "" {
		return // no worker to steer (dispatch never delivered, or already released)
	}
	if _, err := e.st.AddUserMessage(store.AddUserMessageIn{
		SessionID: sessionID,
		Body:      stopWorkerSteerBody,
		CreatedBy: model.ControllerActor,
	}); err != nil {
		e.logger().Warn("bacio engine: stop steer message failed", "issue", iss.Key, "session", sessionID, "err", err)
	}
}

// FailRunning reconciles an in_pipeline card whose worker died on an
// Anthropic API error (BACI-296). It's the dual of StopRunning — same
// "cancel the in-flight job" teardown — and since BACI-300 it pauses the
// card in place for BOTH error classes, never auto-retrying. The card
// stays in_pipeline; the error class only selects the pause reason so the
// Pipeline UI can word the halt differently:
//
//   - Transient (server_error / rate_limit): an outage. Set
//     engine_pause_reason = "agent_error_transient" — "Start to retry
//     once it clears". We deliberately don't re-queue, which would risk a
//     tight rebind/die loop during a sustained outage.
//
//   - Terminal (auth / billing / org-not-allowed / unknown / …): set
//     engine_pause_reason = "agent_error_terminal" — the user must fix an
//     account/config problem before a retry can succeed. (Pre-BACI-300
//     this yanked the card out to needs_action; that state is gone.)
//
// A no-op (nil, nil) when the card isn't in_pipeline or has no running
// job: a worker that died before its job started running (or after it
// already settled) has nothing to reconcile, and the agent-errored state
// recorded separately by the hook is enough.
func (e *Engine) FailRunning(issueID int64, errType, errMsg string) ([]Advance, error) {
	// Both error classes pause the chain in place (BACI-300): the only
	// thing the class selects is the pause reason (so the UI can word the
	// halt differently) and the advance-reason label. No auto-retry for
	// either: re-binding into a sustained outage or a billing failure just
	// tight-loops, so the user re-arms with Start/Auto once the cause
	// clears. The shared teardown lives in failOrCancelRunning.
	transient := model.IsTransientAPIError(errType)
	pauseReason := model.EnginePauseReasonAgentErrorTerminal
	label := "terminal; paused"
	if transient {
		pauseReason = model.EnginePauseReasonAgentErrorTransient
		label = "transient; paused"
	}
	return e.failOrCancelRunning(issueID, "job.failed", pauseReason,
		fmt.Sprintf("error=%s (%s)", errType, label))
}

// CancelRunning reconciles an in_pipeline card whose worker was
// soft-cancelled (Esc / interrupt) by the user (BACI-328). Control
// returned to the supervisor session, which calls the
// `report_subagent_incomplete` channel tool; this is the engine primitive
// behind it. Same "cancel the in-flight job" teardown as FailRunning, but
// it stamps the neutral engine_pause_reason = "subagent_cancelled" — a
// deliberate user cancel, not a failure, so the card pauses in place with
// a neutral "Cancelled — Start to retry" halt rather than a red error pill.
//
// A no-op (nil, nil) when the card isn't in_pipeline or has no running
// job, so the tool is idempotent: a dispatch whose job already settled (or
// a too-early cancel before the job started running) has nothing to
// reconcile.
func (e *Engine) CancelRunning(issueID int64) ([]Advance, error) {
	return e.failOrCancelRunning(issueID, "job.cancelled.subagent",
		model.EnginePauseReasonSubagentCancelled, "cancelled; paused")
}

// failOrCancelRunning is the shared teardown behind FailRunning (API
// error) and CancelRunning (user cancel): cancel the running job + its
// dispatch, halt Auto, and stamp the given pause reason — the card stays
// in_pipeline. advanceKind / detail name the audit advance the caller
// wants (the pause reason is the only behavioural difference between the
// two entry points). A no-op (nil, nil) when the card isn't in_pipeline or
// has no running job.
func (e *Engine) failOrCancelRunning(issueID int64, advanceKind, pauseReason, detail string) ([]Advance, error) {
	if e == nil || e.st == nil {
		return nil, nil
	}
	iss, err := e.st.GetIssueByID(issueID)
	if err != nil {
		return nil, err
	}
	if iss.State != model.StateInPipeline {
		return nil, nil
	}
	jobs, err := e.st.ListPipelineJobs(issueID)
	if err != nil {
		return nil, err
	}
	running := firstByStatus(jobs, model.JobRunning)
	if running == nil {
		return nil, nil
	}

	if running.DispatchID != nil {
		if _, err := e.st.CancelDispatch(*running.DispatchID); err != nil {
			e.logger().Warn("bacio engine: could not cancel dispatch (likely already delivered)",
				"issue", iss.Key, "dispatch", *running.DispatchID, "err", err)
		}
	}
	if err := e.st.SetPipelineJobStatus(running.ID, model.JobCancelled); err != nil {
		return nil, err
	}
	if err := e.st.SetIssueEngineMode(issueID, model.EngineOff); err != nil {
		return nil, err
	}
	if err := e.st.SetIssueEnginePauseReason(issueID, pauseReason); err != nil {
		return nil, err
	}
	return []Advance{e.advance(iss, advanceKind, fmt.Sprintf("seq=%d mode=%s %s", running.Sequence, running.Mode, detail))}, nil
}

// Handoff is the manual "Ship" control (and the in-process Ship stage):
// move an in_pipeline card to to_be_shipped. A no-op when the card isn't
// in_pipeline. Same transition the Auto chain reaches at the ship
// sentinel. A no-Ship-process card never reaches this automatically, but
// a user may still consciously ship one through this control.
func (e *Engine) Handoff(issueID int64) ([]Advance, error) {
	if e == nil || e.st == nil {
		return nil, nil
	}
	iss, err := e.st.GetIssueByID(issueID)
	if err != nil {
		return nil, err
	}
	if iss.State != model.StateInPipeline {
		return nil, nil
	}
	return e.handoff(iss)
}

// reconcileRunning inspects the running job's dispatch. Returns
// completed=true only when the job moved to complete (so the caller may
// advance the chain). A cancelled dispatch halts the chain (Auto off);
// an in-flight dispatch sets/clears the pause reason from the job's open
// question and returns completed=false.
func (e *Engine) reconcileRunning(iss *model.Issue, job *model.PipelineJob) (bool, []Advance, error) {
	if job.DispatchID == nil {
		// running + no dispatch is an inconsistent state the engine never
		// writes (StartPipelineJobWithDispatch sets both atomically). Log
		// and leave for manual inspection rather than risk a re-queue loop.
		e.logger().Warn("bacio engine: running job has no dispatch", "issue", iss.Key, "job", job.ID)
		return false, nil, nil
	}
	d, err := e.st.GetDispatch(*job.DispatchID)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			// The dispatch was pruned out from under a still-running job
			// (only settled dispatches are pruned, so this means the ack
			// signal aged out). Treat as complete so the chain isn't
			// wedged forever.
			e.logger().Warn("bacio engine: running job's dispatch vanished; completing", "issue", iss.Key, "job", job.ID)
			if err := e.st.SetPipelineJobStatus(job.ID, model.JobComplete); err != nil {
				return false, nil, err
			}
			e.setPause(iss, false)
			return true, []Advance{e.advance(iss, "job.complete", fmt.Sprintf("seq=%d mode=%s (dispatch pruned)", job.Sequence, job.Mode))}, nil
		}
		return false, nil, err
	}
	switch d.Status {
	case model.DispatchAcked:
		if err := e.st.SetPipelineJobStatus(job.ID, model.JobComplete); err != nil {
			return false, nil, err
		}
		e.setPause(iss, false)
		return true, []Advance{e.advance(iss, "job.complete", fmt.Sprintf("seq=%d mode=%s", job.Sequence, job.Mode))}, nil
	case model.DispatchCancelled:
		if err := e.st.SetPipelineJobStatus(job.ID, model.JobCancelled); err != nil {
			return false, nil, err
		}
		// A cancelled job is a Stop — halt Auto so the engine doesn't run
		// straight on to the next stage. The user re-arms with Auto/Start.
		if err := e.st.SetIssueEngineMode(iss.ID, model.EngineOff); err != nil {
			return false, nil, err
		}
		e.setPause(iss, false)
		return false, []Advance{e.advance(iss, "job.cancelled", fmt.Sprintf("seq=%d mode=%s", job.Sequence, job.Mode))}, nil
	default:
		// queued / pending / delivered — in flight. The pause reason
		// mirrors whether the current job has an open question (§6.1).
		hasQ, err := e.st.HasOpenQuestionForJob(job.ID)
		if err != nil {
			return false, nil, err
		}
		e.setPause(iss, hasQ)
		return false, nil, nil
	}
}

// startJob queues a dispatch for the job's mode and CAS-claims the job to
// running. If the CAS loses (a concurrent start/cancel raced it), the
// freshly-queued dispatch is cancelled so it doesn't orphan.
func (e *Engine) startJob(iss *model.Issue, job *model.PipelineJob) ([]Advance, error) {
	d, err := e.st.AddDispatch(store.AddDispatchIn{
		RepoID:        iss.RepoID,
		IssueID:       &iss.ID,
		Mode:          model.DispatchMode(job.Mode),
		CreatedBy:     model.ControllerActor,
		InitialStatus: model.DispatchQueued,
	})
	if err != nil {
		return nil, err
	}
	won, err := e.st.StartPipelineJobWithDispatch(job.ID, d.ID)
	if err != nil {
		return nil, err
	}
	if !won {
		if _, cerr := e.st.CancelDispatch(d.ID); cerr != nil {
			e.logger().Warn("bacio engine: cancel orphan dispatch failed", "dispatch", d.ID, "err", cerr)
		}
		return nil, nil
	}
	e.setPause(iss, false)
	return []Advance{e.advance(iss, "job.start", fmt.Sprintf("seq=%d mode=%s dispatch=%d", job.Sequence, job.Mode, d.ID))}, nil
}

// handoff moves the card to to_be_shipped: it marks any pending ship
// sentinel job complete, flips the issue state (the engine-governed
// guard permits in_pipeline → to_be_shipped — it only blocks processing
// states), and clears the pause reason. Idempotent: if the issue already
// left in_pipeline, SetIssueState is a no-op on the non-pipeline state.
func (e *Engine) handoff(iss *model.Issue) ([]Advance, error) {
	jobs, err := e.st.ListPipelineJobs(iss.ID)
	if err != nil {
		return nil, err
	}
	for _, j := range jobs {
		if j.IsShipHandoff() && j.Status == model.JobPending {
			if err := e.st.SetPipelineJobStatus(j.ID, model.JobComplete); err != nil {
				return nil, err
			}
		}
	}
	if err := e.st.SetIssueState(iss.ID, model.StateToBeShipped); err != nil {
		return nil, err
	}
	if err := e.st.SetIssueEnginePauseReason(iss.ID, ""); err != nil {
		return nil, err
	}
	return []Advance{e.advance(iss, "handoff", "-> to_be_shipped")}, nil
}

// shelve returns the card to Backlog at the Shelve sentinel (BACI-332):
// the demoting counterpart of handoff. It clears the card's whole job
// chain — including the just-reached shelve sentinel and any completed
// history — then flips the issue back to todo. Clearing first means the
// in_pipeline → todo teardown in SetIssueState (cancelRunningPipelineJob:
// disarm Auto, clear the pause reason) sees an empty chain — there is no
// running job at the sentinel anyway — so the demoted card is left
// indistinguishable from one a user dragged back to Backlog, with no
// lingering pipeline_jobs rows, and is freely re-pipeable (AC #5).
func (e *Engine) shelve(iss *model.Issue) ([]Advance, error) {
	if err := e.st.ClearIssueProcess(iss.ID); err != nil {
		return nil, err
	}
	if err := e.st.SetIssueState(iss.ID, model.StateTodo); err != nil {
		return nil, err
	}
	return []Advance{e.advance(iss, "shelve", "-> todo")}, nil
}

// setPause writes the engine pause reason iff it changed — avoids a write
// (and sync churn) on every tick while a question stays open/closed.
func (e *Engine) setPause(iss *model.Issue, paused bool) {
	want := ""
	if paused {
		want = model.EnginePauseReasonOpenQuestion
	}
	if iss.EnginePauseReason == want {
		return
	}
	if err := e.st.SetIssueEnginePauseReason(iss.ID, want); err != nil {
		e.logger().Warn("bacio engine: set pause reason failed", "issue", iss.Key, "err", err)
		return
	}
	iss.EnginePauseReason = want
}

func (e *Engine) advance(iss *model.Issue, kind, detail string) Advance {
	return Advance{
		IssueKey:   iss.Key,
		IssueID:    iss.ID,
		RepoID:     iss.RepoID,
		RepoPrefix: prefixOfKey(iss.Key),
		Kind:       kind,
		Detail:     detail,
	}
}

// firstByStatus returns the first job (sequence order) in the given
// status, or nil. jobs are already sequence-ordered by ListPipelineJobs.
func firstByStatus(jobs []*model.PipelineJob, status model.JobStatus) *model.PipelineJob {
	for _, j := range jobs {
		if j.Status == status {
			return j
		}
	}
	return nil
}

// prefixOfKey returns "MINI" from "MINI-42" (everything before the last
// '-'); the whole key if there's no '-'.
func prefixOfKey(key string) string {
	if i := strings.LastIndex(key, "-"); i > 0 {
		return key[:i]
	}
	return key
}
