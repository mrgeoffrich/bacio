package sync

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/mrgeoffrich/bacio/internal/model"
	"github.com/mrgeoffrich/bacio/internal/store"
)

// ExportResult is the structured summary of one Export pass — what was
// (or would have been) written. Phase 2 reports only counts; later
// phases will likely add per-record diffs and op-level details.
type ExportResult struct {
	Repos        int    `json:"repos"`
	Features     int    `json:"features"`
	Issues       int    `json:"issues"`
	Comments     int    `json:"comments"`
	Documents    int    `json:"documents"`
	Files        int    `json:"files"`
	BytesWritten int64  `json:"bytes_written"`
	Target       string `json:"target"`
	DryRun       bool   `json:"dry_run,omitempty"`
}

// Export walks every repo in the DB and writes the canonical YAML +
// markdown layout under `target`. Phase 2 uses full overwrite — atomic
// staging, delta computation, and `git mv`-aware rename detection are
// Phase 4 work. We do, however, only write each file once (the emitter
// is deterministic, so re-running on an unchanged DB produces
// byte-identical output).
//
// The ctx parameter is honoured by checking ctx.Err() between repos —
// per-record cancellation isn't worth the bookkeeping for Phase 2's
// scale.
func (e *Engine) Export(ctx context.Context, target string) (*ExportResult, error) {
	if e.Store == nil {
		return nil, fmt.Errorf("sync.Export: Store is nil")
	}
	if target == "" {
		return nil, fmt.Errorf("sync.Export: target path is empty")
	}

	// Build a global issue key → uuid map up front. Relations can in
	// principle cross repos (the FK is to issues.id, no repo filter),
	// so we need every issue in scope to resolve the {label, uuid}
	// pairs the schema requires.
	allIssues, err := e.Store.ListIssues(store.IssueFilter{
		AllRepos:           true,
		IncludeDescription: false, // we re-fetch with description per-repo below
	})
	if err != nil {
		return nil, fmt.Errorf("list issues: %w", err)
	}
	issueByID := make(map[int64]*model.Issue, len(allIssues))
	uuidByKey := make(map[string]string, len(allIssues))
	for _, iss := range allIssues {
		issueByID[iss.ID] = iss
		uuidByKey[iss.Key] = iss.UUID
	}

	repos, err := e.Store.ListRepos()
	if err != nil {
		return nil, fmt.Errorf("list repos: %w", err)
	}

	w := &exportWriter{target: target, dryRun: e.DryRun}
	res := &ExportResult{Target: target, DryRun: e.DryRun}

	for _, repo := range repos {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if err := e.exportRepo(w, repo, issueByID, uuidByKey, res); err != nil {
			return nil, fmt.Errorf("export repo %s: %w", repo.Prefix, err)
		}
		res.Repos++
	}

	res.Files = w.files
	res.BytesWritten = w.bytes
	return res, nil
}

// exportRepo writes the repo.yaml and every feature/issue/document
// folder underneath it. Features must be exported before issues so the
// feature-uuid lookup is populated for the issues' `feature` field —
// although in practice we just build the map before either pass.
func (e *Engine) exportRepo(w *exportWriter, repo *model.Repo, issueByID map[int64]*model.Issue, uuidByKey map[string]string, res *ExportResult) error {
	// repo.yaml
	repoYAML, err := buildRepoYAML(repo)
	if err != nil {
		return err
	}
	if err := w.writeYAML(RepoYAMLFile(repo.Prefix), repoYAML); err != nil {
		return err
	}

	// Features. We need a feature-id → (slug, uuid) lookup for issues
	// that reference a feature, so build the map regardless of whether
	// we end up writing any feature folders.
	features, err := e.Store.ListFeatures(repo.ID, true)
	if err != nil {
		return fmt.Errorf("list features: %w", err)
	}
	featureByID := make(map[int64]*model.Feature, len(features))
	for _, f := range features {
		featureByID[f.ID] = f
	}

	for _, f := range features {
		if err := e.exportFeature(w, repo, f); err != nil {
			return fmt.Errorf("feature %s: %w", f.Slug, err)
		}
		res.Features++
	}

	// Issues. We re-fetch with description for this repo specifically,
	// because the global `allIssues` map dropped descriptions to keep
	// it lean.
	id := repo.ID
	issues, err := e.Store.ListIssues(store.IssueFilter{
		RepoID:             &id,
		IncludeDescription: true,
	})
	if err != nil {
		return fmt.Errorf("list issues: %w", err)
	}

	for _, iss := range issues {
		comments, err := e.exportIssue(w, repo, iss, featureByID, uuidByKey)
		if err != nil {
			return fmt.Errorf("issue %s: %w", iss.Key, err)
		}
		res.Issues++
		res.Comments += comments
	}

	// Documents.
	docs, err := e.Store.ListDocuments(store.DocumentFilter{RepoID: repo.ID})
	if err != nil {
		return fmt.Errorf("list documents: %w", err)
	}
	for _, d := range docs {
		// ListDocuments doesn't fetch content; the DB call we want is
		// GetByID with content. Same for the issue/feature labels on
		// each link.
		full, err := e.Store.GetDocumentByID(d.ID, true)
		if err != nil {
			return fmt.Errorf("get document %s: %w", d.Filename, err)
		}
		if err := e.exportDocument(w, repo, full, issueByID, featureByID); err != nil {
			return fmt.Errorf("document %s: %w", d.Filename, err)
		}
		res.Documents++
	}

	return nil
}

