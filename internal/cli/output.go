package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/mrgeoffrich/bacio/internal/model"
	"github.com/mrgeoffrich/bacio/internal/store"
	"github.com/mrgeoffrich/bacio/internal/sync"
)

// localTime renders a UTC timestamp in the user's local timezone for text
// output. JSON marshalling uses Go's default RFC 3339 / UTC, untouched.
func localTime(t time.Time) string {
	return t.Local().Format("2006-01-02 15:04 MST")
}

func emit(v any) error {
	if opts.output == outputJSON {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(v)
	}
	return renderText(os.Stdout, v)
}

// emitDryRun writes a `[dry-run]` marker to stderr (so agents can grep for
// it) and emits v on stdout exactly as a real call would. The shape matches
// the real-call output so downstream parsers don't need a special path.
func emitDryRun(v any) error {
	fmt.Fprintln(os.Stderr, "[dry-run] no changes were written")
	return emit(v)
}

func renderText(w io.Writer, v any) error {
	switch x := v.(type) {
	case *model.Repo:
		return printRepo(w, x)
	case []*model.Repo:
		for _, r := range x {
			fmt.Fprintf(w, "%s  %s\t%s\n", r.Prefix, r.Name, r.Path)
		}
	case *model.Feature:
		fmt.Fprintf(w, "%s\t%s\n", x.Slug, x.Title)
		fmt.Fprintf(w, "Created:  %s\n", localTime(x.CreatedAt))
		fmt.Fprintf(w, "Updated:  %s\n", localTime(x.UpdatedAt))
		if x.Description != "" {
			fmt.Fprintln(w)
			fmt.Fprintln(w, x.Description)
		}
	case []*model.Feature:
		for _, f := range x {
			fmt.Fprintf(w, "%s\t%s\n", f.Slug, f.Title)
		}
	case *issueView:
		return printIssueView(w, x)
	case *featureView:
		return printFeatureView(w, x)
	case *model.Issue:
		return printIssue(w, x)
	case []*model.Issue:
		for _, i := range x {
			feat := ""
			if i.FeatureSlug != "" {
				feat = " [" + i.FeatureSlug + "]"
			}
			tagStr := ""
			if len(i.Tags) > 0 {
				tagStr = "  #" + strings.Join(i.Tags, " #")
			}
			fmt.Fprintf(w, "%-10s %-12s%s  %s%s\n", i.Key, i.State, feat, i.Title, tagStr)
		}
	case *model.Comment:
		fmt.Fprintf(w, "%s — %s\n%s\n", x.Author, localTime(x.CreatedAt), x.Body)
	case []*model.Comment:
		for _, c := range x {
			fmt.Fprintf(w, "%s — %s\n%s\n\n", c.Author, localTime(c.CreatedAt), c.Body)
		}
	case *model.PullRequest:
		fmt.Fprintln(w, x.URL)
	case []*model.PullRequest:
		for _, pr := range x {
			fmt.Fprintln(w, pr.URL)
		}
	case *model.Document:
		printDocument(w, x)
	case []*model.Document:
		for _, d := range x {
			fmt.Fprintf(w, "%-30s %-22s %d bytes\n", d.Filename, d.Type, d.SizeBytes)
		}
	case *model.DocumentLink:
		printDocLinkLine(w, x)
	case []*model.DocumentLink:
		for _, l := range x {
			printDocLinkLine(w, l)
		}
	case *docView:
		return printDocView(w, x)
	case *store.IssueRelations:
		printRelations(w, x)
	case *planView:
		printPlan(w, x)
	case *claimResult:
		printClaim(w, x)
	case []*model.HistoryEntry:
		for _, e := range x {
			printHistoryLine(w, e)
		}
	case exportResult:
		printExportResult(w, x)
	case importResult:
		printImportResult(w, x)
	case syncRunResult:
		printSyncRunResult(w, x)
	case syncInitResult:
		printSyncInitResult(w, x)
	case syncCloneResult:
		printSyncCloneResult(w, x)
	case syncVerifyResult:
		printSyncVerifyResult(w, x)
	case syncInspectResult:
		printSyncInspectResult(w, x)
	case message:
		fmt.Fprintln(w, x.Text)
	default:
		fmt.Fprintf(w, "%v\n", v)
	}
	return nil
}

