package main

import (
	"context"
	"fmt"
	"time"

	"github.com/mrgeoffrich/bacio/internal/cli/inputs"
	"github.com/mrgeoffrich/bacio/internal/client"
	"github.com/mrgeoffrich/bacio/internal/model"
)

// FeatureSummary is one feature, shaped for the desktop feature list.
//
// Emoji (BACI-184) carries the per-feature glyph so the Features panel
// list can render it alongside the title — same glyph BACI-172 paints
// on every kanban card. Empty when no emoji has been set.
//
// HiddenOnBoard (BACI-177) mirrors the per-feature "Show on board"
// toggle on the Features screen — when true, every kanban card
// belonging to this feature is hidden from the board on this machine.
// Lives in the per-repo board-hide KV (tui_settings), not on the
// features row.
type FeatureSummary struct {
	Slug  string `json:"slug"`
	Title string `json:"title"`
	Emoji string `json:"emoji"`
	// State (BACI-199) is the three-state column on the feature row
	// — `active` (default — work in flight), `done` (delivered) or
	// `cancelled` (abandoned). The Features panel renders a state
	// pill so a glance distinguishes work in flight from delivered /
	// abandoned work.
	State string `json:"state"`
	// BranchName (BACI-231) is the per-feature integration branch
	// (e.g. "feat/auth"). Empty string when the feature ships
	// straight to main (the legacy default). The FeatureRow renders a
	// branch chip when this is truthy; the detail pane carries the
	// editable value. Always present in JSON (empty when null) to
	// match the rest of the surfaced fields.
	BranchName    string    `json:"branchName"`
	UpdatedAt     time.Time `json:"updatedAt"`
	HiddenOnBoard bool      `json:"hiddenOnBoard"`
}

// FeatureLinkedIssue is one issue grouped under a feature, for the detail pane.
type FeatureLinkedIssue struct {
	Key        string `json:"key"`
	Title      string `json:"title"`
	State      string `json:"state"`
	StateLabel string `json:"stateLabel"`
}

// FeatureComment mirrors model.FeatureComment, shaped for the desktop
// feature detail pane (BACI-124).
type FeatureComment struct {
	UUID      string    `json:"uuid"`
	Author    string    `json:"author"`
	Body      string    `json:"body"`
	CreatedAt time.Time `json:"createdAt"`
}

// FeatureLinkedDoc is one document linked to this feature via
// `bacio doc link <file> <feature-slug>`, shaped for the desktop /
// web feature detail pane (BACI-214). Filename + Type drive the
// per-row chrome; Description carries the link-time `--why` (which is
// model.DocumentLink.Description, not the doc's own description).
// SourcePath is reserved for parity with the issue-workspace
// LinkedDocPanel shape but stays empty here — `view.Documents` is a
// link slice, not a doc slice, so resolving the source path would
// cost an extra round-trip per doc and isn't surfaced on the feature
// pane today. Deliberately narrower than the issue brief's LinkedDoc
// (no `linkedVia` / no `sizeBytes`): the feature pane is single-source
// of truth for "linked to this feature" so the disambiguating chips
// the issue panel renders don't apply.
type FeatureLinkedDoc struct {
	Filename    string `json:"filename"`
	Type        string `json:"type"`
	Description string `json:"description"`
	SourcePath  string `json:"sourcePath"`
}

// FeatureDetail is one feature with its description and linked issues — the
// payload for the desktop feature detail pane.
type FeatureDetail struct {
	Slug        string `json:"slug"`
	Title       string `json:"title"`
	Description string `json:"description"`
	// Emoji (BACI-172) is the per-feature glyph rendered in the
	// top-left of every kanban card belonging to this feature.
	// Empty when none has been set — the FeaturesView surfaces a
	// "set emoji" affordance in that case.
	Emoji string `json:"emoji"`
	// BranchName (BACI-231) is the per-feature integration branch
	// the detail pane's "Integration branch" Properties row reads
	// from / writes to. Empty string when the feature ships straight
	// to main. Always present in JSON to match Emoji's shape.
	BranchName string `json:"branchName"`
	// State + StateManual (BACI-199) round-trip the per-feature state
	// column and its sticky bit. The drawer's segmented control reads
	// State to highlight the active button, and StateManual to render
	// a "pinned" indicator next to it so the user knows the
	// auto-completion sweep won't move it.
	State       string               `json:"state"`
	StateManual bool                 `json:"stateManual"`
	// CollectHandoffs (BACI-333) mirrors the per-feature collect-handoffs
	// toggle. ON (the default) collects worker close-out handoff comments;
	// OFF silences a standing bucket. The drawer's segmented control reads
	// it the same way as StateManual / Auto Close.
	CollectHandoffs bool                 `json:"collectHandoffs"`
	CreatedAt       time.Time            `json:"createdAt"`
	UpdatedAt   time.Time            `json:"updatedAt"`
	Issues      []FeatureLinkedIssue `json:"issues"`
	// Comments is the BACI-124 chronological-handoff timeline, oldest
	// first. Drives the feature drawer's comment panel.
	Comments []FeatureComment `json:"comments"`
	// Documents (BACI-214) is the list of documents linked to this
	// feature via `bacio doc link <file> <feature-slug>`. Drives the
	// "Documents" section on the FeaturesView detail pane — each row
	// links to the canonical /documents/<filename> viewer.
	Documents []FeatureLinkedDoc `json:"documents"`
	// HiddenOnBoard (BACI-177) mirrors the per-feature "Show on board"
	// toggle. When true, every kanban card belonging to this feature
	// is hidden from the board on this machine.
	HiddenOnBoard bool `json:"hiddenOnBoard"`
}

