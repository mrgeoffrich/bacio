package api

import (
	"errors"
	"fmt"
	"net/http"
	"strconv"

	"github.com/mrgeoffrich/bacio/internal/cli/inputs"
	"github.com/mrgeoffrich/bacio/internal/inputio"
	"github.com/mrgeoffrich/bacio/internal/model"
	"github.com/mrgeoffrich/bacio/internal/pipeline"
)

// handleIssueReorder — PUT /repos/{prefix}/issues/{key}/reorder. Moves
// the card to a 1-based position within its (repo, state) ordering band
// (Pipeline Backlog / Shipping drag-to-reorder).
func (d deps) handleIssueReorder(w http.ResponseWriter, r *http.Request) {
	repo, ok := resolveRepoFromPath(w, r, d.store)
	if !ok {
		return
	}
	raw, ok := readBody(r, w)
	if !ok {
		return
	}
	in, _, err := inputio.DecodeStrict[inputs.IssueReorderInput](raw)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_input", err.Error(), nil)
		return
	}
	if in.Position < 1 {
		writeError(w, http.StatusBadRequest, "invalid_input", "position must be >= 1", map[string]any{"field": "position"})
		return
	}
	iss, ok := resolveIssueOnRepo(w, r, d.store, repo)
	if !ok {
		return
	}
	if isDryRun(r) {
		writeDryRun(w, http.StatusOK, iss)
		return
	}
	if err := d.store.ReorderIssue(iss.ID, in.Position); err != nil {
		status, code := statusForError(err)
		writeError(w, status, code, err.Error(), nil)
		return
	}
	recordOp(d.store, d.logger, model.HistoryEntry{
		RepoID: &iss.RepoID, RepoPrefix: repo.Prefix,
		Actor: ActorFromContext(r.Context()),
		Op:    "issue.reorder", Kind: "issue",
		TargetID: &iss.ID, TargetLabel: iss.Key,
		Details: fmt.Sprintf("position=%d", in.Position),
	})
	updated, err := d.store.GetIssueByID(iss.ID)
	if err != nil {
		status, code := statusForError(err)
		writeError(w, status, code, err.Error(), nil)
		return
	}
	writeJSON(w, http.StatusOK, updated)
}

// handleIssueProcess — POST /repos/{prefix}/issues/{key}/process. Assigns
// a process, materialising the card's pending job chain. Accepts either a
// preset slug (process) or an explicit ordered stage list (stages) —
// mutually exclusive (model.ResolveProcess).
func (d deps) handleIssueProcess(w http.ResponseWriter, r *http.Request) {
	repo, ok := resolveRepoFromPath(w, r, d.store)
	if !ok {
		return
	}
	raw, ok := readBody(r, w)
	if !ok {
		return
	}
	in, _, err := inputio.DecodeStrict[inputs.IssueProcessInput](raw)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_input", err.Error(), nil)
		return
	}
	proc, err := model.ResolveProcess(in.Process, in.Stages)
	if err != nil {
		field := "process"
		if len(in.Stages) > 0 {
			field = "stages"
		}
		writeError(w, http.StatusBadRequest, "invalid_input", err.Error(), map[string]any{"field": field})
		return
	}
	iss, ok := resolveIssueOnRepo(w, r, d.store, repo)
	if !ok {
		return
	}
	if isDryRun(r) {
		writeDryRun(w, http.StatusOK, iss)
		return
	}
	jobs, err := d.store.SetIssueProcess(iss.ID, proc)
	if err != nil {
		status, code := statusForError(err)
		writeError(w, status, code, err.Error(), nil)
		return
	}
	recordOp(d.store, d.logger, model.HistoryEntry{
		RepoID: &iss.RepoID, RepoPrefix: repo.Prefix,
		Actor: ActorFromContext(r.Context()),
		Op:    "issue.process", Kind: "issue",
		TargetID: &iss.ID, TargetLabel: iss.Key,
		Details: proc.Slug,
	})
	writeJSON(w, http.StatusOK, jobs)
}