func printRepo(w io.Writer, r *model.Repo) error {
	fmt.Fprintf(w, "Prefix:    %s\n", r.Prefix)
	fmt.Fprintf(w, "Name:      %s\n", r.Name)
	fmt.Fprintf(w, "Path:      %s\n", r.Path)
	if r.RemoteURL != "" {
		fmt.Fprintf(w, "Remote:    %s\n", r.RemoteURL)
	}
	fmt.Fprintf(w, "NextIssue: %s-%d\n", r.Prefix, r.NextIssueNumber)
	fmt.Fprintf(w, "Created:   %s\n", localTime(r.CreatedAt))
	return nil
}

func printIssue(w io.Writer, i *model.Issue) error {
	fmt.Fprintf(w, "%s  %s\n", i.Key, i.Title)
	fmt.Fprintf(w, "State:    %s\n", i.State)
	if i.FeatureSlug != "" {
		fmt.Fprintf(w, "Feature:  %s\n", i.FeatureSlug)
	}
	if i.Assignee != "" {
		fmt.Fprintf(w, "Assignee: %s\n", i.Assignee)
	}
	if len(i.Tags) > 0 {
		fmt.Fprintf(w, "Tags:     %s\n", strings.Join(i.Tags, ", "))
	}
	fmt.Fprintf(w, "Created:  %s\n", localTime(i.CreatedAt))
	fmt.Fprintf(w, "Updated:  %s\n", localTime(i.UpdatedAt))
	if i.Description != "" {
		fmt.Fprintf(w, "\n%s\n", i.Description)
	}
	return nil
}

type issueView struct {
	Issue        *model.Issue          `json:"issue"`
	Comments     []*model.Comment      `json:"comments"`
	Relations    *store.IssueRelations `json:"relations"`
	PullRequests []*model.PullRequest  `json:"pull_requests"`
	Documents    []*model.DocumentLink `json:"documents"`
}

type featureView struct {
	Feature   *model.Feature        `json:"feature"`
	Issues    []*model.Issue        `json:"issues"`
	Documents []*model.DocumentLink `json:"documents"`
}

func printIssueView(w io.Writer, v *issueView) error {
	if err := printIssue(w, v.Issue); err != nil {
		return err
	}
	if len(v.PullRequests) > 0 {
		fmt.Fprintln(w)
		fmt.Fprintln(w, "Pull requests:")
		for _, pr := range v.PullRequests {
			fmt.Fprintf(w, "  %s\n", pr.URL)
		}
	}
	if v.Relations != nil && (len(v.Relations.Outgoing) > 0 || len(v.Relations.Incoming) > 0) {
		fmt.Fprintln(w)
		fmt.Fprintln(w, "Relations:")
		printRelations(w, v.Relations)
	}
	if len(v.Documents) > 0 {
		fmt.Fprintln(w)
		fmt.Fprintln(w, "Linked documents:")
		for _, l := range v.Documents {
			printDocLinkInEntityContext(w, l)
		}
	}
	if len(v.Comments) > 0 {
		fmt.Fprintln(w)
		fmt.Fprintln(w, "Comments:")
		fmt.Fprintln(w, strings.Repeat("-", 40))
		for _, c := range v.Comments {
			fmt.Fprintf(w, "%s — %s\n%s\n\n", c.Author, localTime(c.CreatedAt), c.Body)
		}
	}
	return nil
}

func printFeatureView(w io.Writer, v *featureView) error {
	f := v.Feature
	fmt.Fprintf(w, "%s\t%s\n", f.Slug, f.Title)
	fmt.Fprintf(w, "Created:  %s\n", localTime(f.CreatedAt))
	fmt.Fprintf(w, "Updated:  %s\n", localTime(f.UpdatedAt))
	if f.Description != "" {
		fmt.Fprintln(w)
		fmt.Fprintln(w, f.Description)
	}
	if len(v.Issues) > 0 {
		fmt.Fprintln(w)
		fmt.Fprintln(w, "Issues:")
		for _, i := range v.Issues {
			fmt.Fprintf(w, "  %-10s %-12s %s\n", i.Key, i.State, i.Title)
		}
	}
	if len(v.Documents) > 0 {
		fmt.Fprintln(w)
		fmt.Fprintln(w, "Linked documents:")
		for _, l := range v.Documents {
			printDocLinkInEntityContext(w, l)
		}
	}
	return nil
}

