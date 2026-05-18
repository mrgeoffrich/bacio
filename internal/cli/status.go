package cli

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/mrgeoffrich/bacio/internal/agentmode"
	"github.com/mrgeoffrich/bacio/internal/git"
	"github.com/mrgeoffrich/bacio/internal/model"
	"github.com/mrgeoffrich/bacio/internal/store"
	"github.com/mrgeoffrich/bacio/internal/wtenv"
)

// statusReport is the unified shape returned by `bacio status`. `status` is
// strictly read-only: inside a git tree it reports whether the repo is
// registered without ever writing a row. Branches: registered repo,
// unregistered git tree, or no git tree.
type statusReport struct {
	DBPath     string            `json:"db_path"`
	APIAddr    string            `json:"api_addr"`
	EnvSource  string            `json:"env_source"`             // "flag" | "env" | "worktree" | "default"
	EnvPath    string            `json:"env_path,omitempty"`     // populated when a manifest fed resolution
	InRepo     bool              `json:"in_repo"`
	Registered bool              `json:"registered"`
	Path       string            `json:"path,omitempty"`
	Repo       *model.Repo       `json:"repo,omitempty"`
	Stats      statusStats       `json:"stats"`
	AgentMode  statusAgentMode   `json:"agent_mode"`

	// LLMRecommendations carries plain-English, actionable advice for
	// an agent reading `bacio status -o json`: setup problems bacio
	// noticed and a concrete fix for each. Empty (and omitted) when
	// there's nothing to flag. Strictly advisory — `status` never acts
	// on them itself and stays read-only.
	LLMRecommendations []string `json:"llm_recommendations,omitempty"`
}

// statusAgentMode reports the BACIO_AGENT_MODE env-var state: the raw
// value (so a misset like "TRUE" or "yes" is visible) and the parsed
// active flag. The hooks + channel poller activate only when Active is
// true. Surfaced on `bacio status` so an agent debugging "why isn't a
// dispatch reaching me?" can see the gate without grepping env.
type statusAgentMode struct {
	EnvVar string `json:"env_var"`
	Value  string `json:"value"`
	Active bool   `json:"active"`
}

type statusStats struct {
	// Repo-scoped (populated when InRepo and Registered)
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
		Short: "Show current repo, DB location, and quick stats (read-only)",
		RunE: func(cmd *cobra.Command, args []string) error {
			res, err := resolveEnv()
			if err != nil {
				return err
			}
			s, err := store.Open(res.DBPath)
			if err != nil {
				return err
			}
			defer s.Close()

			cwd, err := os.Getwd()
			if err != nil {
				return err
			}
			report, err := buildStatusReport(s, res, cwd)
			if err != nil {
				return err
			}
			if opts.output == outputJSON {
				return emit(report)
			}
			return printStatus(os.Stdout, report)
		},
	}
}

