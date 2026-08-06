package cli

import (
	"os"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"github.com/mrgeoffrich/bacio/internal/client"
	"github.com/mrgeoffrich/bacio/internal/store"
)

// withGlobalOpts swaps the package-level CLI globals for the duration of
// one test and restores them afterwards. `opts` is a package var the
// cobra flags bind into, so a test that pokes it must put it back or the
// next test in the package inherits the value.
func withGlobalOpts(t *testing.T, mutate func(*globalOpts)) {
	t.Helper()
	saved := opts
	t.Cleanup(func() { opts = saved })
	mutate(&opts)
}

// TestRepoSelector covers the precedence chain and canonicalisation of
// the global --repo selector: flag beats env, both are uppercased and
// trimmed, and absent-everywhere yields "" (which is what keeps the cwd
// / git.Detect path the default).
func TestRepoSelector(t *testing.T) {
	cases := []struct {
		name string
		flag string
		env  string
		want string
	}{
		{"neither set", "", "", ""},
		{"flag only", "mini", "", "MINI"},
		{"env only", "", "opsx", "OPSX"},
		{"flag beats env", "mini", "opsx", "MINI"},
		{"whitespace trimmed", "  mini  ", "", "MINI"},
		{"blank flag falls through to env", "   ", "opsx", "OPSX"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			withGlobalOpts(t, func(o *globalOpts) { o.repoPrefix = tc.flag })
			t.Setenv("BACIO_REPO", tc.env)
			if got := repoSelector(); got != tc.want {
				t.Fatalf("repoSelector() = %q, want %q", got, tc.want)
			}
		})
	}
}

// TestResolveRepoC_SelectorShortCircuitsGitDetect is the load-bearing
// assertion for workspaces: a workspace has NO path, so git.Detect can
// never find one. The selector must therefore be consumed before cwd
// detection runs — and it must work from a directory that is not a git
// repo at all, which is the situation a user driving a workspace is
// always in.
func TestResolveRepoC_SelectorShortCircuitsGitDetect(t *testing.T) {
	s, err := store.Open(":memory:")
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	ws, err := s.CreateWorkspace("HOME", "Home Renovation")
	if err != nil {
		t.Fatalf("create workspace: %v", err)
	}

	// A temp dir is not a git working tree, so the cwd path would fail.
	saved, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	t.Cleanup(func() { _ = os.Chdir(saved) })
	if err := os.Chdir(t.TempDir()); err != nil {
		t.Fatalf("chdir: %v", err)
	}

	c := client.NewLocalFromStore(s, "test")
	withGlobalOpts(t, func(o *globalOpts) { o.repoPrefix = "home" })
	t.Setenv("BACIO_REPO", "")

	got, err := resolveRepoC(c)
	if err != nil {
		t.Fatalf("resolveRepoC with --repo: %v", err)
	}
	if got.ID != ws.ID {
		t.Fatalf("resolved repo id %d, want %d (%s)", got.ID, ws.ID, ws.Prefix)
	}

	// Same call with the selector cleared must fall back to cwd
	// detection — and fail, because we are not in a git repo. That is
	// the regression guard on "the git path is untouched when the flag
	// is absent".
	withGlobalOpts(t, func(o *globalOpts) { o.repoPrefix = "" })
	if _, err := resolveRepoC(c); err == nil {
		t.Fatal("expected resolveRepoC to fail outside a git repo with no --repo, got nil")
	}
}

// TestResolveRepo_SelectorShortCircuitsGitDetect is the store-direct
// twin of the test above. resolveRepo has one production caller today
// (`bacio tui`, whose own --repo flag shadows the global one), but the
// two functions are documented as behaving identically, so the selector
// lives in both. This pins that.
func TestResolveRepo_SelectorShortCircuitsGitDetect(t *testing.T) {
	s, err := store.Open(":memory:")
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	ws, err := s.CreateWorkspace("HOME", "Home Renovation")
	if err != nil {
		t.Fatalf("create workspace: %v", err)
	}

	saved, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	t.Cleanup(func() { _ = os.Chdir(saved) })
	if err := os.Chdir(t.TempDir()); err != nil {
		t.Fatalf("chdir: %v", err)
	}

	withGlobalOpts(t, func(o *globalOpts) { o.repoPrefix = "" })
	t.Setenv("BACIO_REPO", "HOME")

	got, err := resolveRepo(s)
	if err != nil {
		t.Fatalf("resolveRepo with $BACIO_REPO: %v", err)
	}
	if got.ID != ws.ID {
		t.Fatalf("resolved repo id %d, want %d (%s)", got.ID, ws.ID, ws.Prefix)
	}
}