// printDocLinkInEntityContext renders one doc link from the perspective of an
// issue or feature: filename + type, with the optional --why description.
// The link's target is implicit (it's the issue/feature being shown).
func printDocLinkInEntityContext(w io.Writer, l *model.DocumentLink) {
	label := l.DocumentFilename
	if l.DocumentType != "" {
		label = fmt.Sprintf("%s (%s)", label, l.DocumentType)
	}
	if l.Description != "" {
		fmt.Fprintf(w, "  %s — %s\n", label, l.Description)
	} else {
		fmt.Fprintf(w, "  %s\n", label)
	}
}

type docView struct {
	Document *model.Document       `json:"document"`
	Links    []*model.DocumentLink `json:"links"`
}

func printDocument(w io.Writer, d *model.Document) {
	fmt.Fprintf(w, "%s  type=%s  %d bytes\n", d.Filename, d.Type, d.SizeBytes)
	fmt.Fprintf(w, "Created: %s\n", localTime(d.CreatedAt))
	fmt.Fprintf(w, "Updated: %s\n", localTime(d.UpdatedAt))
}

func printDocView(w io.Writer, v *docView) error {
	printDocument(w, v.Document)
	if v.Document.Content != "" {
		fmt.Fprintln(w)
		fmt.Fprint(w, v.Document.Content)
		if !strings.HasSuffix(v.Document.Content, "\n") {
			fmt.Fprintln(w)
		}
	}
	if len(v.Links) > 0 {
		fmt.Fprintln(w)
		fmt.Fprintln(w, "Linked to:")
		for _, l := range v.Links {
			printDocLinkLine(w, l)
		}
	}
	return nil
}

func printDocLinkLine(w io.Writer, l *model.DocumentLink) {
	target := l.Target()
	if l.Description != "" {
		fmt.Fprintf(w, "  %s — %s\n", target, l.Description)
	} else {
		fmt.Fprintf(w, "  %s\n", target)
	}
}

func printPlan(w io.Writer, p *planView) {
	if len(p.Order) == 0 {
		fmt.Fprintf(w, "feature %s has no open issues\n", p.Feature)
		return
	}
	fmt.Fprintf(w, "Plan for feature %s (%d open issues):\n", p.Feature, len(p.Order))
	for i, e := range p.Order {
		line := fmt.Sprintf("  %2d. %-10s %-12s %s", i+1, e.Key, e.State, e.Title)
		if e.Assignee != "" {
			line += "  (@" + e.Assignee + ")"
		}
		if len(e.BlockedBy) > 0 {
			line += "  [blocked by: " + strings.Join(e.BlockedBy, ", ") + "]"
		}
		fmt.Fprintln(w, line)
	}
}

func printClaim(w io.Writer, c *claimResult) {
	if c.Issue == nil {
		fmt.Fprintln(w, "no claimable work right now (everything is claimed, done, or still blocked)")
		return
	}
	fmt.Fprintf(w, "claimed %s by %s\n", c.Issue.Key, c.Issue.Assignee)
	fmt.Fprintf(w, "%s\n", c.Issue.Title)
	if c.Issue.Description != "" {
		fmt.Fprintf(w, "\n%s\n", c.Issue.Description)
	}
}

func printRelations(w io.Writer, r *store.IssueRelations) {
	for _, rel := range r.Outgoing {
		fmt.Fprintf(w, "  %s %s\n", rel.Type, rel.ToIssue)
	}
	for _, rel := range r.Incoming {
		fmt.Fprintf(w, "  %s by %s\n", rel.Type, rel.FromIssue)
	}
}

func printHistoryLine(w io.Writer, e *model.HistoryEntry) {
	target := e.TargetLabel
	if target == "" {
		target = "-"
	}
	if e.RepoPrefix != "" && e.Kind != "repo" {
		target = e.RepoPrefix + "/" + target
	}
	line := fmt.Sprintf("%s  %-12s %-22s %s",
		localTime(e.CreatedAt), e.Actor, e.Op, target)
	if e.Details != "" {
		line += "  " + e.Details
	}
	fmt.Fprintln(w, line)
}

