// Package wtenv resolves per-worktree bacio environment manifests so
// that side-by-side git worktrees can each run their own bacio
// instance (CLI / api / desktop / channel / hooks) without clashing
// on the shared writer state at ~/.bacio/db.sqlite or the
// 127.0.0.1:5320 API port.
//
// Source of truth is a YAML file at the worktree root,
// `environment-config.yaml`. It is written by `bacio worktree init`
// and gitignored at the same time so two clones of the project don't
// end up sharing one user's allocations. A second file —
// ~/.bacio/worktrees.yaml — is a global registry used for cross-
// worktree visibility (`bacio worktree list`) and for collision-free
// port allocation; the per-worktree YAML wins on disagreement.
//
// Manifest-free behaviour is identical to bacio's pre-BACI-63 default
// (~/.bacio/db.sqlite + 127.0.0.1:5320). Resolving an environment
// follows this precedence (highest first):
//
//  1. Explicit flags (--db, --addr / --port). Power-user override.
//  2. Env override: BACIO_ENV=<absolute path to a manifest YAML>.
//  3. Worktree YAML: resolve the LINKED worktree's own root via
//     `git rev-parse --show-toplevel` (see git.WorktreeRoot — this
//     is deliberately different from git.Detect, which returns the
//     main worktree's root for repo-identity sharing), and look for
//     <root>/environment-config.yaml.
//  4. Default: ~/.bacio/db.sqlite + 127.0.0.1:5320.
//
// Step 3 returning "no manifest present" is not an error; it falls
// through to step 4 so existing users see no behaviour change.
package wtenv

import (
	"errors"
	"fmt"
	"hash/fnv"
	"io"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"go.yaml.in/yaml/v4"

	"github.com/mrgeoffrich/bacio/internal/git"
)

// DefaultManifestFilename is the name of the per-worktree manifest at
// the worktree root.
const DefaultManifestFilename = "environment-config.yaml"

// DefaultAPIAddr is the historical bind address bacio falls back to
// when no manifest is in play. Kept here so the resolver and the
// `bacio api` command agree.
const DefaultAPIAddr = "127.0.0.1:5320"

// DefaultAPIPort is the historical API port (the port half of
// DefaultAPIAddr).
const DefaultAPIPort = 5320

// DefaultAPIHost is the historical bind host (the host half of
// DefaultAPIAddr).
const DefaultAPIHost = "127.0.0.1"

// EnvVar names the environment variable that overrides the worktree
// manifest lookup.
const EnvVar = "BACIO_ENV"

// Source identifies which step of the resolution chain produced a
// given DBPath/APIAddr pair. Surfaced on `bacio status` so a user can
// see at a glance which manifest (if any) is in effect.
type Source string

const (
	// SourceFlag means an explicit --db / --addr flag was supplied.
	// Manifest may still be non-nil if a worktree YAML is present;
	// the flag just took precedence.
	SourceFlag Source = "flag"
	// SourceEnv means BACIO_ENV pointed at a manifest.
	SourceEnv Source = "env"
	// SourceWorktree means a worktree-root environment-config.yaml
	// was resolved.
	SourceWorktree Source = "worktree"
	// SourceDefault means no manifest was found; fell through to
	// ~/.bacio/db.sqlite + 127.0.0.1:5320.
	SourceDefault Source = "default"
)

// Manifest is the on-disk shape of environment-config.yaml.
//
// Extras is an opaque map of strings: bacio round-trips it untouched
// on rewrite so adjacent tooling (Vite, Wails dev URL, future MCP
// listeners) can add keys without schema churn.
type Manifest struct {
	Identity    Identity       `yaml:"identity"     json:"identity"`
	Allocations Allocations    `yaml:"allocations"  json:"allocations"`
	Extras      map[string]any `yaml:"extras,omitempty" json:"extras,omitempty"`
}

// Identity carries the human-readable label for a worktree manifest.
type Identity struct {
	Slug      string    `yaml:"slug"        json:"slug"`
	Worktree  string    `yaml:"worktree"    json:"worktree"`
	CreatedAt time.Time `yaml:"created_at"  json:"created_at"`
}

// Allocations is the per-worktree resource map. DBPath may be
// relative (resolved against the worktree root) or absolute.
//
// LogDir is the optional per-worktree log directory (BACI-73). When
// empty, the logging resolver synthesises `<worktree-root>/.bacio/logs/`.
// Relative paths resolve against the manifest's directory, same as
// DBPath. Hand-edited manifests can pin this to anything they like;
// `bacio worktree init` deliberately leaves it empty so existing
// manifests stay byte-stable.
type Allocations struct {
	APIPort int    `yaml:"api_port"             json:"api_port"`
	DBPath  string `yaml:"db_path"              json:"db_path"`
	LogDir  string `yaml:"log_dir,omitempty"    json:"log_dir,omitempty"`
}

