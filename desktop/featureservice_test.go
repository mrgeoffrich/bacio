package main

import (
	"context"
	"errors"
	"testing"

	"github.com/mrgeoffrich/bacio/internal/cli/inputs"
	"github.com/mrgeoffrich/bacio/internal/client"
	"github.com/mrgeoffrich/bacio/internal/model"
	"github.com/mrgeoffrich/bacio/internal/store"
)

// fakeFeatureClient is a minimal client.Client for FeatureService tests
// — embeds the interface so unused methods panic if called, and only
// implements the touches GetFeaturePlan needs. Mirrors the
// fakeBoardClient pattern in boardservice_test.go.
type fakeFeatureClient struct {
	client.Client
	repo            *model.Repo
	lastIncludeFlag bool
	plan            *client.PlanView
	planErr         error
	// BACI-333: records the last SetFeatureCollectHandoffs call and the
	// feature row ShowFeature should reflect back.
	lastCollectHandoffs *bool
	showFeature         *model.Feature
	// New/Edit Epic pages: records the last CreateFeature payload and the
	// last UpdateFeature pointer set, so the two seam tests can assert
	// presence semantics (nil = no change) survive the binding.
	lastCreate    *inputs.FeatureAddInput
	createErr     error
	lastUpdate    *fakeFeatureUpdate
	updateErr     error
	updateCallHit bool
}

// fakeFeatureUpdate captures one UpdateFeature call's four presence
// pointers verbatim — the whole point of the test is that a nil stays nil.
type fakeFeatureUpdate struct {
	title       *string
	description *string
	emoji       *string
	branchName  *string
}

func (f *fakeFeatureClient) CreateFeature(_ context.Context, _ *model.Repo, in inputs.FeatureAddInput, _ bool) (*model.Feature, error) {
	f.lastCreate = &in
	if f.createErr != nil {
		return nil, f.createErr
	}
	slug := in.Slug
	if slug == "" {
		slug = store.Slugify(in.Title)
	}
	created := &model.Feature{Slug: slug, Title: in.Title, Description: in.Description, Emoji: in.Emoji}
	f.showFeature = created
	return created, nil
}

func (f *fakeFeatureClient) UpdateFeature(_ context.Context, _ *model.Repo, _ string, title, description, emoji, branchName *string, _ bool) (*model.Feature, error) {
	f.updateCallHit = true
	f.lastUpdate = &fakeFeatureUpdate{title: title, description: description, emoji: emoji, branchName: branchName}
	if f.updateErr != nil {
		return nil, f.updateErr
	}
	if f.showFeature != nil {
		if title != nil {
			f.showFeature.Title = *title
		}
		if description != nil {
			f.showFeature.Description = *description
		}
	}
	return f.showFeature, nil
}

func (f *fakeFeatureClient) GetRepoByPrefix(context.Context, string) (*model.Repo, error) {
	return f.repo, nil
}

func (f *fakeFeatureClient) SetFeatureCollectHandoffs(_ context.Context, _ *model.Repo, _ string, enabled, _ bool) (*model.Feature, error) {
	f.lastCollectHandoffs = &enabled
	if f.showFeature != nil {
		f.showFeature.CollectHandoffs = enabled
	}
	return f.showFeature, nil
}

func (f *fakeFeatureClient) ShowFeature(_ context.Context, _ *model.Repo, _ string) (*client.FeatureView, error) {
	return &client.FeatureView{Feature: f.showFeature}, nil
}

func (f *fakeFeatureClient) PlanFeature(_ context.Context, _ *model.Repo, _ string, includeClosed bool) (*client.PlanView, error) {
	f.lastIncludeFlag = includeClosed
	if f.planErr != nil {
		return nil, f.planErr
	}
	return f.plan, nil
}

