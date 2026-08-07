// Per-space nav-surface gate API tests: the resolved values riding every
// repo payload, and the two PUT routes that write them.
package api_test

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/mrgeoffrich/bacio/internal/api"
)

// surfacesOf pulls the two gates off a repo-shaped JSON body.
type surfacesOf struct {
	Prefix            string `json:"prefix"`
	ShowAgentSurfaces bool   `json:"show_agent_surfaces"`
	ShowKanban        bool   `json:"show_kanban"`
}

// TestReposListCarriesResolvedSurfaces pins that GET /repos serves the
// gates resolved against each space's kind, so the frontend never has to
// re-derive a default. This is the only read path — there is no GET
// twin for the PUT routes, deliberately.
func TestReposListCarriesResolvedSurfaces(t *testing.T) {
	ts, s := newTestAPI(t, api.Options{})
	seedRepo(t, s) // MINI, a git repo
	if _, err := s.CreateWorkspace("WKSP", "wksp"); err != nil {
		t.Fatalf("create workspace: %v", err)
	}

	resp, body := apiGet(t, ts.URL+"/repos")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("list: status %d, body %s", resp.StatusCode, body)
	}
	var rows []surfacesOf
	if err := json.Unmarshal(body, &rows); err != nil {
		t.Fatalf("decode: %v (body %s)", err, body)
	}
	byPrefix := map[string]surfacesOf{}
	for _, r := range rows {
		byPrefix[r.Prefix] = r
	}
	// A git repo has a working tree, so agents can work in it.
	if got := byPrefix["MINI"]; !got.ShowAgentSurfaces || got.ShowKanban {
		t.Fatalf("git repo: got %+v, want {agent=true kanban=false}", got)
	}
	// A workspace has none, so the Kanban is its board instead.
	if got := byPrefix["WKSP"]; got.ShowAgentSurfaces || !got.ShowKanban {
		t.Fatalf("workspace: got %+v, want {agent=false kanban=true}", got)
	}
}

// TestRepoShowKanbanRoundtrip walks the write path: persist, echo,
// resurface on the next list, and audit exactly once per real change.
func TestRepoShowKanbanRoundtrip(t *testing.T) {
	ts, s := newTestAPI(t, api.Options{})
	seedRepo(t, s)

	resp, body := apiPut(t, ts.URL+"/repos/MINI/show-kanban", map[string]any{"enabled": true})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("put: status %d, body %s", resp.StatusCode, body)
	}
	var out map[string]bool
	if err := json.Unmarshal(body, &out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !out["show_kanban"] {
		t.Fatalf("echo: got %v, want show_kanban=true", out)
	}

	// It survives onto the list payload.
	_, body = apiGet(t, ts.URL+"/repos")
	var rows []surfacesOf
	if err := json.Unmarshal(body, &rows); err != nil {
		t.Fatalf("decode list: %v", err)
	}
	if len(rows) != 1 || !rows[0].ShowKanban {
		t.Fatalf("after write: got %+v, want show_kanban=true", rows)
	}

	// A second identical write is not a change, so it must not audit —
	// the comparison is against the RESOLVED previous value, so the
	// first write (kind default false → true) is the only real change.
	if _, body = apiPut(t, ts.URL+"/repos/MINI/show-kanban", map[string]any{"enabled": true}); len(body) == 0 {
		t.Fatal("second put returned an empty body")
	}
	assertHistoryOps(t, s, []string{"repo_setting.update"})
}

// TestRepoShowAgentSurfacesNoAuditOnKindDefault pins the subtler half of
// the change check: writing the value a space already resolves to — even
// though its column was NULL — is not a change worth an audit row.
func TestRepoShowAgentSurfacesNoAuditOnKindDefault(t *testing.T) {
	ts, s := newTestAPI(t, api.Options{})
	seedRepo(t, s) // a git repo already resolves show_agent_surfaces=true

	resp, body := apiPut(t, ts.URL+"/repos/MINI/show-agent-surfaces", map[string]any{"enabled": true})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("put: status %d, body %s", resp.StatusCode, body)
	}
	assertHistoryOps(t, s, nil)

	// Flipping it off IS a change.
	apiPut(t, ts.URL+"/repos/MINI/show-agent-surfaces", map[string]any{"enabled": false})
	assertHistoryOps(t, s, []string{"repo_setting.update"})
}

// TestRepoSurfaceDryRun pins that ?dry_run=true echoes without touching
// the store — the same contract every other mutating route honours.
func TestRepoSurfaceDryRun(t *testing.T) {
	ts, s := newTestAPI(t, api.Options{})
	seedRepo(t, s)

	resp, body := apiPut(t, ts.URL+"/repos/MINI/show-kanban?dry_run=true", map[string]any{"enabled": true})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("dry-run put: status %d, body %s", resp.StatusCode, body)
	}
	_, body = apiGet(t, ts.URL+"/repos")
	var rows []surfacesOf
	if err := json.Unmarshal(body, &rows); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if rows[0].ShowKanban {
		t.Fatal("dry run persisted the write")
	}
	assertHistoryOps(t, s, nil)
}

// TestRepoSurfaceRejectsUnknownField pins the strict decode — the same
// guard every other typed body gets.
func TestRepoSurfaceRejectsUnknownField(t *testing.T) {
	ts, s := newTestAPI(t, api.Options{})
	seedRepo(t, s)

	resp, body := apiPut(t, ts.URL+"/repos/MINI/show-kanban", map[string]any{
		"enabled": true, "enabledd": true,
	})
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("unknown field: status %d, want 400 (body %s)", resp.StatusCode, body)
	}
}