// ResolveOpts feeds Resolve. Cwd defaults to os.Getwd() when empty.
// Flag overrides take precedence over the env var / worktree
// manifest; pass empty strings to skip a given override.
type ResolveOpts struct {
	Cwd       string // current working directory; defaults to os.Getwd()
	FlagDB    string // explicit --db flag value, if any
	FlagAddr  string // explicit --addr flag value, if any
	EnvOnly   bool   // when true, skip the worktree-root walk (used by tests)
	HomeDir   string // override for ~ — used by tests
	EnvLookup func(string) string
	// GitDetect resolves the main worktree's root for a given cwd
	// (defaults to git.Detect). Kept around for callers that want
	// repo-wide identity; the manifest-layer step uses
	// GitWorktreeRoot instead.
	GitDetect func(cwd string) (*git.Info, error)
	// GitWorktreeRoot resolves the LINKED worktree's own root for a
	// given cwd (defaults to git.WorktreeRoot). BACI-71: the
	// manifest layer wants the linked worktree, not the main one,
	// so the read side here matches the write side in
	// `bacio worktree init`.
	GitWorktreeRoot func(cwd string) (string, error)
	FileExists      func(path string) bool
}

// Resolved is the output of Resolve. DBPath and APIAddr are always
// populated; ManifestPath is set only when a manifest file actually
// fed the resolution (i.e. Source is Env or Worktree, or Flag with a
// nearby manifest).
type Resolved struct {
	Source       Source
	ManifestPath string
	Manifest     *Manifest
	DBPath       string
	APIAddr      string
}

// ManifestSlug returns the slug of the resolved manifest, or empty
// when no manifest fed resolution. Convenience for surfaces that
// want a short label without nil-checking the manifest field.
func (r Resolved) ManifestSlug() string {
	if r.Manifest == nil {
		return ""
	}
	return r.Manifest.Identity.Slug
}

// Resolve walks the precedence chain documented in the package doc
// and returns the resulting DB / API allocations.
func Resolve(opts ResolveOpts) (Resolved, error) {
	if opts.EnvLookup == nil {
		opts.EnvLookup = os.Getenv
	}
	if opts.GitDetect == nil {
		opts.GitDetect = git.Detect
	}
	if opts.GitWorktreeRoot == nil {
		opts.GitWorktreeRoot = git.WorktreeRoot
	}
	if opts.FileExists == nil {
		opts.FileExists = func(p string) bool {
			_, err := os.Stat(p)
			return err == nil
		}
	}
	if opts.Cwd == "" {
		cwd, err := os.Getwd()
		if err == nil {
			opts.Cwd = cwd
		}
	}
	homeDir := opts.HomeDir
	if homeDir == "" {
		if h, err := os.UserHomeDir(); err == nil {
			homeDir = h
		}
	}

	defaultDB := filepath.Join(homeDir, ".bacio", "db.sqlite")

	// Helper: build a Resolved from a manifest path + manifest, applying
	// the supplied flag overrides on top.
	fromManifest := func(source Source, manifestPath string, m *Manifest) (Resolved, error) {
		var dbPath, apiAddr string
		if m != nil {
			dbPath = manifestDBPath(manifestPath, m)
			apiAddr = manifestAPIAddr(m)
		} else {
			dbPath = defaultDB
			apiAddr = DefaultAPIAddr
		}
		if opts.FlagDB != "" {
			dbPath = opts.FlagDB
			source = SourceFlag
		}
		if opts.FlagAddr != "" {
			apiAddr = opts.FlagAddr
			source = SourceFlag
		}
		return Resolved{
			Source:       source,
			ManifestPath: manifestPath,
			Manifest:     m,
			DBPath:       dbPath,
			APIAddr:      apiAddr,
		}, nil
	}

	// If --db is set, short-circuit the resolution chain to the flag —
	// the explicit DB override is more specific than any manifest, so
	// don't even read a (possibly broken) BACIO_ENV / worktree YAML
	// when the user has named the path directly.
	if opts.FlagDB != "" && opts.FlagAddr != "" {
		return Resolved{
			Source:  SourceFlag,
			DBPath:  opts.FlagDB,
			APIAddr: opts.FlagAddr,
		}, nil
	}

	// Step 2: BACIO_ENV.
	if envPath := strings.TrimSpace(opts.EnvLookup(EnvVar)); envPath != "" {
		abs, err := filepath.Abs(envPath)
		if err != nil {
			return Resolved{}, fmt.Errorf("resolve %s=%q: %w", EnvVar, envPath, err)
		}
		m, err := LoadManifest(abs)
		if err != nil {
			// When --db is set, a broken BACIO_ENV shouldn't take down
			// the call — the explicit flag is the higher-precedence
			// override. Log to stderr and fall through to the
			// flag-only result.
			if opts.FlagDB != "" {
				return Resolved{
					Source:  SourceFlag,
					DBPath:  opts.FlagDB,
					APIAddr: applyFlagAddr(DefaultAPIAddr, opts.FlagAddr),
				}, nil
			}
			return Resolved{}, fmt.Errorf("%s=%q: %w", EnvVar, envPath, err)
		}
		return fromManifest(SourceEnv, abs, m)
	}

	// Step 3: worktree-root environment-config.yaml. BACI-71: probe
	// the LINKED worktree's own root (the manifest lives there), not
	// the main worktree's. Using GitDetect here was the bug that
	// silently bypassed isolation for every linked worktree.
	if !opts.EnvOnly && opts.Cwd != "" {
		if root, err := opts.GitWorktreeRoot(opts.Cwd); err == nil && root != "" {
			candidate := filepath.Join(root, DefaultManifestFilename)
			if opts.FileExists(candidate) {
				m, err := LoadManifest(candidate)
				if err != nil {
					return Resolved{}, fmt.Errorf("worktree manifest %s: %w", candidate, err)
				}
				return fromManifest(SourceWorktree, candidate, m)
			}
		}
	}

	// Step 4: default. Flags still apply.
	res := Resolved{
		Source:  SourceDefault,
		DBPath:  defaultDB,
		APIAddr: DefaultAPIAddr,
	}
	if opts.FlagDB != "" {
		res.DBPath = opts.FlagDB
		res.Source = SourceFlag
	}
	if opts.FlagAddr != "" {
		res.APIAddr = opts.FlagAddr
		res.Source = SourceFlag
	}
	return res, nil
}

