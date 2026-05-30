package api_test

import (
	"net/http"
	"strings"
	"testing"

	"github.com/mrgeoffrich/bacio/internal/api"
	"github.com/mrgeoffrich/bacio/internal/model"
)

// TestPipelineEndpoints exercises the Pipeline REST surface end to end:
// assign a process, read the chain, toggle engine mode, hand off to
// Shipping, toggle auto-ship, and reorder a Shipping card.
func TestPipelineEndpoints(t *testing.T) {
	ts, s := newTestAPI(t, api.Options{})
	repo, err := s.CreateRepo("HTTP", "http-pipe", t.TempDir(), "")
	if err != nil {
		t.Fatalf("repo: %v", err)
	}
	iss, err := s.CreateIssue(repo.ID, nil, "card", "", model.StateInPipeline, nil, "")
	if err != nil {
		t.Fatalf("issue: %v", err)
	}
	base := ts.URL + "/repos/" + repo.Prefix + "/issues/" + iss.Key

	// Assign a process → 3-job chain.
	resp := do(t, http.MethodPost, base+"/process", strings.NewReader(`{"process":"plan-implement-ship"}`), nil)
	if resp.StatusCode != 200 {
		t.Fatalf("process status %d", resp.StatusCode)
	}
	jobs := decode[[]map[string]any](t, resp.Body)
	resp.Body.Close()
	if len(jobs) != 3 {
		t.Fatalf("process jobs = %d, want 3", len(jobs))
	}

	// GET the chain.
	resp = do(t, http.MethodGet, base+"/jobs", nil, nil)
	if resp.StatusCode != 200 {
		t.Fatalf("jobs status %d", resp.StatusCode)
	}
	jobs = decode[[]map[string]any](t, resp.Body)
	resp.Body.Close()
	if len(jobs) != 3 {
		t.Fatalf("GET jobs = %d, want 3", len(jobs))
	}

	// Engine mode → auto.
	resp = do(t, http.MethodPut, base+"/engine-mode", strings.NewReader(`{"mode":"auto"}`), nil)
	if resp.StatusCode != 200 {
		t.Fatalf("engine-mode status %d", resp.StatusCode)
	}
	resp.Body.Close()
	if got, _ := s.GetIssueByID(iss.ID); got.EngineMode != model.EngineAuto {
		t.Fatalf("engine mode = %s, want auto", got.EngineMode)
	}

	// Ship hand-off → to_be_shipped.
	resp = do(t, http.MethodPost, base+"/ship", nil, nil)
	if resp.StatusCode != 200 {
		t.Fatalf("ship status %d", resp.StatusCode)
	}
	resp.Body.Close()
	if got, _ := s.GetIssueByID(iss.ID); got.State != model.StateToBeShipped {
		t.Fatalf("state = %s, want to_be_shipped", got.State)
	}

	// Auto-ship toggle (per-repo).
	resp = do(t, http.MethodPut, ts.URL+"/repos/"+repo.Prefix+"/auto-ship", strings.NewReader(`{"enabled":true}`), nil)
	if resp.StatusCode != 200 {
		t.Fatalf("auto-ship status %d", resp.StatusCode)
	}
	resp.Body.Close()
	if rs, _ := s.GetRepoSettings(repo.ID); !rs.AutoShip {
		t.Fatal("auto-ship not persisted")
	}

	// Backlog-collapsed toggle (per-repo, BACI-288). GET defaults false.
	bcURL := ts.URL + "/repos/" + repo.Prefix + "/backlog-collapsed"
	resp = do(t, http.MethodGet, bcURL, nil, nil)
	if resp.StatusCode != 200 {
		t.Fatalf("backlog-collapsed GET status %d", resp.StatusCode)
	}
	bc := decode[map[string]any](t, resp.Body)
	resp.Body.Close()
	if got, _ := bc["backlog_collapsed"].(bool); got {
		t.Fatalf("backlog-collapsed default = true, want false")
	}

	// PUT {collapsed:true} persists; GET then reads true.
	resp = do(t, http.MethodPut, bcURL, strings.NewReader(`{"collapsed":true}`), nil)
	if resp.StatusCode != 200 {
		t.Fatalf("backlog-collapsed PUT status %d", resp.StatusCode)
	}
	resp.Body.Close()
	if collapsed, _ := s.IsBacklogCollapsed(repo.ID); !collapsed {
		t.Fatal("backlog-collapsed not persisted")
	}
	resp = do(t, http.MethodGet, bcURL, nil, nil)
	bc = decode[map[string]any](t, resp.Body)
	resp.Body.Close()
	if got, _ := bc["backlog_collapsed"].(bool); !got {
		t.Fatalf("backlog-collapsed GET after PUT = false, want true")
	}

	// dry_run leaves the persisted value untouched.
	resp = do(t, http.MethodPut, bcURL+"?dry_run=true", strings.NewReader(`{"collapsed":false}`), nil)
	if resp.StatusCode != 200 {
		t.Fatalf("backlog-collapsed dry-run status %d", resp.StatusCode)
	}
	resp.Body.Close()
	if collapsed, _ := s.IsBacklogCollapsed(repo.ID); !collapsed {
		t.Fatal("backlog-collapsed dry-run mutated the store")
	}

	// Reorder a Shipping card to the top (position 1 → priority 0).
	iss2, err := s.CreateIssue(repo.ID, nil, "card2", "", model.StateToBeShipped, nil, "")
	if err != nil {
		t.Fatalf("issue2: %v", err)
	}
	resp = do(t, http.MethodPut, ts.URL+"/repos/"+repo.Prefix+"/issues/"+iss2.Key+"/reorder", strings.NewReader(`{"position":1}`), nil)
	if resp.StatusCode != 200 {
		t.Fatalf("reorder status %d", resp.StatusCode)
	}
	resp.Body.Close()
	if got, _ := s.GetIssueByID(iss2.ID); got.Priority != 0 {
		t.Fatalf("reordered priority = %d, want 0", got.Priority)
	}
}

