// Package cli — `bacio pr create` (BACI-163).
//
// Ship-mode workers were opening duplicate PRs for the same issue
// because nothing guarded the `gh pr create` seam. This wrapper does
// three things on top of the bare `gh pr create`:
//
//   1. Pre-flight duplicate check — refuses to open a second PR when
//      an OPEN or MERGED PR already carries the per-issue label
//      (belt + braces: probes both `gh pr list --label bacio:<KEY>`
//      and `bacio pr list <KEY>`).
//   2. Idempotent per-issue label create (`bacio:<KEY>`).
//   3. Funnels the resulting URL through `bacio pr attach` so the
//      local DB stays in sync, and writes a `pr.create` history op
//      on top of the inner `pr.attach` so the wrapper's intent
//      shows up in the audit log.
//
// The verb shells out to the `gh` CLI, which only makes sense from a
// workstation with `gh` configured — not a remote API server. We
// therefore refuse `--remote`, mirroring the carve-out for the
// other filesystem-touching verbs (init, install-*, doc export, …).

package cli

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/mrgeoffrich/bacio/internal/cli/inputs"
	"github.com/mrgeoffrich/bacio/internal/ghcli"
	"github.com/mrgeoffrich/bacio/internal/inputio"
	"github.com/mrgeoffrich/bacio/internal/model"
)

// labelColor is the GitHub label colour used for every `bacio:<KEY>`
// label. The constant value was picked in the BACI-163 ticket. A
// one-line edit here will roll out to new labels; `gh label create
// --force` updates existing ones.
const labelColor = "BFD4F2"

// newGHCli is the package-level seam tests override to inject a fake
// gh-CLI client. Production callers go through `ghcli.NewExec()`.
var newGHCli = func() ghcli.Client { return ghcli.NewExec() }

// prCreateResult is the JSON + text shape `bacio pr create` returns on
// the success path. Kept in sync with `prCreatePreview` below so
// downstream parsers can read either with the same code.
type prCreateResult struct {
	IssueKey    string              `json:"issue_key"`
	Label       string              `json:"label"`
	PullRequest *model.PullRequest  `json:"pull_request"`
	Warnings    []string            `json:"warnings,omitempty"`
}

// prCreatePreview is the dry-run payload. Matches prCreateResult's
// shape where it can, plus the projected `gh` argv and the
// pre-flight verdicts the caller would have seen.
type prCreatePreview struct {
	IssueKey          string   `json:"issue_key"`
	Label             string   `json:"label"`
	ProjectedGHArgv   []string `json:"projected_gh_argv"`
	PreflightRefusals []string `json:"preflight_refusals,omitempty"`
	PreflightWarnings []string `json:"preflight_warnings,omitempty"`
}

func prCreateCmd() *cobra.Command {
	var (
		rawInput string
		force    bool
	)
	cmd := &cobra.Command{
		Use:   "create <KEY> [-- gh pr create flags...]",
		Short: "Open a GitHub PR labelled bacio:<KEY> after pre-flighting for duplicates",
		Long: "Open a GitHub PR for an issue, labelled `bacio:<KEY>`, after pre-flighting " +
			"for an existing PR carrying the same label. Refuses on OPEN/MERGED unless " +
			"--force is set; warns on CLOSED-only. On success the URL is funnelled " +
			"through `bacio pr attach` so the local DB stays in sync, and a `pr.create` " +
			"history op is written on top of the inner `pr.attach`.\n\n" +
			"Anything after `--` is forwarded verbatim to `gh pr create`. `--label` is " +
			"injected by the wrapper and must not appear in the passthrough.\n\n" +
			"Race note: two workers running concurrently can both pass the pre-flight " +
			"and then both call `gh pr create`. GitHub's branch dedup catches most of " +
			"this in practice but not all of it — the wrapper reduces duplicates by " +
			"orders of magnitude, doesn't eliminate them.",
		// We don't constrain Args here: the positional <KEY> may be
		// missing on the --json path, and cobra's --json/positional
		// mutual exclusion is enforced inside RunE via parseJSONInput.
		Args: cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if inRemoteMode() {
				return fmt.Errorf("bacio pr create: not supported in remote mode (shells out to the local `gh` CLI); run from a workstation with gh configured against the local DB")
			}
			// Cobra surfaces `--` as the boundary marker in args via
			// ArgsLenAtDash. Anything past it is passthrough for `gh`.
			cutoff := cmd.ArgsLenAtDash()
			var passthrough []string
			if cutoff >= 0 {
				passthrough = append(passthrough, args[cutoff:]...)
				args = args[:cutoff]
			}
			raw, err := parseJSONInput(cmd, args, rawInput, "force")
			if err != nil {
				return err
			}
			if raw != nil {
				if len(passthrough) > 0 {
					return fmt.Errorf("--json cannot be combined with `--` passthrough; put the gh args in the JSON `gh_args` field instead")
				}
				in, _, err := inputio.DecodeStrict[inputs.PRCreateInput](raw)
				if err != nil {
					return err
				}
				if in.IssueKey == "" {
					return fmt.Errorf("issue_key is required")
				}
				if !isIssueKey(in.IssueKey) {
					return fmt.Errorf("issue_key %q must be canonical (e.g. \"MINI-42\")", in.IssueKey)
				}
				return runPRCreate(*in)
			}
			if len(args) != 1 {
				return fmt.Errorf("requires <KEY> positional or --json")
			}
			return runPRCreate(inputs.PRCreateInput{
				IssueKey: args[0],
				GHArgs:   passthrough,
				Force:    force,
			})
		},
	}
	cmd.Flags().BoolVar(&force, "force", false, "open the PR even if the pre-flight finds an existing OPEN/MERGED labelled PR")
	addInputFlag(cmd, &rawInput)
	return cmd
}

