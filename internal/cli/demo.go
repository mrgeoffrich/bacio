package cli

import (
	"errors"
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/mrgeoffrich/bacio/internal/model"
	"github.com/mrgeoffrich/bacio/internal/store"
	"github.com/mrgeoffrich/bacio/internal/wasm"
)

// newDemoCmd builds the hidden `bacio demo` parent. It's a developer
// convenience for stress-testing the TUI / CLI against realistic
// volumes of data — not part of the agent-facing surface, so it's
// hidden from the default help. Two sizes are exposed:
//
//	bacio demo small        # base seed: 4 features, ~38 issues, 9 docs
//	bacio demo large        # 10x base: 40 features, ~380 issues, 90 docs
//
// Both refuse to run if the target prefix is already taken; pass
// --reset to wipe the existing demo repo (and its history rows)
// before re-seeding.
func newDemoCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:    "demo",
		Short:  "Seed a synthetic repo with demo data (dev only)",
		Long:   "Seed the local SQLite store with the gelato demo dataset so you can stress-test the UI against realistic volumes. Hidden from the default help because it's a developer convenience, not part of the agent surface.",
		Hidden: true,
	}
	cmd.AddCommand(newDemoSeedCmd("small", "Seed the base gelato dataset (4 features, ~38 issues, 9 docs)", 1))
	cmd.AddCommand(newDemoSeedCmd("large", "Seed 10x the base gelato dataset (40 features, ~380 issues, 90 docs)", 10))
	return cmd
}

func newDemoSeedCmd(name, short string, copies int) *cobra.Command {
	var (
		prefix string
		reset  bool
	)
	cmd := &cobra.Command{
		Use:   name,
		Short: short,
		RunE: func(cmd *cobra.Command, args []string) error {
			if inRemoteMode() {
				return fmt.Errorf("bacio demo: not supported in remote mode (this verb writes to the local SQLite store directly)")
			}
			p, err := store.ValidatePrefix(prefix)
			if err != nil {
				return err
			}
			return runDemoSeed(p, copies, reset)
		},
	}
	cmd.Flags().StringVar(&prefix, "prefix", "DEMO", "4-char repo prefix to seed under")
	cmd.Flags().BoolVar(&reset, "reset", false, "wipe an existing repo at this prefix (and its history rows) before re-seeding")
	return cmd
}

func runDemoSeed(prefix string, copies int, reset bool) error {
	s, err := openStore()
	if err != nil {
		return err
	}
	defer s.Close()

	if existing, err := s.GetRepoByPrefix(prefix); err == nil {
		if !reset {
			return fmt.Errorf("repo %s already exists (path=%s). Pass --reset to wipe it before re-seeding", existing.Prefix, existing.Path)
		}
		// Wipe history first (FK-less, survives DeleteRepo otherwise),
		// then drop the repo — cascade tears down every dependent row.
		if err := s.DeleteHistoryByRepo(existing.ID); err != nil {
			return fmt.Errorf("reset: clear history for %s: %w", existing.Prefix, err)
		}
		if err := s.DeleteRepo(existing.ID); err != nil && !errors.Is(err, store.ErrNotFound) {
			return fmt.Errorf("reset: delete %s: %w", existing.Prefix, err)
		}
	}

	repo, err := wasm.Seed(s, wasm.SeedOptions{
		Prefix: prefix,
		Name:   "bacio-demo-" + prefix,
		// Synthetic path namespaced by prefix so multiple demo repos
		// (DEMO, DEMO1, ...) coexist without colliding on uniq_repos_path.
		Path:   "bacio-demo://" + prefix,
		Copies: copies,
	})
	if err != nil {
		return err
	}

	counts, err := demoCounts(s, repo.ID)
	if err != nil {
		// Counting is best-effort — surface the warning but don't fail
		// the seed itself.
		fmt.Fprintln(os.Stderr, "bacio: warning: failed to count seeded rows:", err)
	}

	// One audit-log entry for the seed itself, on top of the
	// representative events seed.go writes. Makes it easy to grep
	// `bacio history -u <you>` for when each demo dataset was loaded.
	recordOp(s, model.HistoryEntry{
		RepoID: &repo.ID, RepoPrefix: repo.Prefix,
		Op: "demo.seed", Kind: "repo",
		TargetID: &repo.ID, TargetLabel: repo.Prefix,
		Details: fmt.Sprintf("copies=%d", copies),
	})

	return emit(demoSeedResult{
		Repo:      repo,
		Copies:    copies,
		Features:  counts.features,
		Issues:    counts.issues,
		Comments:  counts.comments,
		Documents: counts.documents,
	})
}

type demoSeedResult struct {
	Repo      *model.Repo `json:"repo"`
	Copies    int         `json:"copies"`
	Features  int         `json:"features"`
	Issues    int         `json:"issues"`
	Comments  int         `json:"comments"`
	Documents int         `json:"documents"`
}

type demoRowCounts struct {
	features, issues, comments, documents int
}

func demoCounts(s *store.Store, repoID int64) (demoRowCounts, error) {
	c, err := s.RepoCascadeCountsForID(repoID)
	if err != nil {
		return demoRowCounts{}, err
	}
	return demoRowCounts{
		features:  c.Features,
		issues:    c.Issues,
		comments:  c.Comments,
		documents: c.Documents,
	}, nil
}
