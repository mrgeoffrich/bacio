package api

import (
	"fmt"
	"net/http"

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
