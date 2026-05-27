package api_test

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/mrgeoffrich/bacio/internal/model"
	"github.com/mrgeoffrich/bacio/internal/store"
)

// uuidFor maps a friendly test name (e.g. "sess-a") to a deterministic,
// structurally valid UUID. Since BACI-100 the register entry points
// reject a non-UUID session_id, so tests can't use bare "sess-X"
// strings as session ids any more — uuidFor keeps each test's id stable
// and readable while satisfying the validator. Deterministic so the
// same name resolves to the same UUID across the register call, the
// URL path, and the later GetAgentSession lookup.
func uuidFor(name string) string {
	return uuid.NewSHA1(uuid.NameSpaceDNS, []byte(name)).String()
}

func seedRepo(t *testing.T, s *store.Store) *model.Repo {
	t.Helper()
	repo, err := s.CreateRepo("MINI", "mini", t.TempDir(), "")
	if err != nil {
		t.Fatalf("seed repo: %v", err)
	}
	return repo
}

func seedRepo2(t *testing.T, s *store.Store) *model.Repo {
	t.Helper()
	repo, err := s.CreateRepo("OTHR", "other", t.TempDir(), "")
	if err != nil {
		t.Fatalf("seed repo2: %v", err)
	}
	return repo
}

func seedIssue(t *testing.T, s *store.Store, repo *model.Repo, title string) *model.Issue {
	t.Helper()
	iss, err := s.CreateIssue(repo.ID, nil, title, "", model.StateTodo, nil, "")
	if err != nil {
		t.Fatalf("seed issue: %v", err)
	}
	return iss
}

func seedFeature(t *testing.T, s *store.Store, repo *model.Repo, slug, title string) *model.Feature {
	t.Helper()
	feat, err := s.CreateFeature(repo.ID, slug, title, "", "", "")
	if err != nil {
		t.Fatalf("seed feature: %v", err)
	}
	return feat
}

func apiReq(t *testing.T, method, url string, body any, headers map[string]string) (*http.Response, []byte) {
	t.Helper()
	var rdr io.Reader
	if body != nil {
		switch v := body.(type) {
		case string:
			rdr = strings.NewReader(v)
		case []byte:
			rdr = bytes.NewReader(v)
		default:
			b, err := json.Marshal(v)
			if err != nil {
				t.Fatalf("marshal body: %v", err)
			}
			rdr = bytes.NewReader(b)
		}
	}
	resp := do(t, method, url, rdr, headers)
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	return resp, raw
}

func apiGet(t *testing.T, url string) (*http.Response, []byte) {
	return apiReq(t, http.MethodGet, url, nil, nil)
}

func apiPost(t *testing.T, url string, body any) (*http.Response, []byte) {
	return apiReq(t, http.MethodPost, url, body, nil)
}

func apiPut(t *testing.T, url string, body any) (*http.Response, []byte) {
	return apiReq(t, http.MethodPut, url, body, nil)
}

func apiPatch(t *testing.T, url string, body any) (*http.Response, []byte) {
	return apiReq(t, http.MethodPatch, url, body, nil)
}

func apiDelete(t *testing.T, url string, body any) (*http.Response, []byte) {
	return apiReq(t, http.MethodDelete, url, body, nil)
}

func assertHistoryOps(t *testing.T, s *store.Store, want []string) {
	t.Helper()
	rows, err := s.ListHistory(store.HistoryFilter{OldestFirst: true})
	if err != nil {
		t.Fatalf("history: %v", err)
	}
	got := make([]string, 0, len(rows))
	for _, r := range rows {
		got = append(got, r.Op)
	}
	if len(got) != len(want) {
		t.Fatalf("history ops: want %v, got %v", want, got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("history ops[%d]: want %q, got %q (full: %v)", i, want[i], got[i], got)
		}
	}
}
