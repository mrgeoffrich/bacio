package cli

import (
	"context"
	"encoding/json"
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

func newIssueCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:               "issue",
		Short:             "Manage issues",
		PersistentPreRunE: requireClaimForGroup, // BACI-126b
	}
	cmd.AddCommand(
		issueAddCmd(),
		issueListCmd(),
		issueShowCmd(),
		issueBriefCmd(),
		issueEditCmd(),
		issueStateCmd(),
		issueAssignCmd(),
		issueUnassignCmd(),
		issueNextCmd(),
		issuePeekCmd(),
		issueRmCmd(),
		issueArchiveCmd(),
		issueUnarchiveCmd(),
		issueReorderCmd(),
		issueProcessCmd(),
		issueShipCmd(),
		issueMarkDoneCmd(),
		issueAutoShipCmd(),
	)
	return cmd
}

func issueAddCmd() *cobra.Command {
	var (
		featureSlug, description, descriptionFile, stateStr, baseBranch, customerImpact string
		tags                                                                            []string
		autoRun                                                                         bool
		rawInput                                                                        string
	)
	cmd := &cobra.Command{
		Use:   "add [title]",
		Short: "Create an issue in the current repo",
		Args:  cobra.RangeArgs(0, 1),
		RunE: func(cmd *cobra.Command, args []string) error {
			raw, err := parseJSONInput(cmd, args, rawInput,
				"feature", "description", "description-file", "state", "tag", "base-branch", "customer-impact", "auto-run")
			if err != nil {
				return err
			}
			if raw != nil {
				return runIssueAddJSON(raw)
			}
			if len(args) != 1 {
				return fmt.Errorf("requires <title> positional or --json")
			}
			return runIssueAdd(args[0], featureSlug, description, descriptionFile, stateStr, tags, baseBranch, customerImpact, autoRun)
		},
	}
	cmd.Flags().StringVarP(&featureSlug, "feature", "f", "", "feature slug to attach to")
	cmd.Flags().StringVar(&description, "description", "", "description text or '-' for stdin")
	cmd.Flags().StringVar(&descriptionFile, "description-file", "", "path to a markdown file")
	cmd.Flags().StringVar(&stateStr, "state", "", "initial state (default: todo)")
	cmd.Flags().StringSliceVar(&tags, "tag", nil, "tag to attach (repeatable)")
	cmd.Flags().StringVar(&baseBranch, "base-branch", "", "per-issue PR base-branch override (BACI-232; empty inherits from the feature, ultimately main)")
	cmd.Flags().StringVar(&customerImpact, "customer-impact", "", "optional one-line customer impact in the user's terms (BACI-349; blank = no user-facing change, read surfaces fall back to the title)")
	cmd.Flags().BoolVar(&autoRun, "auto-run", false, "start the new issue running the full Scope → Plan → Implement → Ship chain immediately (BACI-374; off by default here, the UI composers default it on)")
	addInputFlag(cmd, &rawInput)
	return cmd
}

func runIssueAdd(title, featureSlug, description, descriptionFile, stateStr string, tags []string, baseBranch, customerImpact string, autoRun bool) error {
	desc, err := readLongText(description, descriptionFile, false, "description")
	if err != nil {
		return err
	}
	return createIssue(inputs.IssueAddInput{
		Title:          title,
		FeatureSlug:    featureSlug,
		Description:    desc,
		State:          stateStr,
		Tags:           tags,
		BaseBranch:     baseBranch,
		CustomerImpact: customerImpact,
		AutoRun:        autoRun,
	})
}

func runIssueAddJSON(raw []byte) error {
	in, _, err := inputio.DecodeStrict[inputs.IssueAddInput](raw)
	if err != nil {
		return err
	}
	return createIssue(*in)
}

func createIssue(in inputs.IssueAddInput) error {
	c, err := openClient()
	if err != nil {
		return err
	}
	defer c.Close()
	repo, err := resolveRepoC(c)
	if err != nil {
		return err
	}
	iss, err := c.CreateIssue(context.Background(), repo, in, opts.dryRun)
	if err != nil {
		return err
	}
	if opts.dryRun {
		return emitDryRun(iss)
	}
	return emit(iss)
}