// TestGetFeaturePlanDefault pins that the FeatureService binding
// forwards includeClosed=false straight through to the client and
// reshapes the PlanView into the camelCase FeaturePlan DTO the
// frontend consumes (BACI-236).
func TestGetFeaturePlanDefault(t *testing.T) {
	repo := &model.Repo{ID: 1, Prefix: "MINI"}
	fake := &fakeFeatureClient{
		repo: repo,
		plan: &client.PlanView{
			Feature: "auth",
			Order: []client.PlanEntry{
				{Key: "MINI-1", Title: "ready", State: model.StateTodo, BlockedBy: nil, Closed: false},
				{Key: "MINI-2", Title: "blocked", State: model.StateTodo, BlockedBy: []string{"MINI-1"}, Closed: false},
			},
		},
	}
	svc := NewFeatureService(fake)
	got, err := svc.GetFeaturePlan("MINI", "auth", false)
	if err != nil {
		t.Fatalf("GetFeaturePlan: %v", err)
	}
	if fake.lastIncludeFlag {
		t.Fatalf("includeClosed=false should not flip the flag")
	}
	if got.Slug != "auth" {
		t.Fatalf("slug = %q, want auth", got.Slug)
	}
	if len(got.Order) != 2 {
		t.Fatalf("order len = %d, want 2 — %+v", len(got.Order), got.Order)
	}
	if got.Order[0].Key != "MINI-1" || len(got.Order[0].BlockedBy) != 0 {
		t.Fatalf("first entry mismatch: %+v", got.Order[0])
	}
	if got.Order[1].Key != "MINI-2" || len(got.Order[1].BlockedBy) != 1 || got.Order[1].BlockedBy[0] != "MINI-1" {
		t.Fatalf("second entry mismatch: %+v", got.Order[1])
	}
	for _, e := range got.Order {
		if e.Closed {
			t.Fatalf("default path should not emit closed entries: %+v", e)
		}
	}
}

// TestGetFeaturePlanIncludeClosed pins the BACI-236 widened-payload
// path — the client returns terminal entries with Closed=true and the
// binding surfaces that bool unchanged.
func TestGetFeaturePlanIncludeClosed(t *testing.T) {
	repo := &model.Repo{ID: 1, Prefix: "MINI"}
	fake := &fakeFeatureClient{
		repo: repo,
		plan: &client.PlanView{
			Feature: "auth",
			Order: []client.PlanEntry{
				{Key: "MINI-1", Title: "delivered", State: model.StateDone, Closed: true},
				{Key: "MINI-2", Title: "live", State: model.StateTodo, BlockedBy: []string{"MINI-1"}, Closed: false},
			},
		},
	}
	svc := NewFeatureService(fake)
	got, err := svc.GetFeaturePlan("MINI", "auth", true)
	if err != nil {
		t.Fatalf("GetFeaturePlan: %v", err)
	}
	if !fake.lastIncludeFlag {
		t.Fatalf("includeClosed=true should flip the flag on the client")
	}
	if len(got.Order) != 2 {
		t.Fatalf("order len = %d, want 2", len(got.Order))
	}
	if !got.Order[0].Closed {
		t.Fatalf("first entry should be closed: %+v", got.Order[0])
	}
	if got.Order[1].Closed {
		t.Fatalf("second entry should be live: %+v", got.Order[1])
	}
	if got.Order[1].BlockedBy[0] != "MINI-1" {
		t.Fatalf("blocked-by lost the closed blocker: %+v", got.Order[1])
	}
}

// TestGetFeaturePlanPropagatesError pins that PlanFeature errors flow
// straight back through the binding rather than being swallowed.
func TestGetFeaturePlanPropagatesError(t *testing.T) {
	repo := &model.Repo{ID: 1, Prefix: "MINI"}
	fake := &fakeFeatureClient{
		repo:    repo,
		planErr: errors.New("boom"),
	}
	svc := NewFeatureService(fake)
	if _, err := svc.GetFeaturePlan("MINI", "auth", false); err == nil {
		t.Fatalf("expected error to propagate")
	}
}

// TestSetFeatureCollectHandoffs (BACI-333) pins that the FeatureService
// binding forwards the enabled flag to the client and reshapes the
// refreshed feature into the DTO the frontend's segmented control reads.
func TestSetFeatureCollectHandoffs(t *testing.T) {
	repo := &model.Repo{ID: 1, Prefix: "MINI"}
	fake := &fakeFeatureClient{
		repo:        repo,
		showFeature: &model.Feature{Slug: "maintenance", Title: "Maintenance", State: model.FeatureStateActive, CollectHandoffs: true},
	}
	svc := NewFeatureService(fake)

	got, err := svc.SetFeatureCollectHandoffs("MINI", "maintenance", false)
	if err != nil {
		t.Fatalf("SetFeatureCollectHandoffs: %v", err)
	}
	if fake.lastCollectHandoffs == nil || *fake.lastCollectHandoffs {
		t.Fatalf("client got enabled=%v, want false", fake.lastCollectHandoffs)
	}
	if got.CollectHandoffs {
		t.Fatalf("DTO CollectHandoffs = true, want false after disable")
	}
}

