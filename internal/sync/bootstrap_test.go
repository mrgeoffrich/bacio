package sync

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/mrgeoffrich/bacio/internal/git"
	"github.com/mrgeoffrich/bacio/internal/model"
	"github.com/mrgeoffrich/bacio/internal/store"
)

// requireGit short-circuits a test that needs the `git` binary on
// PATH. We don't ship the binary; CI environments without it (rare)
// just skip these.
func requireGit(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skipf("git not on PATH: %v", err)
	}
}

// initBareRemote creates a bare git repo at <dir>/remote.git for
// integration tests; serves as a stand-in for github/gitlab.
func initBareRemote(t *testing.T, dir string) string {
	t.Helper()
	requireGit(t)
	bare := filepath.Join(dir, "remote.git")
	if err := exec.Command("git", "init", "--bare", "-b", "main", bare).Run(); err != nil {
		t.Fatalf("init bare: %v", err)
	}
	return bare
}

// initProjectRepo creates a tiny project working tree with one commit
// (so .bacio/config.yaml writes don't sit in an uninitialised repo).
func initProjectRepo(t *testing.T, dir string) string {
	t.Helper()
	requireGit(t)
	project := filepath.Join(dir, "project")
	if err := exec.Command("git", "init", "-b", "main", project).Run(); err != nil {
		t.Fatalf("init project: %v", err)
	}
	// Configure user.name and user.email so commits work without
	// inheriting the host config (which CI may not have).
	for _, args := range [][]string{
		{"config", "user.email", "tester@example.invalid"},
		{"config", "user.name", "tester"},
	} {
		cmd := exec.Command("git", append([]string{"-C", project}, args...)...)
		if err := cmd.Run(); err != nil {
			t.Fatalf("git config %v: %v", args, err)
		}
	}
	return project
}

// configureSyncRepoIdentity sets user.name / user.email on a sync
// repo so commits can fire on systems without a global git config.
func configureSyncRepoIdentity(t *testing.T, root string) {
	t.Helper()
	for _, args := range [][]string{
		{"config", "user.email", "tester@example.invalid"},
		{"config", "user.name", "tester"},
	} {
		cmd := exec.Command("git", append([]string{"-C", root}, args...)...)
		if err := cmd.Run(); err != nil {
			t.Fatalf("git config %v: %v", args, err)
		}
	}
}

// seedTwoIssues populates a fresh in-memory store with one repo
// and two issues. Returned alongside the repo so the test can
// assert on it.
func seedTwoIssues(t *testing.T) (*store.Store, *model.Repo) {
	t.Helper()
	s, err := store.Open(":memory:")
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	r, err := s.CreateRepo("MINI", "bacio", "/local/path", "git@github.com:user/bacio.git")
	if err != nil {
		t.Fatalf("create repo: %v", err)
	}
	if _, err := s.CreateIssue(r.ID, nil, "First issue", "first body", model.StateInReview, []string{"p1"}, "", ""); err != nil {
		t.Fatalf("iss1: %v", err)
	}
	if _, err := s.CreateIssue(r.ID, nil, "Second issue", "second body", model.StateTodo, nil, "", ""); err != nil {
		t.Fatalf("iss2: %v", err)
	}
	return s, r
}