// handleIssueProcessEdit — PUT /repos/{prefix}/issues/{key}/process/tail.
// Edits the pending tail of an in_pipeline card's chain (BACI-294): keeps
// the completed / running / cancelled jobs as a locked prefix and replaces
// the pending tail with the validated, re-sequenced `stages` list. The
// client sends only the tail; the server reads the locked prefix from the
// store as the source of truth, so a stale client can never corrupt
// history. Validation errors return 400 with field=stages.
func (d deps) handleIssueProcessEdit(w http.ResponseWriter, r *http.Request) {
	repo, ok := resolveRepoFromPath(w, r, d.store)
	if !ok {
		return
	}
	raw, ok := readBody(r, w)
	if !ok {
		return
	}
	in, _, err := inputio.DecodeStrict[inputs.IssueProcessEditInput](raw)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_input", err.Error(), nil)
		return
	}
	iss, ok := resolveIssueOnRepo(w, r, d.store, repo)
	if !ok {
		return
	}
	// Read the live chain to partition the locked prefix and validate the
	// combined chain before any write — the same source of truth the store
	// op uses, so the dry-run projection and the 400-on-bad-tail check
	// agree with the eventual write.
	existing, err := d.store.ListPipelineJobs(iss.ID)
	if err != nil {
		status, code := statusForError(err)
		writeError(w, status, code, err.Error(), nil)
		return
	}
	if len(existing) == 0 {
		writeError(w, http.StatusBadRequest, "invalid_input", "issue has no job chain to edit", map[string]any{"field": "stages"})
		return
	}
	var lockedModes []string
	var locked []*model.PipelineJob
	for _, j := range existing {
		if j.Status != model.JobPending {
			lockedModes = append(lockedModes, j.Mode)
			locked = append(locked, j)
		}
	}
	proc, err := model.ProcessFromStagesWithPrefix(lockedModes, in.Stages)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_input", err.Error(), map[string]any{"field": "stages"})
		return
	}
	if isDryRun(r) {
		// Project the locked prefix + re-sequenced tail without writing.
		out := append([]*model.PipelineJob(nil), locked...)
		for i, mode := range proc.Stages {
			out = append(out, &model.PipelineJob{
				IssueID: iss.ID, Sequence: len(locked) + i + 1, Mode: mode, Status: model.JobPending,
			})
		}
		writeDryRun(w, http.StatusOK, out)
		return
	}
	jobs, err := d.store.EditIssueProcessTail(iss.ID, in.Stages)
	if err != nil {
		status, code := statusForError(err)
		writeError(w, status, code, err.Error(), nil)
		return
	}
	recordOp(d.store, d.logger, model.HistoryEntry{
		RepoID: &iss.RepoID, RepoPrefix: repo.Prefix,
		Actor: ActorFromContext(r.Context()),
		Op:    "issue.process.edit", Kind: "issue",
		TargetID: &iss.ID, TargetLabel: iss.Key,
		Details: proc.Slug,
	})
	writeJSON(w, http.StatusOK, jobs)
}

// handleIssueProcessReset — POST /repos/{prefix}/issues/{key}/process/reset.
// Wipes the card's ENTIRE job chain (BACI-314) — the deliberate counterpart
// to handleIssueProcess that bypasses the started>0 guard so a started /
// finished / aborted chain can be cleared, dropping the card back to the
// from-scratch picker. A running job is refused (Stop first): the engine
// returns pipeline.ErrJobRunning, mapped to 409 conflict. Returns the
// refreshed (empty) chain.
func (d deps) handleIssueProcessReset(w http.ResponseWriter, r *http.Request) {
	repo, ok := resolveRepoFromPath(w, r, d.store)
	if !ok {
		return
	}
	iss, ok := resolveIssueOnRepo(w, r, d.store, repo)
	if !ok {
		return
	}
	if isDryRun(r) {
		writeDryRun(w, http.StatusOK, []*model.PipelineJob{})
		return
	}
	if err := pipeline.New(d.store).WithLogger(d.logger).ResetProcess(iss.ID); err != nil {
		if errors.Is(err, pipeline.ErrJobRunning) {
			writeError(w, http.StatusConflict, "conflict", err.Error(), nil)
			return
		}
		status, code := statusForError(err)
		writeError(w, status, code, err.Error(), nil)
		return
	}
	recordOp(d.store, d.logger, model.HistoryEntry{
		RepoID: &iss.RepoID, RepoPrefix: repo.Prefix,
		Actor: ActorFromContext(r.Context()),
		Op:    "issue.process.reset", Kind: "issue",
		TargetID: &iss.ID, TargetLabel: iss.Key,
	})
	jobs, err := d.store.ListPipelineJobs(iss.ID)
	if err != nil {
		status, code := statusForError(err)
		writeError(w, status, code, err.Error(), nil)
		return
	}
	writeJSON(w, http.StatusOK, jobs)
}

