package api_test

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/mrgeoffrich/bacio/internal/api"
	"github.com/mrgeoffrich/bacio/internal/model"
	"github.com/mrgeoffrich/bacio/internal/store"
)

func seedDocument(t *testing.T, s *store.Store, repo *model.Repo, filename string, dt model.DocumentType, content string) *model.Document {
	t.Helper()
	d, err := s.CreateDocument(repo.ID, filename, dt, content, "")
	if err != nil {
		t.Fatalf("seed document %q: %v", filename, err)
	}
	return d
}

func TestDocumentsListEmpty(t *testing.T) {
	ts, s := newTestAPI(t, api.Options{})
	repo := seedRepo(t, s)
	resp, raw := apiGet(t, ts.URL+"/repos/"+repo.Prefix+"/documents")
	if resp.StatusCode != 200 {
		t.Fatalf("status: %d body=%s", resp.StatusCode, raw)
	}
	var docs []*model.Document
	if err := json.Unmarshal(raw, &docs); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(docs) != 0 {
		t.Fatalf("expected empty, got %d", len(docs))
	}
	// Empty array, not null:
	if string(raw) == "null\n" || string(raw) == "null" {
		t.Fatalf("expected []; got null")
	}
}

func TestDocumentsListLeanByDefault(t *testing.T) {
	ts, s := newTestAPI(t, api.Options{})
	repo := seedRepo(t, s)
	seedDocument(t, s, repo, "a.md", model.DocTypeArchitecture, "ALPHA")

	resp, raw := apiGet(t, ts.URL+"/repos/"+repo.Prefix+"/documents")
	if resp.StatusCode != 200 {
		t.Fatalf("status: %d body=%s", resp.StatusCode, raw)
	}
	var docs []*model.Document
	if err := json.Unmarshal(raw, &docs); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(docs) != 1 {
		t.Fatalf("len: %d", len(docs))
	}
	if docs[0].Content != "" {
		t.Fatalf("expected lean (no content), got %q", docs[0].Content)
	}
	if docs[0].SizeBytes != int64(len("ALPHA")) {
		t.Fatalf("size_bytes: %d", docs[0].SizeBytes)
	}
}

func TestDocumentsListWithContent(t *testing.T) {
	ts, s := newTestAPI(t, api.Options{})
	repo := seedRepo(t, s)
	seedDocument(t, s, repo, "a.md", model.DocTypeArchitecture, "ALPHA")

	resp, raw := apiGet(t, ts.URL+"/repos/"+repo.Prefix+"/documents?with_content=true")
	if resp.StatusCode != 200 {
		t.Fatalf("status: %d body=%s", resp.StatusCode, raw)
	}
	var docs []*model.Document
	if err := json.Unmarshal(raw, &docs); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(docs) != 1 || docs[0].Content != "ALPHA" {
		t.Fatalf("got: %+v", docs)
	}
}

func TestDocumentsListTypeFilter(t *testing.T) {
	ts, s := newTestAPI(t, api.Options{})
	repo := seedRepo(t, s)
	seedDocument(t, s, repo, "a.md", model.DocTypeArchitecture, "X")
	seedDocument(t, s, repo, "b.md", model.DocTypeDesigns, "Y")

	resp, raw := apiGet(t, ts.URL+"/repos/"+repo.Prefix+"/documents?type=architecture")
	if resp.StatusCode != 200 {
		t.Fatalf("status: %d body=%s", resp.StatusCode, raw)
	}
	var docs []*model.Document
	_ = json.Unmarshal(raw, &docs)
	if len(docs) != 1 || docs[0].Filename != "a.md" {
		t.Fatalf("got: %+v", docs)
	}
}

func TestDocumentsListUnknownType(t *testing.T) {
	ts, s := newTestAPI(t, api.Options{})
	repo := seedRepo(t, s)
	resp, _ := apiGet(t, ts.URL+"/repos/"+repo.Prefix+"/documents?type=nonsense")
	if resp.StatusCode != 400 {
		t.Fatalf("status: %d", resp.StatusCode)
	}
}