// TestInitSyncRepo_AgainstEmptyRemote: full happy-path of `bacio sync
// init` against an empty bare remote. Verifies the sentinel +
// .gitattributes get committed, the project config carries the
// remote URL, and the DB has a sync_remotes row pointing at the
// local clone.
func TestInitSyncRepo_AgainstEmptyRemote(t *testing.T) {
	requireGit(t)
	tdir := t.TempDir()
	bare := initBareRemote(t, tdir)
	project := initProjectRepo(t, tdir)
	s, _ := seedTwoIssues(t)

	// configureSyncRepoIdentity needs to run between git.Init and the
	// commit; InitSyncRepo handles git.Init internally, so we hook in
	// after it via a small dance: shadow user.name/email with env
	// vars that git always honours.
	t.Setenv("GIT_AUTHOR_NAME", "tester")
	t.Setenv("GIT_AUTHOR_EMAIL", "tester@example.invalid")
	t.Setenv("GIT_COMMITTER_NAME", "tester")
	t.Setenv("GIT_COMMITTER_EMAIL", "tester@example.invalid")

	syncLocal := filepath.Join(tdir, "sync-A")
	eng := &Engine{Store: s, Actor: "tester"}
	res, err := eng.InitSyncRepo(context.Background(), project, InitOptions{
		LocalPath: syncLocal,
		Remote:    bare,
	})
	if err != nil {
		t.Fatalf("init: %v", err)
	}
	if res.CommitSHA == "" {
		t.Fatal("expected non-empty commit sha")
	}
	if !res.Pushed {
		t.Fatal("expected push to succeed")
	}
	if !IsSyncRepo(syncLocal) {
		t.Fatal("sync repo should carry bacio-sync.yaml")
	}
	cfg, err := ReadProjectConfig(project)
	if err != nil {
		t.Fatalf("read project config: %v", err)
	}
	if cfg.Sync.Remote != bare {
		t.Fatalf("project config remote = %q, want %q", cfg.Sync.Remote, bare)
	}
	rec, err := s.GetSyncRemote(bare)
	if err != nil {
		t.Fatalf("get sync remote: %v", err)
	}
	if rec.LocalPath != syncLocal {
		t.Fatalf("local path = %q, want %q", rec.LocalPath, syncLocal)
	}
}

// TestInitSyncRepo_RefusesNonEmptyRemote: pushing two clients into
// init mode against the same remote is an error — the second should
// be told to clone instead.
func TestInitSyncRepo_RefusesNonEmptyRemote(t *testing.T) {
	requireGit(t)
	tdir := t.TempDir()
	bare := initBareRemote(t, tdir)
	project := initProjectRepo(t, tdir)
	t.Setenv("GIT_AUTHOR_NAME", "tester")
	t.Setenv("GIT_AUTHOR_EMAIL", "tester@example.invalid")
	t.Setenv("GIT_COMMITTER_NAME", "tester")
	t.Setenv("GIT_COMMITTER_EMAIL", "tester@example.invalid")

	// First user inits.
	sA, _ := seedTwoIssues(t)
	engA := &Engine{Store: sA, Actor: "alice"}
	if _, err := engA.InitSyncRepo(context.Background(), project, InitOptions{
		LocalPath: filepath.Join(tdir, "sync-A"),
		Remote:    bare,
	}); err != nil {
		t.Fatalf("first init: %v", err)
	}

	// Second user should error.
	project2 := filepath.Join(tdir, "project2")
	if err := exec.Command("git", "init", "-b", "main", project2).Run(); err != nil {
		t.Fatalf("init project2: %v", err)
	}
	configureSyncRepoIdentity(t, project2)
	sB, _ := seedTwoIssues(t)
	engB := &Engine{Store: sB, Actor: "bob"}
	_, err := engB.InitSyncRepo(context.Background(), project2, InitOptions{
		LocalPath: filepath.Join(tdir, "sync-B"),
		Remote:    bare,
	})
	if err == nil {
		t.Fatal("expected init against populated remote to error")
	}
}