// handleIssueJobs — GET /repos/{prefix}/issues/{key}/jobs. Returns the
// card's process chain (sequence-ordered).
func (d deps) handleIssueJobs(w http.ResponseWriter, r *http.Request) {
	repo, ok := resolveRepoFromPath(w, r, d.store)
	if !ok {
		return
	}
	iss, ok := resolveIssueOnRepo(w, r, d.store, repo)
	if !ok {
		return
	}
	jobs, err := d.store.ListPipelineJobs(iss.ID)
	if err != nil {
		status, code := statusForError(err)
		writeError(w, status, code, err.Error(), nil)
		return
	}
	writeJSON(w, http.StatusOK, jobs)
}

// handleIssueJobStart — POST /repos/{prefix}/issues/{key}/jobs/start. The
// manual Start control: advance one step (start next job or hand off).
func (d deps) handleIssueJobStart(w http.ResponseWriter, r *http.Request) {
	d.engineMutate(w, r, "issue.job.start", func(eng *pipeline.Engine, issueID int64) error {
		_, err := eng.StartNext(issueID)
		return err
	})
}

// handleIssueJobStop — POST /repos/{prefix}/issues/{key}/jobs/stop. The
// manual Stop control: cancel the running job and halt Auto.
func (d deps) handleIssueJobStop(w http.ResponseWriter, r *http.Request) {
	d.engineMutate(w, r, "issue.job.stop", func(eng *pipeline.Engine, issueID int64) error {
		_, err := eng.StopRunning(issueID)
		return err
	})
}

// handleIssueJobRerun — POST /repos/{prefix}/issues/{key}/jobs/{seq}/rerun.
// The per-job Re-run control on an aborted step (BACI-291): reset the
// cancelled job at {seq} back to pending and re-dispatch it. Returns the
// refreshed chain. A non-cancelled job yields 409; an unknown seq, 404.
// Body-less like Start/Stop, but {seq}-scoped because re-run targets a
// specific aborted job, not "the next pending one".
func (d deps) handleIssueJobRerun(w http.ResponseWriter, r *http.Request) {
	repo, ok := resolveRepoFromPath(w, r, d.store)
	if !ok {
		return
	}
	iss, ok := resolveIssueOnRepo(w, r, d.store, repo)
	if !ok {
		return
	}
	seq, err := strconv.Atoi(r.PathValue("seq"))
	if err != nil || seq < 1 {
		writeError(w, http.StatusBadRequest, "invalid_input", "seq must be a positive integer", map[string]any{"field": "seq"})
		return
	}
	jobs, err := d.store.ListPipelineJobs(iss.ID)
	if err != nil {
		status, code := statusForError(err)
		writeError(w, status, code, err.Error(), nil)
		return
	}
	var target *model.PipelineJob
	for _, j := range jobs {
		if j.Sequence == seq {
			target = j
			break
		}
	}
	if target == nil {
		writeError(w, http.StatusNotFound, "not_found", fmt.Sprintf("no job at sequence %d", seq), nil)
		return
	}
	if _, err := pipeline.New(d.store).WithLogger(d.logger).RerunJob(iss.ID, target.ID); err != nil {
		if errors.Is(err, pipeline.ErrJobNotCancelled) {
			writeError(w, http.StatusConflict, "conflict", err.Error(), map[string]any{"field": "seq"})
			return
		}
		status, code := statusForError(err)
		writeError(w, status, code, err.Error(), nil)
		return
	}
	recordOp(d.store, d.logger, model.HistoryEntry{
		RepoID: &iss.RepoID, RepoPrefix: repo.Prefix,
		Actor: ActorFromContext(r.Context()),
		Op:    "issue.job.rerun", Kind: "issue",
		TargetID: &iss.ID, TargetLabel: iss.Key,
		Details: fmt.Sprintf("seq=%d mode=%s", target.Sequence, target.Mode),
	})
	jobs, err = d.store.ListPipelineJobs(iss.ID)
	if err != nil {
		status, code := statusForError(err)
		writeError(w, status, code, err.Error(), nil)
		return
	}
	writeJSON(w, http.StatusOK, jobs)
}

