package cli

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/mrgeoffrich/bacio/internal/cli/inputs"
	"github.com/mrgeoffrich/bacio/internal/git"
	"github.com/mrgeoffrich/bacio/internal/procfind"
	"github.com/mrgeoffrich/bacio/internal/wtenv"
)

// initRealGitRepo seeds a real git working tree at t.TempDir() and
// returns its absolute (symlink-resolved) path. Tests need a real
// `.git/` so git.Detect resolves correctly.
func initRealGitRepo(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skipf("git not on PATH: %v", err)
	}
	dir := t.TempDir()
	if err := exec.Command("git", "init", "-b", "main", dir).Run(); err != nil {
		t.Fatalf("git init: %v", err)
	}
	resolved, err := filepath.EvalSymlinks(dir)
	if err != nil {
		t.Fatalf("eval symlinks: %v", err)
	}
	return resolved
}

// TestWorktreeInit_RoundTrip drives runWorktreeInit + commitWorktreeInit
// + ensureWorktreeManifestGitignored end-to-end. Verifies the manifest
// is written, the registry row lands, and .gitignore picks up the
// filename exactly once on a repeat call.
func TestWorktreeInit_RoundTrip(t *testing.T) {
	root := initRealGitRepo(t)
	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)

	res, err := runWorktreeInit(root, filepath.Base(root), inputs.WorktreeInitInput{Slug: "demo-wt"})
	if err != nil {
		t.Fatalf("runWorktreeInit: %v", err)
	}
	if res.Manifest.Identity.Slug != "demo-wt" {
		t.Fatalf("slug: got %q", res.Manifest.Identity.Slug)
	}
	if res.Manifest.Allocations.APIPort == wtenv.DefaultAPIPort || res.Manifest.Allocations.APIPort == 0 {
		t.Fatalf("port: got %d", res.Manifest.Allocations.APIPort)
	}
	// BACI-87: a default init (no --isolate-db) pins the shared store
	// by absolute path, not a per-worktree .bacio/db.sqlite.
	wantShared := filepath.Join(tmpHome, ".bacio", "db.sqlite")
	if res.Manifest.Allocations.DBPath != wantShared {
		t.Fatalf("db_path: got %q, want shared %q", res.Manifest.Allocations.DBPath, wantShared)
	}

	if err := commitWorktreeInit(res); err != nil {
		t.Fatalf("commitWorktreeInit: %v", err)
	}
	// Manifest exists.
	if _, err := os.Stat(res.ManifestPath); err != nil {
		t.Fatalf("manifest missing: %v", err)
	}
	// .gitignore got the line on first run.
	if !res.GitignoreAdded {
		t.Errorf("gitignore_added: want true on first run")
	}

	// Re-running with --force is allowed; idempotent.
	res2, err := runWorktreeInit(root, filepath.Base(root), inputs.WorktreeInitInput{Slug: "demo-wt", Force: true})
	if err != nil {
		t.Fatalf("re-run: %v", err)
	}
	if err := commitWorktreeInit(res2); err != nil {
		t.Fatalf("commit re-run: %v", err)
	}
	if res2.GitignoreAdded {
		t.Errorf("gitignore_added: want false on idempotent second run")
	}

	// Registry has the entry.
	reg, err := wtenv.ReadRegistry(tmpHome)
	if err != nil {
		t.Fatalf("read registry: %v", err)
	}
	entry, ok := reg.FindBySlug("demo-wt")
	if !ok {
		t.Fatalf("registry missing slug: %+v", reg.Worktrees)
	}
	if entry.Path != root {
		t.Errorf("registry path: got %q want %q", entry.Path, root)
	}
	if entry.APIPort != res.Manifest.Allocations.APIPort {
		t.Errorf("registry port: got %d want %d", entry.APIPort, res.Manifest.Allocations.APIPort)
	}

	// .gitignore line appears exactly once.
	gi, err := os.ReadFile(filepath.Join(root, ".gitignore"))
	if err != nil {
		t.Fatalf("read .gitignore: %v", err)
	}
	if strings.Count(string(gi), wtenv.DefaultManifestFilename) != 1 {
		t.Errorf(".gitignore: line count: %q", string(gi))
	}
}