// FeatureService is the Wails-bound feature API the desktop frontend talks to.
// It wraps a local bacio client.Client and reshapes its results into the DTOs
// the Features view expects. Read-only: features are created and edited via
// the CLI. Features are per-repo, so every method needs a concrete repo
// prefix (the "all repositories" pseudo-board has no feature scope).
type FeatureService struct {
	client client.Client
}

func NewFeatureService(c client.Client) *FeatureService {
	return &FeatureService{client: c}
}

// resolveRepo turns a repo prefix into a *model.Repo, rejecting the empty /
// "all" pseudo-board since features are always scoped to one repo.
func (f *FeatureService) resolveRepo(ctx context.Context, repoPrefix string) (*model.Repo, error) {
	if repoPrefix == "" || repoPrefix == "all" {
		return nil, fmt.Errorf("select a repository to view its features")
	}
	return f.client.GetRepoByPrefix(ctx, repoPrefix)
}

// ListFeatures returns every feature in one repo as a summary row.
// Archived features (BACI-68) follow display.show_archived.
func (f *FeatureService) ListFeatures(repoPrefix string) ([]FeatureSummary, error) {
	ctx := context.Background()
	repo, err := f.resolveRepo(ctx, repoPrefix)
	if err != nil {
		return nil, err
	}
	showArchived, _ := f.client.GetDisplayShowArchived(ctx)
	feats, err := f.client.ListFeatures(ctx, repo, false, showArchived)
	if err != nil {
		return nil, err
	}
	out := make([]FeatureSummary, 0, len(feats))
	for _, feat := range feats {
		branch := ""
		if feat.BranchName != nil {
			branch = *feat.BranchName
		}
		out = append(out, FeatureSummary{
			Slug:          feat.Slug,
			Title:         feat.Title,
			Emoji:         feat.Emoji,
			State:         string(feat.State),
			BranchName:    branch,
			UpdatedAt:     feat.UpdatedAt,
			HiddenOnBoard: feat.HiddenOnBoard,
		})
	}
	return out, nil
}

// GetFeature returns one feature with its description and the issues grouped
// under it, for the detail pane.
func (f *FeatureService) GetFeature(repoPrefix, slug string) (FeatureDetail, error) {
	ctx := context.Background()
	repo, err := f.resolveRepo(ctx, repoPrefix)
	if err != nil {
		return FeatureDetail{}, err
	}
	view, err := f.client.ShowFeature(ctx, repo, slug)
	if err != nil {
		return FeatureDetail{}, err
	}
	feat := view.Feature
	issues := make([]FeatureLinkedIssue, 0, len(view.Issues))
	for _, iss := range view.Issues {
		issues = append(issues, FeatureLinkedIssue{
			Key:        iss.Key,
			Title:      iss.Title,
			State:      string(iss.State),
			StateLabel: stateLabel(iss.State),
		})
	}
	comments := make([]FeatureComment, 0, len(view.Comments))
	for _, c := range view.Comments {
		comments = append(comments, FeatureComment{
			UUID:      c.UUID,
			Author:    c.Author,
			Body:      c.Body,
			CreatedAt: c.CreatedAt,
		})
	}
	docs := make([]FeatureLinkedDoc, 0, len(view.Documents))
	for _, l := range view.Documents {
		docs = append(docs, FeatureLinkedDoc{
			Filename:    l.DocumentFilename,
			Type:        string(l.DocumentType),
			Description: l.Description,
			// SourcePath stays empty — model.DocumentLink doesn't
			// carry it and the feature pane doesn't surface it.
		})
	}
	branch := ""
	if feat.BranchName != nil {
		branch = *feat.BranchName
	}
	return FeatureDetail{
		Slug:          feat.Slug,
		Title:         feat.Title,
		Description:   feat.Description,
		Emoji:         feat.Emoji,
		BranchName:    branch,
		State:           string(feat.State),
		StateManual:     feat.StateManual,
		CollectHandoffs: feat.CollectHandoffs,
		CreatedAt:       feat.CreatedAt,
		UpdatedAt:       feat.UpdatedAt,
		Issues:          issues,
		Comments:        comments,
		Documents:       docs,
		HiddenOnBoard:   feat.HiddenOnBoard,
	}, nil
}