type message struct {
	Text string `json:"message"`
}

func ok(format string, a ...any) error {
	return emit(message{Text: fmt.Sprintf(format, a...)})
}

// printExportResult renders a sync.ExportResult as a one-line-per-kind
// summary. Used by `bacio sync export` (and, eventually, the steady-state
// `bacio sync`). Kept separate from the JSON output so callers parsing
// the structured form aren't subject to text-format churn.
func printExportResult(w io.Writer, r exportResult) {
	if r.ExportResult == nil {
		return
	}
	fmt.Fprintf(w, "Exported to %s\n", r.Target)
	fmt.Fprintf(w, "  repos:     %d\n", r.Repos)
	fmt.Fprintf(w, "  features:  %d\n", r.Features)
	fmt.Fprintf(w, "  issues:    %d\n", r.Issues)
	fmt.Fprintf(w, "  comments:  %d\n", r.Comments)
	fmt.Fprintf(w, "  documents: %d\n", r.Documents)
	fmt.Fprintf(w, "  files:     %d\n", r.Files)
	fmt.Fprintf(w, "  bytes:     %d\n", r.BytesWritten)
}

// printImportResult mirrors printExportResult: a one-line-per-kind
// counts summary plus per-event lists for the observable side
// effects (renumbers, renames, deletions, dangling refs).
func printImportResult(w io.Writer, r importResult) {
	if r.ImportResult == nil {
		return
	}
	fmt.Fprintf(w, "Imported from %s\n", r.Source)
	fmt.Fprintf(w, "  repos:     %d\n", r.Repos)
	fmt.Fprintf(w, "  features:  %d\n", r.Features)
	fmt.Fprintf(w, "  issues:    %d\n", r.Issues)
	fmt.Fprintf(w, "  comments:  %d\n", r.Comments)
	fmt.Fprintf(w, "  documents: %d\n", r.Documents)
	fmt.Fprintf(w, "  inserted:  %d\n", r.Inserted)
	fmt.Fprintf(w, "  updated:   %d\n", r.Updated)
	fmt.Fprintf(w, "  noop:      %d\n", r.NoOp)
	if len(r.Renumbered) > 0 {
		fmt.Fprintln(w, "Renumbered:")
		for _, e := range r.Renumbered {
			fmt.Fprintf(w, "  %s-%d -> %s-%d (uuid=%s)\n", e.Prefix, e.OldNumber, e.Prefix, e.NewNumber, e.UUID)
		}
	}
	if len(r.Renamed) > 0 {
		fmt.Fprintln(w, "Renamed:")
		for _, e := range r.Renamed {
			fmt.Fprintf(w, "  %s %s -> %s (uuid=%s)\n", e.Kind, e.Old, e.New, e.UUID)
		}
	}
	if len(r.Deleted) > 0 {
		fmt.Fprintln(w, "Deleted:")
		for _, e := range r.Deleted {
			label := e.Label
			if label == "" {
				label = e.UUID
			}
			fmt.Fprintf(w, "  %s %s\n", e.Kind, label)
		}
	}
	if len(r.Dangling) > 0 {
		fmt.Fprintln(w, "Dangling references (target uuid not in DB):")
		for _, d := range r.Dangling {
			fmt.Fprintf(w, "  %s -> %s %s (target uuid=%s)\n", d.From, d.Kind, d.TargetLabel, d.TargetUUID)
		}
	}
	if len(r.Warnings) > 0 {
		fmt.Fprintln(w, "Warnings:")
		for _, w2 := range r.Warnings {
			fmt.Fprintf(w, "  %s\n", w2)
		}
	}
}