func issueListCmd() *cobra.Command {
	var (
		stateCSV        string
		featureSlug     string
		tags            []string
		allRepos        bool
		withDescription bool
		includeArchived bool
	)
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List issues (descriptions are stripped by default; pass --with-description to include them)",
		RunE: func(cmd *cobra.Command, args []string) error {
			// --repo is the global selector now (see repoSelector). It
			// used to be a local flag here that was ONLY read on the
			// sync-repo branch and silently ignored everywhere else;
			// folding it into the global one keeps the same spelling,
			// makes it work outside a sync repo, and is the only way to
			// list a workspace's issues (no cwd, no git.Detect).
			if root, ok := resolveSyncRepoRoot(); ok {
				return listIssuesFromSyncRepo(root, repoSelector(), allRepos, stateCSV, featureSlug, tags, withDescription)
			}
			c, err := openClient()
			if err != nil {
				return err
			}
			defer c.Close()
			f := client.IssueFilter{AllRepos: allRepos, IncludeDescription: withDescription, FeatureSlug: featureSlug}
			if !allRepos {
				repo, err := resolveRepoC(c)
				if err != nil {
					return err
				}
				f.Repo = repo
			}
			if stateCSV != "" {
				for _, raw := range strings.Split(stateCSV, ",") {
					st, err := model.ParseState(raw)
					if err != nil {
						return err
					}
					f.States = append(f.States, st)
				}
			}
			if len(tags) > 0 {
				cleanTags, err := store.NormalizeTags(tags)
				if err != nil {
					return err
				}
				f.Tags = cleanTags
			}
			// BACI-68: archived rows hidden by default. Per-call
			// --include-archived overrides; the display.show_archived
			// setting also lifts the filter when on.
			f.IncludeArchived = includeArchived
			if !f.IncludeArchived {
				show, _ := c.GetDisplayShowArchived(context.Background())
				f.IncludeArchived = show
			}
			issues, err := c.ListIssues(context.Background(), f)
			if err != nil {
				return err
			}
			return emit(issues)
		},
	}
	cmd.Flags().StringVar(&stateCSV, "state", "", "comma-separated states to filter (e.g. todo,in_review)")
	cmd.Flags().StringVarP(&featureSlug, "feature", "f", "", "limit to a feature")
	cmd.Flags().StringSliceVar(&tags, "tag", nil, "require this tag (repeatable; AND semantics)")
	cmd.Flags().BoolVar(&allRepos, "all-repos", false, "search across all tracked repos")
	cmd.Flags().BoolVar(&withDescription, "with-description", false, "include each issue's full description in JSON output (off by default to keep responses small)")
	cmd.Flags().BoolVar(&includeArchived, "include-archived", false, "include archived issues in the list (BACI-68); overrides the display.show_archived setting for this call")
	return cmd
}

// listIssuesFromSyncRepo serves `bacio issue list` when run inside a
// sync repo. Reads issue.yaml + description.md off disk (no DB) and
// pipes results through the existing []*model.Issue renderer.
func listIssuesFromSyncRepo(syncRoot, repoPrefix string, allRepos bool, stateCSV, featureSlug string, tags []string, withDescription bool) error {
	prefixes, err := resolveSyncRepoListPrefixes(syncRoot, repoPrefix, allRepos)
	if err != nil {
		return err
	}

	stateFilter := map[model.State]struct{}{}
	if stateCSV != "" {
		for _, raw := range strings.Split(stateCSV, ",") {
			st, err := model.ParseState(raw)
			if err != nil {
				return err
			}
			stateFilter[st] = struct{}{}
		}
	}
	tagFilter, err := store.NormalizeTags(tags)
	if err != nil {
		return err
	}

	var issues []*model.Issue
	for _, prefix := range prefixes {
		parsed, err := sync.ListIssuesOnDisk(syncRoot, prefix)
		if err != nil {
			return err
		}
		for _, p := range parsed {
			state, err := model.ParseState(p.State)
			if err != nil {
				return fmt.Errorf("%s-%d: %w", prefix, p.Number, err)
			}
			if len(stateFilter) > 0 {
				if _, ok := stateFilter[state]; !ok {
					continue
				}
			}
			if featureSlug != "" {
				if p.Feature == nil || p.Feature.Label != featureSlug {
					continue
				}
			}
			if len(tagFilter) > 0 && !containsAllTags(p.Tags, tagFilter) {
				continue
			}
			iss := &model.Issue{
				UUID:      p.UUID,
				Number:    p.Number,
				Key:       fmt.Sprintf("%s-%d", prefix, p.Number),
				Title:     p.Title,
				State:     state,
				Assignee:  p.Assignee,
				Tags:      p.Tags,
				CreatedAt: p.CreatedAt,
				UpdatedAt: p.UpdatedAt,
			}
			if p.Feature != nil {
				iss.FeatureSlug = p.Feature.Label
			}
			if withDescription {
				body, err := sync.ReadIssueDescription(syncRoot, prefix, iss.Key)
				if err != nil {
					return err
				}
				iss.Description = body
			}
			issues = append(issues, iss)
		}
	}
	return emit(issues)
}

