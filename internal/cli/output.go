package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/mrgeoffrich/bacio/internal/client"
	"github.com/mrgeoffrich/bacio/internal/model"
	"github.com/mrgeoffrich/bacio/internal/store"
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
		// `kind` is part of the JSON contract already (model.Repo carries
		// it with no omitempty). Surface it in the human read too, and
		// show a workspace's pathlessness as the deliberate state it is
		// rather than a suspicious blank column.
		for _, r := range x {
			fmt.Fprintf(w, "%-6s %-9s %s\t%s\n", r.Prefix, r.Kind, r.Name, repoPathLabel(r))
		}
	case []*docFolderRow:
		for _, f := range x {
			fmt.Fprintf(w, "%-40s %s\n", f.Path, f.UUID)
		}
	case *docFolderRow:
		fmt.Fprintf(w, "Path:     %s\n", x.Path)
		fmt.Fprintf(w, "Name:     %s\n", x.Name)
		fmt.Fprintf(w, "UUID:     %s\n", x.UUID)
		fmt.Fprintf(w, "Position: %d\n", x.Position)
	case *client.DocFolderDeletePreview:
		fmt.Fprintf(w, "Folder:            %s\n", x.Path)
		fmt.Fprintf(w, "Subfolders:        %d (deleted with it)\n", x.Cascade.Subfolders)
		fmt.Fprintf(w, "Documents:         %d (re-rooted, never deleted)\n", x.Cascade.DocumentsReRooted)
	case []*kanbanLaneRow:
		for _, c := range x {
			fmt.Fprintf(w, "%-3d %-24s %d card(s)\n", c.Position, c.Name, c.Cards)
		}
	case *kanbanColumnRow:
		fmt.Fprintf(w, "Name:     %s\n", x.Name)
		fmt.Fprintf(w, "UUID:     %s\n", x.UUID)
		fmt.Fprintf(w, "Position: %d\n", x.Position)
	case *client.KanbanColumnDeletePreview:
		fmt.Fprintf(w, "Lane:              %s (position %d)\n", x.Column.Name, x.Column.Position)
		fmt.Fprintf(w, "Cards off-boarded: %d (the issues themselves are kept)\n", x.Cascade.IssuesRemovedFromBoard)
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
	case *prCreateResult:
		// BACI-163: human path is the new PR URL + the injected label
		// + any pre-flight warnings we surfaced above (e.g. a forced
		// override or a CLOSED-only allow-with-warning).
		for _, ws := range x.Warnings {
			fmt.Fprintln(w, "warning:", ws)
		}
		if x.PullRequest != nil {
			fmt.Fprintf(w, "opened %s\n", x.PullRequest.URL)
			fmt.Fprintf(w, "  labelled: %s\n", x.Label)
		}
	case *prCreatePreview:
		fmt.Fprintf(w, "issue:    %s\n", x.IssueKey)
		fmt.Fprintf(w, "label:    %s\n", x.Label)
		fmt.Fprintf(w, "would run: %s\n", strings.Join(x.ProjectedGHArgv, " "))
		for _, r := range x.PreflightRefusals {
			fmt.Fprintln(w, "refusal:", r)
		}
		for _, ws := range x.PreflightWarnings {
			fmt.Fprintln(w, "warning:", ws)
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
	case []*model.ProxyFQDNStat:
		printProxyStats(w, x)
	case []*model.ProxyMessageMatch:
		printProxyMatches(w, x)
	case *model.ProxyMessage:
		printProxyMessage(w, x)
	case *model.AnthropicTranscript:
		printAnthropicTranscript(w, x)
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
	case syncRemotesResult:
		printSyncRemotesResult(w, x)
	case demoSeedResult:
		fmt.Fprintf(w, "Seeded demo repo %s (%s) at %s\n", x.Repo.Prefix, x.Repo.Name, x.Repo.Path)
		fmt.Fprintf(w, "  copies:    %d\n", x.Copies)
		fmt.Fprintf(w, "  features:  %d\n", x.Features)
		fmt.Fprintf(w, "  issues:    %d\n", x.Issues)
		fmt.Fprintf(w, "  comments:  %d\n", x.Comments)
		fmt.Fprintf(w, "  documents: %d\n", x.Documents)
		fmt.Fprintf(w, "\nTry: bacio tui --repo %s\n", x.Repo.Prefix)
		fmt.Fprintln(w, "     bacio issue list --all-repos")
	case message:
		fmt.Fprintln(w, x.Text)
	case *model.AgentSession:
		printAgentSession(w, x)
	case *model.AgentClaim:
		printAgentClaim(w, x)
	case *model.AgentDispatch:
		printAgentDispatch(w, x)
	case []*model.AgentDispatch:
		printAgentDispatchList(w, x)
	case *promptTemplateView:
		printPromptTemplate(w, x)
	case []*promptTemplateSummary:
		for _, t := range x {
			printPromptTemplateSummary(w, t)
		}
	case store.ArchiveSweepResult:
		fmt.Fprintf(w, "Archived issues:    %d\n", x.IssuesArchived)
		fmt.Fprintf(w, "Archived features:  %d\n", x.FeaturesArchived)
		fmt.Fprintf(w, "Archived documents: %d\n", x.DocumentsArchived)
		if x.Total() == 0 {
			fmt.Fprintln(w, "Nothing to archive.")
		}
	case *worktreeInitResult:
		return printWorktreeInit(w, x)
	case *worktreeRmResult:
		return printWorktreeRm(w, x, opts.dryRun)
	case showArchivedResult:
		if x.ShowArchived {
			fmt.Fprintln(w, "show_archived: on")
		} else {
			fmt.Fprintln(w, "show_archived: off")
		}
	case syncBackgroundResult:
		if x.BackgroundEnabled {
			fmt.Fprintln(w, "sync.background_enabled: on")
		} else {
			fmt.Fprintln(w, "sync.background_enabled: off")
		}
	default:
		fmt.Fprintf(w, "%v\n", v)
	}
	return nil
}

