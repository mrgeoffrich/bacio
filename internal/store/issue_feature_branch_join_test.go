package store

import (
	"testing"

	"github.com/mrgeoffrich/bacio/internal/model"
)

// TestIssueFeatureBranchNameJoin exercises the BACI-231 issue→feature
// denormalisation: the issue scanner reads f.branch_name via the
// issueSelect join so kanban cards and the ActivityTray can group
// by branch without a second lookup. Issues with no feature, or
// features with NULL branch_name, see an empty FeatureBranchName
// field — no NULL leakage. Mirrors TestIssueFeatureEmojiJoin.
func TestIssueFeatureBranchNameJoin(t *testing.T) {
	s := newTestStore(t)
	repo, err := s.CreateRepo("FBJ", "fbj", t.TempDir(), "")
	if err != nil {
		t.Fatalf("create repo: %v", err)
	}
	feat, err := s.CreateFeature(repo.ID, "auth", "Auth", "", "", "feat/auth")
	if err != nil {
		t.Fatalf("create feature with branch: %v", err)
	}
	featNoBranch, err := s.CreateFeature(repo.ID, "ops", "Ops", "", "", "")
	if err != nil {
		t.Fatalf("create feature without branch: %v", err)
	}
	issWithBranch, err := s.CreateIssue(repo.ID, &feat.ID, "Has branch", "", model.StateTodo, nil, "")
	if err != nil {
		t.Fatalf("create issue with branched feature: %v", err)
	}
	issEmptyBranch, err := s.CreateIssue(repo.ID, &featNoBranch.ID, "Feature ships to main", "", model.StateTodo, nil, "")
	if err != nil {
		t.Fatalf("create issue with un-branched feature: %v", err)
	}
	issNoFeature, err := s.CreateIssue(repo.ID, nil, "No feature", "", model.StateTodo, nil, "")
	if err != nil {
		t.Fatalf("create issue without feature: %v", err)
	}

	got1, err := s.GetIssueByID(issWithBranch.ID)
	if err != nil {
		t.Fatalf("get1: %v", err)
	}
	if got1.FeatureBranchName != "feat/auth" {
		t.Fatalf("with-branch issue: FeatureBranchName = %q, want %q", got1.FeatureBranchName, "feat/auth")
	}
	got2, err := s.GetIssueByID(issEmptyBranch.ID)
	if err != nil {
		t.Fatalf("get2: %v", err)
	}
	if got2.FeatureBranchName != "" {
		t.Fatalf("empty-branch feature issue: FeatureBranchName = %q, want empty", got2.FeatureBranchName)
	}
	got3, err := s.GetIssueByID(issNoFeature.ID)
	if err != nil {
		t.Fatalf("get3: %v", err)
	}
	if got3.FeatureBranchName != "" {
		t.Fatalf("no-feature issue: FeatureBranchName = %q, want empty", got3.FeatureBranchName)
	}
}
