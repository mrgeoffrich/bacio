package client

// Client-layer coverage for the workspaces / Kanban / doc-folders pivot.
//
// Three things are pinned here that live NOWHERE else in the stack:
//
//  1. The workspace `issue add` default (a new card in a workspace lands
//     on the first Kanban lane; in a git repo it stays off the board).
//     The store deliberately refuses to know about the repo-kind axis, so
//     this client path is the only place the rule exists.
//  2. The dispatch refusal on a workspace.
//  3. The `Path == ""` sweep — every site that used to conflate a
//     pathless workspace with a phantom.

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/mrgeoffrich/bacio/internal/cli/inputs"
	"github.com/mrgeoffrich/bacio/internal/git"
	"github.com/mrgeoffrich/bacio/internal/model"
	"github.com/mrgeoffrich/bacio/internal/store"
)

// seedWorkspace registers a workspace through the client so the test
// exercises the same path the CLI / HTTP / desktop surfaces will.
func seedWorkspace(t *testing.T, c *localClient, name string) *model.Repo {
	t.Helper()
	repo, err := c.CreateWorkspace(context.Background(), WorkspaceCreateInput{Name: name}, false)
	if err != nil {
		t.Fatalf("CreateWorkspace(%q): %v", name, err)
	}
	return repo
}

// seedBootstrappedRepo is seedRepo plus the bootstrap the real
// registration paths run (EnsureRepo / resolveRepoC do it; the bare
// store.CreateRepo that seedRepo calls does not). Tests that need a git
// repo with a starter Kanban board use this — the point of several of
// them is that a git repo HAS a board and still keeps new cards off it.
func seedBootstrappedRepo(t *testing.T, c *localClient, prefix string) *model.Repo {
	t.Helper()
	repo := seedRepo(t, c.store, prefix, t.TempDir())
	if err := c.store.BootstrapRepoDefaults(repo.ID); err != nil {
		t.Fatalf("BootstrapRepoDefaults(%s): %v", prefix, err)
	}
	return repo
}

// ---------- CreateWorkspace ----------

func TestCreateWorkspace(t *testing.T) {
	ctx := context.Background()

	t.Run("creates_a_pathless_workspace_with_a_board", func(t *testing.T) {
		c, _ := openTestLocalClient(t)
		repo, err := c.CreateWorkspace(ctx, WorkspaceCreateInput{Name: "Product Planning"}, false)
		if err != nil {
			t.Fatalf("CreateWorkspace: %v", err)
		}
		if !repo.IsWorkspace() {
			t.Errorf("kind = %q, want workspace", repo.Kind)
		}
		if repo.HasWorkingTree() {
			t.Errorf("path = %q, want empty", repo.Path)
		}
		if repo.IsPhantom() {
			t.Error("a workspace must never report as a phantom")
		}
		if repo.Prefix == "" {
			t.Error("prefix was not allocated")
		}
		// BootstrapRepoDefaults runs inside store.CreateWorkspace, so a
		// fresh workspace opens with the starter board — the workspace
		// issue-create default below depends on it.
		lanes, err := c.ListKanbanColumns(ctx, repo)
		if err != nil {
			t.Fatalf("ListKanbanColumns: %v", err)
		}
		if len(lanes) != len(store.DefaultKanbanColumnNames) {
			t.Fatalf("lanes = %d, want %d", len(lanes), len(store.DefaultKanbanColumnNames))
		}
		if lanes[0].Name != store.DefaultKanbanColumnNames[0] {
			t.Errorf("first lane = %q, want %q", lanes[0].Name, store.DefaultKanbanColumnNames[0])
		}
	})

	t.Run("honours_an_explicit_prefix", func(t *testing.T) {
		c, _ := openTestLocalClient(t)
		repo, err := c.CreateWorkspace(ctx, WorkspaceCreateInput{Name: "Ops", Prefix: "opsx"}, false)
		if err != nil {
			t.Fatalf("CreateWorkspace: %v", err)
		}
		if repo.Prefix != "OPSX" {
			t.Errorf("prefix = %q, want OPSX (upper-cased)", repo.Prefix)
		}
	})

	t.Run("refuses_a_taken_prefix", func(t *testing.T) {
		c, _ := openTestLocalClient(t)
		seedRepo(t, c.store, "TAKE", t.TempDir())
		if _, err := c.CreateWorkspace(ctx, WorkspaceCreateInput{Name: "Clash", Prefix: "TAKE"}, false); err == nil {
			t.Fatal("expected a refusal for an already-used prefix")
		}
	})

	t.Run("requires_a_name", func(t *testing.T) {
		c, _ := openTestLocalClient(t)
		if _, err := c.CreateWorkspace(ctx, WorkspaceCreateInput{Name: "   "}, false); err == nil {
			t.Fatal("expected a refusal for a blank name")
		}
	})

	t.Run("dry_run_writes_nothing", func(t *testing.T) {
		c, _ := openTestLocalClient(t)
		projected, err := c.CreateWorkspace(ctx, WorkspaceCreateInput{Name: "Rehearsal"}, true)
		if err != nil {
			t.Fatalf("CreateWorkspace(dry-run): %v", err)
		}
		if projected.Kind != model.RepoKindWorkspace {
			t.Errorf("projected kind = %q, want workspace", projected.Kind)
		}
		if projected.ID != 0 {
			t.Errorf("projected id = %d, want the zero value", projected.ID)
		}
		repos, err := c.ListRepos(ctx)
		if err != nil {
			t.Fatalf("ListRepos: %v", err)
		}
		if len(repos) != 0 {
			t.Fatalf("dry-run created %d repo(s)", len(repos))
		}
	})
}