func TestDocumentsListRepoNotFound(t *testing.T) {
	ts, _ := newTestAPI(t, api.Options{})
	resp, _ := apiGet(t, ts.URL+"/repos/NOPE/documents")
	if resp.StatusCode != 404 {
		t.Fatalf("status: %d", resp.StatusCode)
	}
}

func TestDocumentShowHappy(t *testing.T) {
	ts, s := newTestAPI(t, api.Options{})
	repo := seedRepo(t, s)
	doc := seedDocument(t, s, repo, "design.md", model.DocTypeDesigns, "BODY")
	iss := seedIssue(t, s, repo, "an issue")
	if _, err := s.LinkDocument(doc.ID, store.LinkTarget{IssueID: &iss.ID}, "important context"); err != nil {
		t.Fatalf("link: %v", err)
	}

	resp, raw := apiGet(t, ts.URL+"/repos/"+repo.Prefix+"/documents/design.md")
	if resp.StatusCode != 200 {
		t.Fatalf("status: %d body=%s", resp.StatusCode, raw)
	}
	var view struct {
		Document *model.Document         `json:"document"`
		Links    []*model.DocumentLink   `json:"links"`
	}
	if err := json.Unmarshal(raw, &view); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if view.Document.Content != "BODY" {
		t.Fatalf("content: %q", view.Document.Content)
	}
	if len(view.Links) != 1 || view.Links[0].IssueKey == "" {
		t.Fatalf("links: %+v", view.Links)
	}
}

func TestDocumentShowSkipBody(t *testing.T) {
	ts, s := newTestAPI(t, api.Options{})
	repo := seedRepo(t, s)
	seedDocument(t, s, repo, "design.md", model.DocTypeDesigns, "BODY")

	resp, raw := apiGet(t, ts.URL+"/repos/"+repo.Prefix+"/documents/design.md?with_content=false")
	if resp.StatusCode != 200 {
		t.Fatalf("status: %d body=%s", resp.StatusCode, raw)
	}
	var view struct {
		Document *model.Document `json:"document"`
	}
	_ = json.Unmarshal(raw, &view)
	if view.Document.Content != "" {
		t.Fatalf("expected empty content, got %q", view.Document.Content)
	}
	if view.Document.SizeBytes != int64(len("BODY")) {
		t.Fatalf("size_bytes: %d", view.Document.SizeBytes)
	}
}

func TestDocumentShowMissing(t *testing.T) {
	ts, s := newTestAPI(t, api.Options{})
	repo := seedRepo(t, s)
	resp, _ := apiGet(t, ts.URL+"/repos/"+repo.Prefix+"/documents/missing.md")
	if resp.StatusCode != 404 {
		t.Fatalf("status: %d", resp.StatusCode)
	}
}

func TestDocumentShowCrossRepo(t *testing.T) {
	ts, s := newTestAPI(t, api.Options{})
	repo := seedRepo(t, s)
	repo2 := seedRepo2(t, s)
	seedDocument(t, s, repo2, "in-other.md", model.DocTypeDesigns, "X")

	resp, _ := apiGet(t, ts.URL+"/repos/"+repo.Prefix+"/documents/in-other.md")
	if resp.StatusCode != 404 {
		t.Fatalf("status: %d", resp.StatusCode)
	}
}

// quietRead drains a response body so net/http can reuse the connection.
func quietRead(r io.Reader) { _, _ = io.Copy(io.Discard, r) }

func decodeNoBody(resp *http.Response) {
	quietRead(resp.Body)
	resp.Body.Close()
}

func TestDocumentCreateHappy(t *testing.T) {
	ts, s := newTestAPI(t, api.Options{})
	repo := seedRepo(t, s)
	body := map[string]any{
		"filename": "design.md",
		"type":     "designs",
		"content":  "BODY",
	}
	resp, raw := apiPost(t, ts.URL+"/repos/"+repo.Prefix+"/documents", body)
	if resp.StatusCode != 201 {
		t.Fatalf("status: %d body=%s", resp.StatusCode, raw)
	}
	var d model.Document
	_ = json.Unmarshal(raw, &d)
	if d.Filename != "design.md" || d.Type != model.DocTypeDesigns || d.SizeBytes != 4 {
		t.Fatalf("got: %+v", d)
	}
	if d.Content != "" {
		t.Fatalf("expected lean (no content), got %q", d.Content)
	}
	assertHistoryOps(t, s, []string{"document.create"})
}