// TestJobRerunEndpoint covers the BACI-291 per-job re-run REST surface:
// re-running an aborted (cancelled) job returns 200 + the chain with that
// job back to running; an unknown seq is 404; a non-cancelled job is 409.
func TestJobRerunEndpoint(t *testing.T) {
	ts, s := newTestAPI(t, api.Options{})
	repo, err := s.CreateRepo("RERN", "http-rerun", t.TempDir(), "")
	if err != nil {
		t.Fatalf("repo: %v", err)
	}
	iss, err := s.CreateIssue(repo.ID, nil, "card", "", model.StateInPipeline, nil, "")
	if err != nil {
		t.Fatalf("issue: %v", err)
	}
	proc, err := model.ProcessBySlug("plan-implement")
	if err != nil {
		t.Fatalf("ProcessBySlug: %v", err)
	}
	jobs, err := s.SetIssueProcess(iss.ID, proc)
	if err != nil {
		t.Fatalf("SetIssueProcess: %v", err)
	}
	base := ts.URL + "/repos/" + repo.Prefix + "/issues/" + iss.Key

	// Re-run on a pending (non-cancelled) job → 409.
	resp := do(t, http.MethodPost, base+"/jobs/1/rerun", nil, nil)
	if resp.StatusCode != 409 {
		t.Fatalf("rerun pending status %d, want 409", resp.StatusCode)
	}
	resp.Body.Close()

	// Unknown seq → 404.
	resp = do(t, http.MethodPost, base+"/jobs/99/rerun", nil, nil)
	if resp.StatusCode != 404 {
		t.Fatalf("rerun unknown seq status %d, want 404", resp.StatusCode)
	}
	resp.Body.Close()

	// Abort the first job (run → cancel) so it's re-runnable.
	if err := s.SetPipelineJobStatus(jobs[0].ID, model.JobRunning); err != nil {
		t.Fatalf("set running: %v", err)
	}
	if err := s.SetPipelineJobStatus(jobs[0].ID, model.JobCancelled); err != nil {
		t.Fatalf("set cancelled: %v", err)
	}

	// Re-run the aborted job → 200, job 1 back to running.
	resp = do(t, http.MethodPost, base+"/jobs/1/rerun", nil, nil)
	if resp.StatusCode != 200 {
		t.Fatalf("rerun aborted status %d, want 200", resp.StatusCode)
	}
	chain := decode[[]map[string]any](t, resp.Body)
	resp.Body.Close()
	if len(chain) == 0 {
		t.Fatal("rerun returned an empty chain")
	}
	if got, _ := chain[0]["status"].(string); got != "running" {
		t.Fatalf("re-run job 1 status = %q, want running", got)
	}
}