// TestWorktreeInit_RefusesWithoutForce checks that re-running init
// against an existing manifest fails unless --force is set.
func TestWorktreeInit_RefusesWithoutForce(t *testing.T) {
	root := initRealGitRepo(t)
	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)

	if _, err := runWorktreeInit(root, filepath.Base(root), inputs.WorktreeInitInput{Slug: "demo"}); err != nil {
		t.Fatalf("first run: %v", err)
	}
	res, _ := runWorktreeInit(root, filepath.Base(root), inputs.WorktreeInitInput{Slug: "demo"})
	if res != nil {
		// runWorktreeInit only errors when the manifest exists on disk; need to commit first.
		_ = commitWorktreeInit(res)
	}
	if _, err := runWorktreeInit(root, filepath.Base(root), inputs.WorktreeInitInput{Slug: "demo"}); err == nil {
		t.Fatalf("second run: want error without --force")
	}
}

// TestWorktreeInit_RejectsReservedPort ensures the legacy default
// port can't be allocated to a worktree manifest (otherwise it would
// clash with the legacy default bacio instance).
func TestWorktreeInit_RejectsReservedPort(t *testing.T) {
	root := initRealGitRepo(t)
	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)

	_, err := runWorktreeInit(root, filepath.Base(root), inputs.WorktreeInitInput{Slug: "demo", Port: wtenv.DefaultAPIPort})
	if err == nil {
		t.Fatal("want error on reserved port")
	}
	if !strings.Contains(err.Error(), "reserved") {
		t.Errorf("err: %v", err)
	}
}

// addLinkedWorktree adds a second working tree to an existing repo
// (created with initRealGitRepo). The repo needs at least one commit
// for `git worktree add` to succeed, so seed an empty commit first.
// Returns the linked worktree's absolute (symlink-resolved) path.
func addLinkedWorktree(t *testing.T, mainRoot, branch string) string {
	t.Helper()
	// Seed identity so the seed commit succeeds without inheriting
	// the host's global git config.
	t.Setenv("GIT_AUTHOR_NAME", "tester")
	t.Setenv("GIT_AUTHOR_EMAIL", "tester@example.invalid")
	t.Setenv("GIT_COMMITTER_NAME", "tester")
	t.Setenv("GIT_COMMITTER_EMAIL", "tester@example.invalid")
	run := func(args ...string) {
		cmd := exec.Command("git", args...)
		cmd.Dir = mainRoot
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	run("commit", "--allow-empty", "-q", "-m", "seed")
	wt := filepath.Join(t.TempDir(), "linked")
	run("worktree", "add", "-q", wt, "-b", branch)
	resolved, err := filepath.EvalSymlinks(wt)
	if err != nil {
		t.Fatalf("eval symlinks(linked): %v", err)
	}
	return resolved
}

// TestWorktreeInit_LinkedWorktreeWritesToLinkedRoot is the BACI-71
// end-to-end regression on the writer side: running `bacio worktree
// init` from inside a linked git worktree must land the manifest at
// THAT worktree's root, not the main worktree's. Before the fix the
// init path called git.Detect (which returns the main worktree) and
// silently clobbered the parent's manifest.
func TestWorktreeInit_LinkedWorktreeWritesToLinkedRoot(t *testing.T) {
	main := initRealGitRepo(t)
	linked := addLinkedWorktree(t, main, "feature/baci-71")
	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)

	// This is what newWorktreeInitCmd does post-fix: ask git for the
	// linked-worktree root and pass it to runWorktreeInit. Drive that
	// same shape directly so the test fails LOUDLY if anyone reverts
	// the call site back to git.Detect.
	root, err := git.WorktreeRoot(linked)
	if err != nil {
		t.Fatalf("git.WorktreeRoot(linked): %v", err)
	}
	if root != linked {
		t.Fatalf("git.WorktreeRoot(linked) = %q, want %q (BACI-71 regression)", root, linked)
	}

	res, err := runWorktreeInit(root, filepath.Base(root), inputs.WorktreeInitInput{Slug: "linked-wt"})
	if err != nil {
		t.Fatalf("runWorktreeInit: %v", err)
	}
	if err := commitWorktreeInit(res); err != nil {
		t.Fatalf("commitWorktreeInit: %v", err)
	}

	wantManifest := filepath.Join(linked, wtenv.DefaultManifestFilename)
	if res.ManifestPath != wantManifest {
		t.Errorf("manifest_path: got %q, want %q (under the LINKED worktree, not main)", res.ManifestPath, wantManifest)
	}
	if _, err := os.Stat(wantManifest); err != nil {
		t.Errorf("manifest missing under linked root: %v", err)
	}
	if _, err := os.Stat(filepath.Join(main, wtenv.DefaultManifestFilename)); err == nil {
		t.Errorf("manifest leaked into MAIN worktree at %s — BACI-71 regression", main)
	}
	if got, want := res.Manifest.Identity.Worktree, linked; got != want {
		t.Errorf("manifest.identity.worktree: got %q, want %q", got, want)
	}

	// Registry row points at the linked worktree, not the main one.
	reg, err := wtenv.ReadRegistry(tmpHome)
	if err != nil {
		t.Fatalf("read registry: %v", err)
	}
	entry, ok := reg.FindBySlug("linked-wt")
	if !ok {
		t.Fatalf("registry missing slug: %+v", reg.Worktrees)
	}
	if entry.Path != linked {
		t.Errorf("registry path: got %q, want %q (linked root)", entry.Path, linked)
	}
}

