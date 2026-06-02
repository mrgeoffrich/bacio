package wtenv

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mrgeoffrich/bacio/internal/git"
)

func writeFile(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
}

func fakeGit(root string) func(string) (*git.Info, error) {
	return func(cwd string) (*git.Info, error) {
		return &git.Info{Root: root, Name: filepath.Base(root)}, nil
	}
}

// fakeWorktreeRoot mirrors fakeGit for the linked-worktree probe
// added in BACI-71. Tests that exercise Resolve's step 3 need both
// fakes pointed at the same root so the legacy behaviour assertions
// still hold.
func fakeWorktreeRoot(root string) func(string) (string, error) {
	return func(cwd string) (string, error) {
		return root, nil
	}
}

func TestResolve_PrecedenceFlagBeatsEverything(t *testing.T) {
	tmp := t.TempDir()
	home := filepath.Join(tmp, "home")
	root := filepath.Join(tmp, "wt")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(root, DefaultManifestFilename), `identity:
  slug: foo
allocations:
  api_port: 5321
  db_path: .bacio/db.sqlite
`)

	res, err := Resolve(ResolveOpts{
		Cwd:       root,
		FlagDB:    "/explicit/db.sqlite",
		FlagAddr:  "127.0.0.1:9999",
		HomeDir:   home,
		EnvLookup: func(string) string { return "" },
		GitDetect: fakeGit(root),
	})
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if res.Source != SourceFlag {
		t.Errorf("source: got %q, want flag", res.Source)
	}
	if res.DBPath != "/explicit/db.sqlite" {
		t.Errorf("dbpath: got %q", res.DBPath)
	}
	if res.APIAddr != "127.0.0.1:9999" {
		t.Errorf("addr: got %q", res.APIAddr)
	}
	// When both --db and --addr are explicit, the short-circuit skips
	// manifest loading entirely — the flags are strictly more specific.
	if res.Manifest != nil {
		t.Errorf("manifest: want nil when both flags supplied, got %+v", res.Manifest)
	}
}

func TestResolve_EnvBeatsWorktree(t *testing.T) {
	tmp := t.TempDir()
	home := filepath.Join(tmp, "home")
	root := filepath.Join(tmp, "wt")
	envManifest := filepath.Join(tmp, "envm.yaml")
	writeFile(t, filepath.Join(root, DefaultManifestFilename), `identity:
  slug: wt
allocations:
  api_port: 5400
  db_path: .bacio/wt.sqlite
`)
	writeFile(t, envManifest, `identity:
  slug: env
allocations:
  api_port: 5500
  db_path: env.sqlite
`)
	res, err := Resolve(ResolveOpts{
		Cwd:       root,
		HomeDir:   home,
		EnvLookup: func(k string) string { if k == EnvVar { return envManifest }; return "" },
		GitDetect: fakeGit(root),
	})
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if res.Source != SourceEnv {
		t.Errorf("source: got %q, want env", res.Source)
	}
	if !strings.HasSuffix(res.DBPath, "env.sqlite") {
		t.Errorf("dbpath: got %q", res.DBPath)
	}
	if res.APIAddr != "127.0.0.1:5500" {
		t.Errorf("addr: got %q", res.APIAddr)
	}
}

func TestResolve_WorktreeManifest(t *testing.T) {
	tmp := t.TempDir()
	home := filepath.Join(tmp, "home")
	root := filepath.Join(tmp, "wt")
	manifestPath := filepath.Join(root, DefaultManifestFilename)
	writeFile(t, manifestPath, `identity:
  slug: my-wt
allocations:
  api_port: 5350
  db_path: .bacio/db.sqlite
extras:
  vite_port: 5174
`)
	res, err := Resolve(ResolveOpts{
		Cwd:             root,
		HomeDir:         home,
		EnvLookup:       func(string) string { return "" },
		GitDetect:       fakeGit(root),
		GitWorktreeRoot: fakeWorktreeRoot(root),
	})
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if res.Source != SourceWorktree {
		t.Errorf("source: got %q, want worktree", res.Source)
	}
	wantDB := filepath.Join(root, ".bacio", "db.sqlite")
	if res.DBPath != wantDB {
		t.Errorf("dbpath: got %q, want %q", res.DBPath, wantDB)
	}
	if res.APIAddr != "127.0.0.1:5350" {
		t.Errorf("addr: got %q", res.APIAddr)
	}
	if res.ManifestPath != manifestPath {
		t.Errorf("manifest path: got %q, want %q", res.ManifestPath, manifestPath)
	}
}

