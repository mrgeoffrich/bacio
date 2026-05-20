package wtenv

import (
	"errors"
	"fmt"
	"io/fs"
	"net"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"go.yaml.in/yaml/v4"
)

// probePortFree reports whether a TCP port on the loopback host can be
// bound right now. AllocatePort consults it so a freshly-init'd
// worktree never gets handed a port some other process is already
// listening on. The registry is authoritative only for ports bacio
// knows it allocated; this probe covers everything it doesn't — a
// dropped/rebuilt registry entry, a hand-rolled `bacio web --port`,
// the user's own running instance, or an unrelated process. Without
// it, a new worktree's `bacio web` would EADDRINUSE against a live
// port and tempt a cleanup into killing the occupant.
//
// Overridable so tests stay deterministic (a real probe's result
// depends on whatever happens to be bound on the test host).
var probePortFree = func(port int) bool {
	ln, err := net.Listen("tcp", net.JoinHostPort(DefaultAPIHost, strconv.Itoa(port)))
	if err != nil {
		return false
	}
	_ = ln.Close()
	return true
}

// RegistryFilename is the per-user file that tracks every worktree
// manifest bacio knows about. It lives under HOME/.bacio.
const RegistryFilename = "worktrees.yaml"

// MaxAutoPortWalk caps how far AllocatePort will walk forward looking
// for a free port slot before giving up. 100 ports is more than
// enough headroom for a developer's worktrees in practice — going
// beyond it almost certainly means something's broken.
const MaxAutoPortWalk = 100

// Registry is the in-memory shape of ~/.bacio/worktrees.yaml.
type Registry struct {
	Worktrees []RegistryEntry `yaml:"worktrees"  json:"worktrees"`
}

// RegistryEntry is one row in the registry: a single worktree's
// canonical allocations.
type RegistryEntry struct {
	Slug      string    `yaml:"slug"        json:"slug"`
	Path      string    `yaml:"path"        json:"path"`
	APIPort   int       `yaml:"api_port"    json:"api_port"`
	DBPath    string    `yaml:"db_path"     json:"db_path"`
	CreatedAt time.Time `yaml:"created_at"  json:"created_at"`
}

// RegistryPath returns the absolute path to ~/.bacio/worktrees.yaml,
// using the supplied HOME (empty string falls back to
// os.UserHomeDir()).
func RegistryPath(homeDir string) (string, error) {
	if homeDir == "" {
		h, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		homeDir = h
	}
	return filepath.Join(homeDir, ".bacio", RegistryFilename), nil
}

// ReadRegistry loads the registry from HOME. A missing file is
// treated as an empty registry — not an error — so callers don't have
// to special-case the first-run path.
func ReadRegistry(homeDir string) (*Registry, error) {
	path, err := RegistryPath(homeDir)
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(path)
	switch {
	case err == nil:
		// fall through
	case errors.Is(err, fs.ErrNotExist):
		return &Registry{}, nil
	default:
		return nil, err
	}
	var reg Registry
	if len(strings.TrimSpace(string(data))) == 0 {
		return &Registry{}, nil
	}
	if err := yaml.Unmarshal(data, &reg); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	return &reg, nil
}

// WriteRegistry persists the registry to disk in deterministic
// (slug-sorted) order. Creates HOME/.bacio if missing.
func WriteRegistry(homeDir string, reg *Registry) error {
	path, err := RegistryPath(homeDir)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	entries := append([]RegistryEntry(nil), reg.Worktrees...)
	sort.Slice(entries, func(i, j int) bool {
		return entries[i].Slug < entries[j].Slug
	})
	out := Registry{Worktrees: entries}
	data, err := yaml.Marshal(out)
	if err != nil {
		return err
	}
	header := "# worktrees.yaml — bacio's per-user worktree registry.\n" +
		"# Authoritative for port allocation; rebuilt from the per-worktree\n" +
		"# manifests if it drifts. Edit `bacio worktree init` / `rm` to\n" +
		"# mutate it instead of editing this file by hand.\n"
	return os.WriteFile(path, append([]byte(header), data...), 0o644)
}

// Upsert inserts or replaces the entry for entry.Path. Matching is
// on the absolute path; the slug is allowed to change on update.
func (r *Registry) Upsert(entry RegistryEntry) {
	for i, e := range r.Worktrees {
		if e.Path == entry.Path {
			r.Worktrees[i] = entry
			return
		}
	}
	r.Worktrees = append(r.Worktrees, entry)
}

// Remove drops the entry for the given absolute path. Returns true
// when a row was removed.
func (r *Registry) Remove(path string) bool {
	for i, e := range r.Worktrees {
		if e.Path == path {
			r.Worktrees = append(r.Worktrees[:i], r.Worktrees[i+1:]...)
			return true
		}
	}
	return false
}

// FindByPath returns the entry for the given absolute path, if any.
func (r *Registry) FindByPath(path string) (*RegistryEntry, bool) {
	for i, e := range r.Worktrees {
		if e.Path == path {
			return &r.Worktrees[i], true
		}
	}
	return nil, false
}

// FindBySlug returns the entry with the given slug, if any.
func (r *Registry) FindBySlug(slug string) (*RegistryEntry, bool) {
	for i, e := range r.Worktrees {
		if e.Slug == slug {
			return &r.Worktrees[i], true
		}
	}
	return nil, false
}

// AllocatePort picks a free port for a new worktree. The chosen port
// is deterministically seeded from the slug (DefaultAPIPort +
// HashPortOffset(slug)); the search walks forward by 1 until a slot
// turns up that is both unclaimed by another registry entry and not
// currently bound by a live process (probePortFree), or until
// MaxAutoPortWalk is exhausted.
//
// The OS probe matters because the registry is not a complete record
// of what is listening: an entry can be dropped on a registry rebuild,
// a `bacio web --port` may bind a port no manifest claims, and
// non-bacio processes are invisible to it entirely. Probing keeps a
// new worktree from being handed a port the user's own `bacio web` is
// already serving on.
//
// The default port (DefaultAPIPort) is reserved — manifest-free
// users keep using it, so a worktree init that lands there would
// clash with that legacy default. The allocator skips it.
func (r *Registry) AllocatePort(slug string) (int, error) {
	taken := make(map[int]bool, len(r.Worktrees)+1)
	for _, e := range r.Worktrees {
		taken[e.APIPort] = true
	}
	taken[DefaultAPIPort] = true

	start := DefaultAPIPort + 1 + HashPortOffset(slug)
	for off := 0; off < MaxAutoPortWalk; off++ {
		candidate := start + off
		if candidate > 65535 {
			return 0, fmt.Errorf("no free port within %d slots above %d", MaxAutoPortWalk, start)
		}
		if taken[candidate] {
			continue
		}
		if !probePortFree(candidate) {
			continue
		}
		return candidate, nil
	}
	return 0, fmt.Errorf("no free port found within %d slots starting at %d (registry has %d entries)", MaxAutoPortWalk, start, len(r.Worktrees))
}
