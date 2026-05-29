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