// TestWorktreeInit_DBIsolationModes covers the BACI-87 db_path
// precedence: default → shared store pinned absolute; --isolate-db →
// per-worktree .bacio/db.sqlite; --db-path → explicit, and it wins
// even when --isolate-db is also set.
func TestWorktreeInit_DBIsolationModes(t *testing.T) {
	t.Run("default is the shared store", func(t *testing.T) {
		root := initRealGitRepo(t)
		tmpHome := t.TempDir()
		t.Setenv("HOME", tmpHome)

		res, err := runWorktreeInit(root, filepath.Base(root), inputs.WorktreeInitInput{Slug: "shared-wt"})
		if err != nil {
			t.Fatalf("runWorktreeInit: %v", err)
		}
		want := filepath.Join(tmpHome, ".bacio", "db.sqlite")
		if res.Manifest.Allocations.DBPath != want {
			t.Errorf("db_path: got %q, want %q", res.Manifest.Allocations.DBPath, want)
		}
		// Still gets an isolated, non-default port.
		if p := res.Manifest.Allocations.APIPort; p == wtenv.DefaultAPIPort || p == 0 {
			t.Errorf("api_port: got %d, want a freshly allocated non-default port", p)
		}
	})

	t.Run("--isolate-db binds a per-worktree DB", func(t *testing.T) {
		root := initRealGitRepo(t)
		t.Setenv("HOME", t.TempDir())

		res, err := runWorktreeInit(root, filepath.Base(root), inputs.WorktreeInitInput{Slug: "iso-wt", IsolateDB: true})
		if err != nil {
			t.Fatalf("runWorktreeInit: %v", err)
		}
		if want := filepath.Join(".bacio", "db.sqlite"); res.Manifest.Allocations.DBPath != want {
			t.Errorf("db_path: got %q, want %q", res.Manifest.Allocations.DBPath, want)
		}
	})

	t.Run("--db-path overrides --isolate-db", func(t *testing.T) {
		root := initRealGitRepo(t)
		t.Setenv("HOME", t.TempDir())

		res, err := runWorktreeInit(root, filepath.Base(root), inputs.WorktreeInitInput{
			Slug: "explicit-wt", IsolateDB: true, DBPath: "/tmp/pinned/db.sqlite",
		})
		if err != nil {
			t.Fatalf("runWorktreeInit: %v", err)
		}
		if res.Manifest.Allocations.DBPath != "/tmp/pinned/db.sqlite" {
			t.Errorf("db_path: got %q, want the explicit --db-path", res.Manifest.Allocations.DBPath)
		}
	})
}

