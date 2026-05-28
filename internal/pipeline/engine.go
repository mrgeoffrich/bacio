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
// hand the card off to to_be_shipped at the ship sentinel / chain end).
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

// AutoShipTick runs one pass over the Shipping column: for every repo
// with auto-ship on, it acts on the single top (next-to-ship) card —
// queue a ship-mode agent when none has run, wait while one is in flight,
// or advance the card to done once the ship dispatch acks. A
// deliberately-cancelled ship is left alone (no retry loop); the next
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
		if !settings.AutoShip {
			continue
		}
		adv, err := e.autoShipRepo(repo.ID)
		if err != nil {
			e.logger().Warn("bacio engine: auto-ship failed", "repo", repo.Prefix, "err", err)
			continue
		}
		advances = append(advances, adv...)
	}
	return advances, nil
}

func (e *Engine) autoShipRepo(repoID int64) ([]Advance, error) {
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
	switch {
	case latest == nil:
		// No ship has run yet — dispatch the ship agent (the matcher binds
		// it; the ship template's concurrency_limit serialises merges).
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
	case latest.Status == model.DispatchAcked:
		// Ship done → terminal. SetIssueState permits to_be_shipped → done
		// (done is not a processing state). Idempotent if the ship worker
		// already moved it (then it's no longer the top to_be_shipped card).
		if err := e.st.SetIssueState(top.ID, model.StateDone); err != nil {
			return nil, err
		}
		return []Advance{e.advance(top, "ship.done", "auto-ship complete")}, nil
	case latest.Status == model.DispatchCancelled:
		// A deliberately-cancelled ship — leave it so auto-ship doesn't
		// loop. Manual SHIP re-arms it.
		return nil, nil
	default:
		// queued / pending / delivered — ship in flight, wait.
		return nil, nil
	}
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
	//    step via the Start / Ship controls (a later phase's API).
	if iss.EngineMode != model.EngineAuto {
		return advances, nil
	}

	next := firstByStatus(jobs, model.JobPending)
	if next == nil {
		// Chain exhausted with Auto on → hand off to Shipping (§6).
		adv, err := e.handoff(iss)
		return append(advances, adv...), err
	}
	if next.IsShipHandoff() {
		adv, err := e.handoff(iss)
		return append(advances, adv...), err
	}
	adv, err := e.startJob(iss, next)
	return append(advances, adv...), err
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
