package store

import (
	"sort"
	"testing"

	"github.com/mrgeoffrich/bacio/internal/model"
)

// TestStore_FeatureHiddenOnBoard_RoundTrip exercises the BACI-177
// per-feature "Show on board" toggle: defaults to false (visible),
// flips on, flips off, idempotent on both sides.
func TestStore_FeatureHiddenOnBoard_RoundTrip(t *testing.T) {
	s := newTestStore(t)
	repo, err := s.CreateRepo("FH", "fh", t.TempDir(), "")
	if err != nil {
		t.Fatalf("create repo: %v", err)
	}
	if _, err := s.CreateFeature(repo.ID, "auth", "Auth", "", "", ""); err != nil {
		t.Fatalf("create feature: %v", err)
	}

	// Default: not hidden.
	hidden, err := s.IsFeatureHiddenOnBoard(repo.ID, "auth")
	if err != nil {
		t.Fatalf("is hidden (default): %v", err)
	}
	if hidden {
		t.Fatalf("default IsFeatureHiddenOnBoard = true, want false")
	}

	// Flip on.
	if err := s.SetFeatureHiddenOnBoard(repo.ID, "auth", true); err != nil {
		t.Fatalf("set hidden=true: %v", err)
	}
	hidden, err = s.IsFeatureHiddenOnBoard(repo.ID, "auth")
	if err != nil {
		t.Fatalf("is hidden (after set true): %v", err)
	}
	if !hidden {
		t.Fatalf("after set true: IsFeatureHiddenOnBoard = false, want true")
	}

	// Idempotent: set hidden=true again, still hidden.
	if err := s.SetFeatureHiddenOnBoard(repo.ID, "auth", true); err != nil {
		t.Fatalf("set hidden=true (idempotent): %v", err)
	}
	hidden, _ = s.IsFeatureHiddenOnBoard(repo.ID, "auth")
	if !hidden {
		t.Fatalf("after idempotent set true: not hidden")
	}

	// Flip off.
	if err := s.SetFeatureHiddenOnBoard(repo.ID, "auth", false); err != nil {
		t.Fatalf("set hidden=false: %v", err)
	}
	hidden, _ = s.IsFeatureHiddenOnBoard(repo.ID, "auth")
	if hidden {
		t.Fatalf("after set false: still hidden")
	}

	// Idempotent unhide.
	if err := s.SetFeatureHiddenOnBoard(repo.ID, "auth", false); err != nil {
		t.Fatalf("set hidden=false (idempotent): %v", err)
	}
}

// TestStore_ListIssues_HiddenFeatureSlugsFilter pins the SQL filter
// behaviour: issues whose feature has a slug in the hidden set are
// excluded; issues with no feature pass through; unknown slugs are a
// no-op.
func TestStore_ListIssues_HiddenFeatureSlugsFilter(t *testing.T) {
	s := newTestStore(t)
	repo, err := s.CreateRepo("HF", "hf", t.TempDir(), "")
	if err != nil {
		t.Fatalf("create repo: %v", err)
	}
	authFeat, err := s.CreateFeature(repo.ID, "auth", "Auth", "", "", "")
	if err != nil {
		t.Fatalf("create feature auth: %v", err)
	}
	opsFeat, err := s.CreateFeature(repo.ID, "ops", "Ops", "", "", "")
	if err != nil {
		t.Fatalf("create feature ops: %v", err)
	}
	if _, err := s.CreateIssue(repo.ID, &authFeat.ID, "auth-1", "", model.StateTodo, nil, ""); err != nil {
		t.Fatalf("create auth issue: %v", err)
	}
	if _, err := s.CreateIssue(repo.ID, &opsFeat.ID, "ops-1", "", model.StateTodo, nil, ""); err != nil {
		t.Fatalf("create ops issue: %v", err)
	}
	if _, err := s.CreateIssue(repo.ID, nil, "no-feature-1", "", model.StateTodo, nil, ""); err != nil {
		t.Fatalf("create no-feature issue: %v", err)
	}

	// Empty slice: every issue passes through.
	all, err := s.ListIssues(IssueFilter{RepoID: &repo.ID})
	if err != nil {
		t.Fatalf("list (empty filter): %v", err)
	}
	if len(all) != 3 {
		t.Fatalf("empty filter: got %d issues, want 3", len(all))
	}

	// Hide auth: only the auth issue drops; ops + no-feature stay.
	filtered, err := s.ListIssues(IssueFilter{
		RepoID:             &repo.ID,
		HiddenFeatureSlugs: []string{"auth"},
	})
	if err != nil {
		t.Fatalf("list (hide auth): %v", err)
	}
	if len(filtered) != 2 {
		t.Fatalf("hide auth: got %d issues, want 2", len(filtered))
	}
	titles := titlesOf(filtered)
	wantTitles := []string{"no-feature-1", "ops-1"}
	if !stringSliceEqual(titles, wantTitles) {
		t.Fatalf("hide auth: titles = %v, want %v", titles, wantTitles)
	}

	// Unknown slug: silently no-op (no issues dropped).
	all2, err := s.ListIssues(IssueFilter{
		RepoID:             &repo.ID,
		HiddenFeatureSlugs: []string{"nope"},
	})
	if err != nil {
		t.Fatalf("list (unknown slug): %v", err)
	}
	if len(all2) != 3 {
		t.Fatalf("unknown slug: got %d issues, want 3 (no-op)", len(all2))
	}

	// Hide both: only the no-feature issue stays.
	bothHidden, err := s.ListIssues(IssueFilter{
		RepoID:             &repo.ID,
		HiddenFeatureSlugs: []string{"auth", "ops"},
	})
	if err != nil {
		t.Fatalf("list (hide both): %v", err)
	}
	if len(bothHidden) != 1 || bothHidden[0].Title != "no-feature-1" {
		t.Fatalf("hide both: got %d issues, want 1 (no-feature-1)", len(bothHidden))
	}
}