// runPRCreate is the core decision flow. Pre-flight first, then label
// + create + attach, then audit-log. Dry-run short-circuits before any
// of the gh writes.
func runPRCreate(in inputs.PRCreateInput) error {
	if err := validateGHArgs(in.GHArgs); err != nil {
		return err
	}
	c, err := openClient()
	if err != nil {
		return err
	}
	defer c.Close()
	repo, err := repoForIssueKey(c, in.IssueKey)
	if err != nil {
		return err
	}
	// Snap the key into canonical PREFIX-N form once so the label and
	// every later message agree on casing/whitespace.
	ctx := context.Background()
	canonical, err := c.ResolveIssueKey(ctx, repo, in.IssueKey)
	if err != nil {
		return err
	}
	label := fmt.Sprintf("bacio:%s", canonical)

	// BACI-228: resolve the effective base branch via the shared
	// model.ResolveBaseBranch helper (issue override > feature branch
	// > "main") and inject `--base <branch>` into the forwarded gh
	// args. A caller-passed `--base` on the verbatim tail wins. The
	// resolver's invariant is "never empty", so the injection always
	// produces a concrete branch — explicit beats relying on gh's own
	// default-branch detection, which can lag behind a feature-branch
	// workflow.
	iss, err := c.GetIssueByKey(ctx, repo, canonical)
	if err != nil {
		return fmt.Errorf("resolve issue %s: %w", canonical, err)
	}
	var feat *model.Feature
	if iss.FeatureID != nil {
		feat, err = c.GetFeatureByID(ctx, repo, *iss.FeatureID)
		if err != nil {
			return fmt.Errorf("resolve feature for %s: %w", canonical, err)
		}
	}
	baseBranch := model.ResolveBaseBranch(iss, feat)
	ghArgs := in.GHArgs
	if !ghArgsHaveBase(ghArgs) {
		ghArgs = append([]string{"--base", baseBranch}, ghArgs...)
	}

	gh := newGHCli()
	ghPRs, err := gh.ListPRsByLabel(ctx, label)
	if err != nil {
		return fmt.Errorf("pre-flight gh pr list: %w", err)
	}
	bacioPRs, err := c.ListPRs(ctx, repo, canonical)
	if err != nil {
		return fmt.Errorf("pre-flight bacio pr list: %w", err)
	}

	refusals, warnings := classifyPreflight(label, ghPRs, bacioPRs)
	if len(refusals) > 0 && !in.Force {
		// Emit the refusal as a single error — the runtime prints it
		// to stderr and exits non-zero.
		return fmt.Errorf("%s\n\npass --force to open the PR anyway", strings.Join(refusals, "\n"))
	}
	if in.Force && len(refusals) > 0 {
		// Surface the would-have-refused notice as a warning so the
		// audit trail records what we overrode.
		warnings = append(warnings, "--force: overriding pre-flight: "+strings.Join(refusals, "; "))
	}

	// Build the projected argv early so dry-run / errors can show it.
	projected := append([]string{"gh", "pr", "create", "--label", label}, ghArgs...)

	if opts.dryRun {
		for _, w := range warnings {
			fmt.Fprintln(os.Stderr, "warning:", w)
		}
		return emitDryRun(&prCreatePreview{
			IssueKey:          canonical,
			Label:             label,
			ProjectedGHArgv:   projected,
			PreflightRefusals: refusals,
			PreflightWarnings: warnings,
		})
	}

	// Surface warnings on stderr ahead of the real `gh` call so users
	// see the closed-PR / forced-override notice before a possible
	// `gh` failure scrolls them off-screen.
	for _, w := range warnings {
		fmt.Fprintln(os.Stderr, "warning:", w)
	}

	if err := gh.CreateLabelIdempotent(ctx, label, labelColor); err != nil {
		return fmt.Errorf("create label %s: %w", label, err)
	}
	url, err := gh.CreatePR(ctx, label, ghArgs)
	if err != nil {
		return fmt.Errorf("gh pr create: %w", err)
	}

	created, err := c.AttachPR(ctx, repo, canonical, url, false)
	if err != nil {
		return fmt.Errorf("attach %s to %s: %w", url, canonical, err)
	}

	// Layer a `pr.create` history op on top of the `pr.attach` the
	// client just wrote. Two rows is the correct trace: the wrapper's
	// intent (create) and the store mutation it triggered (attach).
	if s, err := openStore(); err == nil {
		defer s.Close()
		repoID := repo.ID
		recordOp(s, model.HistoryEntry{
			RepoID: &repoID, RepoPrefix: repo.Prefix,
			Op: "pr.create", Kind: "issue",
			TargetLabel: canonical,
			Details:     fmt.Sprintf("label=%s url=%s", label, url),
		})
	} else {
		// Don't fail the user-visible command on an audit-log miss —
		// losing the wrapper-intent row is preferable to rolling back
		// the PR we just created.
		fmt.Fprintln(os.Stderr, "bacio: warning: failed to open store for pr.create history:", err)
	}

	return emit(&prCreateResult{
		IssueKey:    canonical,
		Label:       label,
		PullRequest: created,
		Warnings:    warnings,
	})
}

