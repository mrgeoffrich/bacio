package store

import (
	"testing"

	"github.com/mrgeoffrich/bacio/internal/model"
)

// TestAddDispatchStampsBaseBranch_FallbackToMain (BACI-226): a targeted
// pending dispatch against an issue with no override and no feature
// stamps base_branch="main" on insert. The column read-back is the
// matcher's promise to the worker prompt envelope and the BACI-227
// concurrency grouping.
func TestAddDispatchStampsBaseBranch_FallbackToMain(t *testing.T) {
	s, repo, iss, ag, _ := seedDispatchFixture(t)
	d, err := s.AddDispatch(AddDispatchIn{
		RepoID:        repo.ID,
		TargetAgentID: &ag.ID,
		IssueID:       &iss.ID,
		Mode:          model.DispatchModePlan,
		Payload:       "plan it",
		CreatedBy:     "supervisor",
	})
	if err != nil {
		t.Fatalf("add dispatch: %v", err)
	}
	if d.BaseBranch != model.DefaultBaseBranch {
		t.Fatalf("base_branch = %q, want %q (fallback)", d.BaseBranch, model.DefaultBaseBranch)
	}
}

// TestAddDispatchStampsBaseBranch_FeatureWins: when the issue has a
// parent feature whose branch_name is set, the dispatch picks that up
// at insert time without an explicit issue override.
func TestAddDispatchStampsBaseBranch_FeatureWins(t *testing.T) {
	s, repo, _, ag, _ := seedDispatchFixture(t)
	feat, err := s.CreateFeature(repo.ID, "auth-rewrite", "Auth", "", "", "feat/X")
	if err != nil {
		t.Fatalf("create feature: %v", err)
	}
	iss, err := s.CreateIssue(repo.ID, &feat.ID, "login broken", "", model.StateTodo, nil, "", "")
	if err != nil {
		t.Fatalf("create issue: %v", err)
	}
	d, err := s.AddDispatch(AddDispatchIn{
		RepoID:        repo.ID,
		TargetAgentID: &ag.ID,
		IssueID:       &iss.ID,
		Mode:          model.DispatchModePlan,
		Payload:       "plan it",
		CreatedBy:     "supervisor",
	})
	if err != nil {
		t.Fatalf("add dispatch: %v", err)
	}
	if d.BaseBranch != "feat/X" {
		t.Fatalf("base_branch = %q, want %q (feature branch)", d.BaseBranch, "feat/X")
	}
}

// TestAddDispatchStampsBaseBranch_IssueOverrideWins: per the BACI-226
// acceptance criteria, an issue with override=main and a non-main
// feature.branch_name targets main — the terminal merge-feature case.
func TestAddDispatchStampsBaseBranch_IssueOverrideWins(t *testing.T) {
	s, repo, _, ag, _ := seedDispatchFixture(t)
	feat, err := s.CreateFeature(repo.ID, "auth-rewrite", "Auth", "", "", "feat/X")
	if err != nil {
		t.Fatalf("create feature: %v", err)
	}
	iss, err := s.CreateIssue(repo.ID, &feat.ID, "merge feat back", "", model.StateTodo, nil, "main", "")
	if err != nil {
		t.Fatalf("create issue: %v", err)
	}
	d, err := s.AddDispatch(AddDispatchIn{
		RepoID:        repo.ID,
		TargetAgentID: &ag.ID,
		IssueID:       &iss.ID,
		Mode:          model.DispatchModeShip,
		Payload:       "ship it",
		CreatedBy:     "supervisor",
	})
	if err != nil {
		t.Fatalf("add dispatch: %v", err)
	}
	if d.BaseBranch != "main" {
		t.Fatalf("base_branch = %q, want %q (override beats feat/X)", d.BaseBranch, "main")
	}
}