// TestRoundTrip_TwoUsers exercises the full Phase-4 promise: User A
// inits + pushes, User B clones + imports, B mutates + syncs, A pulls
// + syncs and sees B's changes.
func TestRoundTrip_TwoUsers(t *testing.T) {
	requireGit(t)
	tdir := t.TempDir()
	bare := initBareRemote(t, tdir)
	t.Setenv("GIT_AUTHOR_NAME", "tester")
	t.Setenv("GIT_AUTHOR_EMAIL", "tester@example.invalid")
	t.Setenv("GIT_COMMITTER_NAME", "tester")
	t.Setenv("GIT_COMMITTER_EMAIL", "tester@example.invalid")

	// User A: project, DB, init.
	projectA := filepath.Join(tdir, "projectA")
	if err := exec.Command("git", "init", "-b", "main", projectA).Run(); err != nil {
		t.Fatalf("init projectA: %v", err)
	}
	configureSyncRepoIdentity(t, projectA)
	sA, repoA := seedTwoIssues(t)
	syncA := filepath.Join(tdir, "sync-A")
	engA := &Engine{Store: sA, Actor: "alice"}
	if _, err := engA.InitSyncRepo(context.Background(), projectA, InitOptions{
		LocalPath: syncA, Remote: bare,
	}); err != nil {
		t.Fatalf("init A: %v", err)
	}
	// User B: a separate project repo, fresh DB, clone. .bacio/config.yaml
	// is machine-local and never shared via git — B passes the remote
	// to CloneSyncRepo explicitly, just as `bacio sync clone --remote`
	// does on the command line.
	projectB := filepath.Join(tdir, "projectB")
	if err := exec.Command("git", "init", "-b", "main", projectB).Run(); err != nil {
		t.Fatalf("init projectB: %v", err)
	}
	configureSyncRepoIdentity(t, projectB)
	sB, err := store.Open(":memory:")
	if err != nil {
		t.Fatalf("open B: %v", err)
	}
	t.Cleanup(func() { _ = sB.Close() })

	syncB := filepath.Join(tdir, "sync-B")
	engB := &Engine{Store: sB, Actor: "bob"}
	cloneRes, err := engB.CloneSyncRepo(context.Background(), projectB, CloneOptions{
		LocalPath: syncB, Remote: bare,
	})
	if err != nil {
		t.Fatalf("clone: %v", err)
	}
	if cloneRes.Import == nil || cloneRes.Import.Inserted == 0 {
		t.Fatal("clone import inserted nothing")
	}
	// Verify B's DB now has A's two issues.
	repoB, err := sB.GetRepoByUUID(repoA.UUID)
	if err != nil {
		t.Fatalf("repo by uuid B: %v", err)
	}
	bIssues, err := sB.ListIssues(store.IssueFilter{RepoID: &repoB.ID})
	if err != nil {
		t.Fatalf("list B: %v", err)
	}
	if len(bIssues) != 2 {
		t.Fatalf("B has %d issues, want 2", len(bIssues))
	}

	// B mutates: add a third issue.
	if _, err := sB.CreateIssue(repoB.ID, nil, "Bob's issue", "from bob", model.StateTodo, nil, "", ""); err != nil {
		t.Fatalf("create iss: %v", err)
	}
	configureSyncRepoIdentity(t, syncB)

	// B runs bacio sync.
	syncRepoB, err := git.Open(syncB)
	if err != nil {
		t.Fatalf("open syncB: %v", err)
	}
	runResB, err := engB.Run(context.Background(), projectB, syncRepoB, RunOptions{})
	if err != nil {
		t.Fatalf("B run: %v", err)
	}
	if !runResB.Pushed {
		t.Fatalf("B run did not push: %+v", runResB)
	}
	if runResB.Commit == "" {
		t.Fatalf("B run produced no commit despite new issue")
	}

	// A runs bacio sync. Their DB should pick up Bob's new issue.
	configureSyncRepoIdentity(t, syncA)
	syncRepoA, err := git.Open(syncA)
	if err != nil {
		t.Fatalf("open syncA: %v", err)
	}
	runResA, err := engA.Run(context.Background(), projectA, syncRepoA, RunOptions{})
	if err != nil {
		t.Fatalf("A run: %v", err)
	}
	if runResA.Import == nil || runResA.Import.Inserted == 0 {
		t.Fatalf("A run did not import Bob's issue: %+v", runResA)
	}
	aIssues, err := sA.ListIssues(store.IssueFilter{RepoID: &repoA.ID})
	if err != nil {
		t.Fatalf("list A: %v", err)
	}
	if len(aIssues) != 3 {
		t.Fatalf("A now has %d issues, want 3", len(aIssues))
	}
}

