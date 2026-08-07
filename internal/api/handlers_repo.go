package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/mrgeoffrich/bacio/internal/cli/inputs"
	"github.com/mrgeoffrich/bacio/internal/client"
	"github.com/mrgeoffrich/bacio/internal/inputio"
	"github.com/mrgeoffrich/bacio/internal/model"
	"github.com/mrgeoffrich/bacio/internal/store"
)

// RepoOut is model.Repo plus that space's resolved nav-surface gates —
// which top-nav tabs it exposes. The embedded pointer means every
// existing repo field marshals exactly as before, so this is purely
// additive on the wire (remote.go decodes with plain json.Unmarshal,
// and an older client simply ignores the two extra keys).
//
// The gates ride the repo payload rather than getting their own GET
// because the only reader is the frontend's board list, and it needs
// them synchronously: RepoProvider.pickBoard computes the target
// space's home view inside a click handler, before navigating. A
// separate fetch would turn that into an await mid-click.
//
// The values are RESOLVED — never the raw nullable columns. See
// model.ResolveRepoSurfaces.
type RepoOut struct {
	*model.Repo
	ShowAgentSurfaces bool `json:"show_agent_surfaces"`
	ShowKanban        bool `json:"show_kanban"`
}

// repoOut builds one RepoOut from a repo and the resolved-surfaces map.
// One builder so every repo-shaped response carries the gates and none
// can drift; a prefix missing from the map falls back to the kind
// default rather than to the Go zero value, which would blank the nav.
func repoOut(repo *model.Repo, surfaces map[string]model.RepoSurfaces) RepoOut {
	s, ok := surfaces[repo.Prefix]
	if !ok {
		s = model.ResolveRepoSurfaces(repo.Kind, nil, nil)
	}
	return RepoOut{Repo: repo, ShowAgentSurfaces: s.ShowAgentSurfaces, ShowKanban: s.ShowKanban}
}

// repoOutOne is the single-repo form, for handlers that don't already
// hold the bulk map.
func (d deps) repoOutOne(repo *model.Repo) RepoOut {
	s, err := d.store.GetRepoSurfaces(repo.ID)
	if err != nil {
		// A surfaces read failing shouldn't fail the whole repo
		// response; fall back to the kind default, same as a missing
		// map entry above.
		s = model.ResolveRepoSurfaces(repo.Kind, nil, nil)
	}
	return RepoOut{Repo: repo, ShowAgentSurfaces: s.ShowAgentSurfaces, ShowKanban: s.ShowKanban}
}

func (d deps) handleReposList(w http.ResponseWriter, r *http.Request) {
	repos, err := d.store.ListRepos()
	if err != nil {
		status, code := statusForError(err)
		writeError(w, status, code, err.Error(), nil)
		return
	}
	surfaces, err := d.store.ListRepoSurfaces()
	if err != nil {
		status, code := statusForError(err)
		writeError(w, status, code, err.Error(), nil)
		return
	}
	out := make([]RepoOut, 0, len(repos))
	for _, repo := range repos {
		out = append(out, repoOut(repo, surfaces))
	}
	writeJSON(w, http.StatusOK, out)
}

// RepoActivityOut is one row of `GET /repos/activity` (BACI-369) — the
// per-repo activity summary the topbar's repository picker orders itself
// by. Mirrors client.RepoActivity field-for-field; last_activity_at is
// omitted for a repo nothing has happened in yet.
type RepoActivityOut struct {
	Prefix         string     `json:"prefix"`
	LastActivityAt *time.Time `json:"last_activity_at,omitempty"`
	ActiveJobs     int        `json:"active_jobs"`
}