// TestResolveRepoC_SelectorDoesNotCreate pins that --repo is a LOOKUP,
// never a create. The cwd path auto-registers a git repo on first use;
// an explicitly named prefix must not, or a typo would silently mint a
// repo row with a garbage prefix.
func TestResolveRepoC_SelectorDoesNotCreate(t *testing.T) {
	s, err := store.Open(":memory:")
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })

	withGlobalOpts(t, func(o *globalOpts) { o.repoPrefix = "NOPE" })
	t.Setenv("BACIO_REPO", "")

	c := client.NewLocalFromStore(s, "test")
	if _, err := resolveRepoC(c); err == nil {
		t.Fatal("expected --repo NOPE to fail, got nil")
	}
	repos, err := s.ListRepos()
	if err != nil {
		t.Fatalf("list repos: %v", err)
	}
	if len(repos) != 0 {
		t.Fatalf("--repo created %d repo row(s); it must never create", len(repos))
	}
}

// TestRepoCascadeBulletsCoversEveryCount is the guard on the honesty of
// the loudest, most destructive text bacio prints. The `repo rm` /
// `workspace rm` confirm block used to name ten of the cascade counts
// while the JSON payload marshalled the whole struct, so the human
// preview under-reported its own blast radius. Any field added to
// store.RepoCascadeCounts must gain a bullet here too.
func TestRepoCascadeBulletsCoversEveryCount(t *testing.T) {
	// Distinct non-zero values per field, so a duplicated or omitted
	// bullet is detectable by the number alone.
	var counts store.RepoCascadeCounts
	v := reflect.ValueOf(&counts).Elem()
	for i := 0; i < v.NumField(); i++ {
		v.Field(i).SetInt(int64(i + 1))
	}

	bullets := repoCascadeBullets(counts)
	if len(bullets) != v.NumField() {
		t.Fatalf("repoCascadeBullets returned %d lines for %d cascade fields; every field needs a bullet",
			len(bullets), v.NumField())
	}
	joined := strings.Join(bullets, "\n")
	for i := 0; i < v.NumField(); i++ {
		name := v.Type().Field(i).Name
		want := int(v.Field(i).Int())
		prefix := strconv.Itoa(want) + " "
		found := false
		for _, line := range bullets {
			if strings.HasPrefix(line, prefix) {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("no bullet reports %s (expected a line starting %q); got:\n%s", name, prefix, joined)
		}
	}
}

// TestWorkspaceRefusalsNameTheWorkspace pins that the filesystem-shaped
// verbs refuse a workspace with a WORKSPACE-specific message. Reusing
// the phantom wording ("link it first") would send a user hunting for a
// checkout that does not and will never exist.
func TestWorkspaceRefusalsNameTheWorkspace(t *testing.T) {
	s, err := store.Open(":memory:")
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	ws, err := s.CreateWorkspace("HOME", "Home Renovation")
	if err != nil {
		t.Fatalf("create workspace: %v", err)
	}
	git, err := s.CreateRepo("GITR", "gitrepo", t.TempDir(), "")
	if err != nil {
		t.Fatalf("create repo: %v", err)
	}

	err = refuseFilesystemVerbOnWorkspace(ws, "it has no working tree")
	if err == nil {
		t.Fatal("expected a refusal for a workspace")
	}
	msg := err.Error()
	if !strings.Contains(msg, "HOME") || !strings.Contains(msg, "workspace") {
		t.Errorf("refusal %q should name the prefix and say 'workspace'", msg)
	}
	if strings.Contains(strings.ToLower(msg), "phantom") || strings.Contains(msg, "repo link") {
		t.Errorf("refusal %q reuses the phantom wording; a workspace can never be linked", msg)
	}
	if err := refuseFilesystemVerbOnWorkspace(git, "it has no working tree"); err != nil {
		t.Errorf("git repo must not be refused, got %v", err)
	}
	if err := refuseSourcePathOnWorkspace(ws, ""); err != nil {
		t.Errorf("an empty source_path must not be refused, got %v", err)
	}
	if err := refuseSourcePathOnWorkspace(ws, "docs/x.md"); err == nil {
		t.Error("expected --from-path on a workspace to be refused")
	}
}
