package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/mrgeoffrich/bacio/internal/agentmode"
	"github.com/mrgeoffrich/bacio/internal/cli/inputs"
	"github.com/mrgeoffrich/bacio/internal/client"
	"github.com/mrgeoffrich/bacio/internal/inputio"
	"github.com/mrgeoffrich/bacio/internal/model"
	"github.com/mrgeoffrich/bacio/internal/store"
	"github.com/mrgeoffrich/bacio/internal/sync"
)

func newRepoCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "repo", Short: "Inspect tracked repos"}
	cmd.AddCommand(repoListCmd(), repoShowCmd(), repoRmCmd(), repoLinkCmd())
	return cmd
}

func repoListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List all tracked repos",
		RunE: func(cmd *cobra.Command, args []string) error {
			if root, ok := resolveSyncRepoRoot(); ok {
				return listReposFromSyncRepo(root)
			}
			c, err := openClient()
			if err != nil {
				return err
			}
			defer c.Close()
			repos, err := c.ListRepos(context.Background())
			if err != nil {
				return err
			}
			return emit(repos)
		},
	}
}

// listReposFromSyncRepo reads index.yaml at the sync repo root and
// renders the entries through the same `[]*model.Repo` path the
// project-repo branch uses. We convert RepoIndexEntry → model.Repo
// (lossy: no ID, no Path, no NextIssueNumber) so existing JSON/text
// renderers keep working without a new shape. Falls back to a scan
// of `repos/*/repo.yaml` for older sync repos that pre-date index.yaml.
func listReposFromSyncRepo(syncRoot string) error {
	idx, err := sync.ReadIndex(syncRoot)
	if err != nil && !errors.Is(err, sync.ErrNoIndex) {
		return err
	}
	repos := []*model.Repo{}
	if idx != nil {
		for _, e := range idx.Repos {
			repos = append(repos, &model.Repo{
				UUID:      e.UUID,
				Prefix:    e.Prefix,
				Name:      e.Name,
				RemoteURL: e.Remote,
			})
		}
		return emit(repos)
	}
	// Fallback: scan repos/*/repo.yaml when no index.yaml is present
	// (sync repos created before this file existed).
	entries, err := os.ReadDir(filepath.Join(syncRoot, "repos"))
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		b, err := os.ReadFile(filepath.Join(syncRoot, "repos", entry.Name(), "repo.yaml"))
		if err != nil {
			continue
		}
		parsed, err := sync.ParseRepoYAML(b)
		if err != nil {
			continue
		}
		repos = append(repos, &model.Repo{
			UUID:      parsed.UUID,
			Prefix:    parsed.Prefix,
			Name:      parsed.Name,
			RemoteURL: parsed.RemoteURL,
		})
	}
	return emit(repos)
}

func repoShowCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "show [PREFIX]",
		Short: "Show details for a repo (defaults to current directory)",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := openClient()
			if err != nil {
				return err
			}
			defer c.Close()
			if len(args) == 1 {
				repo, err := c.GetRepoByPrefix(context.Background(), strings.ToUpper(args[0]))
				if err != nil {
					return err
				}
				return emit(repo)
			}
			repo, err := resolveRepoC(c)
			if err != nil {
				return err
			}
			return emit(repo)
		},
	}
}

const repoRmLongHelp = `Delete a repo and everything that belongs to it.

DESTRUCTIVE & IRREVERSIBLE. Cascades through every issue, comment,
feature, document, document link, document folder, Kanban lane,
relation, PR attachment, tag, TUI setting, repo setting, agent session,
agent dispatch, agent channel, parked user message, notification and
history row attached to the repo. There is no undo.

Requires --confirm <prefix> (the value must equal the target prefix).
Without it, this command prints an impact preview and exits non-zero so
that an AI agent driving bacio MUST stop and ask the user before re-running
with --confirm. Always rehearse with --dry-run first to inspect the
cascade.`