// handleRepoActivityList serves the cross-repo activity summary. Cheap
// enough for the picker to poll on the shared 10s cadence: one aggregate
// query, one row per repo. Read-only — no dry-run, no schema entry, no
// CLI verb (same class as GET /history).
func (d deps) handleRepoActivityList(w http.ResponseWriter, r *http.Request) {
	rows, err := d.store.ListRepoActivity()
	if err != nil {
		status, code := statusForError(err)
		writeError(w, status, code, err.Error(), nil)
		return
	}
	out := make([]RepoActivityOut, 0, len(rows))
	for _, a := range rows {
		out = append(out, RepoActivityOut{
			Prefix:         a.Prefix,
			LastActivityAt: a.LastActivityAt,
			ActiveJobs:     a.ActiveJobs,
		})
	}
	writeJSON(w, http.StatusOK, out)
}

func (d deps) handleReposShow(w http.ResponseWriter, r *http.Request) {
	prefix := strings.ToUpper(r.PathValue("prefix"))
	repo, err := d.store.GetRepoByPrefix(prefix)
	if err != nil {
		status, code := statusForError(err)
		writeError(w, status, code, err.Error(), nil)
		return
	}
	writeJSON(w, http.StatusOK, d.repoOutOne(repo))
}

func (d deps) handleReposCreate(w http.ResponseWriter, r *http.Request) {
	raw, ok := readBody(r, w)
	if !ok {
		return
	}
	in, _, err := inputio.DecodeStrict[inputs.RepoCreateInput](raw)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_input", err.Error(), nil)
		return
	}
	if strings.TrimSpace(in.Name) == "" {
		writeError(w, http.StatusBadRequest, "invalid_input", "name is required", map[string]any{"field": "name"})
		return
	}

	// The (kind, path) invariant, enforced here as well as at the store
	// boundary so the caller gets a field-attributed 400 instead of a
	// generic refusal. A workspace has no checkout at all — path and
	// remote_url must be empty — while a git repo has nothing to track
	// without a path. Absent kind reads as "git", which is what every
	// pre-pivot caller sends.
	kind := model.RepoKind(strings.ToLower(strings.TrimSpace(in.Kind)))
	switch kind {
	case "":
		kind = model.RepoKindGit
	case model.RepoKindGit, model.RepoKindWorkspace:
	default:
		writeError(w, http.StatusBadRequest, "invalid_input",
			fmt.Sprintf("unknown repo kind %q (want %q or %q)", in.Kind, model.RepoKindGit, model.RepoKindWorkspace),
			map[string]any{"field": "kind"})
		return
	}
	if kind == model.RepoKindWorkspace {
		if strings.TrimSpace(in.Path) != "" {
			writeError(w, http.StatusBadRequest, "invalid_input",
				"a workspace has no working tree — path must be empty",
				map[string]any{"field": "path"})
			return
		}
		if strings.TrimSpace(in.RemoteURL) != "" {
			writeError(w, http.StatusBadRequest, "invalid_input",
				"a workspace has no git remote — remote_url must be empty",
				map[string]any{"field": "remote_url"})
			return
		}
		// Identical body to POST /workspaces, deliberately: one
		// implementation, two entry points.
		d.createWorkspace(w, r, in.Name, in.Prefix)
		return
	}

	if strings.TrimSpace(in.Path) == "" {
		writeError(w, http.StatusBadRequest, "invalid_input", "path is required", map[string]any{"field": "path"})
		return
	}

	var prefix string
	if in.Prefix != "" {
		p, err := store.ValidatePrefix(in.Prefix)
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid_input", err.Error(), map[string]any{"field": "prefix"})
			return
		}
		prefix = p
	}

	// Reachable only on the git branch, so in.Path is non-empty here.
	// That matters: GetRepoByPath("") returns ErrNotFound by design
	// (a pathless row is a phantom or a workspace and can never be
	// "the repo registered at this path"), so an empty path reaching
	// this call would report a false negative rather than a false
	// "already registered" — but the workspace branch above returns
	// before it either way.
	if existing, err := d.store.GetRepoByPath(in.Path); err == nil {
		writeError(w, http.StatusConflict, "conflict",
			"repo already registered for this path",
			map[string]any{"prefix": existing.Prefix, "path": existing.Path})
		return
	} else if !errors.Is(err, store.ErrNotFound) {
		status, code := statusForError(err)
		writeError(w, status, code, err.Error(), nil)
		return
	}

	if prefix != "" {
		if existing, err := d.store.GetRepoByPrefix(prefix); err == nil {
			writeError(w, http.StatusConflict, "conflict",
				"prefix already in use",
				map[string]any{"prefix": existing.Prefix, "path": existing.Path})
			return
		} else if !errors.Is(err, store.ErrNotFound) {
			status, code := statusForError(err)
			writeError(w, status, code, err.Error(), nil)
			return
		}
	} else {
		p, err := d.store.AllocatePrefix(in.Name)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "internal", err.Error(), nil)
			return
		}
		prefix = p
	}

	if isDryRun(r) {
		writeDryRun(w, http.StatusCreated, &model.Repo{
			Prefix: prefix,
			Name:   in.Name,
			// Explicit: model.Repo.Kind has no omitempty, so a zero value
			// would put `"kind": ""` on the wire and the frontend's
			// 'git' | 'workspace' union would not match it.
			Kind:            model.RepoKindGit,
			Path:            in.Path,
			RemoteURL:       in.RemoteURL,
			NextIssueNumber: 1,
		})
		return
	}

	repo, err := d.store.CreateRepo(prefix, in.Name, in.Path, in.RemoteURL)
	if err != nil {
		status, code := statusForError(err)
		writeError(w, status, code, err.Error(), nil)
		return
	}
	recordOp(d.store, d.logger, model.HistoryEntry{
		RepoID: &repo.ID, RepoPrefix: repo.Prefix,
		Actor:    ActorFromContext(r.Context()),
		Op:       "repo.create",
		Kind:     "repo",
		TargetID: &repo.ID, TargetLabel: repo.Prefix,
		Details: "api init (" + repo.Name + ")",
	})
	// Features are mandatory (Pipeline): seed the catch-all features +
	// repo default on first registration. Best-effort, idempotent.
	if err := d.store.BootstrapRepoDefaults(repo.ID); err != nil {
		d.logger.Warn("bacio: bootstrap repo defaults", "repo", repo.Prefix, "err", err)
	}
	writeJSON(w, http.StatusCreated, d.repoOutOne(repo))
}