// ---------- the workspace issue-create default (locked decision D1) ----------

func TestCreateIssueKanbanDefault(t *testing.T) {
	ctx := context.Background()

	t.Run("workspace_card_lands_on_the_first_lane", func(t *testing.T) {
		c, _ := openTestLocalClient(t)
		ws := seedWorkspace(t, c, "Planning")
		lanes, err := c.ListKanbanColumns(ctx, ws)
		if err != nil {
			t.Fatalf("ListKanbanColumns: %v", err)
		}
		iss, err := c.CreateIssue(ctx, ws, inputs.IssueAddInput{Title: "write the brief"}, false)
		if err != nil {
			t.Fatalf("CreateIssue: %v", err)
		}
		if iss.KanbanColumnID == nil {
			t.Fatal("workspace card is not on the board")
		}
		if *iss.KanbanColumnID != lanes[0].ID {
			t.Errorf("landed in lane %d, want the first lane %d", *iss.KanbanColumnID, lanes[0].ID)
		}
	})

	t.Run("cards_append_in_creation_order", func(t *testing.T) {
		c, _ := openTestLocalClient(t)
		ws := seedWorkspace(t, c, "Planning")
		var keys []string
		for _, title := range []string{"first", "second", "third"} {
			iss, err := c.CreateIssue(ctx, ws, inputs.IssueAddInput{Title: title}, false)
			if err != nil {
				t.Fatalf("CreateIssue(%s): %v", title, err)
			}
			keys = append(keys, iss.Key)
			if iss.KanbanPosition != len(keys)-1 {
				t.Errorf("%s: position = %d, want %d", title, iss.KanbanPosition, len(keys)-1)
			}
		}
	})

	t.Run("git_repo_card_stays_off_the_board", func(t *testing.T) {
		c, _ := openTestLocalClient(t)
		repo := seedBootstrappedRepo(t, c, "GITR")
		// The git repo has a board too (BootstrapRepoDefaults seeds one)
		// — the point is that a new card does NOT go on it.
		lanes, err := c.ListKanbanColumns(ctx, repo)
		if err != nil {
			t.Fatalf("ListKanbanColumns: %v", err)
		}
		if len(lanes) == 0 {
			t.Fatal("expected the git repo to have a starter board")
		}
		iss, err := c.CreateIssue(ctx, repo, inputs.IssueAddInput{Title: "pipeline work"}, false)
		if err != nil {
			t.Fatalf("CreateIssue: %v", err)
		}
		if iss.KanbanColumnID != nil {
			t.Fatalf("git-repo card was put on the board (lane %d)", *iss.KanbanColumnID)
		}
	})

	t.Run("empty_board_is_not_an_error", func(t *testing.T) {
		c, _ := openTestLocalClient(t)
		ws := seedWorkspace(t, c, "Bare")
		lanes, err := c.ListKanbanColumns(ctx, ws)
		if err != nil {
			t.Fatalf("ListKanbanColumns: %v", err)
		}
		for _, lane := range lanes {
			if _, _, derr := c.DeleteKanbanColumn(ctx, ws, lane.UUID, false); derr != nil {
				t.Fatalf("DeleteKanbanColumn(%s): %v", lane.Name, derr)
			}
		}
		iss, err := c.CreateIssue(ctx, ws, inputs.IssueAddInput{Title: "no lanes here"}, false)
		if err != nil {
			t.Fatalf("CreateIssue on a workspace with no lanes: %v", err)
		}
		if iss.KanbanColumnID != nil {
			t.Errorf("card was placed despite there being no lanes")
		}
	})

	t.Run("dry_run_projects_the_lane", func(t *testing.T) {
		c, _ := openTestLocalClient(t)
		ws := seedWorkspace(t, c, "Planning")
		lanes, err := c.ListKanbanColumns(ctx, ws)
		if err != nil {
			t.Fatalf("ListKanbanColumns: %v", err)
		}
		projected, err := c.CreateIssue(ctx, ws, inputs.IssueAddInput{Title: "rehearsed"}, true)
		if err != nil {
			t.Fatalf("CreateIssue(dry-run): %v", err)
		}
		if projected.KanbanColumnID == nil || *projected.KanbanColumnID != lanes[0].ID {
			t.Errorf("dry-run projection did not show the first lane")
		}
		issues, err := c.store.ListIssues(store.IssueFilter{RepoID: &ws.ID})
		if err != nil {
			t.Fatalf("ListIssues: %v", err)
		}
		if len(issues) != 0 {
			t.Fatalf("dry-run created %d issue(s)", len(issues))
		}
	})
}

