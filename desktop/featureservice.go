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
	State         string    `json:"state"`
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
	// State + StateManual (BACI-199) round-trip the per-feature state
	// column and its sticky bit. The drawer's segmented control reads
	// State to highlight the active button, and StateManual to render
	// a "pinned" indicator next to it so the user knows the
	// auto-completion sweep won't move it.
	State       string               `json:"state"`
	StateManual bool                 `json:"stateManual"`
	CreatedAt   time.Time            `json:"createdAt"`
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
		out = append(out, FeatureSummary{
			Slug:          feat.Slug,
			Title:         feat.Title,
			Emoji:         feat.Emoji,
			State:         string(feat.State),
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
	return FeatureDetail{
		Slug:          feat.Slug,
		Title:         feat.Title,
		Description:   feat.Description,
		Emoji:         feat.Emoji,
		State:         string(feat.State),
		StateManual:   feat.StateManual,
		CreatedAt:     feat.CreatedAt,
		UpdatedAt:     feat.UpdatedAt,
		Issues:        issues,
		Comments:      comments,
		Documents:     docs,
		HiddenOnBoard: feat.HiddenOnBoard,
	}, nil
}

// SetFeatureState (BACI-199) flips the feature's three-state column
// and returns the refreshed FeatureDetail. Sets state_manual = true
// so the leader-elected archive-sweep's auto-completion pass leaves
// the row alone until the user pins a new value. Parses the state
// string at the boundary so a typo surfaces as a clear error from
// the client.
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
