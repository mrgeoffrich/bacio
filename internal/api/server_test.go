package api_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mrgeoffrich/bacio/internal/api"
	"github.com/mrgeoffrich/bacio/internal/schema"
	"github.com/mrgeoffrich/bacio/internal/store"
)

func newTestAPI(t *testing.T, opts api.Options) (*httptest.Server, *store.Store) {
	t.Helper()
	s, err := store.Open(filepath.Join(t.TempDir(), "test.sqlite"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	srv := api.New(s, opts, logger)
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	return ts, s
}

func decode[T any](t *testing.T, r io.Reader) T {
	t.Helper()
	var v T
	if err := json.NewDecoder(r).Decode(&v); err != nil {
		t.Fatalf("decode: %v", err)
	}
	return v
}

func do(t *testing.T, method, url string, body io.Reader, headers map[string]string) *http.Response {
	t.Helper()
	req, err := http.NewRequest(method, url, body)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	if body != nil && req.Header.Get("Content-Type") == "" {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("do: %v", err)
	}
	return resp
}

func TestHealthz(t *testing.T) {
	ts, _ := newTestAPI(t, api.Options{})
	resp := do(t, http.MethodGet, ts.URL+"/healthz", nil, nil)
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("status: %d", resp.StatusCode)
	}
	var body map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body["ok"] != true {
		t.Fatalf("ok: %v", body["ok"])
	}
	if v, _ := body["version"].(string); v == "" {
		t.Fatalf("expected non-empty version")
	}
}

func TestSchemaAll(t *testing.T) {
	ts, _ := newTestAPI(t, api.Options{})
	resp := do(t, http.MethodGet, ts.URL+"/schema", nil, nil)
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("status: %d", resp.StatusCode)
	}
	var body map[string]json.RawMessage
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	want := schema.Names()
	for _, name := range want {
		if _, ok := body[name]; !ok {
			t.Fatalf("missing %q in /schema", name)
		}
	}
	var addSchema map[string]any
	if err := json.Unmarshal(body["issue.add"], &addSchema); err != nil {
		t.Fatalf("issue.add not an object: %v", err)
	}
	props, _ := addSchema["properties"].(map[string]any)
	if _, ok := props["title"]; !ok {
		t.Fatalf("issue.add missing properties.title")
	}
}

func TestSchemaShow(t *testing.T) {
	ts, _ := newTestAPI(t, api.Options{})
	resp := do(t, http.MethodGet, ts.URL+"/schema/issue.add", nil, nil)
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("status: %d", resp.StatusCode)
	}
	var body map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if title, _ := body["title"].(string); title != "IssueAddInput" {
		t.Fatalf("title: %v", body["title"])
	}

	resp404 := do(t, http.MethodGet, ts.URL+"/schema/does.not.exist", nil, nil)
	defer resp404.Body.Close()
	if resp404.StatusCode != 404 {
		t.Fatalf("expected 404, got %d", resp404.StatusCode)
	}
	env := decode[map[string]any](t, resp404.Body)
	if env["code"] != "not_found" {
		t.Fatalf("code: %v", env["code"])
	}
}

func TestSchemaList(t *testing.T) {
	ts, _ := newTestAPI(t, api.Options{})
	resp := do(t, http.MethodGet, ts.URL+"/schema/list", nil, nil)
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("status: %d", resp.StatusCode)
	}
	var rows []map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&rows); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(rows) == 0 {
		t.Fatalf("empty list")
	}
	seen := map[string]bool{}
	for _, r := range rows {
		seen[r["name"].(string)] = true
	}
	for _, want := range []string{"issue.add", "issue.edit", "issue.state"} {
		if !seen[want] {
			t.Fatalf("missing %q in /schema/list", want)
		}
	}
}