func TestDocumentCreateDryRunQuery(t *testing.T) {
	ts, s := newTestAPI(t, api.Options{})
	repo := seedRepo(t, s)
	body := map[string]any{
		"filename": "design.md", "type": "designs", "content": "BODY",
	}
	resp, _ := apiPost(t, ts.URL+"/repos/"+repo.Prefix+"/documents?dry_run=true", body)
	if resp.StatusCode != 201 {
		t.Fatalf("status: %d", resp.StatusCode)
	}
	if resp.Header.Get("X-Dry-Run") != "applied" {
		t.Fatalf("X-Dry-Run header: %q", resp.Header.Get("X-Dry-Run"))
	}
	rows, _ := s.ListHistory(store.HistoryFilter{})
	if len(rows) != 0 {
		t.Fatalf("dry run wrote history: %d", len(rows))
	}
	docs, _ := s.ListDocuments(store.DocumentFilter{RepoID: repo.ID})
	if len(docs) != 0 {
		t.Fatalf("dry run created doc: %d", len(docs))
	}
}

func TestDocumentCreateDryRunHeader(t *testing.T) {
	ts, s := newTestAPI(t, api.Options{})
	repo := seedRepo(t, s)
	body := map[string]any{
		"filename": "design.md", "type": "designs", "content": "BODY",
	}
	resp, _ := apiReq(t, http.MethodPost,
		ts.URL+"/repos/"+repo.Prefix+"/documents", body,
		map[string]string{"X-Dry-Run": "1"})
	if resp.StatusCode != 201 {
		t.Fatalf("status: %d", resp.StatusCode)
	}
	docs, _ := s.ListDocuments(store.DocumentFilter{RepoID: repo.ID})
	if len(docs) != 0 {
		t.Fatalf("dry run created: %d", len(docs))
	}
}

func TestDocumentCreateMissingFilename(t *testing.T) {
	ts, s := newTestAPI(t, api.Options{})
	repo := seedRepo(t, s)
	resp, _ := apiPost(t, ts.URL+"/repos/"+repo.Prefix+"/documents",
		map[string]any{"type": "designs", "content": "BODY"})
	if resp.StatusCode != 400 {
		t.Fatalf("status: %d", resp.StatusCode)
	}
}

func TestDocumentCreateMissingType(t *testing.T) {
	ts, s := newTestAPI(t, api.Options{})
	repo := seedRepo(t, s)
	resp, _ := apiPost(t, ts.URL+"/repos/"+repo.Prefix+"/documents",
		map[string]any{"filename": "x.md", "content": "BODY"})
	if resp.StatusCode != 400 {
		t.Fatalf("status: %d", resp.StatusCode)
	}
}

func TestDocumentCreateMissingContent(t *testing.T) {
	ts, s := newTestAPI(t, api.Options{})
	repo := seedRepo(t, s)
	resp, _ := apiPost(t, ts.URL+"/repos/"+repo.Prefix+"/documents",
		map[string]any{"filename": "x.md", "type": "designs"})
	if resp.StatusCode != 400 {
		t.Fatalf("status: %d", resp.StatusCode)
	}
}

func TestDocumentCreateConflict(t *testing.T) {
	ts, s := newTestAPI(t, api.Options{})
	repo := seedRepo(t, s)
	seedDocument(t, s, repo, "design.md", model.DocTypeDesigns, "ALPHA")
	body := map[string]any{
		"filename": "design.md", "type": "designs", "content": "BETA",
	}
	resp, _ := apiPost(t, ts.URL+"/repos/"+repo.Prefix+"/documents", body)
	if resp.StatusCode != 409 {
		t.Fatalf("status: %d", resp.StatusCode)
	}
}

func TestDocumentCreateUnknownField(t *testing.T) {
	ts, s := newTestAPI(t, api.Options{})
	repo := seedRepo(t, s)
	resp, _ := apiPost(t, ts.URL+"/repos/"+repo.Prefix+"/documents",
		map[string]any{"filename": "x.md", "type": "designs", "content": "B", "nope": 1})
	if resp.StatusCode != 400 {
		t.Fatalf("status: %d", resp.StatusCode)
	}
}