func printPromptTemplate(w io.Writer, t *promptTemplateView) {
	origin := "custom"
	if t.IsBuiltin {
		origin = "built-in"
		if t.IsDefault {
			origin = "built-in (default body)"
		}
	}
	fmt.Fprintf(w, "%-16s %s  (%s)\n", t.Slug, t.Label, origin)
	fmt.Fprintf(w, "\n%s\n", t.Body)
	if t.IsBuiltin && !t.IsDefault {
		fmt.Fprintf(w, "\nDefault:\n%s\n", t.Default)
	}
}

func printPromptTemplateSummary(w io.Writer, t *promptTemplateSummary) {
	origin := "custom"
	if t.IsBuiltin {
		origin = "built-in"
		if t.IsDefault {
			origin = "built-in (default body)"
		}
	}
	fmt.Fprintf(w, "%-16s %-20s (%s)  %s\n", t.Slug, t.Label, origin, t.Body)
}

// repoPathLabel renders a repo's path for the human read. An empty path
// is not an error or a gap — it means one of two very different things,
// and naming which one saves a user hunting for a checkout that will
// never exist.
func repoPathLabel(r *model.Repo) string {
	switch {
	case r.HasWorkingTree():
		return r.Path
	case r.IsWorkspace():
		return "(workspace — no working tree)"
	default:
		return "(phantom — not linked on this machine)"
	}
}

func printRepo(w io.Writer, r *model.Repo) error {
	fmt.Fprintf(w, "Prefix:    %s\n", r.Prefix)
	fmt.Fprintf(w, "Name:      %s\n", r.Name)
	fmt.Fprintf(w, "Kind:      %s\n", r.Kind)
	fmt.Fprintf(w, "Path:      %s\n", repoPathLabel(r))
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
	Claimants    []*model.AgentClaim   `json:"claimants"`
	Taken        bool                  `json:"taken"`
	// LatestPlan (BACI-216) — mirrors client.IssueView.LatestPlan.
	LatestPlan *model.LatestPlan `json:"latest_plan,omitempty"`
}

