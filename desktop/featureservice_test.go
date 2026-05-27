package main

import (
	"context"
	"errors"
	"testing"

	"github.com/mrgeoffrich/bacio/internal/client"
	"github.com/mrgeoffrich/bacio/internal/model"
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
}

func (f *fakeFeatureClient) GetRepoByPrefix(context.Context, string) (*model.Repo, error) {
	return f.repo, nil
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