func TestDocumentCreateBadFilename(t *testing.T) {
	ts, s := newTestAPI(t, api.Options{})
	repo := seedRepo(t, s)
	for _, bad := range []string{" leading.md", "trailing.md ", "a/b.md", "with\x00nul.md"} {
		body := map[string]any{"filename": bad, "type": "designs", "content": "B"}
		resp, _ := apiPost(t, ts.URL+"/repos/"+repo.Prefix+"/documents", body)
		if resp.StatusCode != 400 {
			t.Fatalf("filename=%q expected 400, got %d", bad, resp.StatusCode)
		}
	}
}

func TestDocumentUpsertCreates(t *testing.T) {
	ts, s := newTestAPI(t, api.Options{})
	repo := seedRepo(t, s)
	body := map[string]any{"type": "designs", "content": "BODY"}
	resp, raw := apiPut(t, ts.URL+"/repos/"+repo.Prefix+"/documents/up.md", body)
	if resp.StatusCode != 200 {
		t.Fatalf("status: %d body=%s", resp.StatusCode, raw)
	}
	var d model.Document
	_ = json.Unmarshal(raw, &d)
	if d.Filename != "up.md" || d.Type != model.DocTypeDesigns {
		t.Fatalf("got: %+v", d)
	}
	assertHistoryOps(t, s, []string{"document.create"})
}

func TestDocumentUpsertReplaces(t *testing.T) {
	ts, s := newTestAPI(t, api.Options{})
	repo := seedRepo(t, s)
	seedDocument(t, s, repo, "up.md", model.DocTypeDesigns, "OLD")
	body := map[string]any{"type": "architecture", "content": "NEW"}
	resp, raw := apiPut(t, ts.URL+"/repos/"+repo.Prefix+"/documents/up.md", body)
	if resp.StatusCode != 200 {
		t.Fatalf("status: %d body=%s", resp.StatusCode, raw)
	}
	got, err := s.GetDocumentByFilename(repo.ID, "up.md", true)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Type != model.DocTypeArchitecture || got.Content != "NEW" {
		t.Fatalf("got: %+v", got)
	}
	assertHistoryOps(t, s, []string{"document.update"})
}

func TestDocumentUpsertSourcePathReplaces(t *testing.T) {
	ts, s := newTestAPI(t, api.Options{})
	repo := seedRepo(t, s)
	seedDocument(t, s, repo, "up.md", model.DocTypeDesigns, "OLD")
	body := map[string]any{
		"type":        "designs",
		"content":     "NEW",
		"source_path": "docs/up.md",
	}
	resp, raw := apiPut(t, ts.URL+"/repos/"+repo.Prefix+"/documents/up.md", body)
	if resp.StatusCode != 200 {
		t.Fatalf("status: %d body=%s", resp.StatusCode, raw)
	}
	got, err := s.GetDocumentByFilename(repo.ID, "up.md", true)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.SourcePath != "docs/up.md" {
		t.Fatalf("source_path: %q", got.SourcePath)
	}
	if got.Content != "NEW" {
		t.Fatalf("content: %q", got.Content)
	}
	rows, err := s.ListHistory(store.HistoryFilter{OldestFirst: true})
	if err != nil {
		t.Fatalf("history: %v", err)
	}
	if len(rows) != 1 || rows[0].Op != "document.update" {
		t.Fatalf("history ops: %+v", rows)
	}
	if !strings.Contains(rows[0].Details, "source_path") {
		t.Fatalf("history details missing source_path: %q", rows[0].Details)
	}
	if !strings.Contains(rows[0].Details, "content") {
		t.Fatalf("history details missing content: %q", rows[0].Details)
	}
}

func TestDocumentUpsertCreateWithSourcePath(t *testing.T) {
	ts, s := newTestAPI(t, api.Options{})
	repo := seedRepo(t, s)
	body := map[string]any{
		"type":        "designs",
		"content":     "BODY",
		"source_path": "docs/new.md",
	}
	resp, raw := apiPut(t, ts.URL+"/repos/"+repo.Prefix+"/documents/new.md", body)
	if resp.StatusCode != 200 {
		t.Fatalf("status: %d body=%s", resp.StatusCode, raw)
	}
	got, err := s.GetDocumentByFilename(repo.ID, "new.md", false)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.SourcePath != "docs/new.md" {
		t.Fatalf("source_path: %q", got.SourcePath)
	}
}