// surfaceToggleIn is the strict-decoded body for the two per-space
// nav-surface gates. A local request struct, not a bacio
// schema-registered inputs.* type: there is no CLI verb for either,
// because nothing in Go reads them — they gate React buttons and one
// route redirect. Same class as backlogCollapsedIn and
// boardHiddenStatesIn; contrast auto_ship, which earns the full
// six-rule CLI treatment because the controller's ticker acts on it.
type surfaceToggleIn struct {
	Enabled bool `json:"enabled"`
}

// handleRepoShowAgentSurfaces — PUT /repos/{prefix}/show-agent-surfaces.
// Persists the per-space "Agent Mode" gate (the Agentic Pipeline /
// Agents / Monitor tabs). Honours ?dry_run=true and audits a
// repo_setting.update row only when the value actually changes.
//
// There is no GET twin: GET /repos and GET /repos/{prefix} already
// carry the resolved value on every repo payload.
func (d deps) handleRepoShowAgentSurfaces(w http.ResponseWriter, r *http.Request) {
	d.handleSurfaceToggle(w, r, "show_agent_surfaces")
}

// handleRepoShowKanban — PUT /repos/{prefix}/show-kanban. Persists the
// per-space "Show Kanban Board" gate.
func (d deps) handleRepoShowKanban(w http.ResponseWriter, r *http.Request) {
	d.handleSurfaceToggle(w, r, "show_kanban")
}