// printSyncRunResult renders the result of a steady-state `bacio sync`.
// Pulls per-phase counts (import, export) and the commit/push status
// out of the embedded RunResult.
func printSyncRunResult(w io.Writer, r syncRunResult) {
	if r.RunResult == nil {
		return
	}
	fmt.Fprintln(w, "bacio sync complete")
	if r.Import != nil {
		fmt.Fprintf(w, "  imported: inserted=%d updated=%d noop=%d\n",
			r.Import.Inserted, r.Import.Updated, r.Import.NoOp)
		if len(r.Import.Renumbered) > 0 {
			fmt.Fprintln(w, "  renumbered:")
			for _, e := range r.Import.Renumbered {
				fmt.Fprintf(w, "    %s-%d -> %s-%d\n", e.Prefix, e.OldNumber, e.Prefix, e.NewNumber)
			}
		}
		if len(r.Import.Renamed) > 0 {
			fmt.Fprintln(w, "  renamed:")
			for _, e := range r.Import.Renamed {
				fmt.Fprintf(w, "    %s %s -> %s\n", e.Kind, e.Old, e.New)
			}
		}
		if len(r.Import.Deleted) > 0 {
			fmt.Fprintf(w, "  deleted: %d\n", len(r.Import.Deleted))
		}
	}
	if r.Export != nil {
		fmt.Fprintf(w, "  exported: renames=%d writes=%d deletes=%d\n",
			r.Export.Renames, r.Export.Writes, r.Export.Deletes)
	}
	if r.Commit != "" {
		fmt.Fprintf(w, "  commit:   %s\n", r.Commit)
	} else {
		fmt.Fprintln(w, "  commit:   (no changes)")
	}
	if r.Pushed {
		fmt.Fprintln(w, "  pushed:   yes")
	}
	for _, msg := range r.Warnings {
		fmt.Fprintf(w, "  warning:  %s\n", msg)
	}
}

// printSyncInitResult renders the result of `bacio sync init`. The
// emphasis is on confirming what was set up (path / remote / commit)
// rather than re-spamming the export counts.
func printSyncInitResult(w io.Writer, r syncInitResult) {
	if r.InitResult == nil {
		return
	}
	fmt.Fprintf(w, "Initialised sync repo at %s\n", r.LocalPath)
	if r.Remote != "" {
		fmt.Fprintf(w, "  remote:   %s\n", r.Remote)
	}
	if r.Export != nil {
		fmt.Fprintf(w, "  exported: %d files (%d bytes)\n", r.Export.Files, r.Export.BytesWritten)
	}
	if r.CommitSHA != "" {
		fmt.Fprintf(w, "  commit:   %s\n", r.CommitSHA)
	}
	if r.Pushed {
		fmt.Fprintln(w, "  pushed:   yes")
	}
}

// printSyncCloneResult renders the result of `bacio sync clone`. When a
// preview is present (collisions detected without --allow-renumber),
// surface the projected renumbers / renames so the user can decide
// whether to re-run with --allow-renumber.
func printSyncCloneResult(w io.Writer, r syncCloneResult) {
	if r.CloneResult == nil {
		return
	}
	fmt.Fprintf(w, "Sync repo at %s\n", r.LocalPath)
	if r.Remote != "" {
		fmt.Fprintf(w, "  remote:   %s\n", r.Remote)
	}
	if r.Import != nil {
		fmt.Fprintf(w, "  imported: inserted=%d updated=%d noop=%d\n",
			r.Import.Inserted, r.Import.Updated, r.Import.NoOp)
	}
	if r.PreviewCollisions != nil {
		if len(r.PreviewCollisions.Renumbered) > 0 {
			fmt.Fprintln(w, "  would renumber:")
			for _, e := range r.PreviewCollisions.Renumbered {
				fmt.Fprintf(w, "    %s-%d -> %s-%d\n", e.Prefix, e.OldNumber, e.Prefix, e.NewNumber)
			}
		}
		if len(r.PreviewCollisions.Renamed) > 0 {
			fmt.Fprintln(w, "  would rename:")
			for _, e := range r.PreviewCollisions.Renamed {
				fmt.Fprintf(w, "    %s %s -> %s\n", e.Kind, e.Old, e.New)
			}
		}
	}
}