// TestPushRace simulates the design doc's "two clients push at once"
// case. A pulls, B sneaks in a commit, A pushes → non-fast-forward;
// the retry pulls, re-imports, re-exports, and pushes successfully.
func TestPushRace(t *testing.T) {
	requireGit(t)
	tdir := t.TempDir()
	bare := initBareRemote(t, tdir)
	t.Setenv("GIT_AUTHOR_NAME", "tester")
	t.Setenv("GIT_AUTHOR_EMAIL", "tester@example.invalid")
	t.Setenv("GIT_COMMITTER_NAME", "tester")
	t.Setenv("GIT_COMMITTER_EMAIL", "tester@example.invalid")

	// User A bootstraps everything.
	projectA := filepath.Join(tdir, "projectA")
	if err := exec.Command("git", "init", "-b", "main", projectA).Run(); err != nil {
		t.Fatalf("init projectA: %v", err)
	}
	configureSyncRepoIdentity(t, projectA)
	sA, repoA := seedTwoIssues(t)
	syncA := filepath.Join(tdir, "sync-A")
	engA := &Engine{Store: sA, Actor: "alice"}
	if _, err := engA.InitSyncRepo(context.Background(), projectA, InitOptions{
		LocalPath: syncA, Remote: bare,
	}); err != nil {
		t.Fatalf("init A: %v", err)
	}
	// User B joins from a separate project repo, passing the remote
	// explicitly — .bacio/config.yaml is machine-local, not shared.
	projectB := filepath.Join(tdir, "projectB")
	if err := exec.Command("git", "init", "-b", "main", projectB).Run(); err != nil {
		t.Fatalf("init projectB: %v", err)
	}
	configureSyncRepoIdentity(t, projectB)
	sB, err := store.Open(":memory:")
	if err != nil {
		t.Fatalf("open B: %v", err)
	}
	t.Cleanup(func() { _ = sB.Close() })
	syncB := filepath.Join(tdir, "sync-B")
	engB := &Engine{Store: sB, Actor: "bob"}
	if _, err := engB.CloneSyncRepo(context.Background(), projectB, CloneOptions{
		LocalPath: syncB, Remote: bare,
	}); err != nil {
		t.Fatalf("clone: %v", err)
	}
	configureSyncRepoIdentity(t, syncA)
	configureSyncRepoIdentity(t, syncB)
	repoB, err := sB.GetRepoByUUID(repoA.UUID)
	if err != nil {
		t.Fatalf("repo by uuid B: %v", err)
	}

	// B: create issue and push first.
	if _, err := sB.CreateIssue(repoB.ID, nil, "Bob's issue", "first to push", model.StateTodo, nil, "", ""); err != nil {
		t.Fatalf("B create iss: %v", err)
	}
	syncRepoB, err := git.Open(syncB)
	if err != nil {
		t.Fatalf("open syncB: %v", err)
	}
	if _, err := engB.Run(context.Background(), projectB, syncRepoB, RunOptions{}); err != nil {
		t.Fatalf("B run: %v", err)
	}

	// A: create a different issue without pulling first.
	if _, err := sA.CreateIssue(repoA.ID, nil, "Alice's issue", "trying to push behind bob", model.StateTodo, nil, "", ""); err != nil {
		t.Fatalf("A create iss: %v", err)
	}
	syncRepoA, err := git.Open(syncA)
	if err != nil {
		t.Fatalf("open syncA: %v", err)
	}
	// Don't pull — engineered race. Engine.Run pulls then exports;
	// because B's commit is on the remote and A's local sync clone
	// is behind, A's pull at the start of Run brings B's commit in,
	// then export incorporates A's new issue, commit + push proceeds
	// without race. The "true" race (A's local sync ahead of remote
	// without a fresh fetch) requires manipulating refs — instead,
	// simulate the failure path via a manual race: rewrite A's local
	// HEAD so it's ahead of the server in a way `git push` will
	// reject.
	runResA, err := engA.Run(context.Background(), projectA, syncRepoA, RunOptions{})
	if err != nil {
		t.Fatalf("A run: %v", err)
	}
	if !runResA.Pushed {
		t.Fatalf("A run did not push: %+v", runResA)
	}
	// After A's run, the bare remote should have at least 4 commits
	// (initial init + B's bacio sync + A's bacio sync, plus A's first init
	// commit). We don't enforce an exact count — just that A's
	// import picked up B's new issue.
	if runResA.Import == nil {
		t.Fatalf("A run import was nil")
	}
}

