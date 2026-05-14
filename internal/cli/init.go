package cli

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/mrgeoffrich/bacio/internal/git"
	"github.com/mrgeoffrich/bacio/internal/model"
	"github.com/mrgeoffrich/bacio/internal/store"
)

func newInitCmd() *cobra.Command {
	var prefix string
	cmd := &cobra.Command{
		Use:   "init",
		Short: "Bind the current git repo to a kanban prefix",
		RunE: func(cmd *cobra.Command, args []string) error {
			if inRemoteMode() {
				return fmt.Errorf("bacio init: not supported in remote mode (this verb auto-detects CWD git state); use POST /repos against the API or run `bacio init` against the local DB instead")
			}
			cwd, err := os.Getwd()
			if err != nil {
				return err
			}
			info, err := git.Detect(cwd)
			if err != nil {
				return err
			}

			s, err := openStore()
			if err != nil {
				return err
			}
			defer s.Close()

			if existing, err := s.GetRepoByPath(info.Root); err == nil {
				return ok("repo already initialised as %s (%s)", existing.Prefix, existing.Path)
			} else if !errors.Is(err, store.ErrNotFound) {
				return err
			}

			var chosen string
			if prefix != "" {
				p, err := store.ValidatePrefix(prefix)
				if err != nil {
					return err
				}
				if existing, err := s.GetRepoByPrefix(p); err == nil {
					return fmt.Errorf("prefix %s already in use by %s", p, existing.Path)
				}
				chosen = p
			} else {
				chosen, err = s.AllocatePrefix(info.Name)
				if err != nil {
					return err
				}
			}

			repo, err := s.CreateRepo(chosen, info.Name, info.Root, info.RemoteURL)
			if err != nil {
				return err
			}
			recordOp(s, model.HistoryEntry{
				RepoID: &repo.ID, RepoPrefix: repo.Prefix,
				Op: "repo.create", Kind: "repo",
				TargetID: &repo.ID, TargetLabel: repo.Prefix,
				Details: "explicit init (" + repo.Name + ")",
			})

			// Best-effort: keep the machine-local .bacio/ directory
			// (sync config, agent identity) out of git. Never fails
			// the command — mirrors the recordOp convention.
			if added, err := ensureBacioGitignored(info.Root); err != nil {
				fmt.Fprintln(os.Stderr, "bacio: warning: could not update .gitignore:", err)
			} else if added {
				fmt.Fprintln(os.Stderr, "bacio: added '.bacio/' to .gitignore (machine-local state, not shared via git)")
			}

			return emit(repo)
		},
	}
	cmd.Flags().StringVar(&prefix, "prefix", "", "explicit 4-char prefix (e.g. AUTH)")
	return cmd
}

// ensureBacioGitignored makes sure the project's .gitignore excludes
// the .bacio/ directory. Returns true if it appended the rule, false
// if it was already covered. The .bacio/ directory holds machine-local
// state — the sync config and the per-machine agent identity — and is
// never meant to be committed.
func ensureBacioGitignored(root string) (bool, error) {
	path := filepath.Join(root, ".gitignore")
	content, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return false, err
	}
	for _, line := range strings.Split(string(content), "\n") {
		switch strings.TrimSpace(line) {
		case ".bacio/", ".bacio", "/.bacio/", "/.bacio":
			return false, nil
		}
	}
	var b strings.Builder
	b.Write(content)
	if len(content) > 0 && !strings.HasSuffix(string(content), "\n") {
		b.WriteByte('\n')
	}
	b.WriteString(".bacio/\n")
	if err := os.WriteFile(path, []byte(b.String()), 0o644); err != nil {
		return false, err
	}
	return true, nil
}