type featureView struct {
	Feature   *model.Feature          `json:"feature"`
	Issues    []*model.Issue          `json:"issues"`
	Documents []*model.DocumentLink   `json:"documents"`
	Comments  []*model.FeatureComment `json:"comments"`
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
	if len(v.Claimants) > 0 {
		fmt.Fprintln(w)
		fmt.Fprintln(w, "Claimed by:")
		printClaimants(w, v.Claimants)
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

// printClaimants renders the per-issue agent-claim history: one line per
// claim, open ones marked "open", released ones marked "released", each
// with the prompt the session ran (when there is one).
func printClaimants(w io.Writer, claimants []*model.AgentClaim) {
	for _, c := range claimants {
		shortSess := c.SessionID
		if len(shortSess) > 12 {
			shortSess = shortSess[:12]
		}
		who := c.AgentName
		if who == "" {
			who = shortSess
		}
		status := "open"
		if c.ReleasedAt != nil {
			status = "released"
		}
		fmt.Fprintf(w, "  %s (%s) — claimed %s [%s]\n", who, shortSess, localTime(c.ClaimedAt), status)
		if c.Prompt != "" {
			fmt.Fprintf(w, "    prompt: %s\n", claimPromptOneLine(c.Prompt))
		}
	}
}

// claimPromptOneLine collapses a multi-line claim prompt to a single
// line for the issue-show "Claimed by:" section, capping the length.
func claimPromptOneLine(s string) string {
	s = strings.ReplaceAll(s, "\n", " ")
	s = strings.ReplaceAll(s, "\t", " ")
	s = strings.Join(strings.Fields(s), " ")
	if len(s) > 120 {
		s = s[:117] + "..."
	}
	return s
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

// printProxyStats renders the BACI-303 per-FQDN rollup as an aligned
// table, busiest host first. JSON output (the parse contract) is
// untouched — this is the human read.
func printProxyStats(w io.Writer, stats []*model.ProxyFQDNStat) {
	if len(stats) == 0 {
		fmt.Fprintln(w, "no proxy traffic captured")
		return
	}
	fmt.Fprintf(w, "%-32s %8s %7s %8s %8s %10s %s\n",
		"HOST", "REQUESTS", "ERR%", "P50ms", "P95ms", "BYTES↑↓", "LAST SEEN")
	for _, s := range stats {
		fmt.Fprintf(w, "%-32s %8d %6.0f%% %8d %8d %10s %s\n",
			s.Host, s.RequestCount, s.ErrorRate*100, s.P50MS, s.P95MS,
			fmt.Sprintf("%d/%d", s.BytesIn, s.BytesOut), localTime(s.LastSeen))
	}
}

// printProxyMatches renders the BACI-320 content-search hits as an aligned
// CAPTURE / DISP / ROLE / BLOCK / MATCH table — the search→drill-in surface, so
// the CAPTURE id is what the reader feeds to `proxy capture <id>`. A dispatch-less
// match shows "-" for DISP. JSON output (the parse contract) carries the full
// rows including the session/agent correlation.
func printProxyMatches(w io.Writer, matches []*model.ProxyMessageMatch) {
	if len(matches) == 0 {
		fmt.Fprintln(w, "no matching captures")
		return
	}
	fmt.Fprintf(w, "%-8s %-6s %-10s %-12s %s\n", "CAPTURE", "DISP", "ROLE", "BLOCK", "MATCH")
	for _, m := range matches {
		disp := "-"
		if m.DispatchID != nil {
			disp = fmt.Sprintf("%d", *m.DispatchID)
		}
		fmt.Fprintf(w, "%-8d %-6s %-10s %-12s %s\n",
			m.ProxyRequestID, disp, m.Role, m.Block, m.Snippet)
	}
}

// printProxyMessage renders one parsed Anthropic capture (BACI-306): the model,
// classification, usage, and the reconstructed assistant turn's blocks. JSON
// output (the parse contract) carries the full shape; this is the human read.
func printProxyMessage(w io.Writer, m *model.ProxyMessage) {
	kind := "auxiliary"
	if m.IsPrimary {
		kind = "primary"
	}
	fmt.Fprintf(w, "Capture:  %d (proxy_request %d, %s)\n", m.ID, m.ProxyRequestID, kind)
	fmt.Fprintf(w, "Model:    %s\n", m.Model)
	if m.DispatchID != nil {
		fmt.Fprintf(w, "Dispatch: %d\n", *m.DispatchID)
	}
	if m.StopReason != "" {
		fmt.Fprintf(w, "Stop:     %s\n", m.StopReason)
	}
	fmt.Fprintf(w, "Usage:    in=%d out=%d cache_read=%d thinking=%d\n",
		m.Usage.InputTokens, m.Usage.OutputTokens, m.Usage.CacheReadInputTokens, m.Usage.ThinkingTokens)
	printTurnBlocks(w, m.TurnJSON)
}

// printAnthropicTranscript renders an assembled per-job transcript (BACI-306):
// the summed usage and the ordered messages, one line per content block. JSON
// output carries the full nested shape.
func printAnthropicTranscript(w io.Writer, tr *model.AnthropicTranscript) {
	if tr.DispatchID != nil {
		fmt.Fprintf(w, "Dispatch:   %d\n", *tr.DispatchID)
	}
	fmt.Fprintf(w, "Model:      %s\n", tr.Model)
	fmt.Fprintf(w, "Usage:      in=%d out=%d cache_read=%d thinking=%d\n",
		tr.Usage.InputTokens, tr.Usage.OutputTokens, tr.Usage.CacheReadInputTokens, tr.Usage.ThinkingTokens)
	fmt.Fprintf(w, "Messages:   %d  (auxiliary turns: %d)\n", len(tr.Messages), len(tr.Auxiliary))
	for _, msg := range tr.Messages {
		fmt.Fprintln(w, strings.Repeat("-", 40))
		fmt.Fprintf(w, "[%s]\n", msg.Role)
		for _, b := range msg.Content {
			printAnthropicBlock(w, b)
		}
	}
}

// printTurnBlocks renders the assistant turn stored as turn_json. A truncated
// (non-JSON) body is shown verbatim so the marker is visible.
func printTurnBlocks(w io.Writer, turnJSON string) {
	var turn model.AnthropicTurn
	if err := json.Unmarshal([]byte(turnJSON), &turn); err != nil {
		fmt.Fprintf(w, "\n%s\n", turnJSON)
		return
	}
	fmt.Fprintln(w)
	for _, b := range turn.Blocks {
		printAnthropicBlock(w, b)
	}
}

// printAnthropicBlock renders one content block as a single labelled line,
// truncating long bodies for the human read (JSON output keeps the full text).
func printAnthropicBlock(w io.Writer, b model.AnthropicBlock) {
	switch b.Type {
	case "text":
		fmt.Fprintf(w, "  text: %s\n", oneLine(b.Text))
	case "thinking":
		fmt.Fprintf(w, "  thinking: %s\n", oneLine(b.Thinking))
	case "tool_use":
		fmt.Fprintf(w, "  tool_use %s(%s): %s\n", b.Name, b.ID, oneLine(string(b.Input)))
	case "tool_result":
		fmt.Fprintf(w, "  tool_result (%s): %s\n", b.ToolUseID, oneLine(string(b.Content)))
	default:
		fmt.Fprintf(w, "  %s\n", b.Type)
	}
}

// oneLine collapses whitespace and caps a block body for the human read.
func oneLine(s string) string {
	s = strings.Join(strings.Fields(s), " ")
	if len(s) > 160 {
		s = s[:157] + "..."
	}
	return s
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

func printAgentSession(w io.Writer, s *model.AgentSession) {
	fmt.Fprintf(w, "Session:  %s\n", s.SessionID)
	if s.AgentName != "" {
		fmt.Fprintf(w, "Agent:    %s\n", s.AgentName)
	}
	fmt.Fprintf(w, "Actor:    %s\n", s.Actor)
	if s.RepoPrefix != "" {
		fmt.Fprintf(w, "Repo:     %s\n", s.RepoPrefix)
	}
	if s.Model != "" {
		fmt.Fprintf(w, "Model:    %s\n", s.Model)
	}
	if s.Branch != "" {
		fmt.Fprintf(w, "Branch:   %s\n", s.Branch)
	}
	fmt.Fprintf(w, "LastSeen: %s\n", localTime(s.LastSeenAt))
	if s.EndedAt != nil {
		fmt.Fprintf(w, "Ended:    %s (reason=%s)\n", localTime(*s.EndedAt), s.EndReason)
	}
}

func printAgentClaim(w io.Writer, c *model.AgentClaim) {
	fmt.Fprintf(w, "Session:  %s\n", c.SessionID)
	fmt.Fprintf(w, "Issue:    %s\n", c.IssueKey)
	fmt.Fprintf(w, "Claimed:  %s\n", localTime(c.ClaimedAt))
	if c.ReleasedAt != nil {
		fmt.Fprintf(w, "Released: %s\n", localTime(*c.ReleasedAt))
	}
}

func printAgentDispatch(w io.Writer, d *model.AgentDispatch) {
	fmt.Fprintf(w, "Dispatch: %d\n", d.ID)
	fmt.Fprintf(w, "Status:   %s\n", d.Status)
	if d.RepoPrefix != "" {
		fmt.Fprintf(w, "Repo:     %s\n", d.RepoPrefix)
	}
	if d.TargetAgentName != "" {
		fmt.Fprintf(w, "To agent: %s\n", d.TargetAgentName)
	}
	if d.TargetSessionID != "" {
		fmt.Fprintf(w, "To sess:  %s\n", d.TargetSessionID)
	}
	if d.IssueKey != "" {
		fmt.Fprintf(w, "Issue:    %s\n", d.IssueKey)
	}
	if d.Mode != "" {
		fmt.Fprintf(w, "Mode:     %s\n", d.Mode)
	}
	fmt.Fprintf(w, "By:       %s\n", d.CreatedBy)
	fmt.Fprintf(w, "Created:  %s\n", localTime(d.CreatedAt))
	if d.DeliveredAt != nil {
		fmt.Fprintf(w, "Delivered:%s\n", localTime(*d.DeliveredAt))
	}
	if d.AckedAt != nil {
		fmt.Fprintf(w, "Acked:    %s\n", localTime(*d.AckedAt))
	}
	if d.Payload != "" {
		fmt.Fprintf(w, "\n%s\n", d.Payload)
	}
	if d.AckNote != "" {
		fmt.Fprintf(w, "\nAck note: %s\n", d.AckNote)
	}
}

func printAgentDispatchList(w io.Writer, ds []*model.AgentDispatch) {
	if len(ds) == 0 {
		fmt.Fprintln(w, "(no dispatches)")
		return
	}
	for _, d := range ds {
		target := d.TargetAgentName
		if target == "" {
			target = shortID(d.TargetSessionID)
		}
		issue := d.IssueKey
		if issue == "" {
			issue = "-"
		}
		fmt.Fprintf(w, "#%-4d %-10s %-24s %-12s %s\n",
			d.ID, d.Status, target, issue, firstLine(d.Payload))
	}
}

// firstLine returns the first line of s, trimmed — keeps list rows to
// one line even when a dispatch payload is a multi-line instruction.
func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return strings.TrimSpace(s[:i])
	}
	return strings.TrimSpace(s)
}
