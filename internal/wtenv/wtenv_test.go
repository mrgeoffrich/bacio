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
		Cwd:       root,
		HomeDir:   home,
		EnvLookup: func(string) string { return "" },
		GitDetect: fakeGit(root),
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
		Cwd:       root,
		HomeDir:   home,
		EnvLookup: func(string) string { return "" },
		GitDetect: fakeGit(root),
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
		// Real git.Detect will fail outside a git repo; emulate it.
		GitDetect: func(string) (*git.Info, error) { return nil, git.ErrNotARepo },
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
		Cwd:       root,
		HomeDir:   home,
		EnvLookup: func(string) string { return "" },
		GitDetect: fakeGit(root),
	})
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if res.DBPath != absDB {
		t.Errorf("dbpath: got %q, want %q", res.DBPath, absDB)
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

func TestRegistry_AllocatePortAvoidsDefaultAndCollisions(t *testing.T) {
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
	a, _ := (&Registry{}).AllocatePort("slug-x")
	b, _ := (&Registry{}).AllocatePort("slug-x")
	if a != b {
		t.Errorf("non-deterministic: got %d vs %d", a, b)
	}
}
