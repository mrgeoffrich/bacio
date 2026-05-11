package cli

import (
	"errors"
	"fmt"
	"os"

	"github.com/mrgeoffrich/bacio/internal/model"
	"github.com/mrgeoffrich/bacio/internal/store"
	"github.com/mrgeoffrich/bacio/internal/sync"
)

// resolveLabelViaRedirects looks up `label` for `kind` in the local
// sync repo's redirects.yaml for `repo`, returning the current label
// if a redirect chain resolves it. Best-effort: any failure (no sync
// configured for this project, no local clone recorded, no redirects
// file, label not in any chain) returns ok=false silently. Callers
// use this only to retry a NotFound lookup with a translated label.
//
// Only fires in local mode; in remote mode it returns ok=false (the
// remote API server is responsible for its own resolution if it
// chooses to support this).
func resolveLabelViaRedirects(repo *model.Repo, kind, label string) (string, bool) {
	if inRemoteMode() || repo == nil || repo.Path == "" {
		return "", false
	}
	cfg, err := sync.ReadProjectConfig(repo.Path)
	if err != nil || cfg == nil || cfg.Sync.Remote == "" {
		return "", false
	}
	s, err := openStore()
	if err != nil {
		return "", false
	}
	defer s.Close()
	rem, err := s.GetSyncRemote(cfg.Sync.Remote)
	if err != nil {
		return "", false
	}
	redirects, err := sync.LoadRedirects(rem.LocalPath, repo.Prefix)
	if err != nil || len(redirects) == 0 {
		return "", false
	}
	return sync.ResolveLabel(redirects, kind, label)
}

// isNotFound reports whether err is a "row not found" failure from
// the store layer or anything wrapping it. Used by show commands to
// decide whether to attempt a redirect-based retry.
func isNotFound(err error) bool {
	return err != nil && errors.Is(err, store.ErrNotFound)
}

// retryWithRedirect runs `lookup(label)` and, if it returns NotFound,
// asks the sync repo's redirects.yaml whether `label` was renamed
// for `kind`; if so, retries `lookup` once with the resolved label
// and prints a one-line redirect notice on stderr. The original
// NotFound is preserved if no resolution exists or if the resolved
// label still doesn't lookup. Generics keep the three show commands
// (issue / feature / doc) sharing one retry shape.
func retryWithRedirect[T any](repo *model.Repo, kind, label string, lookup func(string) (T, error)) (T, error) {
	v, err := lookup(label)
	if !isNotFound(err) {
		return v, err
	}
	newLabel, ok := resolveLabelViaRedirects(repo, kind, label)
	if !ok || newLabel == label {
		return v, err
	}
	v2, err2 := lookup(newLabel)
	if err2 != nil {
		// Resolution found a redirect but the target itself is gone
		// or otherwise unreachable. Surface the original NotFound so
		// the user sees the label they actually asked for.
		return v, err
	}
	fmt.Fprintf(os.Stderr, "bacio: redirect: %s → %s\n", label, newLabel)
	return v2, nil
}
