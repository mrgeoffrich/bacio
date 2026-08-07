package api

// HTTP surface for manual workspaces (the pivot's `kind='workspace'`
// repos row).
//
//	POST /workspaces        {name, prefix?}   -> 201 model.Repo
//
// A workspace is a repos row with kind='workspace', an empty path and an
// empty remote_url. It gets its own route rather than riding POST /repos
// because registering a git repo takes a path and refuses without one,
// and a workspace takes neither — see remote_workspace.go's header for
// the same reasoning on the client side. POST /repos still accepts
// {"kind":"workspace"} (handlers_repo.go) and funnels into the exact
// same body so the two surfaces cannot drift.

import (
	"net/http"
	"strings"

	"github.com/mrgeoffrich/bacio/internal/client"
	"github.com/mrgeoffrich/bacio/internal/inputio"
)

// workspaceCreateIn is the POST /workspaces body — the wire twin of
// client.WorkspaceCreateInput. An empty/absent prefix means "allocate
// one from the name" (store.AllocatePrefix), exactly as `bacio init`
// does for a git repo.
type workspaceCreateIn struct {
	Name   string `json:"name"`
	Prefix string `json:"prefix,omitempty"`
}

func (d deps) handleWorkspaceCreate(w http.ResponseWriter, r *http.Request) {
	raw, ok := readBody(r, w)
	if !ok {
		return
	}
	in, _, err := inputio.DecodeStrict[workspaceCreateIn](raw)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_input", err.Error(), nil)
		return
	}
	d.createWorkspace(w, r, in.Name, in.Prefix)
}

// createWorkspace is the shared body behind POST /workspaces and the
// `kind == "workspace"` branch of POST /repos. Everything real —
// validation, prefix allocation, the dry-run projection, the
// `workspace.create` audit row and BootstrapRepoDefaults (the starter
// Kanban board; a workspace gets no catch-all epics) — lives in
// client.CreateWorkspace; this only shapes the HTTP envelope.
func (d deps) createWorkspace(w http.ResponseWriter, r *http.Request, name, prefix string) {
	if strings.TrimSpace(name) == "" {
		writeError(w, http.StatusBadRequest, "invalid_input", "name is required", fieldDetail("name"))
		return
	}
	c := client.NewLocalFromStore(d.store, ActorFromContext(r.Context()))
	dryRun := isDryRun(r)
	repo, err := c.CreateWorkspace(r.Context(), client.WorkspaceCreateInput{
		Name:   strings.TrimSpace(name),
		Prefix: strings.TrimSpace(prefix),
	}, dryRun)
	if err != nil {
		status, code := statusForError(err)
		writeError(w, status, code, err.Error(), workspaceErrorField(err))
		return
	}
	if dryRun {
		writeDryRun(w, http.StatusCreated, repo)
		return
	}
	writeJSON(w, http.StatusCreated, d.repoOutOne(repo))
}

// workspaceErrorField attributes a create failure to the field that
// caused it so the React modal can highlight the right input. The
// client returns plain fmt.Errorf values here (no sentinels), so this
// matches on the two phrases it owns and stays silent otherwise.
func workspaceErrorField(err error) map[string]any {
	msg := err.Error()
	switch {
	case strings.Contains(msg, "name is required"):
		return fieldDetail("name")
	case strings.Contains(msg, "prefix"):
		return fieldDetail("prefix")
	}
	return nil
}