// applyFlagAddr returns flagAddr when non-empty, else fallback. A
// small helper so the broken-env fall-through stays one-line.
func applyFlagAddr(fallback, flagAddr string) string {
	if flagAddr != "" {
		return flagAddr
	}
	return fallback
}

// manifestDBPath returns the absolute DB path encoded by the
// manifest. Relative paths are resolved against the manifest's
// directory so a manifest at /worktree/environment-config.yaml with
// db_path: .bacio/db.sqlite resolves to /worktree/.bacio/db.sqlite,
// even if a different cwd is current.
func manifestDBPath(manifestPath string, m *Manifest) string {
	db := strings.TrimSpace(m.Allocations.DBPath)
	if db == "" {
		// A manifest without an explicit db_path means "use the
		// per-worktree default at .bacio/db.sqlite under the
		// manifest's directory" — keeps the manifest minimal while
		// still being unambiguous.
		db = filepath.Join(".bacio", "db.sqlite")
	}
	if filepath.IsAbs(db) {
		return db
	}
	return filepath.Join(filepath.Dir(manifestPath), db)
}

// manifestAPIAddr renders the API bind address from a manifest. A
// missing or zero port falls back to DefaultAPIPort, so a partial
// manifest still works.
func manifestAPIAddr(m *Manifest) string {
	port := m.Allocations.APIPort
	if port <= 0 {
		port = DefaultAPIPort
	}
	return net.JoinHostPort(DefaultAPIHost, strconv.Itoa(port))
}

// LoadManifest reads a YAML manifest from disk. Returns an error if
// the file is unreadable, malformed, or carries unknown fields under
// `identity` / `allocations` (strict decode).
func LoadManifest(path string) (*Manifest, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return ParseManifest(data)
}

// ParseManifest decodes raw YAML bytes into a Manifest. Strict-mode
// decoding rejects unknown fields under the typed identity/allocations
// sub-objects, but the free-form `extras` map accepts anything.
func ParseManifest(raw []byte) (*Manifest, error) {
	var m Manifest
	dec := yaml.NewDecoder(strings.NewReader(string(raw)))
	dec.KnownFields(true)
	if err := dec.Decode(&m); err != nil {
		// Empty file decodes as io.EOF — treat as an empty manifest
		// so a hand-created stub still resolves.
		if errors.Is(err, io.EOF) {
			return &Manifest{}, nil
		}
		return nil, fmt.Errorf("decode manifest: %w", err)
	}
	if m.Extras == nil {
		m.Extras = map[string]any{}
	}
	return &m, nil
}

// SaveManifest writes a Manifest to disk as YAML. Creates the parent
// directory if missing. Overwrites any existing file. Extras is
// emitted in deterministic key order.
func SaveManifest(path string, m *Manifest) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create manifest dir: %w", err)
	}
	var buf strings.Builder
	header := "# environment-config.yaml — bacio worktree environment manifest.\n" +
		"# Source of truth for the bacio instance running in this worktree.\n" +
		"# Do not commit; written by `bacio worktree init` and added to\n" +
		"# .gitignore at the same time.\n"
	buf.WriteString(header)
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	if err := enc.Encode(m); err != nil {
		return fmt.Errorf("encode manifest: %w", err)
	}
	if err := enc.Close(); err != nil {
		return fmt.Errorf("close encoder: %w", err)
	}
	return os.WriteFile(path, []byte(buf.String()), 0o644)
}

// HashPortOffset returns a deterministic port offset in [0, 100) for
// a given slug. Used by AllocatePort to seed the search at a stable
// starting point per slug; collisions walk forward from there.
func HashPortOffset(slug string) int {
	h := fnv.New32a()
	_, _ = h.Write([]byte(slug))
	return int(h.Sum32() % 100)
}