func repoRmCmd() *cobra.Command {
	var (
		rawInput string
		confirm  string
	)
	cmd := &cobra.Command{
		Use:   "rm [PREFIX]",
		Short: "Delete a repo (and all its issues/features/docs/history) — requires --confirm <prefix>",
		Long:  repoRmLongHelp,
		Args:  cobra.RangeArgs(0, 1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := agentmode.DenyIfEnabled("repo rm"); err != nil {
				return err
			}
			raw, err := parseJSONInput(cmd, args, rawInput, "confirm")
			if err != nil {
				return err
			}
			var prefix string
			switch {
			case raw != nil:
				in, _, err := inputio.DecodeStrict[inputs.RepoRmInput](raw)
				if err != nil {
					return err
				}
				if strings.TrimSpace(in.Prefix) == "" {
					return fmt.Errorf("prefix is required")
				}
				prefix = in.Prefix
				confirm = in.Confirm
			case len(args) == 1:
				prefix = args[0]
			default:
				return fmt.Errorf("requires <PREFIX> positional or --json")
			}
			return removeRepo(prefix, confirm)
		},
	}
	addInputFlag(cmd, &rawInput)
	cmd.Flags().StringVar(&confirm, "confirm", "",
		"required token; must equal the target repo's prefix (case-insensitive) to actually delete")
	return cmd
}

func removeRepo(prefix, confirm string) error {
	c, err := openClient()
	if err != nil {
		return err
	}
	defer c.Close()
	// Shared with `bacio workspace rm` — same client call, same confirm
	// gate, same dry-run projection. See deleteRepoRow in workspace.go.
	return deleteRepoRow(c, prefix, confirm)
}

// formatRepoConfirmError renders the LLM-targeted alert. In JSON mode
// we emit the structured preview-plus-error envelope on stdout and
// return a terse error to drive the non-zero exit. In text mode we
// print the loud "STOP" block and return the same terse error.
func formatRepoConfirmError(e *client.RepoConfirmError) error {
	if e.Preview == nil {
		return fmt.Errorf("%s", e.Error())
	}
	if opts.output == outputJSON {
		envelope := struct {
			Error            string             `json:"error"`
			Message          string             `json:"message"`
			Repo             any                `json:"repo"`
			Cascade          any                `json:"cascade"`
			Irreversible     bool               `json:"irreversible"`
			ConfirmationHint string             `json:"confirmation_hint"`
		}{
			Error:            "confirm_required",
			Message:          e.Error(),
			Repo:             e.Preview.Repo,
			Cascade:          e.Preview.Cascade,
			Irreversible:     true,
			ConfirmationHint: "re-run with --confirm " + e.Prefix,
		}
		_ = emit(envelope)
		return fmt.Errorf("aborted: %s", e.Error())
	}
	repo := e.Preview.Repo
	verb := "repo rm"
	noun := "repo"
	if repo.IsWorkspace() {
		verb = "workspace rm"
		noun = "workspace"
	}
	fmt.Fprintln(os.Stderr)
	fmt.Fprintln(os.Stderr, "⚠️  STOP — DESTRUCTIVE OPERATION REQUIRES HUMAN APPROVAL")
	fmt.Fprintln(os.Stderr)
	fmt.Fprintf(os.Stderr, "`bacio %s %s` will permanently delete %s %s (%s) and:\n",
		verb, repo.Prefix, noun, repo.Prefix, repo.Name)
	for _, line := range repoCascadeBullets(e.Preview.Cascade) {
		fmt.Fprintf(os.Stderr, "  • %s\n", line)
	}
	fmt.Fprintln(os.Stderr)
	fmt.Fprintln(os.Stderr, "This is IRREVERSIBLE. There is no undo, no trash, no recovery.")
	fmt.Fprintln(os.Stderr)
	fmt.Fprintln(os.Stderr, "If you are an AI agent: do NOT proceed without explicit human")
	fmt.Fprintln(os.Stderr, "approval. Show this preview to the user, get a clear")
	fmt.Fprintf(os.Stderr, "\"yes, delete %s\" in their own words, then re-run with:\n", repo.Prefix)
	fmt.Fprintln(os.Stderr)
	fmt.Fprintf(os.Stderr, "  bacio %s %s --confirm %s\n", verb, repo.Prefix, repo.Prefix)
	fmt.Fprintln(os.Stderr)
	return fmt.Errorf("aborted: %s", e.Error())
}

