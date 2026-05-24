package cli

import (
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/mrgeoffrich/bacio/internal/sync"
)

// printSyncRunResult renders the result of a steady-state `bacio sync`.
// Pulls per-phase counts (import, export) and the commit/push status
// out of the embedded RunResult.
func printSyncRunResult(w io.Writer, r syncRunResult) {
	if r.RunResult == nil {
		return
	}
	fmt.Fprintln(w, "bacio sync complete")
	if r.Import != nil {
		fmt.Fprintf(w, "  imported: inserted=%d updated=%d noop=%d",
			r.Import.Inserted, r.Import.Updated, r.Import.NoOp)
		if r.Import.Skipped > 0 {
			fmt.Fprintf(w, " skipped=%d", r.Import.Skipped)
		}
		fmt.Fprintln(w)
		if len(r.Import.SkippedStale) > 0 {
			fmt.Fprintln(w, "  skipped (local newer than remote):")
			for _, e := range r.Import.SkippedStale {
				label := e.Label
				if label == "" {
					label = e.UUID
				}
				fmt.Fprintf(w, "    %s %s  local=%s remote=%s\n",
					e.Kind, label, e.LocalUpdated, e.RemoteUpdated)
			}
		}
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
		fmt.Fprintf(w, "  imported: inserted=%d updated=%d noop=%d",
			r.Import.Inserted, r.Import.Updated, r.Import.NoOp)
		if r.Import.Skipped > 0 {
			fmt.Fprintf(w, " skipped=%d", r.Import.Skipped)
		}
		fmt.Fprintln(w)
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

// printSyncRemotesResult renders the per-machine sync-repo registry as
// one block per entry. Lean text — matches the per-line `  key: value`
// style of printSyncRunResult and friends. JSON callers get the full
// shape via emit's -o json branch; this is only the human path.
//
// Per-row branches:
//   - empty registry → "(no sync remotes registered)".
//   - clone_present == false → "clone: <path> (missing)", skip projects.
//   - last_sync_error != nil → "error: <msg>" instead of "last sync:".
func printSyncRemotesResult(w io.Writer, r syncRemotesResult) {
	if len(r.Remotes) == 0 {
		fmt.Fprintln(w, "(no sync remotes registered)")
		return
	}
	for i, e := range r.Remotes {
		if i > 0 {
			fmt.Fprintln(w)
		}
		fmt.Fprintf(w, "%s  (label: %s)\n", e.RemoteURL, e.Label)
		if e.ClonePresent {
			fmt.Fprintf(w, "  local:        %s\n", e.LocalPath)
		} else {
			fmt.Fprintf(w, "  clone:        %s (missing)\n", e.LocalPath)
		}
		fmt.Fprintf(w, "  cloned:       %s\n", e.ClonedAt.UTC().Format(time.RFC3339))
		switch {
		case e.LastSyncError != nil:
			fmt.Fprintf(w, "  error:        %s\n", *e.LastSyncError)
		case e.LastSyncAt != nil:
			fmt.Fprintf(w, "  last sync:    %s\n", e.LastSyncAt.UTC().Format(time.RFC3339))
		}
		if !e.ClonePresent {
			continue
		}
		fmt.Fprintf(w, "  projects:     %d\n", len(e.Projects))
		for _, p := range e.Projects {
			fmt.Fprintf(w, "    %-6s %s\n", p.Prefix, p.Status)
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
