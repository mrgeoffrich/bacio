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
	Repos    int `json:"repos"`
	Features int `json:"features"`
	Issues   int `json:"issues"`
	// Comments counts issue-scoped comments written. Kept semantically
	// issue-only so pinned-output tests stay green; the BACI-124
	// feature-scoped comments roll up into FeatureComments below.
	Comments        int `json:"comments"`
	FeatureComments int `json:"feature_comments"`
	Documents       int `json:"documents"`
	// DocFolders / KanbanColumns count the two container record kinds
	// introduced by the workspaces + Kanban pivot. omitempty so a sync
	// repo with neither keeps the pre-pivot JSON shape byte-for-byte.
	DocFolders    int    `json:"doc_folders,omitempty"`
	KanbanColumns int    `json:"kanban_columns,omitempty"`
	Files         int    `json:"files"`
	BytesWritten  int64  `json:"bytes_written"`
	Target        string `json:"target"`
	DryRun        bool   `json:"dry_run,omitempty"`

	// Index carries the per-repo summaries used to render the
	// top-level index.yaml. Populated as each repo finishes exporting
	// so the totals above and the index rows stay in sync.
	Index []RepoIndexEntry `json:"-"`
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
		// Sync is the source of truth across machines — archived rows
		// must round-trip too, so explicitly opt in (BACI-68).
		IncludeArchived: true,
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

	// Write the top-level index.yaml from the accumulated per-repo
	// summaries. Skipped in dry-run mode (no filesystem writes).
	if !e.DryRun {
		if err := WriteIndex(target, res.Index); err != nil {
			return nil, fmt.Errorf("write index: %w", err)
		}
	}
	return res, nil
}