func TestResolve_MissingManifestFallsThroughToDefault(t *testing.T) {
	tmp := t.TempDir()
	home := filepath.Join(tmp, "home")
	root := filepath.Join(tmp, "wt")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	res, err := Resolve(ResolveOpts{
		Cwd:             root,
		HomeDir:         home,
		EnvLookup:       func(string) string { return "" },
		GitDetect:       fakeGit(root),
		GitWorktreeRoot: fakeWorktreeRoot(root),
	})
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if res.Source != SourceDefault {
		t.Errorf("source: got %q, want default", res.Source)
	}
	wantDB := filepath.Join(home, ".bacio", "db.sqlite")
	if res.DBPath != wantDB {
		t.Errorf("dbpath: got %q, want %q", res.DBPath, wantDB)
	}
	if res.APIAddr != DefaultAPIAddr {
		t.Errorf("addr: got %q", res.APIAddr)
	}
}

func TestResolve_NoGitWithoutManifest(t *testing.T) {
	tmp := t.TempDir()
	home := filepath.Join(tmp, "home")
	res, err := Resolve(ResolveOpts{
		Cwd:       tmp,
		HomeDir:   home,
		EnvLookup: func(string) string { return "" },
		// Real git.Detect / git.WorktreeRoot will fail outside a git
		// repo; emulate both so step 3 short-circuits to the default.
		GitDetect:       func(string) (*git.Info, error) { return nil, git.ErrNotARepo },
		GitWorktreeRoot: func(string) (string, error) { return "", git.ErrNotARepo },
	})
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if res.Source != SourceDefault {
		t.Errorf("source: got %q, want default", res.Source)
	}
}

func TestResolve_AbsoluteDBPathInManifest(t *testing.T) {
	tmp := t.TempDir()
	home := filepath.Join(tmp, "home")
	root := filepath.Join(tmp, "wt")
	absDB := filepath.Join(tmp, "abs.sqlite")
	writeFile(t, filepath.Join(root, DefaultManifestFilename), "identity:\n  slug: wt\nallocations:\n  api_port: 5321\n  db_path: "+absDB+"\n")
	res, err := Resolve(ResolveOpts{
		Cwd:             root,
		HomeDir:         home,
		EnvLookup:       func(string) string { return "" },
		GitDetect:       fakeGit(root),
		GitWorktreeRoot: fakeWorktreeRoot(root),
	})
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if res.DBPath != absDB {
		t.Errorf("dbpath: got %q, want %q", res.DBPath, absDB)
	}
}

// TestResolve_PicksLinkedWorktreeManifest is the BACI-71 reader-side
// regression: step 3 must probe the LINKED worktree's own root, not
// the main worktree's. If a (legacy / mis-placed) manifest also sits
// at the main worktree's root, the resolver must prefer the linked
// one when called from inside the linked tree.
func TestResolve_PicksLinkedWorktreeManifest(t *testing.T) {
	tmp := t.TempDir()
	home := filepath.Join(tmp, "home")
	main := filepath.Join(tmp, "main")
	linked := filepath.Join(tmp, "linked")
	writeFile(t, filepath.Join(linked, DefaultManifestFilename), `identity:
  slug: linked-wt
allocations:
  api_port: 5400
  db_path: .bacio/db.sqlite
`)
	// Bonus: drop a stale manifest at the main root so we can be sure
	// the resolver isn't accidentally picking the main one up.
	writeFile(t, filepath.Join(main, DefaultManifestFilename), `identity:
  slug: stale-main
allocations:
  api_port: 5500
  db_path: .bacio/db.sqlite
`)

	res, err := Resolve(ResolveOpts{
		Cwd:       linked,
		HomeDir:   home,
		EnvLookup: func(string) string { return "" },
		// Mirror the bug: GitDetect returns the MAIN worktree's root
		// (the contract Detect intentionally holds). Without the fix,
		// step 3 would join that root with environment-config.yaml
		// and load the stale-main manifest. With the fix, step 3
		// uses GitWorktreeRoot — the linked root — and loads the
		// correct manifest.
		GitDetect:       fakeGit(main),
		GitWorktreeRoot: fakeWorktreeRoot(linked),
	})
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if res.Source != SourceWorktree {
		t.Fatalf("source: got %q, want worktree", res.Source)
	}
	if res.Manifest == nil || res.Manifest.Identity.Slug != "linked-wt" {
		t.Fatalf("manifest: got %+v, want slug=linked-wt (resolver picked the MAIN worktree's manifest — BACI-71 regression)", res.Manifest)
	}
	wantPath := filepath.Join(linked, DefaultManifestFilename)
	if res.ManifestPath != wantPath {
		t.Errorf("manifest_path: got %q, want %q", res.ManifestPath, wantPath)
	}
	wantDB := filepath.Join(linked, ".bacio", "db.sqlite")
	if res.DBPath != wantDB {
		t.Errorf("db_path: got %q, want %q", res.DBPath, wantDB)
	}
	if res.APIAddr != "127.0.0.1:5400" {
		t.Errorf("api_addr: got %q, want %q (port from the linked manifest, not the stale main one)", res.APIAddr, "127.0.0.1:5400")
	}
}