// TestCreateFeatureDerivesSlug pins the New Epic page's seam: an empty
// slug is derived from the title by the store's own Slugify (so the live
// client-side preview and the server agree), and the created feature is
// re-read through GetFeature so the caller gets a full FeatureDetail
// rather than the bare model row the store hands back.
func TestCreateFeatureDerivesSlug(t *testing.T) {
	repo := &model.Repo{ID: 1, Prefix: "MINI"}
	fake := &fakeFeatureClient{repo: repo}
	svc := NewFeatureService(fake)

	got, err := svc.CreateFeature("MINI", "Unified Create Affordance", "", "why", "🚀", "feat/create")
	if err != nil {
		t.Fatalf("CreateFeature: %v", err)
	}
	if fake.lastCreate == nil {
		t.Fatalf("client.CreateFeature was never called")
	}
	if fake.lastCreate.Title != "Unified Create Affordance" || fake.lastCreate.Emoji != "🚀" || fake.lastCreate.BranchName != "feat/create" {
		t.Fatalf("input mismatch: %+v", *fake.lastCreate)
	}
	if fake.lastCreate.Slug != "" {
		t.Fatalf("empty slug must reach the client untouched, got %q", fake.lastCreate.Slug)
	}
	if want := store.Slugify("Unified Create Affordance"); got.Slug != want {
		t.Fatalf("slug = %q, want %q", got.Slug, want)
	}
	if got.Title != "Unified Create Affordance" {
		t.Fatalf("detail title = %q", got.Title)
	}
}

// TestCreateFeaturePropagatesError pins that a store refusal (a duplicate
// slug is the one that matters) flows back to the page rather than being
// swallowed into an empty detail.
func TestCreateFeaturePropagatesError(t *testing.T) {
	repo := &model.Repo{ID: 1, Prefix: "MINI"}
	fake := &fakeFeatureClient{repo: repo, createErr: errors.New("UNIQUE constraint failed: features.repo_id, features.slug")}
	svc := NewFeatureService(fake)
	if _, err := svc.CreateFeature("MINI", "Auth", "auth", "", "", ""); err == nil {
		t.Fatalf("expected the duplicate-slug error to propagate")
	}
}

// TestUpdateFeaturePresence pins the Edit Epic page's batched save: the
// four pointers are PRESENCE, so a nil must arrive at the client as nil
// (no change) and a non-nil empty string must arrive as a clear.
func TestUpdateFeaturePresence(t *testing.T) {
	repo := &model.Repo{ID: 1, Prefix: "MINI"}
	fake := &fakeFeatureClient{
		repo:        repo,
		showFeature: &model.Feature{Slug: "auth", Title: "Auth", State: model.FeatureStateActive},
	}
	svc := NewFeatureService(fake)

	title := "Auth rewrite"
	branch := ""
	got, err := svc.UpdateFeature("MINI", "auth", &title, nil, nil, &branch)
	if err != nil {
		t.Fatalf("UpdateFeature: %v", err)
	}
	if fake.lastUpdate == nil {
		t.Fatalf("client.UpdateFeature was never called")
	}
	if fake.lastUpdate.title == nil || *fake.lastUpdate.title != "Auth rewrite" {
		t.Fatalf("title pointer mismatch: %+v", fake.lastUpdate.title)
	}
	if fake.lastUpdate.description != nil || fake.lastUpdate.emoji != nil {
		t.Fatalf("untouched fields must stay nil: %+v", *fake.lastUpdate)
	}
	if fake.lastUpdate.branchName == nil || *fake.lastUpdate.branchName != "" {
		t.Fatalf("a non-nil empty branch must survive as a clear: %+v", fake.lastUpdate.branchName)
	}
	if got.Title != "Auth rewrite" {
		t.Fatalf("detail title = %q, want the updated one", got.Title)
	}
}

// TestUpdateFeatureNoFieldsIsNoop pins that an all-nil save short-circuits
// to a plain read instead of surfacing the client's "nothing to update".
func TestUpdateFeatureNoFieldsIsNoop(t *testing.T) {
	repo := &model.Repo{ID: 1, Prefix: "MINI"}
	fake := &fakeFeatureClient{
		repo:        repo,
		showFeature: &model.Feature{Slug: "auth", Title: "Auth", State: model.FeatureStateActive},
	}
	svc := NewFeatureService(fake)
	got, err := svc.UpdateFeature("MINI", "auth", nil, nil, nil, nil)
	if err != nil {
		t.Fatalf("UpdateFeature: %v", err)
	}
	if fake.updateCallHit {
		t.Fatalf("all-nil save must not reach the client")
	}
	if got.Slug != "auth" {
		t.Fatalf("slug = %q, want auth", got.Slug)
	}
}