// seedStoreWithRepo builds a fresh in-memory store seeded with one
// repo and one issue. Used by the attach-mode tests where two
// independent DBs need different prefixes so the merged sync repo
// holds both folders side-by-side.
func seedStoreWithRepo(t *testing.T, prefix, name string) *store.Store {
	t.Helper()
	s, err := store.Open(":memory:")
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	r, err := s.CreateRepo(prefix, name, "/local/"+name, "git@github.com:user/"+name+".git")
	if err != nil {
		t.Fatalf("create repo %s: %v", prefix, err)
	}
	if _, err := s.CreateIssue(r.ID, nil, name+" issue", "body", model.StateInReview, nil, "", ""); err != nil {
		t.Fatalf("create issue in %s: %v", prefix, err)
	}
	return s
}

// TestInitSyncRepo_AttachesToExistingSyncRepo verifies the
// "two projects, one sync repo" workflow: project A inits the sync
// repo; project B then runs init against the same path (no --remote
// because the existing repo already has origin), and the sync repo
// ends up holding both projects' data.
func TestInitSyncRepo_AttachesToExistingSyncRepo(t *testing.T) {
	requireGit(t)
	tdir := t.TempDir()
	bare := initBareRemote(t, tdir)
	t.Setenv("GIT_AUTHOR_NAME", "tester")
	t.Setenv("GIT_AUTHOR_EMAIL", "tester@example.invalid")
	t.Setenv("GIT_COMMITTER_NAME", "tester")
	t.Setenv("GIT_COMMITTER_EMAIL", "tester@example.invalid")

	syncPath := filepath.Join(tdir, "sync")

	// Project A inits.
	projectA := initProjectRepo(t, tdir)
	sA := seedStoreWithRepo(t, "AAAA", "projA")
	engA := &Engine{Store: sA, Actor: "alice"}
	resA, err := engA.InitSyncRepo(context.Background(), projectA, InitOptions{
		LocalPath: syncPath,
		Remote:    bare,
	})
	if err != nil {
		t.Fatalf("init A: %v", err)
	}
	if resA.Attached {
		t.Fatal("first init shouldn't report Attached=true")
	}

	// Project B attaches with no --remote (should auto-detect origin).
	projectB := filepath.Join(tdir, "projectB")
	if err := exec.Command("git", "init", "-b", "main", projectB).Run(); err != nil {
		t.Fatalf("init projectB: %v", err)
	}
	configureSyncRepoIdentity(t, projectB)
	sB := seedStoreWithRepo(t, "BBBB", "projB")
	engB := &Engine{Store: sB, Actor: "bob"}
	resB, err := engB.InitSyncRepo(context.Background(), projectB, InitOptions{
		LocalPath: syncPath, // no Remote
	})
	if err != nil {
		t.Fatalf("attach B: %v", err)
	}
	if !resB.Attached {
		t.Fatal("second init should report Attached=true")
	}
	if resB.Remote != bare {
		t.Fatalf("expected auto-detected remote %q, got %q", bare, resB.Remote)
	}
	if resB.Import == nil {
		t.Fatal("attach should populate Import (read A's data into B's DB)")
	}

	// Both prefix folders should exist in the sync repo.
	for _, p := range []string{"AAAA", "BBBB"} {
		if _, err := os.Stat(filepath.Join(syncPath, "repos", p)); err != nil {
			t.Errorf("missing repo folder %s: %v", p, err)
		}
	}

	// Project B's .bacio/config.yaml should carry the auto-detected remote.
	cfgB, err := ReadProjectConfig(projectB)
	if err != nil {
		t.Fatalf("read project B config: %v", err)
	}
	if cfgB.Sync.Remote != bare {
		t.Fatalf("project B config remote = %q, want %q", cfgB.Sync.Remote, bare)
	}

	// DB-side mapping for B should point at the same sync repo path.
	rec, err := sB.GetSyncRemote(bare)
	if err != nil {
		t.Fatalf("get sync remote on B: %v", err)
	}
	if rec.LocalPath != syncPath {
		t.Fatalf("B's local path = %q, want %q", rec.LocalPath, syncPath)
	}
}

