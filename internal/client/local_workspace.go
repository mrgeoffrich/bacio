package client

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/mrgeoffrich/bacio/internal/model"
	"github.com/mrgeoffrich/bacio/internal/store"
)

// CreateWorkspace (local) registers a manual workspace — a repo row with
// kind='workspace' and an empty path and remote_url.
//
// The store does the real work in one call (validate → allocate prefix →
// insert → BootstrapRepoDefaults, which for a workspace seeds the starter
// Kanban board and nothing else — the catch-all epics are a git-repo
// Pipeline concern). This method's job is
// the client-layer contract every other mutation here honours:
// rehearse the validators, project the dry-run row, and write the audit
// entry on the commit path.
//
// The audit row uses op `workspace.create` under kind `repo`: a
// workspace IS a repos row, so `bacio history --kind repo` must still
// find it, while the op distinguishes it from a git registration.
func (c *localClient) CreateWorkspace(ctx context.Context, in WorkspaceCreateInput, dryRun bool) (*model.Repo, error) {
	name := strings.TrimSpace(in.Name)
	if name == "" {
		return nil, fmt.Errorf("name is required")
	}
	// Rehearse the prefix resolution so a dry-run reports the prefix the
	// real call would land on. AllocatePrefix is a pure read (it counts
	// matching repos rows), so running it here writes nothing.
	prefix := strings.TrimSpace(in.Prefix)
	if prefix == "" {
		allocated, err := c.store.AllocatePrefix(name)
		if err != nil {
			return nil, fmt.Errorf("allocate prefix: %w", err)
		}
		prefix = allocated
	} else {
		valid, err := store.ValidatePrefix(prefix)
		if err != nil {
			return nil, err
		}
		prefix = valid
		if _, err := c.store.GetRepoByPrefix(prefix); err == nil {
			return nil, fmt.Errorf("prefix %s is already in use", prefix)
		} else if !errors.Is(err, store.ErrNotFound) {
			return nil, err
		}
	}

	if dryRun {
		// Server-time fields (id, uuid, timestamps) come back zero, as
		// every other dry-run projection in this package does.
		return &model.Repo{
			Prefix:          prefix,
			Name:            name,
			Kind:            model.RepoKindWorkspace,
			NextIssueNumber: 1,
		}, nil
	}

	repo, err := c.store.CreateWorkspace(prefix, name)
	if err != nil {
		return nil, err
	}
	c.recordOp(model.HistoryEntry{
		RepoID: &repo.ID, RepoPrefix: repo.Prefix,
		Op: "workspace.create", Kind: "repo",
		TargetID: &repo.ID, TargetLabel: repo.Prefix,
		Details: repo.Name,
	})
	return repo, nil
}

// errWorkspaceHasNoWorkingTree is the shared refusal for the verbs a
// workspace legitimately cannot do because it has no checkout on disk.
// Kept in one place so every surface says the same thing and nobody
// re-uses the phantom message ("link it first"), which would send the
// user hunting for a git repo that does not and will never exist.
func errWorkspaceHasNoWorkingTree(repo *model.Repo, what string) error {
	return fmt.Errorf("%s is a workspace, not a git repo — %s", repo.Prefix, what)
}

// refuseDispatchOnWorkspace is the guard on every agent-dispatch creation
// path. A dispatched worker's whole contract is "go and work in this
// repo's checkout" — it clones a worktree, builds, runs tests, opens a
// PR. A workspace has no checkout at all, so the dispatch could only ever
// fail deep inside the worker with a confusing error. Refuse at the
// creation boundary instead, where the message can say why.
//
// This is the server-side half of hiding the Agentic Pipeline nav entry
// for workspaces: the UI stops offering it, and this stops the REST /
// CLI / desktop paths from queueing one anyway.
func refuseDispatchOnWorkspace(repo *model.Repo) error {
	if repo == nil || !repo.IsWorkspace() {
		return nil
	}
	return errWorkspaceHasNoWorkingTree(repo,
		"it has no working tree for an agent to work in, so it can't run agent dispatches. "+
			"Track the work here and dispatch from the git repo the change belongs in")
}