// ---------- dispatch refusal ----------

func TestDispatchRefusedOnWorkspace(t *testing.T) {
	ctx := context.Background()
	c, _ := openTestLocalClient(t)
	ws := seedWorkspace(t, c, "Planning")
	iss, err := c.CreateIssue(ctx, ws, inputs.IssueAddInput{Title: "not for an agent"}, false)
	if err != nil {
		t.Fatalf("CreateIssue: %v", err)
	}

	t.Run("create_dispatch", func(t *testing.T) {
		_, err := c.CreateDispatch(ctx, ws, inputs.AgentDispatchInput{
			TargetAgent: "someone", IssueKey: iss.Key, Mode: "implement",
		}, false)
		assertWorkspaceRefusal(t, err, ws.Prefix)
	})

	t.Run("auto_dispatch_issue", func(t *testing.T) {
		_, err := c.AutoDispatchIssue(ctx, ws, iss.Key, "implement", false)
		assertWorkspaceRefusal(t, err, ws.Prefix)
	})

	t.Run("dry_run_is_refused_too", func(t *testing.T) {
		// The refusal is structural, not a write guard: rehearsing a
		// dispatch that can never work is still a wrong answer.
		_, err := c.AutoDispatchIssue(ctx, ws, iss.Key, "implement", true)
		assertWorkspaceRefusal(t, err, ws.Prefix)
	})

	t.Run("git_repo_is_unaffected", func(t *testing.T) {
		repo := seedRepo(t, c.store, "GITD", t.TempDir())
		gitIssue, err := c.CreateIssue(ctx, repo, inputs.IssueAddInput{Title: "real work"}, false)
		if err != nil {
			t.Fatalf("CreateIssue: %v", err)
		}
		if _, err := c.AutoDispatchIssue(ctx, repo, gitIssue.Key, "implement", true); err != nil {
			t.Fatalf("AutoDispatchIssue on a git repo: %v", err)
		}
	})
}

func assertWorkspaceRefusal(t *testing.T, err error, prefix string) {
	t.Helper()
	if err == nil {
		t.Fatal("expected a refusal on a workspace")
	}
	msg := err.Error()
	if !strings.Contains(msg, prefix) || !strings.Contains(msg, "workspace") {
		t.Fatalf("refusal should name the workspace; got %q", msg)
	}
	// The phantom message would send the user hunting for a checkout.
	if strings.Contains(msg, "link it first") {
		t.Fatalf("workspace refusal reused the phantom message: %q", msg)
	}
}

// ---------- the Path == "" sweep ----------