func TestReposEmpty(t *testing.T) {
	ts, _ := newTestAPI(t, api.Options{})
	resp := do(t, http.MethodGet, ts.URL+"/repos", nil, nil)
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("status: %d", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	trimmed := strings.TrimSpace(string(body))
	if trimmed != "[]" {
		t.Fatalf("expected [], got %q", trimmed)
	}
}

func TestReposCreateAndShow(t *testing.T) {
	ts, s := newTestAPI(t, api.Options{})
	body := bytes.NewBufferString(`{"name":"sample","path":"/tmp/sample-repo"}`)
	resp := do(t, http.MethodPost, ts.URL+"/repos", body, nil)
	defer resp.Body.Close()
	if resp.StatusCode != 201 {
		raw, _ := io.ReadAll(resp.Body)
		t.Fatalf("status: %d, body: %s", resp.StatusCode, raw)
	}
	created := decode[map[string]any](t, resp.Body)
	prefix, _ := created["prefix"].(string)
	if prefix == "" {
		t.Fatalf("missing prefix in response")
	}

	resp2 := do(t, http.MethodGet, ts.URL+"/repos/"+prefix, nil, nil)
	defer resp2.Body.Close()
	if resp2.StatusCode != 200 {
		t.Fatalf("show status: %d", resp2.StatusCode)
	}
	shown := decode[map[string]any](t, resp2.Body)
	if shown["prefix"] != prefix {
		t.Fatalf("show prefix mismatch: %v vs %v", shown["prefix"], prefix)
	}

	rows, err := s.ListHistory(store.HistoryFilter{})
	if err != nil {
		t.Fatalf("history: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("history rows: %d", len(rows))
	}
	if rows[0].Op != "repo.create" {
		t.Fatalf("op: %s", rows[0].Op)
	}
	if rows[0].Actor != "api" {
		t.Fatalf("actor: %s", rows[0].Actor)
	}
	if !strings.HasPrefix(rows[0].Details, "api init") {
		t.Fatalf("details: %s", rows[0].Details)
	}
}

func TestReposCreateInvalidJSON(t *testing.T) {
	ts, _ := newTestAPI(t, api.Options{})
	resp := do(t, http.MethodPost, ts.URL+"/repos", bytes.NewBufferString("{not json"), nil)
	defer resp.Body.Close()
	if resp.StatusCode != 400 {
		t.Fatalf("status: %d", resp.StatusCode)
	}
	env := decode[map[string]any](t, resp.Body)
	if env["code"] != "invalid_input" {
		t.Fatalf("code: %v", env["code"])
	}
}

func TestReposCreateUnknownField(t *testing.T) {
	ts, _ := newTestAPI(t, api.Options{})
	resp := do(t, http.MethodPost, ts.URL+"/repos",
		bytes.NewBufferString(`{"name":"x","path":"/p","unknown":1}`), nil)
	defer resp.Body.Close()
	if resp.StatusCode != 400 {
		t.Fatalf("status: %d", resp.StatusCode)
	}
	env := decode[map[string]any](t, resp.Body)
	if env["code"] != "invalid_input" {
		t.Fatalf("code: %v", env["code"])
	}
}

func TestReposCreateConflictPath(t *testing.T) {
	ts, _ := newTestAPI(t, api.Options{})
	body := `{"name":"sample","path":"/tmp/dup-path"}`
	resp1 := do(t, http.MethodPost, ts.URL+"/repos", bytes.NewBufferString(body), nil)
	resp1.Body.Close()
	if resp1.StatusCode != 201 {
		t.Fatalf("first create status: %d", resp1.StatusCode)
	}
	resp2 := do(t, http.MethodPost, ts.URL+"/repos", bytes.NewBufferString(body), nil)
	defer resp2.Body.Close()
	if resp2.StatusCode != 409 {
		t.Fatalf("second create status: %d", resp2.StatusCode)
	}
	env := decode[map[string]any](t, resp2.Body)
	if env["code"] != "conflict" {
		t.Fatalf("code: %v", env["code"])
	}
}

func TestReposCreateConflictPrefix(t *testing.T) {
	ts, _ := newTestAPI(t, api.Options{})
	resp1 := do(t, http.MethodPost, ts.URL+"/repos",
		bytes.NewBufferString(`{"name":"first","path":"/tmp/path-a","prefix":"ABCD"}`), nil)
	resp1.Body.Close()
	if resp1.StatusCode != 201 {
		t.Fatalf("first create status: %d", resp1.StatusCode)
	}
	resp2 := do(t, http.MethodPost, ts.URL+"/repos",
		bytes.NewBufferString(`{"name":"second","path":"/tmp/path-b","prefix":"ABCD"}`), nil)
	defer resp2.Body.Close()
	if resp2.StatusCode != 409 {
		raw, _ := io.ReadAll(resp2.Body)
		t.Fatalf("second create status: %d, body: %s", resp2.StatusCode, raw)
	}
	env := decode[map[string]any](t, resp2.Body)
	if env["code"] != "conflict" {
		t.Fatalf("code: %v", env["code"])
	}
}

func TestRepoCreateDryRun(t *testing.T) {
	ts, s := newTestAPI(t, api.Options{})
	body := `{"name":"sample","path":"/tmp/sample-dry-run","prefix":"DRYR"}`
	resp := do(t, http.MethodPost, ts.URL+"/repos?dry_run=true",
		bytes.NewBufferString(body), nil)
	defer resp.Body.Close()
	if resp.StatusCode != 201 {
		raw, _ := io.ReadAll(resp.Body)
		t.Fatalf("status: %d body: %s", resp.StatusCode, raw)
	}
	if got := resp.Header.Get("X-Dry-Run"); got != "applied" {
		t.Fatalf("X-Dry-Run header: %q", got)
	}
	projected := decode[map[string]any](t, resp.Body)
	if projected["prefix"] != "DRYR" {
		t.Fatalf("projected prefix: %v", projected["prefix"])
	}
	if projected["name"] != "sample" {
		t.Fatalf("projected name: %v", projected["name"])
	}
	if projected["path"] != "/tmp/sample-dry-run" {
		t.Fatalf("projected path: %v", projected["path"])
	}
	// next_issue_number should be 1, encoded as a JSON number which decodes as float64.
	if n, ok := projected["next_issue_number"].(float64); !ok || n != 1 {
		t.Fatalf("projected next_issue_number: %v", projected["next_issue_number"])
	}

	// No row should exist in the DB.
	if _, err := s.GetRepoByPrefix("DRYR"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("expected ErrNotFound after dry-run, got: %v", err)
	}
	// No history row.
	rows, err := s.ListHistory(store.HistoryFilter{})
	if err != nil {
		t.Fatalf("history: %v", err)
	}
	if len(rows) != 0 {
		t.Fatalf("dry-run wrote history: %d rows", len(rows))
	}
}

func TestRepoCreateDryRunAutoPrefix(t *testing.T) {
	// Auto-allocated prefix path also honours dry-run and writes nothing.
	ts, s := newTestAPI(t, api.Options{})
	body := `{"name":"autopfx","path":"/tmp/auto-dry-run"}`
	resp := do(t, http.MethodPost, ts.URL+"/repos?dry_run=true",
		bytes.NewBufferString(body), nil)
	defer resp.Body.Close()
	if resp.StatusCode != 201 {
		raw, _ := io.ReadAll(resp.Body)
		t.Fatalf("status: %d body: %s", resp.StatusCode, raw)
	}
	projected := decode[map[string]any](t, resp.Body)
	pfx, _ := projected["prefix"].(string)
	if pfx == "" {
		t.Fatalf("expected an allocated prefix")
	}
	repos, err := s.ListRepos()
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(repos) != 0 {
		t.Fatalf("dry-run created repo rows: %d", len(repos))
	}
}

func TestAuthTokenUnset(t *testing.T) {
	ts, _ := newTestAPI(t, api.Options{})
	for _, path := range []string{"/healthz", "/repos"} {
		resp := do(t, http.MethodGet, ts.URL+path, nil, nil)
		resp.Body.Close()
		if resp.StatusCode != 200 {
			t.Fatalf("%s status: %d", path, resp.StatusCode)
		}
	}
}

func TestAuthTokenSetMissing(t *testing.T) {
	ts, _ := newTestAPI(t, api.Options{Token: "secret"})
	resp := do(t, http.MethodGet, ts.URL+"/repos", nil, nil)
	defer resp.Body.Close()
	if resp.StatusCode != 401 {
		t.Fatalf("status: %d", resp.StatusCode)
	}
}

func TestAuthTokenSetWrong(t *testing.T) {
	ts, _ := newTestAPI(t, api.Options{Token: "secret"})
	resp := do(t, http.MethodGet, ts.URL+"/repos", nil,
		map[string]string{"Authorization": "Bearer wrong"})
	defer resp.Body.Close()
	if resp.StatusCode != 401 {
		t.Fatalf("status: %d", resp.StatusCode)
	}
}

func TestAuthTokenSetRight(t *testing.T) {
	ts, _ := newTestAPI(t, api.Options{Token: "secret"})
	resp := do(t, http.MethodGet, ts.URL+"/repos", nil,
		map[string]string{"Authorization": "Bearer secret"})
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("status: %d", resp.StatusCode)
	}
}

func TestAuthHealthBypass(t *testing.T) {
	ts, _ := newTestAPI(t, api.Options{Token: "secret"})
	resp := do(t, http.MethodGet, ts.URL+"/healthz", nil, nil)
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("status: %d", resp.StatusCode)
	}
}