// TestWorktreeInit_DefaultResolvesToSharedDB is the BACI-87 end-to-end
// check: after a default init, resolution from inside the worktree
// returns the shared ~/.bacio/db.sqlite (so a dispatched worker's
// issue calls reach the ticket) AND the worktree's own API port.
func TestWorktreeInit_DefaultResolvesToSharedDB(t *testing.T) {
	root := initRealGitRepo(t)
	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)

	res, err := runWorktreeInit(root, filepath.Base(root), inputs.WorktreeInitInput{Slug: "resolve-wt"})
	if err != nil {
		t.Fatalf("runWorktreeInit: %v", err)
	}
	if err := commitWorktreeInit(res); err != nil {
		t.Fatalf("commitWorktreeInit: %v", err)
	}

	resolved, err := wtenv.Resolve(wtenv.ResolveOpts{Cwd: root, HomeDir: tmpHome})
	if err != nil {
		t.Fatalf("wtenv.Resolve: %v", err)
	}
	if want := filepath.Join(tmpHome, ".bacio", "db.sqlite"); resolved.DBPath != want {
		t.Errorf("resolved DB: got %q, want shared %q", resolved.DBPath, want)
	}
	wantPort := res.Manifest.Allocations.APIPort
	if !strings.HasSuffix(resolved.APIAddr, ":"+strconv.Itoa(wantPort)) {
		t.Errorf("resolved API addr %q: want the worktree's allocated port %d", resolved.APIAddr, wantPort)
	}
}

// TestIsolateDBEnvDefault checks the BACIO_WORKTREE_ISOLATE_DB parsing
// that supplies the --isolate-db flag default.
func TestIsolateDBEnvDefault(t *testing.T) {
	cases := map[string]bool{
		"1": true, "true": true, "yes": true, "TRUE": true, " 1 ": true,
		"0": false, "false": false, "": false, "off": false,
	}
	for v, want := range cases {
		t.Setenv(worktreeIsolateDBEnv, v)
		if got := isolateDBEnvDefault(); got != want {
			t.Errorf("isolateDBEnvDefault() with %q=%q: got %v, want %v", worktreeIsolateDBEnv, v, got, want)
		}
	}
}

// TestWorktreeRm_PurgeDBRefusesSharedDB drives `worktree rm --purge-db`
// against a default (shared-DB) manifest: it must refuse and leave the
// shared store on disk (BACI-87).
func TestWorktreeRm_PurgeDBRefusesSharedDB(t *testing.T) {
	root := initRealGitRepo(t)
	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)

	res, err := runWorktreeInit(root, filepath.Base(root), inputs.WorktreeInitInput{Slug: "rm-wt"})
	if err != nil {
		t.Fatalf("runWorktreeInit: %v", err)
	}
	if err := commitWorktreeInit(res); err != nil {
		t.Fatalf("commitWorktreeInit: %v", err)
	}
	// Stand in a shared DB file so we can prove it survives the refusal.
	sharedDB := filepath.Join(tmpHome, ".bacio", "db.sqlite")
	if err := os.WriteFile(sharedDB, []byte("real data"), 0o644); err != nil {
		t.Fatalf("seed shared DB: %v", err)
	}

	cmd := newWorktreeRmCmd()
	cmd.SetArgs([]string{root, "--confirm", "rm-wt", "--purge-db"})
	cmd.SilenceErrors, cmd.SilenceUsage = true, true
	err = cmd.Execute()
	if err == nil {
		t.Fatal("worktree rm --purge-db against a shared-DB manifest: want error")
	}
	if !strings.Contains(err.Error(), "purge-db refused") {
		t.Errorf("err: got %v, want a 'purge-db refused' message", err)
	}
	if _, statErr := os.Stat(sharedDB); statErr != nil {
		t.Errorf("shared DB was removed despite the refusal: %v", statErr)
	}
}