// TestSetupSyncRefusesWorkspaceWithOwnMessage covers sync_setup.go: a
// workspace and a phantom are both pathless but must not share a message.
func TestSetupSyncRefusesWorkspaceWithOwnMessage(t *testing.T) {
	ctx := context.Background()
	c, _ := openTestLocalClient(t)
	ws := seedWorkspace(t, c, "Planning")

	_, err := c.SetupSync(ctx, ws, inputs.SyncSetupInput{Mode: "init", LocalPath: t.TempDir()})
	assertWorkspaceRefusal(t, err, ws.Prefix)
	if !strings.Contains(err.Error(), "mirrored automatically") {
		t.Errorf("workspace sync refusal should explain the mirroring; got %q", err)
	}

	phantom, err := c.store.CreatePhantomRepo("uuid-phan-sweep", "PHAN", "phantom", "")
	if err != nil {
		t.Fatalf("CreatePhantomRepo: %v", err)
	}
	_, err = c.SetupSync(ctx, phantom, inputs.SyncSetupInput{Mode: "init", LocalPath: t.TempDir()})
	if err == nil || !strings.Contains(err.Error(), "link it first") {
		t.Fatalf("phantom refusal changed; got %v", err)
	}
}

// TestLinkPhantomRepoRefusesWorkspace covers local_repo.go's
// `repo.Path != ""` pair: a workspace is pathless but is not a phantom,
// so it gets its own RepoLinkError Kind rather than "not_phantom".
func TestLinkPhantomRepoRefusesWorkspace(t *testing.T) {
	ctx := context.Background()
	c, _ := openTestLocalClient(t)
	ws := seedWorkspace(t, c, "Planning")

	_, err := c.LinkPhantomRepo(ctx, ws.Prefix, t.TempDir(), false)
	if err == nil {
		t.Fatal("expected a refusal linking a workspace")
	}
	var linkErr *RepoLinkError
	if !errors.As(err, &linkErr) {
		t.Fatalf("want a *RepoLinkError, got %T: %v", err, err)
	}
	if linkErr.Kind != "workspace" {
		t.Errorf("Kind = %q, want workspace", linkErr.Kind)
	}
	// The row must be untouched — no half-applied link.
	after, err := c.store.GetRepoByPrefix(ws.Prefix)
	if err != nil {
		t.Fatalf("GetRepoByPrefix: %v", err)
	}
	if after.HasWorkingTree() || !after.IsWorkspace() {
		t.Fatalf("workspace row was mutated: %+v", after)
	}
}

// TestEnsureRepoDoesNotAdoptWorkspace covers local.go's
// matchPhantomByRemote guard: only a *git* phantom may be upgraded into
// a working tree. Belt-and-braces (the store forces a workspace's
// remote_url empty), but the predicate is what keeps it true if that
// ever changes.
func TestEnsureRepoDoesNotAdoptWorkspace(t *testing.T) {
	ctx := context.Background()
	c, _ := openTestLocalClient(t)
	ws := seedWorkspace(t, c, "Planning")

	root := t.TempDir()
	repo, created, err := c.EnsureRepo(ctx, &git.Info{
		Root: root, Name: "some-project", RemoteURL: "git@example.invalid:me/some-project.git",
	})
	if err != nil {
		t.Fatalf("EnsureRepo: %v", err)
	}
	if !created {
		t.Fatal("expected a fresh git registration")
	}
	if repo.Prefix == ws.Prefix {
		t.Fatal("EnsureRepo adopted the workspace")
	}
	if repo.IsWorkspace() {
		t.Fatalf("registered repo has kind %q", repo.Kind)
	}
	after, err := c.store.GetRepoByPrefix(ws.Prefix)
	if err != nil {
		t.Fatalf("GetRepoByPrefix: %v", err)
	}
	if after.HasWorkingTree() {
		t.Fatalf("workspace was upgraded to path %q", after.Path)
	}
}

// TestSyncRegistryOmitsWorkspace covers sync_status.go's unsynced
// residual. Under locked decision D4 a workspace cannot be set up for
// sync at all, so listing it under "Unsynced projects" would offer a
// setup SetupSync refuses.
func TestSyncRegistryOmitsWorkspace(t *testing.T) {
	ctx := context.Background()
	c, _ := openTestLocalClient(t)
	ws := seedWorkspace(t, c, "Planning")
	gitRepo := seedRepo(t, c.store, "GITS", t.TempDir())

	reg, err := c.SyncRegistry(ctx)
	if err != nil {
		t.Fatalf("SyncRegistry: %v", err)
	}
	var prefixes []string
	for _, p := range reg.UnsyncedProjects {
		prefixes = append(prefixes, p.Prefix)
	}
	if !contains(prefixes, gitRepo.Prefix) {
		t.Errorf("git repo %s missing from unsynced projects %v", gitRepo.Prefix, prefixes)
	}
	if contains(prefixes, ws.Prefix) {
		t.Errorf("workspace %s listed as an unsynced project %v", ws.Prefix, prefixes)
	}
}