// handleIssueShip — POST /repos/{prefix}/issues/{key}/ship. The hand-off:
// move an in_pipeline card to to_be_shipped (no agent dispatched here —
// the ship agent fires from the Shipping column). Returns the updated
// issue since the hand-off changes the card's column.
func (d deps) handleIssueShip(w http.ResponseWriter, r *http.Request) {
	repo, ok := resolveRepoFromPath(w, r, d.store)
	if !ok {
		return
	}
	iss, ok := resolveIssueOnRepo(w, r, d.store, repo)
	if !ok {
		return
	}
	if isDryRun(r) {
		projected := *iss
		projected.State = model.StateToBeShipped
		writeDryRun(w, http.StatusOK, &projected)
		return
	}
	if _, err := pipeline.New(d.store).WithLogger(d.logger).Handoff(iss.ID); err != nil {
		status, code := statusForError(err)
		writeError(w, status, code, err.Error(), nil)
		return
	}
	recordOp(d.store, d.logger, model.HistoryEntry{
		RepoID: &iss.RepoID, RepoPrefix: repo.Prefix,
		Actor: ActorFromContext(r.Context()),
		Op:    "issue.ship", Kind: "issue",
		TargetID: &iss.ID, TargetLabel: iss.Key,
	})
	updated, err := d.store.GetIssueByID(iss.ID)
	if err != nil {
		status, code := statusForError(err)
		writeError(w, status, code, err.Error(), nil)
		return
	}
	writeJSON(w, http.StatusOK, updated)
}

// engineMutate is the shared body for the three engine-backed, body-less
// POST verbs (start / stop / ship): resolve repo + issue, run the engine
// op, audit it, and return the refreshed job chain.
func (d deps) engineMutate(w http.ResponseWriter, r *http.Request, op string, fn func(*pipeline.Engine, int64) error) {
	repo, ok := resolveRepoFromPath(w, r, d.store)
	if !ok {
		return
	}
	iss, ok := resolveIssueOnRepo(w, r, d.store, repo)
	if !ok {
		return
	}
	eng := pipeline.New(d.store).WithLogger(d.logger)
	if err := fn(eng, iss.ID); err != nil {
		status, code := statusForError(err)
		writeError(w, status, code, err.Error(), nil)
		return
	}
	recordOp(d.store, d.logger, model.HistoryEntry{
		RepoID: &iss.RepoID, RepoPrefix: repo.Prefix,
		Actor: ActorFromContext(r.Context()),
		Op:    op, Kind: "issue",
		TargetID: &iss.ID, TargetLabel: iss.Key,
	})
	jobs, err := d.store.ListPipelineJobs(iss.ID)
	if err != nil {
		status, code := statusForError(err)
		writeError(w, status, code, err.Error(), nil)
		return
	}
	writeJSON(w, http.StatusOK, jobs)
}

// handleIssueEngineMode — PUT /repos/{prefix}/issues/{key}/engine-mode.
// Sets the controller engine's drive mode for the card ("off" | "auto").
func (d deps) handleIssueEngineMode(w http.ResponseWriter, r *http.Request) {
	repo, ok := resolveRepoFromPath(w, r, d.store)
	if !ok {
		return
	}
	raw, ok := readBody(r, w)
	if !ok {
		return
	}
	in, _, err := inputio.DecodeStrict[inputs.IssueEngineModeInput](raw)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_input", err.Error(), nil)
		return
	}
	mode, err := model.ParseEngineMode(in.Mode)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_input", err.Error(), map[string]any{"field": "mode"})
		return
	}
	iss, ok := resolveIssueOnRepo(w, r, d.store, repo)
	if !ok {
		return
	}
	if isDryRun(r) {
		writeDryRun(w, http.StatusOK, iss)
		return
	}
	if err := d.store.SetIssueEngineMode(iss.ID, mode); err != nil {
		status, code := statusForError(err)
		writeError(w, status, code, err.Error(), nil)
		return
	}
	recordOp(d.store, d.logger, model.HistoryEntry{
		RepoID: &iss.RepoID, RepoPrefix: repo.Prefix,
		Actor: ActorFromContext(r.Context()),
		Op:    "issue.engine_mode", Kind: "issue",
		TargetID: &iss.ID, TargetLabel: iss.Key,
		Details: string(mode),
	})
	updated, err := d.store.GetIssueByID(iss.ID)
	if err != nil {
		status, code := statusForError(err)
		writeError(w, status, code, err.Error(), nil)
		return
	}
	writeJSON(w, http.StatusOK, updated)
}