// printSyncVerifyResult renders the human-readable verify report. The
// emphasis is on grouping by Kind so a user spotting "the bad bits"
// can scan top-to-bottom rather than diffing a wall of mixed output.
// JSON output (-o json) leaves the full structured form to the
// emitter.
func printSyncVerifyResult(w io.Writer, r syncVerifyResult) {
	if r.VerifyResult == nil {
		return
	}
	fmt.Fprintf(w, "Verifying sync repo at %s\n", r.SyncRepo)
	fmt.Fprintf(w, "  repos:     %d\n", r.Repos)
	fmt.Fprintf(w, "  features:  %d\n", r.Features)
	fmt.Fprintf(w, "  issues:    %d\n", r.Issues)
	fmt.Fprintf(w, "  comments:  %d\n", r.Comments)
	fmt.Fprintf(w, "  documents: %d\n", r.Documents)
	if len(r.Errors) == 0 && len(r.Warnings) == 0 {
		fmt.Fprintln(w, "OK: no findings.")
		return
	}
	if len(r.Errors) > 0 {
		fmt.Fprintf(w, "Errors (%d):\n", len(r.Errors))
		printVerifyIssues(w, r.Errors)
	}
	if len(r.Warnings) > 0 {
		fmt.Fprintf(w, "Warnings (%d):\n", len(r.Warnings))
		printVerifyIssues(w, r.Warnings)
	}
}

func printVerifyIssues(w io.Writer, issues []sync.VerifyIssue) {
	// Issues come pre-sorted by Kind; group runs of the same Kind
	// under one heading so the output stays scannable.
	if len(issues) == 0 {
		return
	}
	currentKind := ""
	for _, e := range issues {
		if e.Kind != currentKind {
			fmt.Fprintf(w, "  [%s]\n", e.Kind)
			currentKind = e.Kind
		}
		if e.Path != "" {
			fmt.Fprintf(w, "    %s: %s\n", e.Path, e.Detail)
		} else {
			fmt.Fprintf(w, "    %s\n", e.Detail)
		}
		for _, rel := range e.Related {
			fmt.Fprintf(w, "      related: %s\n", rel)
		}
	}
}

// printSyncInspectResult dispatches to the right per-target renderer.
// One of the four pointers is non-nil; if all four are nil (caller
// bug), we just print nothing rather than panicking.
func printSyncInspectResult(w io.Writer, r syncInspectResult) {
	if r.InspectResult == nil {
		return
	}
	switch {
	case r.RepoSummary != nil:
		printInspectRepoSummary(w, r.Prefix, r.RepoSummary)
	case r.Issue != nil:
		printInspectIssue(w, r.Prefix, r.Issue)
	case r.Feature != nil:
		printInspectFeature(w, r.Prefix, r.Feature)
	case r.Document != nil:
		printInspectDocument(w, r.Prefix, r.Document)
	}
}

func printInspectRepoSummary(w io.Writer, prefix string, sum *sync.InspectRepoSummary) {
	if sum.Repo != nil {
		fmt.Fprintf(w, "%s  %s\n", sum.Repo.Prefix, sum.Repo.Name)
		fmt.Fprintf(w, "  uuid:        %s\n", sum.Repo.UUID)
		if sum.Repo.RemoteURL != "" {
			fmt.Fprintf(w, "  remote:      %s\n", sum.Repo.RemoteURL)
		}
		fmt.Fprintf(w, "  next issue:  %s-%d\n", sum.Repo.Prefix, sum.Repo.NextIssueNumber)
	} else {
		fmt.Fprintf(w, "%s\n", prefix)
	}
	fmt.Fprintf(w, "  issues:      %d\n", sum.Issues)
	fmt.Fprintf(w, "  features:    %d\n", sum.Features)
	fmt.Fprintf(w, "  documents:   %d\n", sum.Documents)
	fmt.Fprintf(w, "  comments:    %d\n", sum.Comments)
	if len(sum.RecentRedirects) > 0 {
		fmt.Fprintf(w, "  recent renames/renumbers (%d):\n", len(sum.RecentRedirects))
		for _, r := range sum.RecentRedirects {
			fmt.Fprintf(w, "    %s  %s: %s -> %s  (%s)\n",
				r.ChangedAt.UTC().Format("2006-01-02"), r.Kind, r.Old, r.New, r.Reason)
		}
	}
}