func (e *Engine) exportFeature(w *exportWriter, repo *model.Repo, f *model.Feature) error {
	folder := FeatureFolder(repo.Prefix, f.Slug)
	descPath := FeatureDescriptionFile(folder)
	yamlPath := FeatureYAMLFile(folder)

	descBytes := NormalizeBody([]byte(f.Description))
	if err := w.writeRaw(descPath, descBytes); err != nil {
		return err
	}
	descHash := ContentHash(descBytes)

	root := Map(
		Pair{"created_at", Time(f.CreatedAt)},
		Pair{"description_hash", Str(descHash)},
		Pair{"slug", Str(f.Slug)},
		Pair{"title", Str(f.Title)},
		Pair{"updated_at", Time(f.UpdatedAt)},
		Pair{"uuid", Str(f.UUID)},
	)
	yamlBytes, err := Emit(root)
	if err != nil {
		return err
	}
	return w.writeRaw(yamlPath, yamlBytes)
}

// exportIssue writes the issue's folder, returning the number of
// comments written so the caller can roll counts up into the result.
func (e *Engine) exportIssue(
	w *exportWriter,
	repo *model.Repo,
	iss *model.Issue,
	featureByID map[int64]*model.Feature,
	uuidByKey map[string]string,
) (int, error) {
	folder := IssueFolder(repo.Prefix, iss.Number)
	descPath := IssueDescriptionFile(folder)
	yamlPath := IssueYAMLFile(folder)

	descBytes := NormalizeBody([]byte(iss.Description))
	if err := w.writeRaw(descPath, descBytes); err != nil {
		return 0, err
	}
	descHash := ContentHash(descBytes)

	// Side-data for issue.yaml: feature ref, relations, prs, tags.
	relations, err := e.Store.ListIssueRelations(iss.ID)
	if err != nil {
		return 0, fmt.Errorf("list relations: %w", err)
	}
	prs, err := e.Store.ListPRs(iss.ID)
	if err != nil {
		return 0, fmt.Errorf("list prs: %w", err)
	}

	pairs := []Pair{
		{"created_at", Time(iss.CreatedAt)},
		{"description_hash", Str(descHash)},
		{"number", Int(iss.Number)},
		{"prs", emitPRs(prs)},
		{"relations", emitRelations(relations, uuidByKey)},
		{"state", Str(string(iss.State))},
		{"tags", emitTags(iss.Tags)},
		{"title", Str(iss.Title)},
		{"updated_at", Time(iss.UpdatedAt)},
		{"uuid", Str(iss.UUID)},
	}
	if iss.Assignee != "" {
		pairs = append(pairs, Pair{"assignee", Str(iss.Assignee)})
	} else {
		// Always emit the key so downstream consumers can rely on a
		// stable schema — empty string is a perfectly fine "unassigned"
		// signal and matches what the JSON output produces.
		pairs = append(pairs, Pair{"assignee", Str("")})
	}
	if iss.FeatureID != nil {
		f := featureByID[*iss.FeatureID]
		if f != nil {
			pairs = append(pairs, Pair{"feature", Map(
				Pair{"label", Str(f.Slug)},
				Pair{"uuid", Str(f.UUID)},
			)})
		}
		// If the lookup misses (shouldn't be possible — the FK is
		// enforced — but defensive), we silently drop the field rather
		// than emitting a half-populated reference.
	}

	yamlBytes, err := Emit(Map(pairs...))
	if err != nil {
		return 0, err
	}
	if err := w.writeRaw(yamlPath, yamlBytes); err != nil {
		return 0, err
	}

	// Comments.
	comments, err := e.Store.ListComments(iss.ID)
	if err != nil {
		return 0, fmt.Errorf("list comments: %w", err)
	}
	for _, c := range comments {
		if err := e.exportComment(w, folder, c); err != nil {
			return 0, fmt.Errorf("comment %s: %w", c.UUID, err)
		}
	}
	return len(comments), nil
}