// TestSyncStatusesSkipsWorkspaceConfigRead covers sync_status.go:58 —
// a workspace has no checkout, so there is no .bacio/config.yaml to read
// and it must never report `configured`.
func TestSyncStatusesSkipsWorkspaceConfigRead(t *testing.T) {
	ctx := context.Background()
	c, _ := openTestLocalClient(t)
	ws := seedWorkspace(t, c, "Planning")

	statuses, err := c.SyncStatuses(ctx)
	if err != nil {
		t.Fatalf("SyncStatuses: %v", err)
	}
	for _, st := range statuses {
		if st.Prefix != ws.Prefix {
			continue
		}
		if st.Configured {
			t.Error("workspace reported as sync-configured")
		}
		if st.Remote != "" {
			t.Errorf("workspace reported remote %q", st.Remote)
		}
		return
	}
	t.Fatalf("workspace %s missing from sync statuses", ws.Prefix)
}

func contains(haystack []string, needle string) bool {
	for _, s := range haystack {
		if s == needle {
			return true
		}
	}
	return false
}

// TestMoveIssueToKanbanColumnDryRunProjectsTheRealSlot pins the one thing
// a --dry-run exists to do: report the value the real call will write.
//
// The append path is the trap. `position` carries the
// kanbanAppendPosition sentinel (math.MaxInt32) whenever the caller omits
// a slot, and echoing it into the projection reported
// `kanban_position: 2147483647` on `bacio kanban move --column Doing
// --dry-run` and on PUT .../kanban?dry_run=true. Every case below asserts
// the projection against what the committed move actually produces rather
// than against a hard-coded number, so the two can't drift apart again.
func TestMoveIssueToKanbanColumnDryRunProjectsTheRealSlot(t *testing.T) {
	ctx := context.Background()

	// rehearseThenCommit runs the same move twice — once dry, once for
	// real — and returns both positions.
	rehearseThenCommit := func(t *testing.T, c *localClient, repo *model.Repo, in IssueKanbanMoveInput) (int, int) {
		t.Helper()
		dry, err := c.MoveIssueToKanbanColumn(ctx, repo, in, true)
		if err != nil {
			t.Fatalf("dry-run move: %v", err)
		}
		real, err := c.MoveIssueToKanbanColumn(ctx, repo, in, false)
		if err != nil {
			t.Fatalf("move: %v", err)
		}
		return dry.KanbanPosition, real.KanbanPosition
	}

	t.Run("append_onto_an_empty_lane", func(t *testing.T) {
		c, _ := openTestLocalClient(t)
		repo := seedBootstrappedRepo(t, c, "DRYA")
		lanes, err := c.ListKanbanColumns(ctx, repo)
		if err != nil {
			t.Fatalf("ListKanbanColumns: %v", err)
		}
		iss, err := c.CreateIssue(ctx, repo, inputs.IssueAddInput{Title: "first card"}, false)
		if err != nil {
			t.Fatalf("CreateIssue: %v", err)
		}
		dry, real := rehearseThenCommit(t, c, repo, IssueKanbanMoveInput{
			IssueKey: iss.Key, ColumnUUID: lanes[0].UUID, Position: nil,
		})
		if dry != real {
			t.Errorf("dry-run projected position %d, the real move produced %d", dry, real)
		}
		if real != 0 {
			t.Errorf("first card in an empty lane should land at 0, got %d", real)
		}
	})

	t.Run("append_onto_a_populated_lane", func(t *testing.T) {
		c, _ := openTestLocalClient(t)
		repo := seedBootstrappedRepo(t, c, "DRYB")
		lanes, err := c.ListKanbanColumns(ctx, repo)
		if err != nil {
			t.Fatalf("ListKanbanColumns: %v", err)
		}
		for _, title := range []string{"one", "two", "three"} {
			iss, err := c.CreateIssue(ctx, repo, inputs.IssueAddInput{Title: title}, false)
			if err != nil {
				t.Fatalf("CreateIssue(%s): %v", title, err)
			}
			if _, err := c.MoveIssueToKanbanColumn(ctx, repo, IssueKanbanMoveInput{
				IssueKey: iss.Key, ColumnUUID: lanes[0].UUID,
			}, false); err != nil {
				t.Fatalf("seed move(%s): %v", title, err)
			}
		}
		// A fourth card appended to a lane of three lands at index 3.
		iss, err := c.CreateIssue(ctx, repo, inputs.IssueAddInput{Title: "four"}, false)
		if err != nil {
			t.Fatalf("CreateIssue: %v", err)
		}
		dry, real := rehearseThenCommit(t, c, repo, IssueKanbanMoveInput{
			IssueKey: iss.Key, ColumnUUID: lanes[0].UUID, Position: nil,
		})
		if dry != real {
			t.Errorf("dry-run projected position %d, the real move produced %d", dry, real)
		}
		if real != 3 {
			t.Errorf("appended card should land at 3, got %d", real)
		}
	})

	t.Run("append_within_the_lane_the_card_is_already_in", func(t *testing.T) {
		// The off-by-one case: renumberKanbanLaneTx lifts the moved card
		// OUT before clamping, so a card already in the lane sees one
		// fewer slot than an arriving one.
		c, _ := openTestLocalClient(t)
		repo := seedBootstrappedRepo(t, c, "DRYC")
		lanes, err := c.ListKanbanColumns(ctx, repo)
		if err != nil {
			t.Fatalf("ListKanbanColumns: %v", err)
		}
		var first *model.Issue
		for _, title := range []string{"one", "two", "three"} {
			iss, err := c.CreateIssue(ctx, repo, inputs.IssueAddInput{Title: title}, false)
			if err != nil {
				t.Fatalf("CreateIssue(%s): %v", title, err)
			}
			if first == nil {
				first = iss
			}
			if _, err := c.MoveIssueToKanbanColumn(ctx, repo, IssueKanbanMoveInput{
				IssueKey: iss.Key, ColumnUUID: lanes[0].UUID,
			}, false); err != nil {
				t.Fatalf("seed move(%s): %v", title, err)
			}
		}
		dry, real := rehearseThenCommit(t, c, repo, IssueKanbanMoveInput{
			IssueKey: first.Key, ColumnUUID: lanes[0].UUID, Position: nil,
		})
		if dry != real {
			t.Errorf("dry-run projected position %d, the real move produced %d", dry, real)
		}
		if real != 2 {
			t.Errorf("re-appending within a 3-card lane should land at 2, got %d", real)
		}
	})

	t.Run("explicit_position_past_the_end_is_clamped_in_both", func(t *testing.T) {
		c, _ := openTestLocalClient(t)
		repo := seedBootstrappedRepo(t, c, "DRYD")
		lanes, err := c.ListKanbanColumns(ctx, repo)
		if err != nil {
			t.Fatalf("ListKanbanColumns: %v", err)
		}
		iss, err := c.CreateIssue(ctx, repo, inputs.IssueAddInput{Title: "only card"}, false)
		if err != nil {
			t.Fatalf("CreateIssue: %v", err)
		}
		want := 99
		dry, real := rehearseThenCommit(t, c, repo, IssueKanbanMoveInput{
			IssueKey: iss.Key, ColumnUUID: lanes[0].UUID, Position: &want,
		})
		if dry != real {
			t.Errorf("dry-run projected position %d, the real move produced %d", dry, real)
		}
		if real != 0 {
			t.Errorf("clamped position should be 0, got %d", real)
		}
	})

	t.Run("off_the_board_projects_zero", func(t *testing.T) {
		c, _ := openTestLocalClient(t)
		ws := seedWorkspace(t, c, "Planning")
		iss, err := c.CreateIssue(ctx, ws, inputs.IssueAddInput{Title: "on the board"}, false)
		if err != nil {
			t.Fatalf("CreateIssue: %v", err)
		}
		dry, real := rehearseThenCommit(t, c, ws, IssueKanbanMoveInput{
			IssueKey: iss.Key, ColumnUUID: "",
		})
		if dry != real || real != 0 {
			t.Errorf("off-board: dry-run %d, real %d, want both 0", dry, real)
		}
	})
}
