package cli

import (
	"errors"
	"fmt"
	"io"
	"os"

	"github.com/spf13/cobra"

	"github.com/mrgeoffrich/bacio/internal/git"
	"github.com/mrgeoffrich/bacio/internal/model"
	"github.com/mrgeoffrich/bacio/internal/store"
)

// statusReport is the unified shape returned by `bacio status`. Inside a git
// repo it auto-registers on first use, like every other mutating command,
// so the only branches are "in a (now-registered) repo" vs "no git repo".
type statusReport struct {
	DBPath         string      `json:"db_path"`
	InRepo         bool        `json:"in_repo"`
	Repo           *model.Repo `json:"repo,omitempty"`
	JustRegistered bool        `json:"just_registered,omitempty"`
	Stats          statusStats `json:"stats"`
}

type statusStats struct {
	// Repo-scoped (populated when InRepo)
	Features      int            `json:"features,omitempty"`
	Issues        int            `json:"issues,omitempty"`
	IssuesByState map[string]int `json:"issues_by_state,omitempty"`
	NextIssueKey  string         `json:"next_issue_key,omitempty"`

	// Global (populated when not InRepo)
	TrackedRepos int `json:"tracked_repos,omitempty"`
	TotalIssues  int `json:"total_issues,omitempty"`
}

func newStatusCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Show current repo, DB location, and quick stats",
		RunE: func(cmd *cobra.Command, args []string) error {
			path := opts.dbPath
			if path == "" {
				p, err := store.DefaultPath()
				if err != nil {
					return err
				}
				path = p
			}
			s, err := store.Open(path)
			if err != nil {
				return err
			}
			defer s.Close()

			report := &statusReport{DBPath: path}

			cwd, err := os.Getwd()
			if err != nil {
				return err
			}
			info, gitErr := git.Detect(cwd)
			switch {
			case gitErr == nil:
				// Auto-register on first use, like every other mutating command.
				repo, err := s.GetRepoByPath(info.Root)
				if errors.Is(err, store.ErrNotFound) {
					prefix, perr := s.AllocatePrefix(info.Name)
					if perr != nil {
						return fmt.Errorf("allocate prefix: %w", perr)
					}
					repo, err = s.CreateRepo(prefix, info.Name, info.Root, info.RemoteURL)
					if err != nil {
						return err
					}
					recordOp(s, model.HistoryEntry{
						RepoID: &repo.ID, RepoPrefix: repo.Prefix,
						Op: "repo.create", Kind: "repo",
						TargetID: &repo.ID, TargetLabel: repo.Prefix,
						Details: "auto-registered via status (" + repo.Name + ")",
					})
					report.JustRegistered = true
				} else if err != nil {
					return err
				}
				report.InRepo = true
				report.Repo = repo
				if err := fillRepoStats(s, repo, &report.Stats); err != nil {
					return err
				}
			case errors.Is(gitErr, git.ErrNotARepo):
				if err := fillGlobalStats(s, &report.Stats); err != nil {
					return err
				}
			default:
				return gitErr
			}
			if opts.output == outputJSON {
				return emit(report)
			}
			return printStatus(os.Stdout, report)
		},
	}
}

func fillRepoStats(s *store.Store, repo *model.Repo, st *statusStats) error {
	feats, err := s.CountFeatures(repo.ID)
	if err != nil {
		return err
	}
	counts, err := s.IssueStateCounts(&repo.ID)
	if err != nil {
		return err
	}
	st.Features = feats
	st.IssuesByState = counts
	for _, n := range counts {
		st.Issues += n
	}
	st.NextIssueKey = fmt.Sprintf("%s-%d", repo.Prefix, repo.NextIssueNumber)
	return nil
}

func fillGlobalStats(s *store.Store, st *statusStats) error {
	repos, err := s.ListRepos()
	if err != nil {
		return err
	}
	counts, err := s.IssueStateCounts(nil)
	if err != nil {
		return err
	}
	st.TrackedRepos = len(repos)
	for _, n := range counts {
		st.TotalIssues += n
	}
	return nil
}

func printStatus(w io.Writer, r *statusReport) error {
	if r.InRepo && r.Repo != nil {
		if r.JustRegistered {
			fmt.Fprintf(w, "Just registered this git repo as %s.\n\n", r.Repo.Prefix)
		}
		fmt.Fprintf(w, "Repo:    %s  (%s)\n", r.Repo.Prefix, r.Repo.Name)
		fmt.Fprintf(w, "Path:    %s\n", r.Repo.Path)
		if r.Repo.RemoteURL != "" {
			fmt.Fprintf(w, "Remote:  %s\n", r.Repo.RemoteURL)
		}
		fmt.Fprintf(w, "DB:      %s\n\n", r.DBPath)

		fmt.Fprintf(w, "Features: %d\n", r.Stats.Features)
		fmt.Fprintf(w, "Issues:   %d\n", r.Stats.Issues)
		for _, st := range model.AllStates() {
			if n := r.Stats.IssuesByState[string(st)]; n > 0 {
				fmt.Fprintf(w, "  %-12s %d\n", string(st)+":", n)
			}
		}
		fmt.Fprintf(w, "Next:    %s\n", r.Stats.NextIssueKey)
		return nil
	}

	fmt.Fprintf(w, "DB:      %s\n", r.DBPath)
	fmt.Fprintf(w, "Repos:   %d\n", r.Stats.TrackedRepos)
	fmt.Fprintf(w, "Issues:  %d (across all repos)\n\n", r.Stats.TotalIssues)
	fmt.Fprintln(w, "Not inside a git repo — cd into one and re-run.")
	return nil
}