func TestActorDefault(t *testing.T) {
	ts, s := newTestAPI(t, api.Options{})
	resp := do(t, http.MethodPost, ts.URL+"/repos",
		bytes.NewBufferString(`{"name":"actor-default","path":"/tmp/actor-default"}`), nil)
	defer resp.Body.Close()
	if resp.StatusCode != 201 {
		t.Fatalf("status: %d", resp.StatusCode)
	}
	rows, err := s.ListHistory(store.HistoryFilter{})
	if err != nil {
		t.Fatalf("history: %v", err)
	}
	if len(rows) != 1 || rows[0].Actor != "api" {
		t.Fatalf("expected actor=api, rows=%+v", rows)
	}
}

func TestActorMalformed(t *testing.T) {
	ts, _ := newTestAPI(t, api.Options{})
	resp := do(t, http.MethodPost, ts.URL+"/repos",
		bytes.NewBufferString(`{"name":"x","path":"/p"}`),
		map[string]string{"X-Actor": "bad\tname"})
	defer resp.Body.Close()
	if resp.StatusCode != 400 {
		t.Fatalf("status: %d", resp.StatusCode)
	}
	var env map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&env); err != nil {
		t.Fatalf("decode: %v", err)
	}
	details, _ := env["details"].(map[string]any)
	if details["field"] != "X-Actor" {
		t.Fatalf("details.field: %v", details["field"])
	}
}