// handleSurfaceToggle is the shared body of the two gate handlers —
// they differ only in which column they write and which resolved field
// they compare against for the change check.
func (d deps) handleSurfaceToggle(w http.ResponseWriter, r *http.Request, field string) {
	repo, ok := resolveRepoFromPath(w, r, d.store)
	if !ok {
		return
	}
	raw, ok := readBody(r, w)
	if !ok {
		return
	}
	in, _, err := inputio.DecodeStrict[surfaceToggleIn](raw)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_input", err.Error(), nil)
		return
	}
	if isDryRun(r) {
		writeDryRun(w, http.StatusOK, map[string]any{field: in.Enabled})
		return
	}
	// Compare against the RESOLVED previous value, not the raw column:
	// on a space that has never been touched the column is NULL, and
	// writing the kind default explicitly is not a change worth an
	// audit row.
	prev, err := d.store.GetRepoSurfaces(repo.ID)
	if err != nil {
		status, code := statusForError(err)
		writeError(w, status, code, err.Error(), nil)
		return
	}
	var was bool
	if field == "show_kanban" {
		was = prev.ShowKanban
		err = d.store.SetRepoShowKanban(repo.ID, in.Enabled)
	} else {
		was = prev.ShowAgentSurfaces
		err = d.store.SetRepoShowAgentSurfaces(repo.ID, in.Enabled)
	}
	if err != nil {
		status, code := statusForError(err)
		writeError(w, status, code, err.Error(), nil)
		return
	}
	if was != in.Enabled {
		recordOp(d.store, d.logger, model.HistoryEntry{
			RepoID: &repo.ID, RepoPrefix: repo.Prefix,
			Actor:       ActorFromContext(r.Context()),
			Op:          "repo_setting.update",
			Kind:        "repo_setting",
			TargetLabel: field,
			Details:     fmt.Sprintf("enabled=%v", in.Enabled),
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{field: in.Enabled})
}

// handleReposDelete implements `DELETE /repos/{prefix}` — the
// destructive end of `bacio repo rm`. The destruction is gated on a
// `confirm=<prefix>` query parameter that must equal the path prefix
// (case-insensitive); without it the server returns 412 Precondition
// Failed with the impact preview embedded in the error envelope's
// `details`. `?dry_run=true` short-circuits to the same preview as a
// 200 OK with no changes.
//
// The gate lives here (not just in the CLI) so that a direct
// curl -XDELETE without the confirmation token gets the same safety
// as the CLI — no agent / proxy can bypass the alert by skipping a
// flag.
func (d deps) handleReposDelete(w http.ResponseWriter, r *http.Request) {
	prefix := strings.ToUpper(r.PathValue("prefix"))
	repo, err := d.store.GetRepoByPrefix(prefix)
	if err != nil {
		status, code := statusForError(err)
		writeError(w, status, code, err.Error(), nil)
		return
	}
	counts, err := d.store.RepoCascadeCountsForID(repo.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal", err.Error(), nil)
		return
	}
	preview := struct {
		Repo        *model.Repo             `json:"repo"`
		Cascade     store.RepoCascadeCounts `json:"cascade"`
		WouldDelete bool                    `json:"would_delete"`
	}{Repo: repo, Cascade: counts, WouldDelete: true}

	if isDryRun(r) {
		writeDryRun(w, http.StatusOK, preview)
		return
	}
	confirm := strings.TrimSpace(r.URL.Query().Get("confirm"))
	if !strings.EqualFold(confirm, prefix) {
		// 412 because we have a target but the precondition (matching
		// confirmation token) failed; not 400 (input is well-formed)
		// nor 403 (auth is fine).
		details := map[string]any{
			"repo":         preview.Repo,
			"cascade":      preview.Cascade,
			"would_delete": true,
		}
		msg := "destructive operation requires ?confirm=" + prefix + " (or --confirm " + prefix + " from the CLI); ask the user before proceeding"
		if confirm != "" {
			msg = "confirm value " + confirm + " does not match repo prefix " + prefix
		}
		writeError(w, http.StatusPreconditionFailed, "confirm_required", msg, details)
		return
	}
	if err := d.store.DeleteHistoryByRepo(repo.ID); err != nil {
		writeError(w, http.StatusInternalServerError, "internal", err.Error(), nil)
		return
	}
	if err := d.store.DeleteRepo(repo.ID); err != nil {
		status, code := statusForError(err)
		writeError(w, status, code, err.Error(), nil)
		return
	}
	recordOp(d.store, d.logger, model.HistoryEntry{
		// repo_id NULL — the row is gone; only the prefix snapshot
		// remains for callers querying history afterwards.
		RepoPrefix:  repo.Prefix,
		Actor:       ActorFromContext(r.Context()),
		Op:          "repo.delete",
		Kind:        "repo",
		TargetLabel: repo.Prefix,
		Details:     repoCascadeDetails(repo, counts),
	})
	w.WriteHeader(http.StatusNoContent)
}

// repoCascadeDetails mirrors local_repo.go:formatCascadeDetails so
// audit messages from CLI and HTTP deletes read identically.
func repoCascadeDetails(repo *model.Repo, c store.RepoCascadeCounts) string {
	return fmt.Sprintf("%s (%d issues, %d comments, %d features, %d documents, %d history)",
		repo.Name, c.Issues, c.Comments, c.Features, c.Documents, c.History)
}

// handleRepoLink (BACI-112) implements `POST /repos/{prefix}/link` —
// the HTTP shim around client.LinkPhantomRepo. Body is the
// `RepoLinkInput` payload (`{path: ...}`); `?dry_run=true` short-
// circuits to the projection. Typed RepoLinkError refusals are mapped
// to specific status codes so the JS / CLI sides can branch on `kind`
// without string-matching the human message.
//
// Status mapping (matches the local client's typed errors 1:1):
//
//	200 OK            — happy path, RepoLinkResult JSON body
//	200 OK + dry_run  — projection only, no DB write
//	404 not_found     — no such prefix
//	409 conflict      — not_phantom / path_already_bound / no_owning_sync_repo / workspace
//	400 invalid_input — path_not_absolute / path_not_exists / path_not_git
//
// Every error envelope embeds the `kind` string in `details` so the
// remote client can rehydrate a *RepoLinkError without string-matching.
func (d deps) handleRepoLink(w http.ResponseWriter, r *http.Request) {
	prefix := strings.ToUpper(r.PathValue("prefix"))
	raw, ok := readBody(r, w)
	if !ok {
		return
	}
	in, _, err := inputio.DecodeStrict[inputs.RepoLinkInput](raw)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_input", err.Error(), nil)
		return
	}
	actor := ActorFromContext(r.Context())
	c := client.NewLocalFromStore(d.store, actor)
	result, err := c.LinkPhantomRepo(r.Context(), prefix, in.Path, isDryRun(r))
	if err != nil {
		var linkErr *client.RepoLinkError
		if errors.As(err, &linkErr) {
			details := map[string]any{
				"kind":   linkErr.Kind,
				"prefix": linkErr.Prefix,
				"path":   linkErr.Path,
			}
			if linkErr.CurrentPath != "" {
				details["current_path"] = linkErr.CurrentPath
			}
			if linkErr.ExistingPrefix != "" {
				details["existing_prefix"] = linkErr.ExistingPrefix
			}
			switch linkErr.Kind {
			// "workspace" joins the not_phantom class: the prefix exists
			// and the request is well-formed, but the target is a
			// workspace — pathless like a phantom yet never linkable,
			// because it has no checkout anywhere on any machine. 409,
			// not 400: nothing about the input is malformed.
			case "not_phantom", "path_already_bound", "no_owning_sync_repo", "workspace":
				writeError(w, http.StatusConflict, "conflict", linkErr.Error(), details)
			case "path_not_absolute", "path_not_exists", "path_not_git":
				writeError(w, http.StatusBadRequest, "invalid_input", linkErr.Error(), details)
			default:
				writeError(w, http.StatusInternalServerError, "internal", linkErr.Error(), details)
			}
			return
		}
		status, code := statusForError(err)
		writeError(w, status, code, err.Error(), nil)
		return
	}
	if isDryRun(r) {
		writeDryRun(w, http.StatusOK, result)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	_ = enc.Encode(body)
}