// handleRepoAutoShip — PUT /repos/{prefix}/auto-ship. Toggles the
// per-repo Shipping-column auto-ship setting.
func (d deps) handleRepoAutoShip(w http.ResponseWriter, r *http.Request) {
	repo, ok := resolveRepoFromPath(w, r, d.store)
	if !ok {
		return
	}
	raw, ok := readBody(r, w)
	if !ok {
		return
	}
	in, _, err := inputio.DecodeStrict[inputs.RepoAutoShipInput](raw)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_input", err.Error(), nil)
		return
	}
	if isDryRun(r) {
		writeDryRun(w, http.StatusOK, map[string]any{"auto_ship": in.Enabled})
		return
	}
	if err := d.store.SetRepoAutoShip(repo.ID, in.Enabled); err != nil {
		status, code := statusForError(err)
		writeError(w, status, code, err.Error(), nil)
		return
	}
	recordOp(d.store, d.logger, model.HistoryEntry{
		RepoID: &repo.ID, RepoPrefix: repo.Prefix,
		Actor: ActorFromContext(r.Context()),
		Op:    "repo.auto_ship", Kind: "repo",
		TargetID: &repo.ID, TargetLabel: repo.Prefix,
		Details: fmt.Sprintf("enabled=%v", in.Enabled),
	})
	writeJSON(w, http.StatusOK, map[string]any{"auto_ship": in.Enabled})
}

// handleRepoAutoShipGet — GET /repos/{prefix}/auto-ship. Reads the
// per-repo Shipping-column auto-ship setting (the DB value the
// controller's auto-ship ticker acts on) so the UI can seed its toggle
// from the source of truth.
func (d deps) handleRepoAutoShipGet(w http.ResponseWriter, r *http.Request) {
	repo, ok := resolveRepoFromPath(w, r, d.store)
	if !ok {
		return
	}
	settings, err := d.store.GetRepoSettings(repo.ID)
	if err != nil {
		status, code := statusForError(err)
		writeError(w, status, code, err.Error(), nil)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"auto_ship": settings.AutoShip})
}

// backlogCollapsedIn is the strict-decoded body for PUT
// /repos/{prefix}/backlog-collapsed (BACI-288). A local request struct —
// there's no CLI verb for this per-repo display preference, so it isn't a
// bacio schema-registered inputs.* type (same precedent as
// board.hidden_states's boardHiddenStatesIn).
type backlogCollapsedIn struct {
	Collapsed bool `json:"collapsed"`
}

// handleRepoBacklogCollapsed — PUT /repos/{prefix}/backlog-collapsed.
// Persists the per-repo Pipeline Backlog-column collapse preference in
// the tui_settings KV (BACI-288). Honours ?dry_run=true and audits a
// repo_setting.update row only when the value actually changes.
func (d deps) handleRepoBacklogCollapsed(w http.ResponseWriter, r *http.Request) {
	repo, ok := resolveRepoFromPath(w, r, d.store)
	if !ok {
		return
	}
	raw, ok := readBody(r, w)
	if !ok {
		return
	}
	in, _, err := inputio.DecodeStrict[backlogCollapsedIn](raw)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_input", err.Error(), nil)
		return
	}
	if isDryRun(r) {
		writeDryRun(w, http.StatusOK, map[string]any{"backlog_collapsed": in.Collapsed})
		return
	}
	prev, err := d.store.IsBacklogCollapsed(repo.ID)
	if err != nil {
		status, code := statusForError(err)
		writeError(w, status, code, err.Error(), nil)
		return
	}
	if err := d.store.SetBacklogCollapsed(repo.ID, in.Collapsed); err != nil {
		status, code := statusForError(err)
		writeError(w, status, code, err.Error(), nil)
		return
	}
	if prev != in.Collapsed {
		recordOp(d.store, d.logger, model.HistoryEntry{
			RepoID: &repo.ID, RepoPrefix: repo.Prefix,
			Actor:       ActorFromContext(r.Context()),
			Op:          "repo_setting.update",
			Kind:        "repo_setting",
			TargetLabel: "pipeline.backlog_collapsed",
			Details:     fmt.Sprintf("collapsed=%v", in.Collapsed),
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"backlog_collapsed": in.Collapsed})
}