// buildStatusReport assembles the status payload without writing to the
// store. Kept as a package-level helper so tests can drive it directly
// after chdir-ing into a temp git tree.
func buildStatusReport(s *store.Store, env wtenv.Resolved, cwd string) (*statusReport, error) {
	report := &statusReport{
		DBPath:    env.DBPath,
		APIAddr:   env.APIAddr,
		EnvSource: string(env.Source),
		EnvPath:   env.ManifestPath,
		AgentMode: statusAgentMode{
			EnvVar: agentmode.EnvVar,
			Value:  os.Getenv(agentmode.EnvVar),
			Active: agentmode.Enabled(),
		},
	}
	info, gitErr := git.Detect(cwd)
	switch {
	case gitErr == nil:
		report.InRepo = true
		report.Path = info.Root
		report.LLMRecommendations = statusRecommendations(info.Root)
		repo, err := s.GetRepoByPath(info.Root)
		if errors.Is(err, store.ErrNotFound) {
			return report, nil
		}
		if err != nil {
			return nil, err
		}
		report.Registered = true
		report.Repo = repo
		if err := fillRepoStats(s, repo, &report.Stats); err != nil {
			return nil, err
		}
	case errors.Is(gitErr, git.ErrNotARepo):
		if err := fillGlobalStats(s, &report.Stats); err != nil {
			return nil, err
		}
	default:
		return nil, gitErr
	}
	return report, nil
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

// statusRecommendations inspects the current repo for bacio setup
// problems and returns an actionable hint for each. Strictly
// read-only: it stats files and asks git about ignore rules but never
// writes anything (BACI-6). Add new checks here as a single appended
// string per finding — the consumer is an LLM reading JSON.
func statusRecommendations(repoRoot string) []string {
	var recs []string

	// .bacio/ holds machine-local state (sync config, agent identity)
	// and must never be committed. Flag it if it exists but git isn't
	// ignoring it.
	if _, err := os.Stat(filepath.Join(repoRoot, ".bacio")); err == nil {
		if ignored, err := git.IsIgnored(repoRoot, ".bacio"); err == nil && !ignored {
			recs = append(recs, "The .bacio/ directory is not gitignored. It holds machine-local state (sync config, agent identity) and must not be committed. Add `.bacio/` to this repo's .gitignore; if it is already tracked, also run `git rm --cached -r .bacio` to untrack it.")
		}
	}

	return recs
}

func printStatus(w io.Writer, r *statusReport) error {
	switch {
	case r.InRepo && r.Registered && r.Repo != nil:
		fmt.Fprintf(w, "Repo:    %s  (%s)\n", r.Repo.Prefix, r.Repo.Name)
		fmt.Fprintf(w, "Path:    %s\n", r.Repo.Path)
		if r.Repo.RemoteURL != "" {
			fmt.Fprintf(w, "Remote:  %s\n", r.Repo.RemoteURL)
		}
		fmt.Fprintf(w, "DB:      %s\n", r.DBPath)
		fmt.Fprintf(w, "API:     %s\n", r.APIAddr)
		fmt.Fprintf(w, "Env:     %s\n", formatEnvSource(r))
		fmt.Fprintf(w, "%s\n\n", formatAgentMode(r.AgentMode))

		fmt.Fprintf(w, "Features: %d\n", r.Stats.Features)
		fmt.Fprintf(w, "Issues:   %d\n", r.Stats.Issues)
		for _, st := range model.AllStates() {
			if n := r.Stats.IssuesByState[string(st)]; n > 0 {
				fmt.Fprintf(w, "  %-12s %d\n", string(st)+":", n)
			}
		}
		fmt.Fprintf(w, "Next:    %s\n", r.Stats.NextIssueKey)

	case r.InRepo:
		fmt.Fprintf(w, "Path:    %s\n", r.Path)
		fmt.Fprintf(w, "Repo:    (unregistered — run `bacio init` to bind a prefix)\n")
		fmt.Fprintf(w, "DB:      %s\n", r.DBPath)
		fmt.Fprintf(w, "API:     %s\n", r.APIAddr)
		fmt.Fprintf(w, "Env:     %s\n", formatEnvSource(r))
		fmt.Fprintf(w, "%s\n", formatAgentMode(r.AgentMode))

	default:
		fmt.Fprintf(w, "DB:      %s\n", r.DBPath)
		fmt.Fprintf(w, "API:     %s\n", r.APIAddr)
		fmt.Fprintf(w, "Env:     %s\n", formatEnvSource(r))
		fmt.Fprintf(w, "%s\n", formatAgentMode(r.AgentMode))
		fmt.Fprintf(w, "Repos:   %d\n", r.Stats.TrackedRepos)
		fmt.Fprintf(w, "Issues:  %d (across all repos)\n\n", r.Stats.TotalIssues)
		fmt.Fprintln(w, "Not inside a git repo — cd into one and re-run.")
	}

	for _, rec := range r.LLMRecommendations {
		fmt.Fprintf(w, "\nRecommendation: %s\n", rec)
	}
	return nil
}

// formatEnvSource renders the env-source row. Tells the user whether
// they're on the legacy default DB/port or a manifest-driven worktree
// allocation, and where the manifest lives if so.
func formatEnvSource(r *statusReport) string {
	switch r.EnvSource {
	case string(wtenv.SourceFlag):
		if r.EnvPath != "" {
			return fmt.Sprintf("flag (manifest at %s)", r.EnvPath)
		}
		return "flag (--db / --addr)"
	case string(wtenv.SourceEnv):
		return fmt.Sprintf("%s (%s)", wtenv.EnvVar, r.EnvPath)
	case string(wtenv.SourceWorktree):
		return fmt.Sprintf("worktree manifest (%s)", r.EnvPath)
	case string(wtenv.SourceDefault), "":
		return "default (legacy ~/.bacio/db.sqlite + 127.0.0.1:5320)"
	default:
		return r.EnvSource
	}
}

// formatAgentMode renders the BACIO_AGENT_MODE row aligned with the
// other Repo: / Path: / DB: lines. The trailing tag (active / inert)
// reads as "is this gate doing anything right now?".
func formatAgentMode(m statusAgentMode) string {
	if m.Active {
		return fmt.Sprintf("Agent:   %s=%s (active)", m.EnvVar, m.Value)
	}
	if m.Value == "" {
		return fmt.Sprintf("Agent:   %s unset (hooks + channel inert)", m.EnvVar)
	}
	return fmt.Sprintf("Agent:   %s=%s (inert — only \"1\" or \"true\" activate)", m.EnvVar, m.Value)
}