func TestManifestRoundTrip_PreservesExtras(t *testing.T) {
	tmp := t.TempDir()
	path := filepath.Join(tmp, "env.yaml")
	in := &Manifest{
		Identity: Identity{
			Slug:     "round-trip",
			Worktree: "/tmp/wt",
		},
		Allocations: Allocations{
			APIPort: 5333,
			DBPath:  ".bacio/db.sqlite",
		},
		Extras: map[string]any{
			"vite_port":     5174,
			"wails_dev_url": "http://127.0.0.1:34115",
		},
	}
	if err := SaveManifest(path, in); err != nil {
		t.Fatalf("save: %v", err)
	}
	out, err := LoadManifest(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if out.Identity.Slug != in.Identity.Slug {
		t.Errorf("slug: got %q, want %q", out.Identity.Slug, in.Identity.Slug)
	}
	if out.Allocations.APIPort != in.Allocations.APIPort {
		t.Errorf("port: got %d, want %d", out.Allocations.APIPort, in.Allocations.APIPort)
	}
	if out.Allocations.DBPath != in.Allocations.DBPath {
		t.Errorf("db: got %q, want %q", out.Allocations.DBPath, in.Allocations.DBPath)
	}
	if v, ok := out.Extras["wails_dev_url"]; !ok || v != "http://127.0.0.1:34115" {
		t.Errorf("extras: wails_dev_url not round-tripped: %v", out.Extras)
	}
	if v, ok := out.Extras["vite_port"]; !ok || (v != 5174 && v != int64(5174) && v != float64(5174)) {
		t.Errorf("extras: vite_port not round-tripped: %v (%T)", v, v)
	}
}

func TestManifestRoundTrip_PreservesLogDir(t *testing.T) {
	// BACI-73: allocations.log_dir is an optional per-worktree log
	// directory. Round-trip a manifest that sets it explicitly so a
	// future binary upgrade can't silently drop the field.
	tmp := t.TempDir()
	path := filepath.Join(tmp, "env.yaml")
	in := &Manifest{
		Identity: Identity{Slug: "log-dir", Worktree: "/tmp/wt"},
		Allocations: Allocations{
			APIPort: 5333,
			DBPath:  ".bacio/db.sqlite",
			LogDir:  "logs",
		},
	}
	if err := SaveManifest(path, in); err != nil {
		t.Fatalf("save: %v", err)
	}
	out, err := LoadManifest(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if out.Allocations.LogDir != "logs" {
		t.Errorf("log_dir: got %q, want %q", out.Allocations.LogDir, "logs")
	}
	// Sanity: leaving log_dir empty must not flip omitempty off.
	in.Allocations.LogDir = ""
	if err := SaveManifest(path, in); err != nil {
		t.Fatalf("save (empty log_dir): %v", err)
	}
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if strings.Contains(string(body), "log_dir:") {
		t.Errorf("empty log_dir leaked into YAML:\n%s", string(body))
	}
}

func TestManifest_UnknownTypedFieldRejected(t *testing.T) {
	body := `identity:
  slug: foo
  unknown_field: nope
allocations:
  api_port: 5321
`
	_, err := ParseManifest([]byte(body))
	if err == nil {
		t.Fatalf("ParseManifest: want error on unknown typed field, got nil")
	}
}

func TestRegistry_UpsertRemoveFind(t *testing.T) {
	tmp := t.TempDir()
	reg := &Registry{}
	reg.Upsert(RegistryEntry{Slug: "a", Path: "/wt/a", APIPort: 5321, DBPath: "/wt/a/.bacio/db.sqlite"})
	reg.Upsert(RegistryEntry{Slug: "b", Path: "/wt/b", APIPort: 5322, DBPath: "/wt/b/.bacio/db.sqlite"})
	if got, ok := reg.FindByPath("/wt/a"); !ok || got.Slug != "a" {
		t.Errorf("FindByPath: got %+v", got)
	}
	if got, ok := reg.FindBySlug("b"); !ok || got.Path != "/wt/b" {
		t.Errorf("FindBySlug: got %+v", got)
	}
	// Update in place
	reg.Upsert(RegistryEntry{Slug: "a-renamed", Path: "/wt/a", APIPort: 5333, DBPath: "/wt/a/.bacio/db.sqlite"})
	if got, _ := reg.FindByPath("/wt/a"); got.Slug != "a-renamed" || got.APIPort != 5333 {
		t.Errorf("Upsert update: got %+v", got)
	}
	if removed := reg.Remove("/wt/a"); !removed {
		t.Errorf("Remove: want true")
	}
	if _, ok := reg.FindByPath("/wt/a"); ok {
		t.Errorf("Remove: row still present")
	}
	// Save + reload preserves ordering.
	if err := WriteRegistry(tmp, reg); err != nil {
		t.Fatalf("write: %v", err)
	}
	got, err := ReadRegistry(tmp)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if len(got.Worktrees) != 1 || got.Worktrees[0].Slug != "b" {
		t.Errorf("roundtrip: got %+v", got.Worktrees)
	}
}

func TestRegistry_ReadMissingFileIsEmpty(t *testing.T) {
	tmp := t.TempDir()
	reg, err := ReadRegistry(tmp)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if len(reg.Worktrees) != 0 {
		t.Errorf("got %d entries", len(reg.Worktrees))
	}
}

// stubPortProbe makes AllocatePort treat the listed ports as already
// bound and every other port as free, restoring the real OS probe
// when the test ends. Keeps AllocatePort tests deterministic — the
// real probe's result depends on whatever is bound on the test host.
func stubPortProbe(t *testing.T, busy ...int) {
	t.Helper()
	busySet := make(map[int]bool, len(busy))
	for _, p := range busy {
		busySet[p] = true
	}
	orig := probePortFree
	probePortFree = func(port int) bool { return !busySet[port] }
	t.Cleanup(func() { probePortFree = orig })
}

func TestRegistry_AllocatePortAvoidsDefaultAndCollisions(t *testing.T) {
	stubPortProbe(t) // no ports bound — isolate from the host
	reg := &Registry{}
	// First slug picks its hash slot.
	first, err := reg.AllocatePort("alpha")
	if err != nil {
		t.Fatalf("first: %v", err)
	}
	if first == DefaultAPIPort {
		t.Errorf("first: collided with default port %d", DefaultAPIPort)
	}
	reg.Upsert(RegistryEntry{Slug: "alpha", Path: "/a", APIPort: first})

	// Second call for same slug collides with the existing entry — must
	// walk forward.
	second, err := reg.AllocatePort("alpha")
	if err != nil {
		t.Fatalf("second: %v", err)
	}
	if second == first {
		t.Errorf("second: same as first %d", first)
	}
	if second <= first {
		t.Errorf("second: didn't walk forward (first=%d second=%d)", first, second)
	}
}

func TestRegistry_AllocatePortDeterministicPerSlug(t *testing.T) {
	stubPortProbe(t)
	a, _ := (&Registry{}).AllocatePort("slug-x")
	b, _ := (&Registry{}).AllocatePort("slug-x")
	if a != b {
		t.Errorf("non-deterministic: got %d vs %d", a, b)
	}
}

// AllocatePort must skip a port that is live (a process is listening
// on it) even when no registry entry claims it — the case that, left
// unhandled, hands a new worktree the port the user's own running
// `bacio web` is serving on. A live port at `seed` blocks two slots,
// not one: `seed` as an API candidate, and `seed+1` whose derived proxy
// port (seed+1−1 = seed) would clash with the live listener (BACI-344
// proxy-pair reservation). So the allocator lands on `seed+2`.
func TestRegistry_AllocatePortSkipsBoundPortNotInRegistry(t *testing.T) {
	seed := DefaultAPIPort + 1 + HashPortOffset("alpha")
	stubPortProbe(t, seed) // seed slot is bound, but no registry entry says so
	got, err := (&Registry{}).AllocatePort("alpha")
	if err != nil {
		t.Fatalf("AllocatePort: %v", err)
	}
	if got == seed {
		t.Errorf("returned the bound port %d instead of walking past it", seed)
	}
	if got == seed+1 {
		t.Errorf("returned %d, whose proxy port %d is the bound port %d", got, got-1, seed)
	}
	if got != seed+2 {
		t.Errorf("expected the next disjoint slot %d, got %d", seed+2, got)
	}
}

// An existing registry entry at API port P reserves the whole pair
// {P−1 (its proxy), P}, and additionally blocks the slot directly above
// it: a worktree at P+1 derives proxy P, colliding with the existing
// API listener. The allocator must skip P → P+2, never handing out the
// adjacent P+1 (BACI-344 proxy-pair reservation — the bug a bare
// APIPort-only `taken` set used to allow).
func TestRegistry_AllocatePortSkipsNeighbourOfExistingEntry(t *testing.T) {
	stubPortProbe(t) // no host ports bound — isolate from the host
	seed := DefaultAPIPort + 1 + HashPortOffset("alpha")
	reg := &Registry{Worktrees: []RegistryEntry{
		{Slug: "other", Path: "/other", APIPort: seed},
	}}
	got, err := reg.AllocatePort("alpha")
	if err != nil {
		t.Fatalf("AllocatePort: %v", err)
	}
	if got == seed {
		t.Errorf("returned the taken API port %d", seed)
	}
	if got == seed+1 {
		t.Errorf("returned %d, whose proxy %d collides with the existing API port %d", got, got-1, seed)
	}
	if got != seed+2 {
		t.Errorf("expected the next disjoint slot %d, got %d", seed+2, got)
	}
}

// AllocatePort hands each worktree an adjacent *pair* of ports — the API
// port and its derived proxy port one below (BACI-344). Allocating a run
// of worktrees, every pair must stay disjoint from every other pair and
// from the reserved default pair {DefaultProxyPort, DefaultAPIPort}.
// This is the cross-worktree invariant the proxy-pair reservation
// guarantees: no worktree's API port ever lands on another's proxy port.
func TestRegistry_AllocatePortKeepsProxyPairsDisjoint(t *testing.T) {
	stubPortProbe(t) // no host ports bound
	reg := &Registry{}
	used := map[int]string{
		DefaultProxyPort: "default:proxy",
		DefaultAPIPort:   "default:api",
	}
	// Distinct slugs so each Upsert is a fresh registry row.
	slugs := []string{"alpha", "bravo", "charlie", "delta", "echo", "foxtrot"}
	for _, slug := range slugs {
		api, err := reg.AllocatePort(slug)
		if err != nil {
			t.Fatalf("allocate %s: %v", slug, err)
		}
		proxy := api - 1
		for _, p := range []int{proxy, api} {
			if owner, ok := used[p]; ok {
				t.Fatalf("worktree %s port %d collides with %s", slug, p, owner)
			}
		}
		used[proxy] = slug + ":proxy"
		used[api] = slug + ":api"
		reg.Upsert(RegistryEntry{Slug: slug, Path: "/wt/" + slug, APIPort: api})
	}
}

// TestResolveProxyAddr covers the BACI-344 stable proxy addr: it is
// always derived as the API port minus one, tracking whatever the API
// addr resolved to across every source (default, worktree manifest,
// flag, env), with a fallback to DefaultProxyAddr for an unparseable
// addr or a port at/below 1 where port−1 would be 0/negative.
func TestResolveProxyAddr(t *testing.T) {
	tmp := t.TempDir()
	home := filepath.Join(tmp, "home")

	t.Run("default", func(t *testing.T) {
		res, err := Resolve(ResolveOpts{
			Cwd:             tmp,
			HomeDir:         home,
			EnvLookup:       func(string) string { return "" },
			GitDetect:       func(string) (*git.Info, error) { return nil, git.ErrNotARepo },
			GitWorktreeRoot: func(string) (string, error) { return "", git.ErrNotARepo },
		})
		if err != nil {
			t.Fatalf("resolve: %v", err)
		}
		if res.ProxyAddr != DefaultProxyAddr {
			t.Errorf("proxy_addr: got %q, want %q", res.ProxyAddr, DefaultProxyAddr)
		}
	})

	t.Run("worktree_manifest_port_minus_one", func(t *testing.T) {
		root := filepath.Join(tmp, "wt")
		writeFile(t, filepath.Join(root, DefaultManifestFilename), `identity:
  slug: my-wt
allocations:
  api_port: 5350
  db_path: .bacio/db.sqlite
`)
		res, err := Resolve(ResolveOpts{
			Cwd:             root,
			HomeDir:         home,
			EnvLookup:       func(string) string { return "" },
			GitDetect:       fakeGit(root),
			GitWorktreeRoot: fakeWorktreeRoot(root),
		})
		if err != nil {
			t.Fatalf("resolve: %v", err)
		}
		if res.APIAddr != "127.0.0.1:5350" {
			t.Fatalf("api_addr: got %q, want 127.0.0.1:5350", res.APIAddr)
		}
		if res.ProxyAddr != "127.0.0.1:5349" {
			t.Errorf("proxy_addr: got %q, want 127.0.0.1:5349 (api port − 1)", res.ProxyAddr)
		}
	})

	t.Run("flag_addr_flows_through", func(t *testing.T) {
		res, err := Resolve(ResolveOpts{
			Cwd:       tmp,
			FlagDB:    "/explicit/db.sqlite",
			FlagAddr:  "127.0.0.1:9999",
			HomeDir:   home,
			EnvLookup: func(string) string { return "" },
		})
		if err != nil {
			t.Fatalf("resolve: %v", err)
		}
		if res.ProxyAddr != "127.0.0.1:9998" {
			t.Errorf("proxy_addr: got %q, want 127.0.0.1:9998 (flag api port − 1)", res.ProxyAddr)
		}
	})

	t.Run("flag_port_only_flows_through", func(t *testing.T) {
		// --addr alone (no --db): the default-branch fall-through derives
		// the proxy addr from the *final* api addr.
		res, err := Resolve(ResolveOpts{
			Cwd:             tmp,
			FlagAddr:        "127.0.0.1:6000",
			HomeDir:         home,
			EnvLookup:       func(string) string { return "" },
			GitDetect:       func(string) (*git.Info, error) { return nil, git.ErrNotARepo },
			GitWorktreeRoot: func(string) (string, error) { return "", git.ErrNotARepo },
		})
		if err != nil {
			t.Fatalf("resolve: %v", err)
		}
		if res.ProxyAddr != "127.0.0.1:5999" {
			t.Errorf("proxy_addr: got %q, want 127.0.0.1:5999", res.ProxyAddr)
		}
	})

	t.Run("env_manifest_port_minus_one", func(t *testing.T) {
		envManifest := filepath.Join(tmp, "envm.yaml")
		writeFile(t, envManifest, `identity:
  slug: env
allocations:
  api_port: 5500
  db_path: env.sqlite
`)
		res, err := Resolve(ResolveOpts{
			Cwd:       tmp,
			HomeDir:   home,
			EnvLookup: func(k string) string { if k == EnvVar { return envManifest }; return "" },
		})
		if err != nil {
			t.Fatalf("resolve: %v", err)
		}
		if res.ProxyAddr != "127.0.0.1:5499" {
			t.Errorf("proxy_addr: got %q, want 127.0.0.1:5499", res.ProxyAddr)
		}
	})
}

// TestProxyAddrFor pins the derivation helper directly, including the
// fallback edges the resolver relies on.
func TestProxyAddrFor(t *testing.T) {
	cases := []struct {
		name    string
		apiAddr string
		want    string
	}{
		{name: "default", apiAddr: "127.0.0.1:5320", want: "127.0.0.1:5319"},
		{name: "worktree_port", apiAddr: "127.0.0.1:5350", want: "127.0.0.1:5349"},
		{name: "non_loopback_host", apiAddr: "0.0.0.0:8080", want: "0.0.0.0:8079"},
		{name: "unparseable_falls_back", apiAddr: "not-an-addr", want: DefaultProxyAddr},
		{name: "no_port_falls_back", apiAddr: "127.0.0.1", want: DefaultProxyAddr},
		{name: "empty_port_falls_back", apiAddr: "127.0.0.1:", want: DefaultProxyAddr},
		{name: "port_one_falls_back", apiAddr: "127.0.0.1:1", want: DefaultProxyAddr},
		{name: "port_zero_falls_back", apiAddr: "127.0.0.1:0", want: DefaultProxyAddr},
		{name: "empty_falls_back", apiAddr: "", want: DefaultProxyAddr},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := proxyAddrFor(c.apiAddr); got != c.want {
				t.Errorf("proxyAddrFor(%q) = %q, want %q", c.apiAddr, got, c.want)
			}
		})
	}
}