// containsAllTags reports whether `haystack` contains every tag in
// `needles` (AND semantics, matching the local-DB filter).
func containsAllTags(haystack, needles []string) bool {
	have := make(map[string]struct{}, len(haystack))
	for _, t := range haystack {
		have[t] = struct{}{}
	}
	for _, n := range needles {
		if _, ok := have[n]; !ok {
			return false
		}
	}
	return true
}

// resolveSyncRepoListPrefixes returns the prefixes a sync-repo-mode
// list command should iterate. `--all-repos` reads every prefix from
// index.yaml (or, on older sync repos, the repos/ folder); a
// `--repo <PREFIX>` flag pins to a single one. Without either, the
// caller gets a friendly error pointing at the right flag plus the
// available prefixes.
func resolveSyncRepoListPrefixes(syncRoot, repoPrefix string, allRepos bool) ([]string, error) {
	if repoPrefix != "" {
		return []string{strings.ToUpper(repoPrefix)}, nil
	}
	available, err := syncRepoPrefixes(syncRoot)
	if err != nil {
		return nil, err
	}
	if allRepos {
		return available, nil
	}
	hint := strings.Join(available, ", ")
	if hint == "" {
		hint = "(none yet)"
	}
	return nil, fmt.Errorf("inside a sync repo: pass --repo <PREFIX> (one of: %s) or --all-repos", hint)
}

// syncRepoPrefixes reads index.yaml when present (the fast path) and
// falls back to scanning repos/ for older sync repos.
func syncRepoPrefixes(syncRoot string) ([]string, error) {
	idx, err := sync.ReadIndex(syncRoot)
	if err != nil && !errors.Is(err, sync.ErrNoIndex) {
		return nil, err
	}
	if idx != nil {
		out := make([]string, 0, len(idx.Repos))
		for _, e := range idx.Repos {
			out = append(out, e.Prefix)
		}
		return out, nil
	}
	entries, err := os.ReadDir(filepath.Join(syncRoot, "repos"))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	out := make([]string, 0, len(entries))
	for _, e := range entries {
		if e.IsDir() {
			out = append(out, e.Name())
		}
	}
	return out, nil
}

func issueShowCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "show <KEY>",
		Short: "Show an issue with comments and relations",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := openClient()
			if err != nil {
				return err
			}
			defer c.Close()
			repo, err := repoForIssueKey(c, args[0])
			if err != nil {
				return err
			}
			view, err := retryWithRedirect(repo, "issue", args[0],
				func(key string) (*client.IssueView, error) {
					return c.ShowIssue(context.Background(), repo, key)
				})
			if err != nil {
				return err
			}
			return emit(&issueView{
				Issue: view.Issue, Comments: view.Comments, Relations: view.Relations,
				PullRequests: view.PullRequests, Documents: view.Documents,
				Claimants: view.Claimants, Taken: view.Taken,
				LatestPlan: view.LatestPlan,
			})
		},
	}
}

// repoForIssueKey returns the repo for a key. For canonical PREFIX-N,
// resolves by prefix; for bare numbers, uses CWD's repo.
func repoForIssueKey(c client.Client, key string) (*model.Repo, error) {
	key = strings.TrimSpace(key)
	if strings.Contains(key, "-") {
		prefix, _, err := store.ParseIssueKey(key)
		if err != nil {
			return nil, err
		}
		return c.GetRepoByPrefix(context.Background(), prefix)
	}
	return resolveRepoC(c)
}

func issueEditCmd() *cobra.Command {
	var (
		title, description, descriptionFile, featureSlug, baseBranch, customerImpact string
		clearFeature                                                                 bool
		rawInput                                                                     string
	)
	cmd := &cobra.Command{
		Use:   "edit [KEY]",
		Short: "Edit an issue's title, description, feature, base branch, or customer impact",
		Args:  cobra.RangeArgs(0, 1),
		RunE: func(cmd *cobra.Command, args []string) error {
			raw, err := parseJSONInput(cmd, args, rawInput,
				"title", "description", "description-file", "feature", "no-feature", "base-branch", "customer-impact")
			if err != nil {
				return err
			}
			if raw != nil {
				return runIssueEditJSON(raw)
			}
			if len(args) != 1 {
				return fmt.Errorf("requires <KEY> positional or --json")
			}
			return runIssueEdit(cmd, args[0], title, description, descriptionFile, featureSlug, clearFeature, baseBranch, customerImpact)
		},
	}
	cmd.Flags().StringVar(&title, "title", "", "new title")
	cmd.Flags().StringVar(&description, "description", "", "new description text or '-' for stdin")
	cmd.Flags().StringVar(&descriptionFile, "description-file", "", "path to a markdown file")
	cmd.Flags().StringVarP(&featureSlug, "feature", "f", "", "move to a feature slug")
	cmd.Flags().BoolVar(&clearFeature, "no-feature", false, "detach from any feature")
	cmd.Flags().StringVar(&baseBranch, "base-branch", "", "new per-issue PR base-branch override (BACI-232; pass --base-branch '' to clear back to inherit-from-feature)")
	cmd.Flags().StringVar(&customerImpact, "customer-impact", "", "new one-line customer impact (BACI-349; pass --customer-impact '' to clear back to no-impact)")
	addInputFlag(cmd, &rawInput)
	return cmd
}