// TestAddDispatchStampsBaseBranch_NoIssueLeavesNull: setup-register
// nudges and idle pings carry no issue and so leave base_branch
// empty. The column maps to NULL — a downstream reader that wants a
// value must call ResolveBaseBranch and inherit the "main" fallback.
func TestAddDispatchStampsBaseBranch_NoIssueLeavesNull(t *testing.T) {
	s, repo, _, _, sess := seedDispatchFixture(t)
	d, err := s.AddDispatch(AddDispatchIn{
		RepoID:          repo.ID,
		TargetSessionID: sess.SessionID,
		Payload:         "register",
		CreatedBy:       "bacio-channel",
	})
	if err != nil {
		t.Fatalf("add dispatch: %v", err)
	}
	if d.BaseBranch != "" {
		t.Fatalf("base_branch = %q, want empty (no issue)", d.BaseBranch)
	}
}

// TestBindQueuedDispatchStampsBaseBranch (BACI-226): a queued
// dispatch with no agent target gets its base_branch stamped when
// the matcher binds it — the persistent value the channel push
// envelope and the BACI-227 concurrency cap both read.
func TestBindQueuedDispatchStampsBaseBranch(t *testing.T) {
	s, repo, _, ag, _ := seedDispatchFixture(t)
	feat, err := s.CreateFeature(repo.ID, "auth-rewrite", "Auth", "", "", "feat/X")
	if err != nil {
		t.Fatalf("create feature: %v", err)
	}
	iss, err := s.CreateIssue(repo.ID, &feat.ID, "queued issue", "", model.StateTodo, nil, "", "")
	if err != nil {
		t.Fatalf("create issue: %v", err)
	}
	queued, err := s.AddDispatch(AddDispatchIn{
		RepoID:        repo.ID,
		IssueID:       &iss.ID,
		Mode:          model.DispatchModePlan,
		Payload:       "plan",
		CreatedBy:     "supervisor",
		InitialStatus: model.DispatchQueued,
	})
	if err != nil {
		t.Fatalf("queue dispatch: %v", err)
	}
	// AddDispatch's pre-tx resolver should have stamped feat/X already.
	if queued.BaseBranch != "feat/X" {
		t.Fatalf("queued base_branch = %q, want %q", queued.BaseBranch, "feat/X")
	}

	bound, err := s.BindQueuedDispatch(queued.ID, ag.ID)
	if err != nil {
		t.Fatalf("bind: %v", err)
	}
	if bound.Status != model.DispatchPending {
		t.Fatalf("status = %q, want pending", bound.Status)
	}
	if bound.BaseBranch != "feat/X" {
		t.Fatalf("bound base_branch = %q, want %q", bound.BaseBranch, "feat/X")
	}
}

// TestBindQueuedDispatch_ResolvesIssueOverride: BindQueuedDispatch
// re-reads the issue at bind time, so a per-issue override added
// after the row was queued is the value the worker prompt envelope
// will see — same source of truth the BACI-227 concurrency cap reads.
func TestBindQueuedDispatch_ResolvesIssueOverride(t *testing.T) {
	s, repo, _, ag, _ := seedDispatchFixture(t)
	feat, err := s.CreateFeature(repo.ID, "auth-rewrite", "Auth", "", "", "feat/X")
	if err != nil {
		t.Fatalf("create feature: %v", err)
	}
	iss, err := s.CreateIssue(repo.ID, &feat.ID, "override later", "", model.StateTodo, nil, "", "")
	if err != nil {
		t.Fatalf("create issue: %v", err)
	}
	queued, err := s.AddDispatch(AddDispatchIn{
		RepoID:        repo.ID,
		IssueID:       &iss.ID,
		Mode:          model.DispatchModePlan,
		Payload:       "plan",
		CreatedBy:     "supervisor",
		InitialStatus: model.DispatchQueued,
	})
	if err != nil {
		t.Fatalf("queue: %v", err)
	}
	// Now the user pins this specific issue to main (terminal merge).
	override := "main"
	if err := s.UpdateIssue(iss.ID, nil, nil, nil, &override, nil); err != nil {
		t.Fatalf("update issue: %v", err)
	}

	bound, err := s.BindQueuedDispatch(queued.ID, ag.ID)
	if err != nil {
		t.Fatalf("bind: %v", err)
	}
	if bound.BaseBranch != "main" {
		t.Fatalf("bound base_branch = %q, want %q (override beats feat/X at bind time)", bound.BaseBranch, "main")
	}
}
