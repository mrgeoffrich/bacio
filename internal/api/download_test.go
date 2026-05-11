package api_test

import (
	"net/url"
	"strings"
	"testing"

	"github.com/mrgeoffrich/bacio/internal/api"
	"github.com/mrgeoffrich/bacio/internal/model"
	"github.com/mrgeoffrich/bacio/internal/store"
)

func TestDocumentDownloadHappy(t *testing.T) {
	ts, s := newTestAPI(t, api.Options{})
	repo := seedRepo(t, s)
	seedDocument(t, s, repo, "design.md", model.DocTypeDesigns, "# Hello\n\nbody")

	resp, raw := apiGet(t, ts.URL+"/repos/"+repo.Prefix+"/documents/design.md/download")
	if resp.StatusCode != 200 {
		t.Fatalf("status: %d", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); ct != "text/markdown; charset=utf-8" {
		t.Fatalf("Content-Type: %q", ct)
	}
	cd := resp.Header.Get("Content-Disposition")
	if !strings.HasPrefix(cd, "attachment;") {
		t.Fatalf("Content-Disposition: %q", cd)
	}
	if !strings.Contains(cd, `filename="design.md"`) {
		t.Fatalf("Content-Disposition missing quoted filename: %q", cd)
	}
	if !strings.Contains(cd, `filename*=UTF-8''design.md`) {
		t.Fatalf("Content-Disposition missing filename*: %q", cd)
	}
	if string(raw) != "# Hello\n\nbody" {
		t.Fatalf("body: %q", string(raw))
	}
}

func TestDocumentDownloadMissing(t *testing.T) {
	ts, s := newTestAPI(t, api.Options{})
	repo := seedRepo(t, s)
	resp, _ := apiGet(t, ts.URL+"/repos/"+repo.Prefix+"/documents/missing.md/download")
	if resp.StatusCode != 404 {
		t.Fatalf("status: %d", resp.StatusCode)
	}
}

func TestDocumentDownloadCrossRepo(t *testing.T) {
	ts, s := newTestAPI(t, api.Options{})
	repo := seedRepo(t, s)
	repo2 := seedRepo2(t, s)
	seedDocument(t, s, repo2, "in-other.md", model.DocTypeDesigns, "X")
	resp, _ := apiGet(t, ts.URL+"/repos/"+repo.Prefix+"/documents/in-other.md/download")
	if resp.StatusCode != 404 {
		t.Fatalf("status: %d", resp.StatusCode)
	}
}

func TestDocumentDownloadFilenameWithSpace(t *testing.T) {
	ts, s := newTestAPI(t, api.Options{})
	repo := seedRepo(t, s)
	name := "with space.md"
	seedDocument(t, s, repo, name, model.DocTypeDesigns, "BODY")
	encoded := url.PathEscape(name)
	resp, raw := apiGet(t, ts.URL+"/repos/"+repo.Prefix+"/documents/"+encoded+"/download")
	if resp.StatusCode != 200 {
		t.Fatalf("status: %d", resp.StatusCode)
	}
	if string(raw) != "BODY" {
		t.Fatalf("body: %q", string(raw))
	}
}

func TestDocumentDownloadNoAudit(t *testing.T) {
	ts, s := newTestAPI(t, api.Options{})
	repo := seedRepo(t, s)
	seedDocument(t, s, repo, "x.md", model.DocTypeDesigns, "B")
	resp, _ := apiGet(t, ts.URL+"/repos/"+repo.Prefix+"/documents/x.md/download")
	if resp.StatusCode != 200 {
		t.Fatalf("status: %d", resp.StatusCode)
	}
	rows, _ := s.ListHistory(store.HistoryFilter{})
	if len(rows) != 0 {
		t.Fatalf("download wrote history: %d", len(rows))
	}
}