// exportRepo writes the repo.yaml and every feature/issue/document
// folder underneath it. Features must be exported before issues so the
// feature-uuid lookup is populated for the issues' `feature` field —
// although in practice we just build the map before either pass.
func (e *Engine) exportRepo(w *exportWriter, repo *model.Repo, issueByID map[int64]*model.Issue, uuidByKey map[string]string, res *ExportResult) error {
	// Snapshot pre-counts so we can derive this repo's contribution
	// without re-walking the tree at the end.
	preFeatures := res.Features
	preIssues := res.Issues
	preComments := res.Comments
	preFeatureComments := res.FeatureComments
	preDocuments := res.Documents

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
	// we end up writing any feature folders. Include archived so the
	// archive flag round-trips (BACI-68).
	features, err := e.Store.ListFeaturesFiltered(store.FeatureFilter{
		RepoID:             repo.ID,
		IncludeDescription: true,
		IncludeArchived:    true,
	})
	if err != nil {
		return fmt.Errorf("list features: %w", err)
	}
	featureByID := make(map[int64]*model.Feature, len(features))
	for _, f := range features {
		featureByID[f.ID] = f
	}

	for _, f := range features {
		fc, err := e.exportFeature(w, repo, f)
		if err != nil {
			return fmt.Errorf("feature %s: %w", f.Slug, err)
		}
		res.Features++
		res.FeatureComments += fc
	}

	// Issues. We re-fetch with description for this repo specifically,
	// because the global `allIssues` map dropped descriptions to keep
	// it lean.
	id := repo.ID
	issues, err := e.Store.ListIssues(store.IssueFilter{
		RepoID:             &id,
		IncludeDescription: true,
		IncludeArchived:    true,
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
	docs, err := e.Store.ListDocuments(store.DocumentFilter{RepoID: repo.ID, IncludeArchived: true})
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

	// Container records: the workspace sentinel, the doc-folder tree and
	// the Kanban lanes. All three land at NEW sibling paths under
	// repos/<PREFIX>/ — never as a new key in repo.yaml / issue.yaml /
	// doc.yaml — so an older bacio neither reads, rewrites nor deletes
	// them. See the A0 rule in paths.go.
	//
	// Exported last so the `docs` and `issues` slices above are in hand:
	// membership lives on the container side, so folder.yaml needs the
	// document rows and column.yaml needs the issue rows.
	if repo.IsWorkspace() {
		if err := e.exportWorkspaceSentinel(w, repo); err != nil {
			return fmt.Errorf("workspace sentinel: %w", err)
		}
	}
	nFolders, err := e.exportDocFolders(w, repo, docs)
	if err != nil {
		return fmt.Errorf("doc folders: %w", err)
	}
	res.DocFolders += nFolders
	nColumns, err := e.exportKanbanColumns(w, repo, issues)
	if err != nil {
		return fmt.Errorf("kanban columns: %w", err)
	}
	res.KanbanColumns += nColumns

	res.Index = append(res.Index, RepoIndexEntry{
		Prefix:          repo.Prefix,
		UUID:            repo.UUID,
		Name:            repo.Name,
		Remote:          repo.RemoteURL,
		Features:        res.Features - preFeatures,
		Issues:          res.Issues - preIssues,
		Comments:        res.Comments - preComments,
		FeatureComments: res.FeatureComments - preFeatureComments,
		Documents:       res.Documents - preDocuments,
	})
	return nil
}

// exportFeature writes the feature.yaml + description.md + each feature
// comment under the feature folder. Returns the number of feature
// comments written so the caller can roll counts up into ExportResult.
func (e *Engine) exportFeature(w *exportWriter, repo *model.Repo, f *model.Feature) (int, error) {
	folder := FeatureFolder(repo.Prefix, f.Slug)
	descPath := FeatureDescriptionFile(folder)
	yamlPath := FeatureYAMLFile(folder)

	descBytes := NormalizeBody([]byte(f.Description))
	if err := w.writeRaw(descPath, descBytes); err != nil {
		return 0, err
	}
	descHash := ContentHash(descBytes)

	pairs := []Pair{
		{"created_at", Time(f.CreatedAt)},
		{"description_hash", Str(descHash)},
		{"slug", Str(f.Slug)},
		{"title", Str(f.Title)},
		{"updated_at", Time(f.UpdatedAt)},
		{"uuid", Str(f.UUID)},
	}
	if f.Emoji != "" {
		// Emit only when set so pre-BACI-172 features (no glyph) keep
		// today's hash-stable YAML output — the sync LWW gate keys on
		// updated_at AND a byte-identical YAML for no-op rows.
		pairs = append(pairs, Pair{"emoji", Str(f.Emoji)})
	}
	if f.ArchivedAt != nil {
		// Emit only when set so live features keep today's hash-stable
		// YAML output (BACI-68 sync round-trip).
		pairs = append(pairs, Pair{"archived_at", Time(*f.ArchivedAt)})
	}
	// BACI-199: round-trip the three-state column and its sticky bit.
	// Both are emitted only when non-default so live `active`
	// features stay byte-identical to the pre-BACI-199 shape — the
	// LWW gate keys on updated_at AND a byte-identical YAML for no-op
	// rows, and a global re-emit after the schema bump would otherwise
	// churn every feature.yaml.
	if f.State != "" && f.State != model.FeatureStateActive {
		pairs = append(pairs, Pair{"state", Str(string(f.State))})
	}
	if f.StateManual {
		pairs = append(pairs, Pair{"state_manual", Bool(true)})
	}
	// BACI-333: round-trip the collect-handoffs opt-out. Unlike every
	// other synced feature flag the default is ON (true), so emit the
	// column only when it is OFF — emitting it on every feature would
	// churn every feature.yaml on the next sync after the schema bump.
	if !f.CollectHandoffs {
		pairs = append(pairs, Pair{"collect_handoffs", Bool(false)})
	}
	yamlBytes, err := Emit(Map(pairs...))
	if err != nil {
		return 0, err
	}
	if err := w.writeRaw(yamlPath, yamlBytes); err != nil {
		return 0, err
	}

	// BACI-124 feature comments. Mirrors the issue-comment loop in
	// exportIssue exactly; identical YAML / MD schema, just rooted at
	// the feature folder instead of the issue folder.
	comments, err := e.Store.ListFeatureComments(f.ID)
	if err != nil {
		return 0, fmt.Errorf("list feature comments: %w", err)
	}
	for _, c := range comments {
		if err := e.exportFeatureComment(w, folder, c); err != nil {
			return 0, fmt.Errorf("feature comment %s: %w", c.UUID, err)
		}
	}
	return len(comments), nil
}

// exportFeatureComment writes one feature-scoped comment (BACI-124).
// Mirrors exportComment 1:1 except for the path helper — the on-disk
// YAML / MD schema is identical between issue and feature comments.
func (e *Engine) exportFeatureComment(w *exportWriter, featureFolder string, c *model.FeatureComment) error {
	yamlPath, mdPath := FeatureCommentFile(featureFolder, c.CreatedAt, c.UUID)
	bodyBytes := NormalizeBody([]byte(c.Body))
	if err := w.writeRaw(mdPath, bodyBytes); err != nil {
		return err
	}
	bodyHash := ContentHash(bodyBytes)
	pairs := []Pair{
		{"author", Str(c.Author)},
		{"body_hash", Str(bodyHash)},
		{"created_at", Time(c.CreatedAt)},
		{"uuid", Str(c.UUID)},
	}
	// BACI-333: emit the kind discriminator only when it is not the
	// 'note' default, mirroring comments.eval — pre-BACI-333 comment
	// YAML stays byte-identical so the LWW gate doesn't churn.
	if c.Kind != "" && c.Kind != store.FeatureCommentKindNote {
		pairs = append(pairs, Pair{"kind", Str(c.Kind)})
	}
	yamlBytes, err := Emit(Map(pairs...))
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
	// BACI-349: customer_impact is emitted only when set — unlike assignee
	// (always-emitted), an empty impact line stays off-disk so every
	// pre-BACI-349 issue file's bytes are unchanged (no churn on the first
	// sync after upgrade). Matches the omitempty tag on ParsedIssue.
	if iss.CustomerImpact != "" {
		pairs = append(pairs, Pair{"customer_impact", Str(iss.CustomerImpact)})
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
	if iss.ArchivedAt != nil {
		pairs = append(pairs, Pair{"archived_at", Time(*iss.ArchivedAt)})
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
	// BACI-131: emit the optional eval-context fields only when eval is
	// true (and only when the underlying value is non-empty). Keeps a
	// normal comment's on-disk YAML byte-identical to the pre-BACI-131
	// shape, so no diff churn on the sync repo for every existing row.
	// dispatch_id is deliberately omitted — see ParsedComment for why.
	pairs := []Pair{
		{"author", Str(c.Author)},
		{"body_hash", Str(bodyHash)},
		{"created_at", Time(c.CreatedAt)},
		{"uuid", Str(c.UUID)},
	}
	if c.Eval {
		pairs = append(pairs, Pair{"eval", Bool(true)})
		if c.AgentSessionID != "" {
			pairs = append(pairs, Pair{"agent_session_id", Str(c.AgentSessionID)})
		}
		if c.Mode != "" {
			pairs = append(pairs, Pair{"mode", Str(c.Mode)})
		}
	}
	yamlBytes, err := Emit(Map(pairs...))
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
	contentPath := DocumentContentFile(folder, d.Filename)
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
	if d.ArchivedAt != nil {
		pairs = append(pairs, Pair{"archived_at", Time(*d.ArchivedAt)})
	}
	yamlBytes, err := Emit(Map(pairs...))
	if err != nil {
		return err
	}
	return w.writeRaw(yamlPath, yamlBytes)
}

// exportWorkspaceSentinel writes repos/<PREFIX>/workspace.yaml. Its
// PRESENCE is the whole signal — a prefix with this file is a
// workspace, a prefix without it is a git repo. repo.yaml is left
// byte-identical to what an older binary writes (A0), which is why the
// discriminator lives in a sibling rather than as a `kind:` key.
//
// The body carries the repo's own uuid + timestamps so the file is
// byte-stable across exports and cross-checkable against repo.yaml.
func (e *Engine) exportWorkspaceSentinel(w *exportWriter, repo *model.Repo) error {
	return w.writeYAML(WorkspaceYAMLFile(repo.Prefix), Map(
		Pair{"created_at", Time(repo.CreatedAt)},
		Pair{"kind", Str(string(model.RepoKindWorkspace))},
		Pair{"updated_at", Time(repo.UpdatedAt)},
		Pair{"uuid", Str(repo.UUID)},
	))
}

// exportDocFolders writes one repos/<PREFIX>/folders/<uuid>/folder.yaml
// per doc folder, carrying the folder's own fields plus the ordered
// sequence of document uuids it contains.
//
// `docs` is the repo's document list as already fetched by exportRepo
// (lean rows — no content — but they carry uuid / folder_id /
// folder_position). Membership is derived here rather than written into
// doc.yaml because a new key in doc.yaml would hard-fail an older
// binary's sync. Returns the number of folder records written.
func (e *Engine) exportDocFolders(w *exportWriter, repo *model.Repo, docs []*model.Document) (int, error) {
	folders, err := e.Store.ListDocFolders(repo.ID)
	if err != nil {
		return 0, fmt.Errorf("list doc folders: %w", err)
	}
	if len(folders) == 0 {
		return 0, nil
	}
	uuidByID := make(map[int64]string, len(folders))
	for _, f := range folders {
		uuidByID[f.ID] = f.UUID
	}

	// Group documents by folder. Sort by (folder_position, filename) —
	// the same order store.ListDocuments applies, so the on-disk
	// sequence is exactly what the UI renders. Doc-folder positions are
	// a sort key rather than a dense index (2B), so the filename
	// tie-break is load-bearing for the all-zero pre-pivot case.
	type docRef struct {
		position int
		filename string
		uuid     string
	}
	byFolder := make(map[int64][]docRef, len(folders))
	for _, d := range docs {
		if d.FolderID == nil {
			continue
		}
		byFolder[*d.FolderID] = append(byFolder[*d.FolderID], docRef{
			position: d.FolderPosition,
			filename: d.Filename,
			uuid:     d.UUID,
		})
	}
	for id := range byFolder {
		refs := byFolder[id]
		sort.Slice(refs, func(i, j int) bool {
			if refs[i].position != refs[j].position {
				return refs[i].position < refs[j].position
			}
			return refs[i].filename < refs[j].filename
		})
		byFolder[id] = refs
	}

	written := 0
	for _, f := range folders {
		parentUUID := ""
		if f.ParentID != nil {
			// A parent outside this repo is impossible (the FK plus the
			// repo-scoped list), but a missing lookup would silently
			// re-root the folder, so drop the reference rather than
			// emitting a dangling one.
			parentUUID = uuidByID[*f.ParentID]
		}
		items := make([]Node, 0, len(byFolder[f.ID]))
		for _, ref := range byFolder[f.ID] {
			items = append(items, Str(ref.uuid))
		}
		folderPath := DocFolderFolder(repo.Prefix, f.UUID)
		if err := w.writeYAML(DocFolderYAMLFile(folderPath), Map(
			Pair{"created_at", Time(f.CreatedAt)},
			Pair{"documents", Seq(items...)},
			Pair{"name", Str(f.Name)},
			Pair{"parent_uuid", Str(parentUUID)},
			Pair{"position", Int(int64(f.Position))},
			Pair{"updated_at", Time(f.UpdatedAt)},
			Pair{"uuid", Str(f.UUID)},
		)); err != nil {
			return written, err
		}
		written++
	}
	return written, nil
}

// exportKanbanColumns writes one
// repos/<PREFIX>/kanban/<uuid>/column.yaml per lane, carrying the
// lane's own fields plus the ordered sequence of issue uuids on it.
//
// Same container-side membership rule as exportDocFolders: nothing
// about board placement serialises into issue.yaml. Returns the number
// of column records written.
func (e *Engine) exportKanbanColumns(w *exportWriter, repo *model.Repo, issues []*model.Issue) (int, error) {
	columns, err := e.Store.ListKanbanColumns(repo.ID)
	if err != nil {
		return 0, fmt.Errorf("list kanban columns: %w", err)
	}
	if len(columns) == 0 {
		return 0, nil
	}

	// Kanban positions ARE dense 0-based indices (2C), unlike doc-folder
	// positions; the issue number tie-break only matters for a DB left
	// non-dense by a hand-edit or an older binary.
	type cardRef struct {
		position int
		number   int64
		uuid     string
	}
	byColumn := make(map[int64][]cardRef, len(columns))
	for _, iss := range issues {
		if iss.KanbanColumnID == nil {
			continue
		}
		byColumn[*iss.KanbanColumnID] = append(byColumn[*iss.KanbanColumnID], cardRef{
			position: iss.KanbanPosition,
			number:   iss.Number,
			uuid:     iss.UUID,
		})
	}
	for id := range byColumn {
		refs := byColumn[id]
		sort.Slice(refs, func(i, j int) bool {
			if refs[i].position != refs[j].position {
				return refs[i].position < refs[j].position
			}
			return refs[i].number < refs[j].number
		})
		byColumn[id] = refs
	}

	written := 0
	for _, c := range columns {
		items := make([]Node, 0, len(byColumn[c.ID]))
		for _, ref := range byColumn[c.ID] {
			items = append(items, Str(ref.uuid))
		}
		colPath := KanbanColumnFolder(repo.Prefix, c.UUID)
		if err := w.writeYAML(KanbanColumnYAMLFile(colPath), Map(
			Pair{"created_at", Time(c.CreatedAt)},
			Pair{"issues", Seq(items...)},
			Pair{"name", Str(c.Name)},
			Pair{"position", Int(int64(c.Position))},
			Pair{"updated_at", Time(c.UpdatedAt)},
			Pair{"uuid", Str(c.UUID)},
		)); err != nil {
			return written, err
		}
		written++
	}
	return written, nil
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