func TestDocumentEditTypeOnly(t *testing.T) {
	ts, s := newTestAPI(t, api.Options{})
	repo := seedRepo(t, s)
	seedDocument(t, s, repo, "x.md", model.DocTypeDesigns, "BODY")
	body := map[string]any{"type": "architecture"}
	resp, _ := apiPatch(t, ts.URL+"/repos/"+repo.Prefix+"/documents/x.md", body)
	if resp.StatusCode != 200 {
		t.Fatalf("status: %d", resp.StatusCode)
	}
	got, _ := s.GetDocumentByFilename(repo.ID, "x.md", true)
	if got.Type != model.DocTypeArchitecture || got.Content != "BODY" {
		t.Fatalf("got: %+v", got)
	}
}

func TestDocumentEditContentOnly(t *testing.T) {
	ts, s := newTestAPI(t, api.Options{})
	repo := seedRepo(t, s)
	seedDocument(t, s, repo, "x.md", model.DocTypeDesigns, "OLD")
	body := map[string]any{"content": "NEW"}
	resp, _ := apiPatch(t, ts.URL+"/repos/"+repo.Prefix+"/documents/x.md", body)
	if resp.StatusCode != 200 {
		t.Fatalf("status: %d", resp.StatusCode)
	}
	got, _ := s.GetDocumentByFilename(repo.ID, "x.md", true)
	if got.Content != "NEW" || got.Type != model.DocTypeDesigns {
		t.Fatalf("got: %+v", got)
	}
}

func TestDocumentEditBoth(t *testing.T) {
	ts, s := newTestAPI(t, api.Options{})
	repo := seedRepo(t, s)
	seedDocument(t, s, repo, "x.md", model.DocTypeDesigns, "OLD")
	body := map[string]any{"type": "architecture", "content": "NEW"}
	resp, _ := apiPatch(t, ts.URL+"/repos/"+repo.Prefix+"/documents/x.md", body)
	if resp.StatusCode != 200 {
		t.Fatalf("status: %d", resp.StatusCode)
	}
}

func TestDocumentEditNullContentClears(t *testing.T) {
	ts, s := newTestAPI(t, api.Options{})
	repo := seedRepo(t, s)
	seedDocument(t, s, repo, "x.md", model.DocTypeDesigns, "OLD")
	resp, _ := apiPatch(t, ts.URL+"/repos/"+repo.Prefix+"/documents/x.md",
		`{"content": null}`)
	if resp.StatusCode != 200 {
		t.Fatalf("status: %d", resp.StatusCode)
	}
	got, _ := s.GetDocumentByFilename(repo.ID, "x.md", true)
	if got.Content != "" {
		t.Fatalf("expected cleared, got: %q", got.Content)
	}
}

func TestDocumentEditNullTypeRejected(t *testing.T) {
	ts, s := newTestAPI(t, api.Options{})
	repo := seedRepo(t, s)
	seedDocument(t, s, repo, "x.md", model.DocTypeDesigns, "OLD")
	resp, _ := apiPatch(t, ts.URL+"/repos/"+repo.Prefix+"/documents/x.md",
		`{"type": null}`)
	if resp.StatusCode != 400 {
		t.Fatalf("status: %d", resp.StatusCode)
	}
}

func TestDocumentEditUnknownField(t *testing.T) {
	ts, s := newTestAPI(t, api.Options{})
	repo := seedRepo(t, s)
	seedDocument(t, s, repo, "x.md", model.DocTypeDesigns, "OLD")
	resp, _ := apiPatch(t, ts.URL+"/repos/"+repo.Prefix+"/documents/x.md",
		map[string]any{"type": "designs", "nope": 1})
	if resp.StatusCode != 400 {
		t.Fatalf("status: %d", resp.StatusCode)
	}
}