func runIssueEdit(cmd *cobra.Command, key, title, description, descriptionFile, featureSlug string, clearFeature bool, baseBranch, customerImpact string) error {
	c, err := openClient()
	if err != nil {
		return err
	}
	defer c.Close()
	repo, err := repoForIssueKey(c, key)
	if err != nil {
		return err
	}
	edit := client.IssueEdit{}
	if cmd.Flags().Changed("title") {
		edit.Title = &title
	}
	if description != "" || descriptionFile != "" {
		d, err := readLongText(description, descriptionFile, true, "description")
		if err != nil {
			return err
		}
		edit.Description = &d
	}
	if clearFeature && featureSlug != "" {
		return fmt.Errorf("--feature and --no-feature are mutually exclusive")
	}
	if clearFeature {
		var none *int64
		edit.FeatureID = &none
	} else if featureSlug != "" {
		feat, err := c.GetFeatureBySlug(context.Background(), repo, featureSlug)
		if err != nil {
			return fmt.Errorf("feature %q: %w", featureSlug, err)
		}
		p := &feat.ID
		edit.FeatureID = &p
		fs := featureSlug
		edit.FeatureSlug = &fs
	}
	if cmd.Flags().Changed("base-branch") {
		edit.BaseBranch = &baseBranch
	}
	if cmd.Flags().Changed("customer-impact") {
		edit.CustomerImpact = &customerImpact
	}
	return applyIssueEditC(c, repo, key, edit)
}

func runIssueEditJSON(raw []byte) error {
	in, present, err := inputio.DecodeStrict[inputs.IssueEditInput](raw)
	if err != nil {
		return err
	}
	if in.Key == "" {
		return fmt.Errorf("key is required")
	}
	c, err := openClient()
	if err != nil {
		return err
	}
	defer c.Close()
	repo, err := repoForIssueKey(c, in.Key)
	if err != nil {
		return err
	}
	edit := client.IssueEdit{}
	if _, ok := present["title"]; ok {
		if in.Title == nil || *in.Title == "" {
			return fmt.Errorf("title cannot be empty or null; omit the field to leave it unchanged")
		}
		edit.Title = in.Title
	}
	if _, ok := present["description"]; ok {
		if in.Description == nil {
			empty := ""
			edit.Description = &empty
		} else {
			edit.Description = in.Description
		}
	}
	if _, ok := present["feature_slug"]; ok {
		if in.FeatureSlug == nil || *in.FeatureSlug == "" {
			var none *int64
			edit.FeatureID = &none
		} else {
			feat, err := c.GetFeatureBySlug(context.Background(), repo, *in.FeatureSlug)
			if err != nil {
				return fmt.Errorf("feature %q: %w", *in.FeatureSlug, err)
			}
			p := &feat.ID
			edit.FeatureID = &p
			fs := *in.FeatureSlug
			edit.FeatureSlug = &fs
		}
	}
	// BACI-232: base_branch absent = no change; present (even null / "")
	// = clear back to NULL (inherit from feature).
	if _, ok := present["base_branch"]; ok {
		if in.BaseBranch == nil {
			empty := ""
			edit.BaseBranch = &empty
		} else {
			edit.BaseBranch = in.BaseBranch
		}
	}
	// BACI-349: customer_impact absent = no change; present (even null /
	// "") = clear back to the "no impact" state ("").
	if _, ok := present["customer_impact"]; ok {
		if in.CustomerImpact == nil {
			empty := ""
			edit.CustomerImpact = &empty
		} else {
			edit.CustomerImpact = in.CustomerImpact
		}
	}
	return applyIssueEditC(c, repo, in.Key, edit)
}

func applyIssueEditC(c client.Client, repo *model.Repo, key string, edit client.IssueEdit) error {
	if edit.Title == nil && edit.Description == nil && edit.FeatureID == nil && edit.BaseBranch == nil && edit.CustomerImpact == nil {
		return fmt.Errorf("nothing to update")
	}
	updated, err := c.UpdateIssue(context.Background(), repo, key, edit, opts.dryRun)
	if err != nil {
		return err
	}
	if opts.dryRun {
		return emitDryRun(updated)
	}
	return emit(updated)
}