// TestProcessResetEndpoint covers the BACI-314 POST /process/reset surface:
// a dry_run projects an empty chain without deleting, a reset on a started
// chain clears it (200 + empty array), and a reset while a job is running
// is refused with 409.
func TestProcessResetEndpoint(t *testing.T) {
	ts, s := newTestAPI(t, api.Options{})
	repo, err := s.CreateRepo("RSET", "http-reset", t.TempDir(), "")
	if err != nil {
		t.Fatalf("repo: %v", err)
	}
	iss, err := s.CreateIssue(repo.ID, nil, "card", "", model.StateInPipeline, nil, "")
	if err != nil {
		t.Fatalf("issue: %v", err)
	}
	proc, err := model.ProcessBySlug("plan-implement")
	if err != nil {
		t.Fatalf("ProcessBySlug: %v", err)
	}
	jobs, err := s.SetIssueProcess(iss.ID, proc)
	if err != nil {
		t.Fatalf("SetIssueProcess: %v", err)
	}
	base := ts.URL + "/repos/" + repo.Prefix + "/issues/" + iss.Key

	// dry_run returns an empty chain but leaves the rows in place.
	resp := do(t, http.MethodPost, base+"/process/reset?dry_run=true", nil, nil)
	if resp.StatusCode != 200 {
		t.Fatalf("reset dry-run status %d", resp.StatusCode)
	}
	projected := decode[[]map[string]any](t, resp.Body)
	resp.Body.Close()
	if len(projected) != 0 {
		t.Fatalf("reset dry-run chain = %d, want 0", len(projected))
	}
	if after, _ := s.ListPipelineJobs(iss.ID); len(after) != 2 {
		t.Fatalf("reset dry-run deleted rows: %d remain, want 2", len(after))
	}

	// Mark the first job complete (a started chain) and reset → 200 + empty.
	if err := s.SetPipelineJobStatus(jobs[0].ID, model.JobComplete); err != nil {
		t.Fatalf("set complete: %v", err)
	}
	resp = do(t, http.MethodPost, base+"/process/reset", nil, nil)
	if resp.StatusCode != 200 {
		t.Fatalf("reset status %d, want 200", resp.StatusCode)
	}
	chain := decode[[]map[string]any](t, resp.Body)
	resp.Body.Close()
	if len(chain) != 0 {
		t.Fatalf("reset returned %d jobs, want empty", len(chain))
	}
	if after, _ := s.ListPipelineJobs(iss.ID); len(after) != 0 {
		t.Fatalf("reset left %d rows, want 0", len(after))
	}

	// A reset while a job is running is refused with 409.
	if _, err := s.SetIssueProcess(iss.ID, proc); err != nil {
		t.Fatalf("re-set process: %v", err)
	}
	running, _ := s.ListPipelineJobs(iss.ID)
	if err := s.SetPipelineJobStatus(running[0].ID, model.JobRunning); err != nil {
		t.Fatalf("set running: %v", err)
	}
	resp = do(t, http.MethodPost, base+"/process/reset", nil, nil)
	if resp.StatusCode != 409 {
		t.Fatalf("reset-while-running status %d, want 409", resp.StatusCode)
	}
	resp.Body.Close()
	if after, _ := s.ListPipelineJobs(iss.ID); len(after) != 2 {
		t.Fatalf("refused reset deleted rows: %d remain, want 2", len(after))
	}
}