func TestDocumentEditDryRun(t *testing.T) {
	ts, s := newTestAPI(t, api.Options{})
	repo := seedRepo(t, s)
	seedDocument(t, s, repo, "x.md", model.DocTypeDesigns, "OLD")
	resp, _ := apiPatch(t, ts.URL+"/repos/"+repo.Prefix+"/documents/x.md?dry_run=true",
		map[string]any{"type": "architecture"})
	if resp.StatusCode != 200 {
		t.Fatalf("status: %d", resp.StatusCode)
	}
	got, _ := s.GetDocumentByFilename(repo.ID, "x.md", false)
	if got.Type != model.DocTypeDesigns {
		t.Fatalf("dry-run mutated: %+v", got)
	}
}

func TestDocumentRenameHappy(t *testing.T) {
	ts, s := newTestAPI(t, api.Options{})
	repo := seedRepo(t, s)
	seedDocument(t, s, repo, "old.md", model.DocTypeDesigns, "B")
	body := map[string]any{"new_filename": "new.md"}
	resp, _ := apiPost(t, ts.URL+"/repos/"+repo.Prefix+"/documents/old.md/rename", body)
	if resp.StatusCode != 200 {
		t.Fatalf("status: %d", resp.StatusCode)
	}
	if _, err := s.GetDocumentByFilename(repo.ID, "new.md", false); err != nil {
		t.Fatalf("not renamed: %v", err)
	}
	assertHistoryOps(t, s, []string{"document.rename"})
}

func TestDocumentRenameCollision(t *testing.T) {
	ts, s := newTestAPI(t, api.Options{})
	repo := seedRepo(t, s)
	seedDocument(t, s, repo, "old.md", model.DocTypeDesigns, "A")
	seedDocument(t, s, repo, "taken.md", model.DocTypeDesigns, "B")
	body := map[string]any{"new_filename": "taken.md"}
	resp, _ := apiPost(t, ts.URL+"/repos/"+repo.Prefix+"/documents/old.md/rename", body)
	if resp.StatusCode != 409 {
		t.Fatalf("status: %d", resp.StatusCode)
	}
}

func TestDocumentLinkIssueHappy(t *testing.T) {
	ts, s := newTestAPI(t, api.Options{})
	repo := seedRepo(t, s)
	seedDocument(t, s, repo, "x.md", model.DocTypeDesigns, "B")
	iss := seedIssue(t, s, repo, "i")
	body := map[string]any{"issue_key": iss.Key, "description": "context"}
	resp, raw := apiPost(t, ts.URL+"/repos/"+repo.Prefix+"/documents/x.md/links", body)
	if resp.StatusCode != 201 {
		t.Fatalf("status: %d body=%s", resp.StatusCode, raw)
	}
	assertHistoryOps(t, s, []string{"document.link"})
}

func TestDocumentLinkFeatureHappy(t *testing.T) {
	ts, s := newTestAPI(t, api.Options{})
	repo := seedRepo(t, s)
	seedDocument(t, s, repo, "x.md", model.DocTypeDesigns, "B")
	feat := seedFeature(t, s, repo, "auth", "Auth")
	body := map[string]any{"feature_slug": feat.Slug}
	resp, _ := apiPost(t, ts.URL+"/repos/"+repo.Prefix+"/documents/x.md/links", body)
	if resp.StatusCode != 201 {
		t.Fatalf("status: %d", resp.StatusCode)
	}
}

func TestDocumentLinkBothOmitted(t *testing.T) {
	ts, s := newTestAPI(t, api.Options{})
	repo := seedRepo(t, s)
	seedDocument(t, s, repo, "x.md", model.DocTypeDesigns, "B")
	resp, _ := apiPost(t, ts.URL+"/repos/"+repo.Prefix+"/documents/x.md/links", map[string]any{})
	if resp.StatusCode != 400 {
		t.Fatalf("status: %d", resp.StatusCode)
	}
}

func TestDocumentLinkBothSupplied(t *testing.T) {
	ts, s := newTestAPI(t, api.Options{})
	repo := seedRepo(t, s)
	seedDocument(t, s, repo, "x.md", model.DocTypeDesigns, "B")
	iss := seedIssue(t, s, repo, "i")
	feat := seedFeature(t, s, repo, "auth", "Auth")
	body := map[string]any{"issue_key": iss.Key, "feature_slug": feat.Slug}
	resp, _ := apiPost(t, ts.URL+"/repos/"+repo.Prefix+"/documents/x.md/links", body)
	if resp.StatusCode != 400 {
		t.Fatalf("status: %d", resp.StatusCode)
	}
}