// stubReapSeams swaps the BACI-93 process-reap seams for the duration
// of a test and restores them on cleanup. listeners is the canned
// discovery result; signalled records each PID a Term/Kill was sent
// to; alive controls the post-signal liveness poll.
func stubReapSeams(t *testing.T, listeners []procfind.Listener, discErr error, aliveAfter bool) *[]int {
	t.Helper()
	origList := listenersOnPortFn
	origTerm := reapProcSignalTerm
	origKill := reapProcSignalKill
	origAlive := reapProcAlive
	t.Cleanup(func() {
		listenersOnPortFn = origList
		reapProcSignalTerm = origTerm
		reapProcSignalKill = origKill
		reapProcAlive = origAlive
	})
	var signalled []int
	listenersOnPortFn = func(int) ([]procfind.Listener, error) {
		return listeners, discErr
	}
	reapProcSignalTerm = func(pid int) error { signalled = append(signalled, pid); return nil }
	reapProcSignalKill = func(pid int) error { signalled = append(signalled, pid); return nil }
	reapProcAlive = func(int) bool { return aliveAfter }
	return &signalled
}

// TestReapWorktreeProcesses_DefaultReap: a matched bacio listener is
// signalled and reported killed.
func TestReapWorktreeProcesses_DefaultReap(t *testing.T) {
	signalled := stubReapSeams(t, []procfind.Listener{{PID: 4242, Command: "/usr/bin/bacio"}}, nil, false)
	res := &worktreeRmResult{}
	reapWorktreeProcesses(res, 5401, false)
	if len(res.ReapedProcesses) != 1 {
		t.Fatalf("got %d reaped, want 1", len(res.ReapedProcesses))
	}
	rp := res.ReapedProcesses[0]
	if rp.PID != 4242 || !rp.Killed || rp.Signal != "term" {
		t.Errorf("reaped = %+v, want pid 4242 killed via term", rp)
	}
	if len(*signalled) != 1 || (*signalled)[0] != 4242 {
		t.Errorf("signalled = %v, want [4242]", *signalled)
	}
}

// TestReapWorktreeProcesses_NonBacioLeftAlive: a stranger on our port
// is reported but never signalled.
func TestReapWorktreeProcesses_NonBacioLeftAlive(t *testing.T) {
	signalled := stubReapSeams(t, []procfind.Listener{{PID: 99, Command: "python"}}, nil, false)
	res := &worktreeRmResult{}
	reapWorktreeProcesses(res, 5401, false)
	if len(res.ReapedProcesses) != 1 {
		t.Fatalf("got %d reaped, want 1", len(res.ReapedProcesses))
	}
	rp := res.ReapedProcesses[0]
	if rp.Killed || rp.Signal != "" {
		t.Errorf("non-bacio listener was signalled: %+v", rp)
	}
	if !strings.Contains(rp.Note, "not a bacio process") {
		t.Errorf("note = %q, want a 'not a bacio process' note", rp.Note)
	}
	if len(*signalled) != 0 {
		t.Errorf("signalled = %v, want nothing", *signalled)
	}
}

// TestReapWorktreeProcesses_Port5320Guard: a manifest somehow pointing
// at the legacy default port skips reaping entirely.
func TestReapWorktreeProcesses_Port5320Guard(t *testing.T) {
	signalled := stubReapSeams(t, []procfind.Listener{{PID: 1, Command: "bacio"}}, nil, false)
	res := &worktreeRmResult{}
	reapWorktreeProcesses(res, wtenv.DefaultAPIPort, false)
	if len(res.ReapedProcesses) != 0 {
		t.Errorf("reaped %d on port 5320, want 0", len(res.ReapedProcesses))
	}
	if !strings.Contains(res.ProcessScanNote, "5320") {
		t.Errorf("scan note = %q, want a port-5320 refusal", res.ProcessScanNote)
	}
	if len(*signalled) != 0 {
		t.Errorf("signalled %v on port 5320, want nothing", *signalled)
	}
}

// TestReapWorktreeProcesses_DryRun: a dry run lists the would-signal
// PID but signals nothing.
func TestReapWorktreeProcesses_DryRun(t *testing.T) {
	signalled := stubReapSeams(t, []procfind.Listener{{PID: 7, Command: "bacio"}}, nil, false)
	res := &worktreeRmResult{}
	reapWorktreeProcesses(res, 5401, true)
	if len(res.ReapedProcesses) != 1 || res.ReapedProcesses[0].PID != 7 {
		t.Fatalf("reaped = %+v, want one would-signal pid 7", res.ReapedProcesses)
	}
	if res.ReapedProcesses[0].Killed || res.ReapedProcesses[0].Signal != "" {
		t.Errorf("dry-run entry was signalled: %+v", res.ReapedProcesses[0])
	}
	if len(*signalled) != 0 {
		t.Errorf("dry-run signalled %v, want nothing", *signalled)
	}
}