func issueStateCmd() *cobra.Command {
	var rawInput string
	cmd := &cobra.Command{
		Use:   "state [KEY] [state]",
		Short: "Set issue state",
		Args:  cobra.RangeArgs(0, 2),
		RunE: func(cmd *cobra.Command, args []string) error {
			raw, err := parseJSONInput(cmd, args, rawInput)
			if err != nil {
				return err
			}
			if raw != nil {
				in, _, err := inputio.DecodeStrict[inputs.IssueStateInput](raw)
				if err != nil {
					return err
				}
				if in.Key == "" || in.State == "" {
					return fmt.Errorf("key and state are required")
				}
				return setIssueState(in.Key, in.State, true)
			}
			if len(args) != 2 {
				return fmt.Errorf("requires <KEY> <state> positionals or --json")
			}
			return setIssueState(args[0], args[1], false)
		},
	}
	addInputFlag(cmd, &rawInput)
	return cmd
}

func setIssueState(key, stateStr string, strict bool) error {
	st, err := model.ParseState(stateStr)
	if err != nil {
		return err
	}
	c, err := openClient()
	if err != nil {
		return err
	}
	defer c.Close()
	if strict {
		key = strings.TrimSpace(key)
		if !strings.Contains(key, "-") {
			return fmt.Errorf("issue key %q must be canonical (e.g. \"MINI-42\")", key)
		}
	}
	repo, err := repoForIssueKey(c, key)
	if err != nil {
		return err
	}
	updated, err := c.SetIssueState(context.Background(), repo, key, st, opts.dryRun)
	if err != nil {
		return err
	}
	if opts.dryRun {
		return emitDryRun(updated)
	}
	return emit(updated)
}

// issueBrief is the bulk-context payload returned by `bacio issue brief`. It's
// designed for skill / LLM consumption: one read, one structured object,
// every doc body inlined so the consumer can reason about the issue without
// chasing N+1 follow-up reads.
type issueBrief struct {
	Issue        *model.Issue          `json:"issue"`
	Feature      *model.Feature        `json:"feature,omitempty"`
	Relations    *store.IssueRelations `json:"relations"`
	PullRequests []*model.PullRequest  `json:"pull_requests"`
	Documents    []*briefDoc           `json:"documents"`
	Comments     []*model.Comment      `json:"comments"`
	Claimants    []*model.AgentClaim   `json:"claimants"`
	Taken        bool                  `json:"taken"`
	// LatestPlan (BACI-216) — mirrors client.IssueBrief.LatestPlan.
	LatestPlan *model.LatestPlan `json:"latest_plan,omitempty"`
	// BaseBranch (BACI-226) — mirrors client.IssueBrief.BaseBranch.
	// Always non-empty (resolver's fallback to "main" ensures it).
	BaseBranch string   `json:"base_branch"`
	Warnings   []string `json:"warnings"`
}

// briefDoc is a single linked document with its full content inlined and
// attribution path captured in LinkedVia (e.g. ["issue"], or
// ["issue", "feature/auth-rewrite"] when the same doc is reachable via
// both the issue and its parent feature). After BACI-115, Content is
// populated only for `plan` / `review` docs; every other type carries
// metadata + SizeBytes and an empty Content.
type briefDoc struct {
	Filename    string             `json:"filename"`
	Type        model.DocumentType `json:"type"`
	Description string             `json:"description,omitempty"`
	SourcePath  string             `json:"source_path,omitempty"`
	LinkedVia   []string           `json:"linked_via"`
	SizeBytes   int64              `json:"size_bytes"`
	Content     string             `json:"content"`
}