// repoCascadeBullets renders EVERY field of store.RepoCascadeCounts as a
// "<n> <label>" line, in the struct's declaration order.
//
// It is a hand-written list rather than reflection so the labels read
// like English, but it is exhaustive on purpose: the human text path
// used to name ten of the counts while the JSON / --dry-run payload
// marshalled the whole struct, so the loudest, most destructive surface
// bacio has was the one under-reporting its own blast radius. Anything
// added to RepoCascadeCounts must be added here too — the compiler will
// not tell you, but the counts test will.
func repoCascadeBullets(c store.RepoCascadeCounts) []string {
	return []string{
		fmt.Sprintf("%d issues", c.Issues),
		fmt.Sprintf("%d comments", c.Comments),
		fmt.Sprintf("%d issue relations", c.Relations),
		fmt.Sprintf("%d PR attachments", c.PullRequests),
		fmt.Sprintf("%d tags", c.Tags),
		fmt.Sprintf("%d features", c.Features),
		fmt.Sprintf("%d documents", c.Documents),
		fmt.Sprintf("%d document links", c.DocumentLinks),
		fmt.Sprintf("%d document folders", c.DocFolders),
		fmt.Sprintf("%d Kanban lanes", c.KanbanColumns),
		fmt.Sprintf("%d TUI settings", c.TUISettings),
		fmt.Sprintf("%d repo settings", c.RepoSettings),
		fmt.Sprintf("%d agent sessions", c.AgentSessions),
		fmt.Sprintf("%d agent dispatches", c.AgentDispatches),
		fmt.Sprintf("%d agent channels", c.AgentChannels),
		fmt.Sprintf("%d parked user messages", c.UserMessages),
		fmt.Sprintf("%d notifications", c.Notifications),
		fmt.Sprintf("%d history rows", c.History),
	}
}

// repoDeletePreview is the text/JSON shape emitted for `--dry-run`.
// Lives in the cli package so the JSON tags match what `bacio` has always
// printed for *.rm previews (lowercase, snake_case).
type repoDeletePreview struct {
	Repo        any  `json:"repo"`
	Cascade     any  `json:"cascade"`
	WouldDelete bool `json:"would_delete"`
}

func toRepoDeletePreview(p *client.RepoDeletePreview) *repoDeletePreview {
	if p == nil {
		return nil
	}
	return &repoDeletePreview{
		Repo:        p.Repo,
		Cascade:     p.Cascade,
		WouldDelete: p.WouldDelete,
	}
}

const repoLinkLongHelp = `Bind a phantom repo (a sync_clone-imported row with no local path) to a
local working tree.

A phantom repo is created by ` + "`bacio sync clone`" + ` when it imports a
` + "`repos/<prefix>/`" + ` folder for which this machine has no local DB row.
This verb makes the link explicit — surfaces the same path that today
fires implicitly when bacio happens to run from inside the matching
checkout (cli/context.go).

The path must be an absolute path to an existing git working tree.
After linking, the project's ` + "`.bacio/config.yaml`" + ` is written
pointing at the sync repo's remote URL — the path bacio sync uses on
every subsequent run from this checkout.

Idempotent: re-linking to the same path is a no-op (no audit row, no
config rewrite). If the path is already bound to another repo, the call
errors loudly.

In remote mode (--remote), the path is on the server running ` + "`bacio api`" + `,
NOT on this machine.`