func TestDocumentLinkUnknownIssue(t *testing.T) {
	ts, s := newTestAPI(t, api.Options{})
	repo := seedRepo(t, s)
	seedDocument(t, s, repo, "x.md", model.DocTypeDesigns, "B")
	body := map[string]any{"issue_key": "MINI-999"}
	resp, _ := apiPost(t, ts.URL+"/repos/"+repo.Prefix+"/documents/x.md/links", body)
	if resp.StatusCode != 404 {
		t.Fatalf("status: %d", resp.StatusCode)
	}
}

func TestDocumentLinkUnknownFeature(t *testing.T) {
	ts, s := newTestAPI(t, api.Options{})
	repo := seedRepo(t, s)
	seedDocument(t, s, repo, "x.md", model.DocTypeDesigns, "B")
	body := map[string]any{"feature_slug": "nope"}
	resp, _ := apiPost(t, ts.URL+"/repos/"+repo.Prefix+"/documents/x.md/links", body)
	if resp.StatusCode != 404 {
		t.Fatalf("status: %d", resp.StatusCode)
	}
}

func TestDocumentLinkDryRun(t *testing.T) {
	ts, s := newTestAPI(t, api.Options{})
	repo := seedRepo(t, s)
	doc := seedDocument(t, s, repo, "x.md", model.DocTypeDesigns, "B")
	iss := seedIssue(t, s, repo, "i")
	body := map[string]any{"issue_key": iss.Key}
	resp, _ := apiPost(t, ts.URL+"/repos/"+repo.Prefix+"/documents/x.md/links?dry_run=true", body)
	if resp.StatusCode != 201 {
		t.Fatalf("status: %d", resp.StatusCode)
	}
	links, _ := s.ListDocumentLinks(doc.ID)
	if len(links) != 0 {
		t.Fatalf("dry-run mutated: %d", len(links))
	}
}

func TestDocumentUnlinkHappy(t *testing.T) {
	ts, s := newTestAPI(t, api.Options{})
	repo := seedRepo(t, s)
	doc := seedDocument(t, s, repo, "x.md", model.DocTypeDesigns, "B")
	iss := seedIssue(t, s, repo, "i")
	if _, err := s.LinkDocument(doc.ID, store.LinkTarget{IssueID: &iss.ID}, ""); err != nil {
		t.Fatalf("link: %v", err)
	}
	body := map[string]any{"issue_key": iss.Key}
	resp, _ := apiDelete(t, ts.URL+"/repos/"+repo.Prefix+"/documents/x.md/links", body)
	if resp.StatusCode != 204 {
		t.Fatalf("status: %d", resp.StatusCode)
	}
	rows, _ := s.ListHistory(store.HistoryFilter{})
	if len(rows) != 1 || rows[0].Op != "document.unlink" {
		t.Fatalf("history: %+v", rows)
	}
}

func TestDocumentUnlinkAbsentNoAudit(t *testing.T) {
	ts, s := newTestAPI(t, api.Options{})
	repo := seedRepo(t, s)
	seedDocument(t, s, repo, "x.md", model.DocTypeDesigns, "B")
	iss := seedIssue(t, s, repo, "i")
	body := map[string]any{"issue_key": iss.Key}
	resp, _ := apiDelete(t, ts.URL+"/repos/"+repo.Prefix+"/documents/x.md/links", body)
	if resp.StatusCode != 204 {
		t.Fatalf("status: %d", resp.StatusCode)
	}
	rows, _ := s.ListHistory(store.HistoryFilter{})
	if len(rows) != 0 {
		t.Fatalf("idempotent unlink wrote audit: %d", len(rows))
	}
}

func TestDocumentUnlinkBothOmitted(t *testing.T) {
	ts, s := newTestAPI(t, api.Options{})
	repo := seedRepo(t, s)
	seedDocument(t, s, repo, "x.md", model.DocTypeDesigns, "B")
	resp, _ := apiDelete(t, ts.URL+"/repos/"+repo.Prefix+"/documents/x.md/links", map[string]any{})
	if resp.StatusCode != 400 {
		t.Fatalf("status: %d", resp.StatusCode)
	}
}