func issueBriefCmd() *cobra.Command {
	var (
		noFeatureDocs bool
		noComments    bool
		noDocContent  bool
	)
	cmd := &cobra.Command{
		Use:   "brief <KEY>",
		Short: "Bulk JSON context for an issue (issue + feature + linked docs with content + comments + relations + PRs)",
		Long: `Single bulk-context fetch for an issue. Always emits JSON regardless of
--output, since this verb is structured-data-by-design — it exists to
collapse the issue + feature + linked-docs + content + comments dance
that every skill was open-coding into one read.

Linked docs from the parent feature are included by default (use
--no-feature-docs to skip). Each doc carries a "linked_via" array that
records every attribution path (e.g. ["issue"] or
["issue", "feature/auth-rewrite"]) and a "size_bytes" field carrying
the document size.

Document bodies are inlined only for the "plan" and "review" doc types.
Every other type (transcripts, designs, project_complete, …) is
surfaced as metadata only — filename, type, source_path, linked_via,
description, size_bytes — with an empty content string. Fetch a
specific body via bacio doc show. Pass --no-doc-content to drop the
plan/review bodies too.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := openClient()
			if err != nil {
				return err
			}
			defer c.Close()
			repo, err := repoForIssueKey(c, args[0])
			if err != nil {
				return err
			}
			view, err := c.BriefIssue(context.Background(), repo, args[0], client.BriefOptions{
				NoFeatureDocs: noFeatureDocs,
				NoComments:    noComments,
				NoDocContent:  noDocContent,
			})
			if err != nil {
				return err
			}
			docs := make([]*briefDoc, 0, len(view.Documents))
			for _, d := range view.Documents {
				docs = append(docs, &briefDoc{
					Filename:    d.Filename,
					Type:        d.Type,
					Description: d.Description,
					SourcePath:  d.SourcePath,
					LinkedVia:   d.LinkedVia,
					SizeBytes:   d.SizeBytes,
					Content:     d.Content,
				})
			}
			brief := &issueBrief{
				Issue:        view.Issue,
				Feature:      view.Feature,
				Relations:    view.Relations,
				PullRequests: view.PullRequests,
				Documents:    docs,
				Comments:     view.Comments,
				Claimants:    view.Claimants,
				Taken:        view.Taken,
				LatestPlan:   view.LatestPlan,
				BaseBranch:   view.BaseBranch,
				Warnings:     view.Warnings,
			}
			enc := json.NewEncoder(os.Stdout)
			enc.SetIndent("", "  ")
			return enc.Encode(brief)
		},
	}
	cmd.Flags().BoolVar(&noFeatureDocs, "no-feature-docs", false, "skip docs linked to the parent feature")
	cmd.Flags().BoolVar(&noComments, "no-comments", false, "skip the comments section")
	cmd.Flags().BoolVar(&noDocContent, "no-doc-content", false, "keep linked-doc metadata but drop their bodies (fetch via `bacio doc show`)")
	return cmd
}

func issueAssignCmd() *cobra.Command {
	var rawInput string
	cmd := &cobra.Command{
		Use:   "assign [KEY] [name]",
		Short: "Assign an issue to a person or agent",
		Args:  cobra.RangeArgs(0, 2),
		RunE: func(cmd *cobra.Command, args []string) error {
			raw, err := parseJSONInput(cmd, args, rawInput)
			if err != nil {
				return err
			}
			if raw != nil {
				in, _, err := inputio.DecodeStrict[inputs.IssueAssignInput](raw)
				if err != nil {
					return err
				}
				if in.Key == "" || strings.TrimSpace(in.Assignee) == "" {
					return fmt.Errorf("key and non-empty assignee are required")
				}
				return assignIssue(in.Key, in.Assignee, true)
			}
			if len(args) != 2 {
				return fmt.Errorf("requires <KEY> <name> positionals or --json")
			}
			return assignIssue(args[0], args[1], false)
		},
	}
	addInputFlag(cmd, &rawInput)
	return cmd
}

func assignIssue(key, name string, strict bool) error {
	name = strings.TrimSpace(name)
	if name == "" {
		return fmt.Errorf("assignee name must be non-empty (use `bacio issue unassign` to clear)")
	}
	c, err := openClient()
	if err != nil {
		return err
	}
	defer c.Close()
	if strict {
		key = strings.TrimSpace(key)
		if !strings.Contains(key, "-") {
			return fmt.Errorf("issue key %q must be canonical (e.g. \"MINI-42\")", key)
		}
	}
	repo, err := repoForIssueKey(c, key)
	if err != nil {
		return err
	}
	updated, err := c.AssignIssue(context.Background(), repo, key, name, opts.dryRun)
	if err != nil {
		return err
	}
	if opts.dryRun {
		return emitDryRun(updated)
	}
	return emit(updated)
}

func issueUnassignCmd() *cobra.Command {
	var rawInput string
	cmd := &cobra.Command{
		Use:   "unassign [KEY]",
		Short: "Clear the assignee on an issue",
		Args:  cobra.RangeArgs(0, 1),
		RunE: func(cmd *cobra.Command, args []string) error {
			raw, err := parseJSONInput(cmd, args, rawInput)
			if err != nil {
				return err
			}
			if raw != nil {
				in, _, err := inputio.DecodeStrict[inputs.IssueUnassignInput](raw)
				if err != nil {
					return err
				}
				if in.Key == "" {
					return fmt.Errorf("key is required")
				}
				return unassignIssue(in.Key, true)
			}
			if len(args) != 1 {
				return fmt.Errorf("requires <KEY> positional or --json")
			}
			return unassignIssue(args[0], false)
		},
	}
	addInputFlag(cmd, &rawInput)
	return cmd
}

func unassignIssue(key string, strict bool) error {
	c, err := openClient()
	if err != nil {
		return err
	}
	defer c.Close()
	if strict {
		key = strings.TrimSpace(key)
		if !strings.Contains(key, "-") {
			return fmt.Errorf("issue key %q must be canonical (e.g. \"MINI-42\")", key)
		}
	}
	repo, err := repoForIssueKey(c, key)
	if err != nil {
		return err
	}
	updated, err := c.UnassignIssue(context.Background(), repo, key, opts.dryRun)
	if err != nil {
		return err
	}
	if opts.dryRun {
		return emitDryRun(updated)
	}
	return emit(updated)
}

// claimResult is the structured payload `bacio issue next` emits. Issue is nil
// when no work is currently claimable (so JSON consumers see {"issue": null}
// and can poll without parsing errors).
type claimResult struct {
	Issue *model.Issue `json:"issue"`
}

func issueNextCmd() *cobra.Command {
	var featureSlug, rawInput string
	cmd := &cobra.Command{
		Use:   "next",
		Short: "Atomically claim the next ready issue in a feature",
		Long: `Picks the lowest-numbered todo issue in --feature whose blockers are all
done/cancelled and whose assignee is empty, and stamps the assignee with
the calling agent's identity. The issue stays in todo — claiming is a
focus marker, not a state move (BACI-300).

Designed for agent loops: call repeatedly to walk through a feature in
dependency order. When nothing is currently claimable (everything is
either claimed, done, or still blocked) the command emits an empty
result with exit code 0 — the caller should wait and retry rather than
treat it as an error.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			raw, err := parseJSONInput(cmd, args, rawInput, "feature")
			if err != nil {
				return err
			}
			if raw != nil {
				in, _, err := inputio.DecodeStrict[inputs.IssueNextInput](raw)
				if err != nil {
					return err
				}
				if in.FeatureSlug == "" {
					return fmt.Errorf("feature_slug is required")
				}
				return claimNextIssue(in.FeatureSlug)
			}
			if featureSlug == "" {
				return fmt.Errorf("--feature is required")
			}
			return claimNextIssue(featureSlug)
		},
	}
	cmd.Flags().StringVarP(&featureSlug, "feature", "f", "", "feature slug to pull from (required)")
	addInputFlag(cmd, &rawInput)
	return cmd
}