// handleRepoBacklogCollapsedGet — GET /repos/{prefix}/backlog-collapsed.
// Reads the per-repo Pipeline Backlog-column collapse preference so the
// Pipeline page seeds its rail state from the persisted KV (BACI-288).
func (d deps) handleRepoBacklogCollapsedGet(w http.ResponseWriter, r *http.Request) {
	repo, ok := resolveRepoFromPath(w, r, d.store)
	if !ok {
		return
	}
	collapsed, err := d.store.IsBacklogCollapsed(repo.ID)
	if err != nil {
		status, code := statusForError(err)
		writeError(w, status, code, err.Error(), nil)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"backlog_collapsed": collapsed})
}

// impactPrimaryIn is the strict-decoded body for PUT
// /repos/{prefix}/impact-primary (BACI-349). A local request struct like
// backlogCollapsedIn — no CLI verb backs this per-repo display preference.
type impactPrimaryIn struct {
	ImpactPrimary bool `json:"impact_primary"`
}

// handleRepoImpactPrimary — PUT /repos/{prefix}/impact-primary.
// Persists the per-repo Pipeline impact-primary display preference in the
// tui_settings KV (BACI-349). Honours ?dry_run=true and audits a
// repo_setting.update row only when the value actually changes. Clone of
// handleRepoBacklogCollapsed.
func (d deps) handleRepoImpactPrimary(w http.ResponseWriter, r *http.Request) {
	repo, ok := resolveRepoFromPath(w, r, d.store)
	if !ok {
		return
	}
	raw, ok := readBody(r, w)
	if !ok {
		return
	}
	in, _, err := inputio.DecodeStrict[impactPrimaryIn](raw)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_input", err.Error(), nil)
		return
	}
	if isDryRun(r) {
		writeDryRun(w, http.StatusOK, map[string]any{"impact_primary": in.ImpactPrimary})
		return
	}
	prev, err := d.store.IsImpactPrimary(repo.ID)
	if err != nil {
		status, code := statusForError(err)
		writeError(w, status, code, err.Error(), nil)
		return
	}
	if err := d.store.SetImpactPrimary(repo.ID, in.ImpactPrimary); err != nil {
		status, code := statusForError(err)
		writeError(w, status, code, err.Error(), nil)
		return
	}
	if prev != in.ImpactPrimary {
		recordOp(d.store, d.logger, model.HistoryEntry{
			RepoID: &repo.ID, RepoPrefix: repo.Prefix,
			Actor:       ActorFromContext(r.Context()),
			Op:          "repo_setting.update",
			Kind:        "repo_setting",
			TargetLabel: "pipeline.impact_primary",
			Details:     fmt.Sprintf("impact_primary=%v", in.ImpactPrimary),
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"impact_primary": in.ImpactPrimary})
}

// handleRepoImpactPrimaryGet — GET /repos/{prefix}/impact-primary.
// Reads the per-repo Pipeline impact-primary display preference so the
// Pipeline page seeds its toggle state from the persisted KV (BACI-349).
func (d deps) handleRepoImpactPrimaryGet(w http.ResponseWriter, r *http.Request) {
	repo, ok := resolveRepoFromPath(w, r, d.store)
	if !ok {
		return
	}
	impactPrimary, err := d.store.IsImpactPrimary(repo.ID)
	if err != nil {
		status, code := statusForError(err)
		writeError(w, status, code, err.Error(), nil)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"impact_primary": impactPrimary})
}