// TestReapWorktreeProcesses_DiscoveryError: a discovery failure is
// non-fatal — recorded as a note, nothing signalled.
func TestReapWorktreeProcesses_DiscoveryError(t *testing.T) {
	signalled := stubReapSeams(t, nil, procfind.ErrToolMissing, false)
	res := &worktreeRmResult{}
	reapWorktreeProcesses(res, 5401, false)
	if len(res.ReapedProcesses) != 0 {
		t.Errorf("reaped %d on discovery error, want 0", len(res.ReapedProcesses))
	}
	if !strings.Contains(res.ProcessScanNote, "could not scan") {
		t.Errorf("scan note = %q, want a discovery-failure note", res.ProcessScanNote)
	}
	if len(*signalled) != 0 {
		t.Errorf("signalled %v after discovery error, want nothing", *signalled)
	}
}

// TestReapWorktreeProcesses_SigtermGraceEscalates: a process that
// survives the SIGTERM grace window is escalated to SIGKILL.
func TestReapWorktreeProcesses_SigtermGraceEscalates(t *testing.T) {
	signalled := stubReapSeams(t, []procfind.Listener{{PID: 555, Command: "bacio"}}, nil, true /* stays alive */)
	res := &worktreeRmResult{}
	reapWorktreeProcesses(res, 5401, false)
	rp := res.ReapedProcesses[0]
	if rp.Signal != "kill" || !rp.Killed {
		t.Errorf("reaped = %+v, want escalated to kill", rp)
	}
	if !strings.Contains(rp.Note, "SIGKILL") {
		t.Errorf("note = %q, want a SIGKILL escalation note", rp.Note)
	}
	// Both SIGTERM and SIGKILL should have been sent to 555.
	if len(*signalled) != 2 {
		t.Errorf("signalled = %v, want both term + kill", *signalled)
	}
}

// TestWorktreeRm_KeepProcessesSkipsDiscovery: --keep-processes never
// calls the discovery seam.
func TestWorktreeRm_KeepProcessesSkipsDiscovery(t *testing.T) {
	root := initRealGitRepo(t)
	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)
	res, err := runWorktreeInit(root, filepath.Base(root), inputs.WorktreeInitInput{Slug: "keep-wt"})
	if err != nil {
		t.Fatalf("runWorktreeInit: %v", err)
	}
	if err := commitWorktreeInit(res); err != nil {
		t.Fatalf("commitWorktreeInit: %v", err)
	}

	origList := listenersOnPortFn
	t.Cleanup(func() { listenersOnPortFn = origList })
	called := false
	listenersOnPortFn = func(int) ([]procfind.Listener, error) {
		called = true
		return nil, nil
	}

	cmd := newWorktreeRmCmd()
	cmd.SetArgs([]string{root, "--confirm", "keep-wt", "--keep-processes"})
	cmd.SilenceErrors, cmd.SilenceUsage = true, true
	if err := cmd.Execute(); err != nil {
		t.Fatalf("worktree rm --keep-processes: %v", err)
	}
	if called {
		t.Error("--keep-processes still ran process discovery")
	}
	if _, statErr := os.Stat(filepath.Join(root, wtenv.DefaultManifestFilename)); statErr == nil {
		t.Error("manifest survived worktree rm")
	}
}

// TestSanitiseDefaultSlug exercises the helper that derives the
// default manifest slug from a directory basename.
func TestSanitiseDefaultSlug(t *testing.T) {
	cases := map[string]string{
		"bacio":             "bacio",
		"bacio-BACI-63":     "bacio-baci-63",
		"My Repo":           "my-repo",
		"feature/foo":       "feature-foo",
		"  trim  ":          "trim",
		"...":               "worktree",
		"":                  "worktree",
	}
	for in, want := range cases {
		got := sanitiseDefaultSlug(in)
		if got != want {
			t.Errorf("sanitiseDefaultSlug(%q): got %q, want %q", in, got, want)
		}
	}
}