// TestProcessEditTailEndpoint covers the BACI-294 PUT /process/tail
// surface: editing the pending tail keeps the locked prefix, a dry_run
// projects without writing, a ship-not-last tail is 400 with field=stages,
// and a card with no chain is 400.
func TestProcessEditTailEndpoint(t *testing.T) {
	ts, s := newTestAPI(t, api.Options{})
	repo, err := s.CreateRepo("EDIT", "http-edit", t.TempDir(), "")
	if err != nil {
		t.Fatalf("repo: %v", err)
	}
	iss, err := s.CreateIssue(repo.ID, nil, "card", "", model.StateInPipeline, nil, "")
	if err != nil {
		t.Fatalf("issue: %v", err)
	}
	base := ts.URL + "/repos/" + repo.Prefix + "/issues/" + iss.Key

	// No chain yet → 400.
	resp := do(t, http.MethodPut, base+"/process/tail", strings.NewReader(`{"stages":["plan"]}`), nil)
	if resp.StatusCode != 400 {
		t.Fatalf("edit no-chain status %d, want 400", resp.StatusCode)
	}
	resp.Body.Close()

	// Seed a plan→implement→ship chain and lock job 1 (complete).
	proc, _ := model.ProcessBySlug("plan-implement-ship")
	jobs, err := s.SetIssueProcess(iss.ID, proc)
	if err != nil {
		t.Fatalf("SetIssueProcess: %v", err)
	}
	if err := s.SetPipelineJobStatus(jobs[0].ID, model.JobComplete); err != nil {
		t.Fatalf("complete job 1: %v", err)
	}

	// Ship-not-last tail → 400 with field=stages.
	resp = do(t, http.MethodPut, base+"/process/tail", strings.NewReader(`{"stages":["ship","implement"]}`), nil)
	if resp.StatusCode != 400 {
		t.Fatalf("edit ship-not-last status %d, want 400", resp.StatusCode)
	}
	eb := decode[map[string]any](t, resp.Body)
	resp.Body.Close()
	if det, _ := eb["details"].(map[string]any); det == nil || det["field"] != "stages" {
		t.Fatalf("error details = %v, want field=stages", eb["details"])
	}

	// dry_run projects the locked prefix + re-sequenced tail without writing.
	resp = do(t, http.MethodPut, base+"/process/tail?dry_run=true",
		strings.NewReader(`{"stages":["review","implement","ship"]}`), nil)
	if resp.StatusCode != 200 {
		t.Fatalf("edit dry-run status %d", resp.StatusCode)
	}
	resp.Body.Close()
	if got, _ := s.ListPipelineJobs(iss.ID); len(got) != 3 {
		t.Fatalf("dry-run mutated the chain to %d jobs, want 3", len(got))
	}

	// Real edit → 200, the locked prefix preserved and the tail re-sequenced.
	resp = do(t, http.MethodPut, base+"/process/tail",
		strings.NewReader(`{"stages":["review","implement","ship"]}`), nil)
	if resp.StatusCode != 200 {
		t.Fatalf("edit status %d", resp.StatusCode)
	}
	chain := decode[[]map[string]any](t, resp.Body)
	resp.Body.Close()
	wantModes := []string{"plan", "review", "implement", "ship"}
	if len(chain) != len(wantModes) {
		t.Fatalf("edited chain = %d, want %d", len(chain), len(wantModes))
	}
	for i, wm := range wantModes {
		if got, _ := chain[i]["mode"].(string); got != wm {
			t.Fatalf("edited job[%d].mode = %q, want %q", i, got, wm)
		}
	}
	if got, _ := chain[0]["status"].(string); got != "complete" {
		t.Fatalf("locked job 1 status = %q, want complete", got)
	}
}

// TestProcessFromStagesEndpoint covers the BACI-283 explicit-stage-list
// path on POST /process: a free-form chain materialises, and the
// mutually-exclusive guard rejects both / neither.
func TestProcessFromStagesEndpoint(t *testing.T) {
	ts, s := newTestAPI(t, api.Options{})
	repo, err := s.CreateRepo("HTTP", "http-stages", t.TempDir(), "")
	if err != nil {
		t.Fatalf("repo: %v", err)
	}
	iss, err := s.CreateIssue(repo.ID, nil, "card", "", model.StateInPipeline, nil, "")
	if err != nil {
		t.Fatalf("issue: %v", err)
	}
	base := ts.URL + "/repos/" + repo.Prefix + "/issues/" + iss.Key

	// Explicit ordered stage list → a 4-job chain in order.
	resp := do(t, http.MethodPost, base+"/process",
		strings.NewReader(`{"stages":["design","plan_large","implement","ship"]}`), nil)
	if resp.StatusCode != 200 {
		t.Fatalf("stages status %d", resp.StatusCode)
	}
	jobs := decode[[]map[string]any](t, resp.Body)
	resp.Body.Close()
	if len(jobs) != 4 {
		t.Fatalf("stages jobs = %d, want 4", len(jobs))
	}
	wantModes := []string{"design", "plan_large", "implement", "ship"}
	for i, w := range wantModes {
		if got, _ := jobs[i]["mode"].(string); got != w {
			t.Fatalf("job[%d].mode = %q, want %q", i, got, w)
		}
	}

	// Both process and stages → 400.
	resp = do(t, http.MethodPost, base+"/process",
		strings.NewReader(`{"process":"plan","stages":["implement"]}`), nil)
	if resp.StatusCode != 400 {
		t.Fatalf("both status %d, want 400", resp.StatusCode)
	}
	resp.Body.Close()

	// Neither → 400.
	resp = do(t, http.MethodPost, base+"/process", strings.NewReader(`{}`), nil)
	if resp.StatusCode != 400 {
		t.Fatalf("neither status %d, want 400", resp.StatusCode)
	}
	resp.Body.Close()
}