// SetFeatureState (BACI-199) flips the feature's three-state column
// and returns the refreshed FeatureDetail. Parses the state string at
// the boundary so a typo surfaces as a clear error from the client.
//
// BACI-250 decoupled this from the auto-close pin: this method no
// longer touches state_manual. Use SetFeatureAutoClose to flip the
// per-feature pin against the BACI-199 auto-completion sweep.
func (f *FeatureService) SetFeatureState(repoPrefix, slug, state string) (FeatureDetail, error) {
	ctx := context.Background()
	repo, err := f.resolveRepo(ctx, repoPrefix)
	if err != nil {
		return FeatureDetail{}, err
	}
	st, err := model.ParseFeatureState(state)
	if err != nil {
		return FeatureDetail{}, err
	}
	if _, err := f.client.SetFeatureState(ctx, repo, slug, st, false); err != nil {
		return FeatureDetail{}, err
	}
	return f.GetFeature(repoPrefix, slug)
}

// SetFeatureAutoClose (BACI-250) flips the per-feature auto-close
// toggle — the sticky-bit `state_manual` column that gates the
// BACI-199 archive-sweep's auto-completion pass — and returns the
// refreshed FeatureDetail. enabled=true clears the bit (the sweep may
// promote this feature once every child is terminal); enabled=false
// sets the bit (long-lived catch-alls stay `active` indefinitely).
// Idempotent — flipping to the same state is a no-op write. Mirrors
// SetHiddenOnBoard's shape.
func (f *FeatureService) SetFeatureAutoClose(repoPrefix, slug string, enabled bool) (FeatureDetail, error) {
	ctx := context.Background()
	repo, err := f.resolveRepo(ctx, repoPrefix)
	if err != nil {
		return FeatureDetail{}, err
	}
	if _, err := f.client.SetFeatureAutoClose(ctx, repo, slug, enabled, false); err != nil {
		return FeatureDetail{}, err
	}
	return f.GetFeature(repoPrefix, slug)
}

// SetFeatureCollectHandoffs (BACI-333) flips the per-feature
// collect-handoffs toggle and returns the refreshed FeatureDetail.
// enabled=true collects worker close-out handoff comments; enabled=false
// silences a standing bucket. Idempotent — flipping to the same state is
// a no-op write. Mirrors SetFeatureAutoClose's shape.
func (f *FeatureService) SetFeatureCollectHandoffs(repoPrefix, slug string, enabled bool) (FeatureDetail, error) {
	ctx := context.Background()
	repo, err := f.resolveRepo(ctx, repoPrefix)
	if err != nil {
		return FeatureDetail{}, err
	}
	if _, err := f.client.SetFeatureCollectHandoffs(ctx, repo, slug, enabled, false); err != nil {
		return FeatureDetail{}, err
	}
	return f.GetFeature(repoPrefix, slug)
}

// SetFeatureEmoji (BACI-172) updates the per-feature emoji glyph and
// returns the refreshed FeatureDetail. Empty string clears the
// emoji. Validates at the store boundary so multi-cluster input
// (e.g. "FEATURE") surfaces as an error from the client.
func (f *FeatureService) SetFeatureEmoji(repoPrefix, slug, emoji string) (FeatureDetail, error) {
	ctx := context.Background()
	repo, err := f.resolveRepo(ctx, repoPrefix)
	if err != nil {
		return FeatureDetail{}, err
	}
	if _, err := f.client.UpdateFeature(ctx, repo, slug, nil, nil, &emoji, nil, false); err != nil {
		return FeatureDetail{}, err
	}
	return f.GetFeature(repoPrefix, slug)
}

// SetFeatureBranchName (BACI-231) updates the per-feature integration
// branch and returns the refreshed FeatureDetail. Empty string clears
// the branch (the feature ships straight to main again). Validates at
// the store boundary via ValidateBranchName so invalid refnames
// (whitespace, leading `-`, embedded `..`, etc.) surface as an error
// from the client. Parallel to SetFeatureEmoji.
func (f *FeatureService) SetFeatureBranchName(repoPrefix, slug, branch string) (FeatureDetail, error) {
	ctx := context.Background()
	repo, err := f.resolveRepo(ctx, repoPrefix)
	if err != nil {
		return FeatureDetail{}, err
	}
	if _, err := f.client.UpdateFeature(ctx, repo, slug, nil, nil, nil, &branch, false); err != nil {
		return FeatureDetail{}, err
	}
	return f.GetFeature(repoPrefix, slug)
}