func (e *Engine) exportComment(w *exportWriter, issueFolder string, c *model.Comment) error {
	yamlPath, mdPath := CommentFile(issueFolder, c.CreatedAt, c.UUID)
	bodyBytes := NormalizeBody([]byte(c.Body))
	if err := w.writeRaw(mdPath, bodyBytes); err != nil {
		return err
	}
	bodyHash := ContentHash(bodyBytes)
	yamlBytes, err := Emit(Map(
		Pair{"author", Str(c.Author)},
		Pair{"body_hash", Str(bodyHash)},
		Pair{"created_at", Time(c.CreatedAt)},
		Pair{"uuid", Str(c.UUID)},
	))
	if err != nil {
		return err
	}
	return w.writeRaw(yamlPath, yamlBytes)
}

func (e *Engine) exportDocument(
	w *exportWriter,
	repo *model.Repo,
	d *model.Document,
	issueByID map[int64]*model.Issue,
	featureByID map[int64]*model.Feature,
) error {
	folder := DocumentFolder(repo.Prefix, d.Filename)
	contentPath := DocumentContentFile(folder)
	yamlPath := DocumentYAMLFile(folder)

	contentBytes := NormalizeBody([]byte(d.Content))
	if err := w.writeRaw(contentPath, contentBytes); err != nil {
		return err
	}
	contentHash := ContentHash(contentBytes)

	links, err := e.Store.ListDocumentLinks(d.ID)
	if err != nil {
		return fmt.Errorf("list document links: %w", err)
	}

	pairs := []Pair{
		{"content_hash", Str(contentHash)},
		{"created_at", Time(d.CreatedAt)},
		{"filename", Str(d.Filename)},
		{"links", emitDocLinks(links, issueByID, featureByID)},
		{"source_path", Str(d.SourcePath)},
		{"type", Str(string(d.Type))},
		{"updated_at", Time(d.UpdatedAt)},
		{"uuid", Str(d.UUID)},
	}
	yamlBytes, err := Emit(Map(pairs...))
	if err != nil {
		return err
	}
	return w.writeRaw(yamlPath, yamlBytes)
}

// buildRepoYAML produces the canonical repo.yaml node for one repo.
// Client-state fields (id, path, created_at-of-local-row) are excluded
// per the design doc; only uuid + presentation fields go on disk.
func buildRepoYAML(repo *model.Repo) (Node, error) {
	pairs := []Pair{
		{"created_at", Time(repo.CreatedAt)},
		{"name", Str(repo.Name)},
		{"next_issue_number", Int(repo.NextIssueNumber)},
		{"prefix", Str(repo.Prefix)},
		{"remote_url", Str(repo.RemoteURL)},
		{"updated_at", Time(repo.UpdatedAt)},
		{"uuid", Str(repo.UUID)},
	}
	return Map(pairs...), nil
}

// emitTags sorts the tag list deterministically and returns a YAML
// sequence of quoted strings. The DB normally stores tags lexicographically
// already (loadTagsForIssues sorts), but we re-sort here so the
// emitter doesn't depend on store-side ordering.
func emitTags(tags []string) Node {
	out := make([]string, 0, len(tags))
	out = append(out, tags...)
	sort.Strings(out)
	items := make([]Node, len(out))
	for i, t := range out {
		items[i] = Str(t)
	}
	return Seq(items...)
}

// emitPRs writes the issue's pull-request URLs as a sorted seq of
// quoted strings. Sort by URL for determinism — the DB orders by
// created_at + id, but a re-import on another machine may have
// different ids, so URL order is the only stable choice.
func emitPRs(prs []*model.PullRequest) Node {
	urls := make([]string, len(prs))
	for i, p := range prs {
		urls[i] = p.URL
	}
	sort.Strings(urls)
	items := make([]Node, len(urls))
	for i, u := range urls {
		items[i] = Str(u)
	}
	return Seq(items...)
}