// TestStore_ListFeaturesFiltered_WithHiddenOnBoard verifies that the
// opt-in WithHiddenOnBoard flag inflates feat.HiddenOnBoard from the
// per-repo board-hide KV; without the flag, HiddenOnBoard stays false.
func TestStore_ListFeaturesFiltered_WithHiddenOnBoard(t *testing.T) {
	s := newTestStore(t)
	repo, err := s.CreateRepo("HW", "hw", t.TempDir(), "")
	if err != nil {
		t.Fatalf("create repo: %v", err)
	}
	if _, err := s.CreateFeature(repo.ID, "auth", "Auth", "", "", ""); err != nil {
		t.Fatalf("create feature auth: %v", err)
	}
	if _, err := s.CreateFeature(repo.ID, "ops", "Ops", "", "", ""); err != nil {
		t.Fatalf("create feature ops: %v", err)
	}
	if err := s.SetFeatureHiddenOnBoard(repo.ID, "auth", true); err != nil {
		t.Fatalf("hide auth: %v", err)
	}

	// Without WithHiddenOnBoard, the field stays at zero value.
	plain, err := s.ListFeaturesFiltered(FeatureFilter{RepoID: repo.ID})
	if err != nil {
		t.Fatalf("list (plain): %v", err)
	}
	for _, f := range plain {
		if f.HiddenOnBoard {
			t.Fatalf("plain list: feature %s has HiddenOnBoard=true (caller didn't ask)", f.Slug)
		}
	}

	// With WithHiddenOnBoard, the auth row reads true.
	enriched, err := s.ListFeaturesFiltered(FeatureFilter{
		RepoID:            repo.ID,
		WithHiddenOnBoard: true,
	})
	if err != nil {
		t.Fatalf("list (enriched): %v", err)
	}
	bySlug := map[string]bool{}
	for _, f := range enriched {
		bySlug[f.Slug] = f.HiddenOnBoard
	}
	if !bySlug["auth"] {
		t.Fatalf("enriched: auth.HiddenOnBoard = false, want true")
	}
	if bySlug["ops"] {
		t.Fatalf("enriched: ops.HiddenOnBoard = true, want false")
	}

	// GetFeatureBySlugWithHidden mirrors the same enrichment.
	authFeat, err := s.GetFeatureBySlugWithHidden(repo.ID, "auth")
	if err != nil {
		t.Fatalf("get auth: %v", err)
	}
	if !authFeat.HiddenOnBoard {
		t.Fatalf("GetFeatureBySlugWithHidden(auth).HiddenOnBoard = false, want true")
	}
	opsFeat, err := s.GetFeatureBySlugWithHidden(repo.ID, "ops")
	if err != nil {
		t.Fatalf("get ops: %v", err)
	}
	if opsFeat.HiddenOnBoard {
		t.Fatalf("GetFeatureBySlugWithHidden(ops).HiddenOnBoard = true, want false")
	}
}

func titlesOf(issues []*model.Issue) []string {
	out := make([]string, 0, len(issues))
	for _, i := range issues {
		out = append(out, i.Title)
	}
	sort.Strings(out)
	return out
}

func stringSliceEqual(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