func claimNextIssue(featureSlug string) error {
	c, err := openClient()
	if err != nil {
		return err
	}
	defer c.Close()
	repo, err := resolveRepoC(c)
	if err != nil {
		return err
	}
	iss, err := c.ClaimNextIssue(context.Background(), repo, featureSlug, opts.dryRun)
	if err != nil {
		return err
	}
	if opts.dryRun {
		return emitDryRun(&claimResult{Issue: iss})
	}
	return emit(&claimResult{Issue: iss})
}

func issuePeekCmd() *cobra.Command {
	var featureSlug string
	cmd := &cobra.Command{
		Use:   "peek",
		Short: "Show the next ready issue in a feature without claiming it",
		Long: `Read-only counterpart to ` + "`bacio issue next`" + `: returns the same issue
the claim would pick (lowest-numbered todo with all blockers
done/cancelled and no assignee) but does not mutate state.

Emits an empty result with exit code 0 when nothing is currently
claimable, matching the shape of ` + "`bacio issue next`" + `.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if featureSlug == "" {
				return fmt.Errorf("--feature is required")
			}
			c, err := openClient()
			if err != nil {
				return err
			}
			defer c.Close()
			repo, err := resolveRepoC(c)
			if err != nil {
				return err
			}
			iss, err := c.PeekNextIssue(context.Background(), repo, featureSlug)
			if err != nil {
				return err
			}
			return emit(&claimResult{Issue: iss})
		},
	}
	cmd.Flags().StringVarP(&featureSlug, "feature", "f", "", "feature slug to peek into (required)")
	return cmd
}

func issueRmCmd() *cobra.Command {
	var rawInput string
	cmd := &cobra.Command{
		Use:   "rm [KEY]",
		Short: "Delete an issue (and its comments)",
		Args:  cobra.RangeArgs(0, 1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := agentmode.DenyIfEnabled("issue rm"); err != nil {
				return err
			}
			raw, err := parseJSONInput(cmd, args, rawInput)
			if err != nil {
				return err
			}
			if raw != nil {
				in, _, err := inputio.DecodeStrict[inputs.IssueRmInput](raw)
				if err != nil {
					return err
				}
				if in.Key == "" {
					return fmt.Errorf("key is required")
				}
				return removeIssue(in.Key, true)
			}
			if len(args) != 1 {
				return fmt.Errorf("requires <KEY> positional or --json")
			}
			return removeIssue(args[0], false)
		},
	}
	addInputFlag(cmd, &rawInput)
	return cmd
}

func removeIssue(key string, strict bool) error {
	c, err := openClient()
	if err != nil {
		return err
	}
	defer c.Close()
	if strict {
		key = strings.TrimSpace(key)
		if !strings.Contains(key, "-") {
			return fmt.Errorf("issue key %q must be canonical (e.g. \"MINI-42\")", key)
		}
	}
	repo, err := repoForIssueKey(c, key)
	if err != nil {
		return err
	}
	deleted, preview, err := c.DeleteIssue(context.Background(), repo, key, opts.dryRun)
	if err != nil {
		return err
	}
	if opts.dryRun {
		return emitDryRun(&issueDeletePreview{
			Issue: preview.Issue,
			Cascade: cascadeCount{
				Comments:      preview.Cascade.Comments,
				Relations:     preview.Cascade.Relations,
				PullRequests:  preview.Cascade.PullRequests,
				DocumentLinks: preview.Cascade.DocumentLinks,
				Tags:          preview.Cascade.Tags,
			},
			WouldDelete: preview.WouldDelete,
		})
	}
	return ok("issue %s deleted", deleted.Key)
}

// issueDeletePreview is the dry-run payload for `bacio issue rm`. It records
// the row that would be deleted plus how many dependent rows would be
// cascade-removed alongside it.
type issueDeletePreview struct {
	Issue       *model.Issue `json:"issue"`
	Cascade     cascadeCount `json:"cascade"`
	WouldDelete bool         `json:"would_delete"`
}

// cascadeCount summarises how many dependent rows ride along with a
// destructive operation. Zeros are kept (not omitempty) so the JSON shape
// is stable for downstream parsers.
type cascadeCount struct {
	Comments      int `json:"comments"`
	Relations     int `json:"relations"`
	PullRequests  int `json:"pull_requests"`
	DocumentLinks int `json:"document_links"`
	Tags          int `json:"tags"`
}

// issueArchiveCmd / issueUnarchiveCmd are the BACI-68 manual archive
// verbs on the issue surface. The auto-sweep handles bulk archival
// every hour on the leader, but users (and agents) can also flip
// archived_at on demand.
func issueArchiveCmd() *cobra.Command {
	var rawInput string
	cmd := &cobra.Command{
		Use:   "archive [KEY]",
		Short: "Archive an issue (BACI-68) — hides it from default lists; row + history are retained",
		Long: `Archive an issue: stamps archived_at and hides it from default
lists (board, kanban, JSON list, API). The row, its comments, relations,
PRs, tags and audit history are kept. Idempotent — archiving an
already-archived row is a no-op. Sticky: reopening (via ` + "`bacio issue state`" + `)
does NOT auto-unarchive. Use ` + "`bacio issue unarchive`" + ` to undo.`,
		Args: cobra.RangeArgs(0, 1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runIssueArchiveVerb(cmd, args, rawInput, true)
		},
	}
	addInputFlag(cmd, &rawInput)
	return cmd
}

func issueUnarchiveCmd() *cobra.Command {
	var rawInput string
	cmd := &cobra.Command{
		Use:   "unarchive [KEY]",
		Short: "Unarchive an issue (BACI-68) — clears archived_at",
		Args:  cobra.RangeArgs(0, 1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runIssueArchiveVerb(cmd, args, rawInput, false)
		},
	}
	addInputFlag(cmd, &rawInput)
	return cmd
}

// runIssueArchiveVerb is the shared mutation path for archive +
// unarchive. The two cobra commands collapse to one runner because the
// positional / --json shapes are identical — only the boolean varies.
func runIssueArchiveVerb(cmd *cobra.Command, args []string, rawInput string, archive bool) error {
	raw, err := parseJSONInput(cmd, args, rawInput)
	if err != nil {
		return err
	}
	var key string
	if raw != nil {
		if archive {
			in, _, err := inputio.DecodeStrict[inputs.IssueArchiveInput](raw)
			if err != nil {
				return err
			}
			key = in.Key
		} else {
			in, _, err := inputio.DecodeStrict[inputs.IssueUnarchiveInput](raw)
			if err != nil {
				return err
			}
			key = in.Key
		}
		key = strings.TrimSpace(key)
		if !strings.Contains(key, "-") {
			return fmt.Errorf("issue key %q must be canonical (e.g. \"MINI-42\")", key)
		}
	} else {
		if len(args) != 1 {
			return fmt.Errorf("requires <KEY> positional or --json")
		}
		key = args[0]
	}
	c, err := openClient()
	if err != nil {
		return err
	}
	defer c.Close()
	repo, err := repoForIssueKey(c, key)
	if err != nil {
		return err
	}
	var updated *model.Issue
	if archive {
		updated, err = c.ArchiveIssue(context.Background(), repo, key, opts.dryRun)
	} else {
		updated, err = c.UnarchiveIssue(context.Background(), repo, key, opts.dryRun)
	}
	if err != nil {
		return err
	}
	if opts.dryRun {
		return emitDryRun(updated)
	}
	return emit(updated)
}