func repoLinkCmd() *cobra.Command {
	var rawInput string
	cmd := &cobra.Command{
		Use:   "link [PREFIX] [PATH]",
		Short: "Bind a phantom repo to a local working tree (writes .bacio/config.yaml)",
		Long:  repoLinkLongHelp,
		Args:  cobra.RangeArgs(0, 2),
		RunE: func(cmd *cobra.Command, args []string) error {
			raw, err := parseJSONInput(cmd, args, rawInput)
			if err != nil {
				return err
			}
			var prefix, path string
			switch {
			case raw != nil:
				in, _, err := inputio.DecodeStrict[inputs.RepoLinkInput](raw)
				if err != nil {
					return err
				}
				if strings.TrimSpace(in.Prefix) == "" {
					return fmt.Errorf("prefix is required")
				}
				if strings.TrimSpace(in.Path) == "" {
					return fmt.Errorf("path is required")
				}
				prefix = in.Prefix
				path = in.Path
			case len(args) == 2:
				prefix = args[0]
				path = args[1]
			default:
				return fmt.Errorf("requires <PREFIX> <PATH> positionals or --json")
			}
			// Expand `.` (and relative paths in general) to an absolute
			// path BEFORE openClient(). openClient() detects the current
			// git repo and may trigger the implicit cwd-driven phantom
			// upgrade in resolveRepo — if we passed `.` straight through,
			// the explicit-link call would race with the implicit one and
			// the row would already be upgraded by the time we look it
			// up. Resolving here keeps the verb deterministic.
			abs, err := filepath.Abs(path)
			if err != nil {
				return fmt.Errorf("resolve path %q: %w", path, err)
			}
			path = abs
			return linkPhantomRepo(prefix, path)
		},
	}
	addInputFlag(cmd, &rawInput)
	return cmd
}

func linkPhantomRepo(prefix, path string) error {
	c, err := openClient()
	if err != nil {
		return err
	}
	defer c.Close()
	result, err := c.LinkPhantomRepo(context.Background(), prefix, path, opts.dryRun)
	if err != nil {
		var linkErr *client.RepoLinkError
		if errors.As(err, &linkErr) {
			// Render a verb-tailored hint per kind so the user sees the
			// concrete next step, not just the generic error string.
			return formatRepoLinkError(linkErr)
		}
		return err
	}
	if opts.dryRun {
		return emitDryRun(result)
	}
	if result.AlreadyLinked {
		return ok("repo %s already linked to %s (no changes)", result.Repo.Prefix, result.Repo.Path)
	}
	return ok("repo %s linked to %s (sync remote: %s)",
		result.Repo.Prefix, result.Repo.Path, result.SyncRemoteURL)
}

// formatRepoLinkError renders a per-kind text alert so the user / agent
// sees the right next step. Mirrors the kind→hint mapping the HTTP
// handler uses; the JSON-mode path emits the typed envelope so callers
// can structurally branch.
func formatRepoLinkError(e *client.RepoLinkError) error {
	if opts.output == outputJSON {
		envelope := struct {
			Error          string `json:"error"`
			Kind           string `json:"kind"`
			Prefix         string `json:"prefix,omitempty"`
			Path           string `json:"path,omitempty"`
			CurrentPath    string `json:"current_path,omitempty"`
			ExistingPrefix string `json:"existing_prefix,omitempty"`
			Message        string `json:"message"`
		}{
			Error:          "repo_link_refused",
			Kind:           e.Kind,
			Prefix:         e.Prefix,
			Path:           e.Path,
			CurrentPath:    e.CurrentPath,
			ExistingPrefix: e.ExistingPrefix,
			Message:        e.Error(),
		}
		_ = emit(envelope)
		return fmt.Errorf("aborted: %s", e.Error())
	}
	return fmt.Errorf("%s", e.Error())
}