func printInspectIssue(w io.Writer, prefix string, ir *sync.InspectIssue) {
	is := ir.Issue
	fmt.Fprintf(w, "%s-%d  %s\n", prefix, is.Number, is.Title)
	fmt.Fprintf(w, "  uuid:      %s\n", is.UUID)
	fmt.Fprintf(w, "  state:     %s\n", is.State)
	if is.Assignee != "" {
		fmt.Fprintf(w, "  assignee:  %s\n", is.Assignee)
	}
	if is.Feature != nil && is.Feature.Label != "" {
		fmt.Fprintf(w, "  feature:   %s (uuid=%s)\n", is.Feature.Label, is.Feature.UUID)
	}
	if len(is.Tags) > 0 {
		fmt.Fprintf(w, "  tags:      %s\n", strings.Join(is.Tags, ", "))
	}
	if len(is.PRs) > 0 {
		fmt.Fprintln(w, "  prs:")
		for _, p := range is.PRs {
			fmt.Fprintf(w, "    %s\n", p)
		}
	}
	if len(is.Relations.Blocks)+len(is.Relations.RelatesTo)+len(is.Relations.DuplicateOf) > 0 {
		fmt.Fprintln(w, "  relations:")
		printInspectRefs(w, "blocks", is.Relations.Blocks)
		printInspectRefs(w, "relates_to", is.Relations.RelatesTo)
		printInspectRefs(w, "duplicate_of", is.Relations.DuplicateOf)
	}
	fmt.Fprintf(w, "  created:   %s\n", is.CreatedAt.UTC().Format(time.RFC3339))
	fmt.Fprintf(w, "  updated:   %s\n", is.UpdatedAt.UTC().Format(time.RFC3339))
	if ir.Description != "" {
		fmt.Fprintln(w)
		fmt.Fprint(w, ir.Description)
		if !strings.HasSuffix(ir.Description, "\n") {
			fmt.Fprintln(w)
		}
	}
	if len(ir.Comments) > 0 {
		fmt.Fprintln(w)
		fmt.Fprintf(w, "Comments (%d):\n", len(ir.Comments))
		fmt.Fprintln(w, strings.Repeat("-", 40))
		for _, c := range ir.Comments {
			fmt.Fprintf(w, "%s — %s\n%s\n\n", c.Comment.Author, c.Comment.CreatedAt.UTC().Format(time.RFC3339), c.Body)
		}
	}
}

func printInspectRefs(w io.Writer, label string, refs []sync.ParsedRef) {
	if len(refs) == 0 {
		return
	}
	fmt.Fprintf(w, "    %s:\n", label)
	for _, r := range refs {
		fmt.Fprintf(w, "      %s (uuid=%s)\n", r.Label, r.UUID)
	}
}

func printInspectFeature(w io.Writer, prefix string, fr *sync.InspectFeature) {
	f := fr.Feature
	fmt.Fprintf(w, "%s/%s  %s\n", prefix, f.Slug, f.Title)
	fmt.Fprintf(w, "  uuid:      %s\n", f.UUID)
	fmt.Fprintf(w, "  created:   %s\n", f.CreatedAt.UTC().Format(time.RFC3339))
	fmt.Fprintf(w, "  updated:   %s\n", f.UpdatedAt.UTC().Format(time.RFC3339))
	if fr.Description != "" {
		fmt.Fprintln(w)
		fmt.Fprint(w, fr.Description)
		if !strings.HasSuffix(fr.Description, "\n") {
			fmt.Fprintln(w)
		}
	}
}

func printInspectDocument(w io.Writer, prefix string, dr *sync.InspectDocument) {
	d := dr.Document
	fmt.Fprintf(w, "%s/docs/%s\n", prefix, d.Filename)
	fmt.Fprintf(w, "  uuid:        %s\n", d.UUID)
	fmt.Fprintf(w, "  type:        %s\n", d.Type)
	if d.SourcePath != "" {
		fmt.Fprintf(w, "  source_path: %s\n", d.SourcePath)
	}
	if len(d.Links) > 0 {
		fmt.Fprintln(w, "  links:")
		for _, l := range d.Links {
			fmt.Fprintf(w, "    %s -> %s (uuid=%s)\n", l.Kind, l.TargetLabel, l.TargetUUID)
		}
	}
	fmt.Fprintf(w, "  created:     %s\n", d.CreatedAt.UTC().Format(time.RFC3339))
	fmt.Fprintf(w, "  updated:     %s\n", d.UpdatedAt.UTC().Format(time.RFC3339))
	if dr.Content != "" {
		fmt.Fprintln(w)
		fmt.Fprint(w, dr.Content)
		if !strings.HasSuffix(dr.Content, "\n") {
			fmt.Fprintln(w)
		}
	}
}