func TestActorAccepted(t *testing.T) {
	ts, s := newTestAPI(t, api.Options{})
	resp := do(t, http.MethodPost, ts.URL+"/repos",
		bytes.NewBufferString(`{"name":"actor-named","path":"/tmp/actor-named"}`),
		map[string]string{"X-Actor": "agent-alice"})
	defer resp.Body.Close()
	if resp.StatusCode != 201 {
		raw, _ := io.ReadAll(resp.Body)
		t.Fatalf("status: %d, body: %s", resp.StatusCode, raw)
	}
	rows, err := s.ListHistory(store.HistoryFilter{})
	if err != nil {
		t.Fatalf("history: %v", err)
	}
	if len(rows) != 1 || rows[0].Actor != "agent-alice" {
		t.Fatalf("expected actor=agent-alice, rows=%+v", rows)
	}
}

func TestBodyCap(t *testing.T) {
	ts, _ := newTestAPI(t, api.Options{})
	big := bytes.Repeat([]byte("a"), 5<<20)
	resp := do(t, http.MethodPost, ts.URL+"/repos", bytes.NewReader(big), nil)
	defer resp.Body.Close()
	if resp.StatusCode != 413 {
		t.Fatalf("status: %d", resp.StatusCode)
	}
	env := decode[map[string]any](t, resp.Body)
	if env["code"] != "payload_too_large" {
		t.Fatalf("code: %v", env["code"])
	}
}