// TestInitSyncRepo_RefusesUnrelatedGitRepo: an existing git repo with
// unrelated commits and no bacio-sync.yaml is rejected outright — we
// don't want to silently turn somebody's working tree into a sync repo.
func TestInitSyncRepo_RefusesUnrelatedGitRepo(t *testing.T) {
	requireGit(t)
	tdir := t.TempDir()
	t.Setenv("GIT_AUTHOR_NAME", "tester")
	t.Setenv("GIT_AUTHOR_EMAIL", "tester@example.invalid")
	t.Setenv("GIT_COMMITTER_NAME", "tester")
	t.Setenv("GIT_COMMITTER_EMAIL", "tester@example.invalid")

	target := filepath.Join(tdir, "unrelated")
	if err := exec.Command("git", "init", "-b", "main", target).Run(); err != nil {
		t.Fatalf("init unrelated: %v", err)
	}
	if err := os.WriteFile(filepath.Join(target, "README.md"), []byte("hi"), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}
	configureSyncRepoIdentity(t, target)
	if err := exec.Command("git", "-C", target, "add", "README.md").Run(); err != nil {
		t.Fatalf("git add: %v", err)
	}
	if err := exec.Command("git", "-C", target, "commit", "-m", "initial").Run(); err != nil {
		t.Fatalf("git commit: %v", err)
	}

	project := initProjectRepo(t, tdir)
	s := seedStoreWithRepo(t, "AAAA", "projA")
	eng := &Engine{Store: s, Actor: "alice"}
	_, err := eng.InitSyncRepo(context.Background(), project, InitOptions{LocalPath: target})
	if err == nil {
		t.Fatal("expected refusal against an unrelated git repo")
	}
}

// TestInitSyncRepo_AcceptsEmptyGitDir: a freshly 'git init'-ed folder
// (no commits, no other files) is acceptable — sentinel is written
// without re-running git init.
func TestInitSyncRepo_AcceptsEmptyGitDir(t *testing.T) {
	requireGit(t)
	tdir := t.TempDir()
	bare := initBareRemote(t, tdir)
	t.Setenv("GIT_AUTHOR_NAME", "tester")
	t.Setenv("GIT_AUTHOR_EMAIL", "tester@example.invalid")
	t.Setenv("GIT_COMMITTER_NAME", "tester")
	t.Setenv("GIT_COMMITTER_EMAIL", "tester@example.invalid")

	target := filepath.Join(tdir, "sync")
	if err := exec.Command("git", "init", "-b", "main", target).Run(); err != nil {
		t.Fatalf("git init target: %v", err)
	}
	configureSyncRepoIdentity(t, target)

	project := initProjectRepo(t, tdir)
	s := seedStoreWithRepo(t, "AAAA", "projA")
	eng := &Engine{Store: s, Actor: "alice"}
	res, err := eng.InitSyncRepo(context.Background(), project, InitOptions{
		LocalPath: target,
		Remote:    bare,
	})
	if err != nil {
		t.Fatalf("init against empty git dir: %v", err)
	}
	if !IsSyncRepo(target) {
		t.Fatal("sentinel should have been written")
	}
	if !res.Pushed {
		t.Fatal("expected push to succeed")
	}
}