// emitRelations builds the `relations: {blocks, relates_to,
// duplicate_of}` map from a *store.IssueRelations slice. We only emit
// outgoing edges — incoming edges are derived (the other side of the
// edge is the source), so writing them on both sides would just give
// the importer two ways to disagree.
//
// The map structure is fixed: every kind always appears, with an empty
// list when there are no edges of that kind. This keeps the schema
// predictable for downstream consumers.
func emitRelations(rel *store.IssueRelations, uuidByKey map[string]string) Node {
	buckets := map[model.RelationType][]Node{
		model.RelBlocks:      {},
		model.RelRelatesTo:   {},
		model.RelDuplicateOf: {},
	}
	if rel != nil {
		for _, r := range rel.Outgoing {
			buckets[r.Type] = append(buckets[r.Type], Map(
				Pair{"label", Str(r.ToIssue)},
				Pair{"uuid", Str(uuidByKey[r.ToIssue])},
			))
		}
	}
	// Sort each bucket by label so the on-disk order is stable across
	// runs (the DB orders by created_at, which is fine but not the
	// sort that survives a renumber).
	for k, v := range buckets {
		sort.Slice(v, func(i, j int) bool {
			return nodeLabel(v[i]) < nodeLabel(v[j])
		})
		buckets[k] = v
	}
	return Map(
		Pair{"blocks", Seq(buckets[model.RelBlocks]...)},
		Pair{"duplicate_of", Seq(buckets[model.RelDuplicateOf]...)},
		Pair{"relates_to", Seq(buckets[model.RelRelatesTo]...)},
	)
}

// nodeLabel pulls the "label" field out of a {label, uuid} reference
// node so the slice can be sorted by it. Returns "" if the node isn't
// shaped that way (only used for sorting, so the fallback is fine).
func nodeLabel(n Node) string {
	if n.kind != kindMap {
		return ""
	}
	for _, p := range n.pairs {
		if p.key == "label" && p.val.kind == kindStr {
			return p.val.str
		}
	}
	return ""
}

// emitDocLinks renders the document-link edges as a sorted seq of
// {kind, target_label, target_uuid} maps. The kind discriminator is
// either "issue" or "feature".
func emitDocLinks(
	links []*model.DocumentLink,
	issueByID map[int64]*model.Issue,
	featureByID map[int64]*model.Feature,
) Node {
	items := make([]Node, 0, len(links))
	for _, l := range links {
		var (
			kind, label, uuid string
		)
		switch {
		case l.IssueID != nil:
			iss := issueByID[*l.IssueID]
			if iss == nil {
				continue // shouldn't happen — FK enforced — but defensive
			}
			kind = "issue"
			label = iss.Key
			uuid = iss.UUID
		case l.FeatureID != nil:
			f := featureByID[*l.FeatureID]
			if f == nil {
				continue
			}
			kind = "feature"
			label = f.Slug
			uuid = f.UUID
		default:
			continue
		}
		items = append(items, Map(
			Pair{"kind", Str(kind)},
			Pair{"target_label", Str(label)},
			Pair{"target_uuid", Str(uuid)},
		))
	}
	sort.Slice(items, func(i, j int) bool {
		// Sort by (kind, target_label) for stability.
		ki := nodeStringField(items[i], "kind")
		kj := nodeStringField(items[j], "kind")
		if ki != kj {
			return ki < kj
		}
		return nodeStringField(items[i], "target_label") < nodeStringField(items[j], "target_label")
	})
	return Seq(items...)
}

func nodeStringField(n Node, key string) string {
	if n.kind != kindMap {
		return ""
	}
	for _, p := range n.pairs {
		if p.key == key && p.val.kind == kindStr {
			return p.val.str
		}
	}
	return ""
}

// exportWriter is a thin filesystem helper that knows how to write
// (path, bytes) under a target root, with dry-run support and counters
// for the result. We write each file as 0644 mode and create parent
// directories on demand.
type exportWriter struct {
	target string
	dryRun bool

	files int
	bytes int64
}

func (w *exportWriter) writeYAML(rel string, n Node) error {
	b, err := Emit(n)
	if err != nil {
		return err
	}
	return w.writeRaw(rel, b)
}

// writeRaw writes bytes verbatim. Used for the markdown bodies
// (which are not YAML) and for already-emitted YAML blobs.
//
// The relative path is resolved against the target root using
// filepath.Join — `rel` is always slash-separated (produced by paths.go)
// so this works correctly on Windows too.
func (w *exportWriter) writeRaw(rel string, b []byte) error {
	w.files++
	w.bytes += int64(len(b))
	if w.dryRun {
		return nil
	}
	// Defensive: rel must be relative. If it ever isn't, bail loudly
	// rather than scribbling outside the target.
	if filepath.IsAbs(rel) || strings.HasPrefix(rel, "..") {
		return fmt.Errorf("sync: refusing to write outside target: %q", rel)
	}
	abs := filepath.Join(w.target, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
		return fmt.Errorf("create dir for %s: %w", rel, err)
	}
	if err := os.WriteFile(abs, b, 0o644); err != nil {
		return fmt.Errorf("write %s: %w", rel, err)
	}
	return nil
}
