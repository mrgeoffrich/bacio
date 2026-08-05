package client

// Launch-repo resolution (BACI-368). The CLI has always resolved "the
// current repo" from cwd; the web and desktop surfaces never did, so
// they opened on whatever repo was last picked. EnsureRepoForDir is
// the shared seam both server entry points (bacio web / bacio api) and
// the desktop binary call once at process start, so the UI can default
// to the repo it was launched from — enrolling it on first sight, the
// same way every other bacio entry point does.

import (
	"context"

	"github.com/mrgeoffrich/bacio/internal/git"
	"github.com/mrgeoffrich/bacio/internal/model"
	bsync "github.com/mrgeoffrich/bacio/internal/sync"
)

// EnsureRepoForDir resolves the repo containing dir, registering it on
// first sight (prefix allocation, repo.create audit row, default
// features) via Client.EnsureRepo.
//
// The "no repo here" outcomes are ordinary, not failures: an empty
// dir, a directory outside any git working tree (launched from the
// Finder or a bare shell), and a bacio sync repo all return
// (nil, nil). Only a genuine store error propagates. Callers treat a
// nil repo as "no launch repo" and carry on.
func EnsureRepoForDir(ctx context.Context, c Client, dir string) (*model.Repo, error) {
	if dir == "" {
		return nil, nil
	}
	info, err := git.Detect(dir)
	if err != nil {
		return nil, nil
	}
	// A sync repo is bacio's own metadata checkout, never a project —
	// the same refusal resolveRepo makes, minus the error (nothing to
	// tell the user here; the UI just falls back).
	if bsync.IsSyncRepo(info.Root) {
		return nil, nil
	}
	repo, _, err := c.EnsureRepo(ctx, info)
	if err != nil {
		return nil, err
	}
	return repo, nil
}