// classifyPreflight applies the BACI-163 decision tree across the two
// pre-flight lists. Returns (refusals, warnings) — both are
// human-readable strings ready for stderr.
//
//   - Any OPEN or MERGED PR (in either list) → refusal.
//   - Any attached PR on the bacio side whose state isn't known →
//     refusal (we can't tell from a URL alone; bacio attachments
//     came from somewhere on purpose).
//   - Otherwise, any CLOSED-only gh hit → warning.
//   - Otherwise, silent OK.
func classifyPreflight(label string, ghPRs []ghcli.PRSummary, bacioPRs []*model.PullRequest) (refusals, warnings []string) {
	// Index gh PRs by URL so we can tell which bacio attachments are
	// already accounted for by a gh row (don't refuse twice).
	ghByURL := map[string]ghcli.PRSummary{}
	for _, pr := range ghPRs {
		ghByURL[pr.URL] = pr
		switch strings.ToUpper(pr.State) {
		case ghcli.StateOpen, ghcli.StateMerged:
			refusals = append(refusals, fmt.Sprintf("gh: PR #%d (%s) labelled %s is %s: %s",
				pr.Number, pr.URL, label, strings.ToUpper(pr.State), pr.URL))
		}
	}
	// Belt-and-braces: a PR attached on the bacio side that isn't in
	// the gh list (or that doesn't have a state on the gh row) is a
	// red flag — refuse rather than open a duplicate.
	for _, pr := range bacioPRs {
		if g, ok := ghByURL[pr.URL]; ok {
			// Already handled above if it was OPEN/MERGED; if CLOSED
			// we still want to warn (handled below) — skip here.
			_ = g
			continue
		}
		refusals = append(refusals, fmt.Sprintf("bacio: %s is attached to this issue (state unknown to gh); detach with `bacio pr detach` or pass --force",
			pr.URL))
	}
	// CLOSED-only warnings (only meaningful when there are no
	// refusals — otherwise the refusal message dominates).
	if len(refusals) == 0 {
		for _, pr := range ghPRs {
			if strings.ToUpper(pr.State) == ghcli.StateClosed {
				warnings = append(warnings, fmt.Sprintf("gh: PR #%d (%s) labelled %s is CLOSED; proceeding with a fresh PR",
					pr.Number, pr.URL, label))
			}
		}
	}
	return refusals, warnings
}

// validateGHArgs rejects the obviously-wrong shapes early. We don't
// re-derive a full `gh pr create` flag parser — just guard the
// invariants the wrapper depends on.
func validateGHArgs(args []string) error {
	for i, a := range args {
		if a == "--label" {
			return fmt.Errorf("--label is injected by `bacio pr create` and must not appear in the passthrough args (position %d)", i)
		}
		// Catch the `--label=foo` form too.
		if strings.HasPrefix(a, "--label=") {
			return fmt.Errorf("--label is injected by `bacio pr create` and must not appear in the passthrough args (position %d)", i)
		}
	}
	return nil
}

// ghArgsHaveBase reports whether the caller already passed `--base`
// (either `--base feat/X` or `--base=feat/X`) on the verbatim
// passthrough tail. When true the wrapper leaves the args alone so the
// caller's override wins; when false the wrapper injects `--base <branch>`
// from the resolved base branch (BACI-228).
func ghArgsHaveBase(args []string) bool {
	for _, a := range args {
		if a == "--base" || strings.HasPrefix(a, "--base=") {
			return true
		}
	}
	return false
}
