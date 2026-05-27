package model

import "testing"

// TestResolveBaseBranch_FallbackToMain covers the third resolver step:
// neither issue nor feature pins anything, so the constant "main"
// fallback is returned.
func TestResolveBaseBranch_FallbackToMain(t *testing.T) {
	cases := map[string]struct {
		issue   *Issue
		feature *Feature
	}{
		"both nil":             {nil, nil},
		"nil issue, nil feat":  {nil, nil},
		"empty issue no feat":  {&Issue{}, nil},
		"empty issue+feat":     {&Issue{}, &Feature{}},
		"feature nil ptr":      {&Issue{}, &Feature{BranchName: nil}},
		"issue ptr empty str":  {&Issue{BaseBranch: stringPtr("")}, nil},
		"feat ptr empty str":   {&Issue{}, &Feature{BranchName: stringPtr("")}},
		"issue+feat both ptr empty": {
			&Issue{BaseBranch: stringPtr("")},
			&Feature{BranchName: stringPtr("")},
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			got := ResolveBaseBranch(tc.issue, tc.feature)
			if got != DefaultBaseBranch {
				t.Fatalf("ResolveBaseBranch = %q, want %q (default)", got, DefaultBaseBranch)
			}
		})
	}
}

// TestResolveBaseBranch_FeatureBranchWins covers the second step:
// issue has no override but the feature carries a branch_name.
func TestResolveBaseBranch_FeatureBranchWins(t *testing.T) {
	feat := &Feature{BranchName: stringPtr("feat/X")}
	got := ResolveBaseBranch(&Issue{}, feat)
	if got != "feat/X" {
		t.Fatalf("ResolveBaseBranch = %q, want %q", got, "feat/X")
	}
	// nil issue is tolerated and falls through to the feature too.
	if got := ResolveBaseBranch(nil, feat); got != "feat/X" {
		t.Fatalf("ResolveBaseBranch(nil, feat) = %q, want %q", got, "feat/X")
	}
}

// TestResolveBaseBranch_IssueOverrideWins covers step one: the per-
// issue override beats the feature's branch_name and the default,
// even when the override resolves back to "main" (the explicit
// terminal merge-feature case from the BACI-226 acceptance criteria).
func TestResolveBaseBranch_IssueOverrideWins(t *testing.T) {
	cases := []struct {
		name    string
		issue   *Issue
		feature *Feature
		want    string
	}{
		{
			name:    "override beats feature",
			issue:   &Issue{BaseBranch: stringPtr("hotfix/abc")},
			feature: &Feature{BranchName: stringPtr("feat/X")},
			want:    "hotfix/abc",
		},
		{
			name:    "override=main beats feature=feat/X (terminal merge case)",
			issue:   &Issue{BaseBranch: stringPtr("main")},
			feature: &Feature{BranchName: stringPtr("feat/X")},
			want:    "main",
		},
		{
			name:    "override beats nil feature",
			issue:   &Issue{BaseBranch: stringPtr("release/v1")},
			feature: nil,
			want:    "release/v1",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := ResolveBaseBranch(tc.issue, tc.feature)
			if got != tc.want {
				t.Fatalf("ResolveBaseBranch = %q, want %q", got, tc.want)
			}
		})
	}
}

// TestResolveBaseBranch_NeverEmpty is the fallback invariant: every
// callable shape returns a non-empty string, so consumers can treat
// the result as always usable without an extra empty-check.
func TestResolveBaseBranch_NeverEmpty(t *testing.T) {
	for _, tc := range []struct {
		issue   *Issue
		feature *Feature
	}{
		{nil, nil},
		{&Issue{}, nil},
		{nil, &Feature{}},
		{&Issue{}, &Feature{}},
		{&Issue{BaseBranch: stringPtr("")}, &Feature{BranchName: stringPtr("")}},
	} {
		if got := ResolveBaseBranch(tc.issue, tc.feature); got == "" {
			t.Fatalf("ResolveBaseBranch returned empty string for %+v / %+v", tc.issue, tc.feature)
		}
	}
}

func stringPtr(s string) *string { return &s }