func TestDocumentDeleteHappy(t *testing.T) {
	ts, s := newTestAPI(t, api.Options{})
	repo := seedRepo(t, s)
	seedDocument(t, s, repo, "x.md", model.DocTypeDesigns, "B")
	resp, _ := apiDelete(t, ts.URL+"/repos/"+repo.Prefix+"/documents/x.md", nil)
	if resp.StatusCode != 204 {
		t.Fatalf("status: %d", resp.StatusCode)
	}
	if _, err := s.GetDocumentByFilename(repo.ID, "x.md", false); err == nil {
		t.Fatalf("doc still present")
	}
	assertHistoryOps(t, s, []string{"document.delete"})
}

func TestDocumentDeleteDryRunCascade(t *testing.T) {
	ts, s := newTestAPI(t, api.Options{})
	repo := seedRepo(t, s)
	doc := seedDocument(t, s, repo, "x.md", model.DocTypeDesigns, "B")
	iss := seedIssue(t, s, repo, "i")
	feat := seedFeature(t, s, repo, "auth", "Auth")
	if _, err := s.LinkDocument(doc.ID, store.LinkTarget{IssueID: &iss.ID}, ""); err != nil {
		t.Fatalf("link issue: %v", err)
	}
	if _, err := s.LinkDocument(doc.ID, store.LinkTarget{FeatureID: &feat.ID}, ""); err != nil {
		t.Fatalf("link feat: %v", err)
	}
	resp, raw := apiDelete(t, ts.URL+"/repos/"+repo.Prefix+"/documents/x.md?dry_run=true", nil)
	if resp.StatusCode != 200 {
		t.Fatalf("status: %d body=%s", resp.StatusCode, raw)
	}
	var prev struct {
		Document    *model.Document `json:"document"`
		WouldDelete bool            `json:"would_delete"`
		Cascade     struct {
			IssueLinks   int `json:"issue_links"`
			FeatureLinks int `json:"feature_links"`
		} `json:"cascade"`
	}
	if err := json.Unmarshal(raw, &prev); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !prev.WouldDelete || prev.Cascade.IssueLinks != 1 || prev.Cascade.FeatureLinks != 1 {
		t.Fatalf("preview: %+v", prev)
	}
	if _, err := s.GetDocumentByFilename(repo.ID, "x.md", false); err != nil {
		t.Fatalf("dry-run mutated: %v", err)
	}
}

func TestDocumentRenameDryRun(t *testing.T) {
	ts, s := newTestAPI(t, api.Options{})
	repo := seedRepo(t, s)
	seedDocument(t, s, repo, "old.md", model.DocTypeDesigns, "B")
	body := map[string]any{"new_filename": "new.md"}
	resp, _ := apiPost(t, ts.URL+"/repos/"+repo.Prefix+"/documents/old.md/rename?dry_run=true", body)
	if resp.StatusCode != 200 {
		t.Fatalf("status: %d", resp.StatusCode)
	}
	if _, err := s.GetDocumentByFilename(repo.ID, "old.md", false); err != nil {
		t.Fatalf("dry-run mutated: %v", err)
	}
}

func TestDocumentUpsertURLFilenameWins(t *testing.T) {
	ts, s := newTestAPI(t, api.Options{})
	repo := seedRepo(t, s)
	body := map[string]any{
		"filename": "lying.md", // body lies; URL wins
		"type":     "designs",
		"content":  "BODY",
	}
	resp, _ := apiPut(t, ts.URL+"/repos/"+repo.Prefix+"/documents/truth.md", body)
	if resp.StatusCode != 200 {
		t.Fatalf("status: %d", resp.StatusCode)
	}
	if _, err := s.GetDocumentByFilename(repo.ID, "lying.md", false); err == nil {
		t.Fatalf("body filename should not have been used")
	}
	if _, err := s.GetDocumentByFilename(repo.ID, "truth.md", false); err != nil {
		t.Fatalf("URL filename not stored: %v", err)
	}
}