// SetHiddenOnBoard (BACI-177) flips the per-feature "Show on board"
// toggle and returns the refreshed FeatureDetail. true hides every
// kanban card belonging to this feature on this machine; false makes
// them visible again. Idempotent — flipping to the same state is a
// no-op write. The flag lives in the per-repo board-hide KV
// (tui_settings), shared with the TUI's feature picker; flipping it
// here is visible on the TUI's next reload.
func (f *FeatureService) SetHiddenOnBoard(repoPrefix, slug string, hidden bool) (FeatureDetail, error) {
	ctx := context.Background()
	repo, err := f.resolveRepo(ctx, repoPrefix)
	if err != nil {
		return FeatureDetail{}, err
	}
	if _, err := f.client.SetFeatureHiddenOnBoard(ctx, repo, slug, hidden, false); err != nil {
		return FeatureDetail{}, err
	}
	return f.GetFeature(repoPrefix, slug)
}

// FeaturePlanEntry mirrors api.PlanEntry, reshaped to the camelCase
// JSON tags the desktop / web bundle expects (BACI-236). One row per
// issue in the feature; BlockedBy carries the keys of in-feature
// blockers the dependency-graph view turns into directed edges.
// Closed is true for done / cancelled issues so the renderer can mute
// them while still drawing their connections to live work.
type FeaturePlanEntry struct {
	Key       string   `json:"key"`
	Title     string   `json:"title"`
	State     string   `json:"state"`
	Assignee  string   `json:"assignee"`
	BlockedBy []string `json:"blockedBy"`
	Closed    bool     `json:"closed"`
}

// FeaturePlan is the dependency-graph payload for one feature
// (BACI-236). Slug echoes the requested feature; Order is the
// topo-sorted issue list driving the graph view's nodes + edges.
type FeaturePlan struct {
	Slug  string             `json:"slug"`
	Order []FeaturePlanEntry `json:"order"`
}

// GetFeaturePlan (BACI-236) returns the dependency-graph payload for
// the FeaturesView. includeClosed=false matches the historical open-
// only `bacio feature plan` shape; true widens to every issue in the
// feature plus every `blocks` edge whose endpoints are both in the
// feature, with Closed=true on terminal entries.
func (f *FeatureService) GetFeaturePlan(repoPrefix, slug string, includeClosed bool) (FeaturePlan, error) {
	ctx := context.Background()
	repo, err := f.resolveRepo(ctx, repoPrefix)
	if err != nil {
		return FeaturePlan{}, err
	}
	view, err := f.client.PlanFeature(ctx, repo, slug, includeClosed)
	if err != nil {
		return FeaturePlan{}, err
	}
	order := make([]FeaturePlanEntry, 0, len(view.Order))
	for _, e := range view.Order {
		// Defensive copy — view.Order entries are values, but the
		// blocked-by slice is shared across calls; allocate a fresh
		// slice so the binding payload doesn't carry an aliased
		// reference into the client cache.
		by := make([]string, len(e.BlockedBy))
		copy(by, e.BlockedBy)
		order = append(order, FeaturePlanEntry{
			Key:       e.Key,
			Title:     e.Title,
			State:     string(e.State),
			Assignee:  e.Assignee,
			BlockedBy: by,
			Closed:    e.Closed,
		})
	}
	return FeaturePlan{Slug: view.Feature, Order: order}, nil
}

// AddFeatureComment posts a chronological handoff comment to a feature
// (BACI-124) and returns the refreshed detail.
func (f *FeatureService) AddFeatureComment(repoPrefix, slug, author, body string) (FeatureDetail, error) {
	ctx := context.Background()
	repo, err := f.resolveRepo(ctx, repoPrefix)
	if err != nil {
		return FeatureDetail{}, err
	}
	if _, err := f.client.AddFeatureComment(ctx, repo, inputs.FeatureCommentAddInput{
		FeatureSlug: slug,
		Author:      author,
		Body:        body,
	}, false); err != nil {
		return FeatureDetail{}, err
	}
	return f.GetFeature(repoPrefix, slug)
}

// DeleteFeatureComment removes a feature comment by uuid (BACI-124) and
// returns the refreshed detail.
func (f *FeatureService) DeleteFeatureComment(repoPrefix, slug, commentUUID string) (FeatureDetail, error) {
	ctx := context.Background()
	repo, err := f.resolveRepo(ctx, repoPrefix)
	if err != nil {
		return FeatureDetail{}, err
	}
	if _, _, err := f.client.DeleteFeatureComment(ctx, repo, inputs.FeatureCommentRmInput{
		FeatureSlug: slug,
		CommentUUID: commentUUID,
	}, false); err != nil {
		return FeatureDetail{}, err
	}
	return f.GetFeature(repoPrefix, slug)
}
