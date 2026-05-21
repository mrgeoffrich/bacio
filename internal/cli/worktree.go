package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/mrgeoffrich/bacio/internal/cli/inputs"
	"github.com/mrgeoffrich/bacio/internal/git"
	"github.com/mrgeoffrich/bacio/internal/procfind"
	"github.com/mrgeoffrich/bacio/internal/store"
	"github.com/mrgeoffrich/bacio/internal/wtenv"
)

// newWorktreeCmd builds the `bacio worktree` subcommand group. Verbs:
//
//   init    — write environment-config.yaml + register in the global
//             registry + gitignore the manifest (mutating)
//   show    — resolve and print the manifest for the current dir or a
//             given path (read-only)
//   list    — list every registered worktree from
//             ~/.bacio/worktrees.yaml (read-only)
//   rm      — remove the manifest + registry row; optionally drop the
//             worktree's SQLite DB too (mutating)
//
// The two mutating verbs honour --json / --dry-run / `bacio schema`
// per the six agent-CLI principles. The read-only verbs follow the
// `bacio status` precedent — no --json/--dry-run/schema entry.
func newWorktreeCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "worktree",
		Short: "Manage per-worktree bacio environment manifests (BACI-63)",
		Long: `Per-worktree environment manifests (BACI-63).

Each git worktree can carry an environment-config.yaml at its root that
binds the bacio instance running there to its own SQLite DB + API port.
This lets two worktrees of the same project run their own ` + "`bacio api`" + ` /
desktop / TUI side by side without clashing on ~/.bacio/db.sqlite or
127.0.0.1:5320.

Resolution chain (highest precedence first):
  1. --db / --addr flags
  2. $BACIO_ENV (path to a manifest)
  3. <worktree-root>/environment-config.yaml
  4. ~/.bacio/db.sqlite + 127.0.0.1:5320 (legacy default)

Manifest-free worktrees keep today's behaviour exactly — set up is
opt-in via ` + "`bacio worktree init`" + `.`,
	}
	cmd.AddCommand(
		newWorktreeInitCmd(),
		newWorktreeShowCmd(),
		newWorktreeListCmd(),
		newWorktreeRmCmd(),
	)
	return cmd
}

// worktreeInitResult is the success payload from `bacio worktree init`.
type worktreeInitResult struct {
	ManifestPath  string          `json:"manifest_path"`
	RegistryPath  string          `json:"registry_path"`
	Manifest      *wtenv.Manifest `json:"manifest"`
	GitignoreAdded bool           `json:"gitignore_added"`
}

// worktreeIsolateDBEnv supplies the default for `bacio worktree init
// --isolate-db`. Set it per-invocation only (e.g.
// `BACIO_WORKTREE_ISOLATE_DB=1 bacio worktree init`). An ambient
// setting (shell profile / .envrc) would be inherited by a dispatched
// worker running in a git worktree of the bacio repo, re-breaking
// BACI-87 — the worker's ticket lives in the shared DB.
const worktreeIsolateDBEnv = "BACIO_WORKTREE_ISOLATE_DB"

// isolateDBEnvDefault reports whether BACIO_WORKTREE_ISOLATE_DB asks for
// DB isolation. Accepts 1/true/yes (case-insensitive); everything else,
// including unset, reads as false — the shared-DB default.
func isolateDBEnvDefault() bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv(worktreeIsolateDBEnv))) {
	case "1", "true", "yes":
		return true
	default:
		return false
	}
}

func newWorktreeInitCmd() *cobra.Command {
	var (
		slug      string
		port      int
		dbPath    string
		isolateDB bool
		force     bool
		rawJSON   string
	)
	cmd := &cobra.Command{
		Use:   "init",
		Short: "Initialise a per-worktree environment manifest at the worktree root",
		Long: `Create environment-config.yaml at the current git worktree's root,
register it in ~/.bacio/worktrees.yaml, and append the filename to
.gitignore so a future clone doesn't accidentally inherit your local
allocations.

Defaults:
  slug    derived from the worktree directory basename
  port    auto-allocated from the registry (deterministic hash of slug
          with collision walk; the legacy default port 5320 is reserved)
  db_path the shared ~/.bacio/db.sqlite — the worktree gets API-port
          isolation only, so issue calls still reach the shared store

DB isolation is opt-in. Pass --isolate-db (or set
BACIO_WORKTREE_ISOLATE_DB=1) to bind a per-worktree DB at
.bacio/db.sqlite — useful when developing bacio itself and testing
schema migrations across sibling worktrees. Set the env var
per-invocation only: an ambient setting would be inherited by a
dispatched worker, whose ticket lives in the shared DB. --db-path pins
an explicit path and overrides --isolate-db.

Fails if a manifest is already present — pass --force to overwrite in
place. Honours --dry-run and --json (see ` + "`bacio schema show worktree.init`" + `).`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if inRemoteMode() {
				return fmt.Errorf("bacio worktree init: not supported in remote mode (touches the local filesystem); run against the local DB instead")
			}
			payload, err := parseJSONInput(cmd, args, rawJSON, "slug", "port", "db-path", "isolate-db", "force")
			if err != nil {
				return err
			}
			in := inputs.WorktreeInitInput{
				Slug:      slug,
				Port:      port,
				DBPath:    dbPath,
				IsolateDB: isolateDB,
				Force:     force,
			}
			if payload != nil {
				in = inputs.WorktreeInitInput{}
				dec := json.NewDecoder(strings.NewReader(string(payload)))
				dec.DisallowUnknownFields()
				if err := dec.Decode(&in); err != nil {
					return fmt.Errorf("decode --json: %w", err)
				}
			}
			cwd, err := os.Getwd()
			if err != nil {
				return err
			}
			// BACI-71: write the manifest at the LINKED worktree's own
			// root, not the main worktree's. git.Detect intentionally
			// returns the main worktree (so resolveRepo / install-agent
			// / sync share one repo identity); the manifest layer needs
			// the opposite — see git.WorktreeRoot's godoc.
			root, err := git.WorktreeRoot(cwd)
			if err != nil {
				return err
			}

			res, err := runWorktreeInit(root, filepath.Base(root), in)
			if err != nil {
				return err
			}
			if opts.dryRun {
				return emitDryRun(res)
			}
			if err := commitWorktreeInit(res); err != nil {
				return err
			}
			return emit(res)
		},
	}
	cmd.Flags().StringVar(&slug, "slug", "", "manifest slug (defaults to worktree basename)")
	cmd.Flags().IntVar(&port, "port", 0, "API port to bind (defaults to a hash-derived free port from ~/.bacio/worktrees.yaml)")
	cmd.Flags().StringVar(&dbPath, "db-path", "", "explicit SQLite DB path (relative to the worktree root, or absolute); overrides --isolate-db")
	cmd.Flags().BoolVar(&isolateDB, "isolate-db", isolateDBEnvDefault(), "bind a per-worktree DB at .bacio/db.sqlite instead of the shared ~/.bacio/db.sqlite (default from "+worktreeIsolateDBEnv+")")
	cmd.Flags().BoolVar(&force, "force", false, "overwrite an existing manifest at the worktree root")
	addInputFlag(cmd, &rawJSON)
	return cmd
}

// runWorktreeInit does the validation + manifest projection without
// touching disk. Used by both the real path (followed by
// commitWorktreeInit) and the --dry-run path. Returning the projected
// result keeps the dry-run output faithful to a real call.
func runWorktreeInit(root, baseName string, in inputs.WorktreeInitInput) (*worktreeInitResult, error) {
	manifestPath := filepath.Join(root, wtenv.DefaultManifestFilename)
	if !in.Force {
		if _, err := os.Stat(manifestPath); err == nil {
			return nil, fmt.Errorf("environment-config.yaml already exists at %s; pass --force to overwrite", manifestPath)
		} else if !errors.Is(err, fs.ErrNotExist) {
			return nil, err
		}
	}
	slug := strings.TrimSpace(in.Slug)
	if slug == "" {
		slug = sanitiseDefaultSlug(baseName)
	}
	slug, err := store.ValidateWorktreeSlug(slug)
	if err != nil {
		return nil, err
	}
	// db_path precedence (BACI-87): an explicit --db-path wins; else
	// --isolate-db binds a per-worktree .bacio/db.sqlite (relative —
	// resolved under the worktree root); else the shared store is
	// pinned by absolute path. The shared path must be written out, not
	// left empty: wtenv.manifestDBPath reads an empty db_path as a
	// per-worktree DB, so omitting it would isolate the DB. Pinning the
	// shared path is what a dispatched worker needs — its ticket lives
	// in ~/.bacio/db.sqlite.
	dbPath := strings.TrimSpace(in.DBPath)
	if dbPath == "" {
		if in.IsolateDB {
			dbPath = filepath.Join(".bacio", "db.sqlite")
		} else {
			shared, err := wtenv.DefaultDBPath("")
			if err != nil {
				return nil, fmt.Errorf("resolve shared DB path: %w", err)
			}
			dbPath = shared
		}
	}

	reg, err := wtenv.ReadRegistry("")
	if err != nil {
		return nil, fmt.Errorf("read registry: %w", err)
	}
	port := in.Port
	if port == 0 {
		// Re-runs against the same path preserve the existing port so
		// `init --force` doesn't reshuffle ports out from under any
		// long-running api/desktop process bound to the old one.
		if existing, ok := reg.FindByPath(root); ok && existing.APIPort != 0 {
			port = existing.APIPort
		} else {
			port, err = reg.AllocatePort(slug)
			if err != nil {
				return nil, err
			}
		}
	}
	if port == wtenv.DefaultAPIPort {
		return nil, fmt.Errorf("port %d is reserved for the legacy default (manifest-free) bacio instance; pick another port", wtenv.DefaultAPIPort)
	}
	if existing, ok := reg.FindBySlug(slug); ok && existing.Path != root {
		return nil, fmt.Errorf("slug %q is already registered against %s; pick a different --slug", slug, existing.Path)
	}

	manifest := &wtenv.Manifest{
		Identity: wtenv.Identity{
			Slug:      slug,
			Worktree:  root,
			CreatedAt: time.Now().UTC(),
		},
		Allocations: wtenv.Allocations{
			APIPort: port,
			DBPath:  dbPath,
		},
	}
	regPath, err := wtenv.RegistryPath("")
	if err != nil {
		return nil, err
	}
	return &worktreeInitResult{
		ManifestPath: manifestPath,
		RegistryPath: regPath,
		Manifest:     manifest,
	}, nil
}

// commitWorktreeInit writes the manifest, upserts the registry entry,
// and gitignores the manifest filename. Idempotent on the gitignore
// step.
func commitWorktreeInit(res *worktreeInitResult) error {
	if err := wtenv.SaveManifest(res.ManifestPath, res.Manifest); err != nil {
		return err
	}
	reg, err := wtenv.ReadRegistry("")
	if err != nil {
		return err
	}
	root := filepath.Dir(res.ManifestPath)
	absDB := res.Manifest.Allocations.DBPath
	if !filepath.IsAbs(absDB) {
		absDB = filepath.Join(root, absDB)
	}
	reg.Upsert(wtenv.RegistryEntry{
		Slug:      res.Manifest.Identity.Slug,
		Path:      root,
		APIPort:   res.Manifest.Allocations.APIPort,
		DBPath:    absDB,
		CreatedAt: res.Manifest.Identity.CreatedAt,
	})
	if err := wtenv.WriteRegistry("", reg); err != nil {
		return err
	}
	added, err := ensureWorktreeManifestGitignored(root)
	if err != nil {
		fmt.Fprintln(os.Stderr, "bacio: warning: could not update .gitignore:", err)
	}
	res.GitignoreAdded = added
	return nil
}

// ensureWorktreeManifestGitignored appends `environment-config.yaml`
// to .gitignore if not already covered. Mirrors
// ensureBacioGitignored in init.go.
func ensureWorktreeManifestGitignored(root string) (bool, error) {
	path := filepath.Join(root, ".gitignore")
	content, err := os.ReadFile(path)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return false, err
	}
	for _, line := range strings.Split(string(content), "\n") {
		switch strings.TrimSpace(line) {
		case wtenv.DefaultManifestFilename, "/" + wtenv.DefaultManifestFilename:
			return false, nil
		}
	}
	var b strings.Builder
	b.Write(content)
	if len(content) > 0 && !strings.HasSuffix(string(content), "\n") {
		b.WriteByte('\n')
	}
	b.WriteString(wtenv.DefaultManifestFilename + "\n")
	if err := os.WriteFile(path, []byte(b.String()), 0o644); err != nil {
		return false, err
	}
	return true, nil
}

// sanitiseDefaultSlug lowercases the directory basename and replaces
// disallowed characters with `-`, so a worktree at
// "../bacio-BACI-63" defaults to "bacio-baci-63". An empty result is
// caught downstream by ValidateWorktreeSlug.
func sanitiseDefaultSlug(name string) string {
	out := make([]rune, 0, len(name))
	for _, r := range strings.ToLower(name) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '-', r == '_', r == '.':
			out = append(out, r)
		case r == ' ', r == '/':
			out = append(out, '-')
		}
	}
	s := strings.Trim(string(out), "._-")
	if s == "" {
		s = "worktree"
	}
	return s
}

// worktreeShowResult is the structured payload for `bacio worktree show`.
type worktreeShowResult struct {
	Source        string          `json:"source"`
	ManifestPath  string          `json:"manifest_path,omitempty"`
	Manifest      *wtenv.Manifest `json:"manifest,omitempty"`
	DBPath        string          `json:"db_path"`
	APIAddr       string          `json:"api_addr"`
	RegistryEntry *wtenv.RegistryEntry `json:"registry_entry,omitempty"`
}

func newWorktreeShowCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "show [path]",
		Short: "Resolve and print the worktree environment for the current dir or a given path",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cwd, err := os.Getwd()
			if err != nil {
				return err
			}
			if len(args) == 1 {
				abs, err := filepath.Abs(args[0])
				if err != nil {
					return err
				}
				cwd = abs
			}
			res, err := wtenv.Resolve(wtenv.ResolveOpts{
				Cwd:       cwd,
				FlagDB:    opts.dbPath,
				EnvLookup: envLookupWithFlag(),
			})
			if err != nil {
				return err
			}
			out := &worktreeShowResult{
				Source:       string(res.Source),
				ManifestPath: res.ManifestPath,
				Manifest:     res.Manifest,
				DBPath:       res.DBPath,
				APIAddr:      res.APIAddr,
			}
			if reg, err := wtenv.ReadRegistry(""); err == nil && res.Manifest != nil {
				if entry, ok := reg.FindBySlug(res.Manifest.Identity.Slug); ok {
					out.RegistryEntry = entry
				}
			}
			if opts.output == outputJSON {
				return emit(out)
			}
			return printWorktreeShow(os.Stdout, out)
		},
	}
	return cmd
}

// printWorktreeInit renders the human-readable summary for a
// successful `bacio worktree init` — the resolved slug / port / DB /
// log dir, plus the explicit `--env` invocation. cwd-based resolution
// only fires for commands run from inside the worktree; the `--env`
// line is the get-it-right-anywhere escape hatch (and survives across
// shells, unlike an exported env var), so it is spelled out in full.
func printWorktreeInit(w io.Writer, r *worktreeInitResult) error {
	m := r.Manifest
	root := filepath.Dir(r.ManifestPath)

	dbPath := m.Allocations.DBPath
	if !filepath.IsAbs(dbPath) {
		dbPath = filepath.Join(root, dbPath)
	}
	// Mirror logging.Resolve's per-worktree default: an explicit
	// allocations.log_dir wins, otherwise <worktree-root>/.bacio/logs/.
	logDir := strings.TrimSpace(m.Allocations.LogDir)
	switch {
	case logDir == "":
		logDir = filepath.Join(root, ".bacio", "logs")
	case !filepath.IsAbs(logDir):
		logDir = filepath.Join(root, logDir)
	}

	fmt.Fprintln(w, "Worktree environment initialised.")
	fmt.Fprintln(w)
	fmt.Fprintf(w, "  Slug:      %s\n", m.Identity.Slug)
	fmt.Fprintf(w, "  API:       %s:%d\n", wtenv.DefaultAPIHost, m.Allocations.APIPort)
	fmt.Fprintf(w, "  DB:        %s%s\n", dbPath, dbScopeSuffix(dbPath))
	fmt.Fprintf(w, "  Log dir:   %s\n", logDir)
	fmt.Fprintf(w, "  Manifest:  %s\n", r.ManifestPath)
	if r.GitignoreAdded {
		fmt.Fprintf(w, "  Gitignore: added %s\n", wtenv.DefaultManifestFilename)
	}
	fmt.Fprintln(w)
	fmt.Fprintln(w, "bacio commands run from inside this worktree pick this up")
	fmt.Fprintln(w, "automatically — resolution walks up from cwd. To target this")
	fmt.Fprintln(w, "environment from anywhere else, pass the manifest explicitly:")
	fmt.Fprintln(w)
	fmt.Fprintf(w, "  bacio --env %s <command>\n", r.ManifestPath)
	return nil
}

// printWorktreeRm renders the human-readable summary for
// `bacio worktree rm`: the manifest/registry removal lines, the
// optional DB purge, and — when non-empty — the reaped-process block.
// dryRun swaps "Reaped processes:" for "Would signal:".
func printWorktreeRm(w io.Writer, r *worktreeRmResult, dryRun bool) error {
	fmt.Fprintln(w, "Worktree environment removed.")
	fmt.Fprintln(w)
	if r.Slug != "" {
		fmt.Fprintf(w, "  Slug:      %s\n", r.Slug)
	}
	if r.ManifestPath != "" {
		fmt.Fprintf(w, "  Manifest:  %s\n", r.ManifestPath)
	}
	fmt.Fprintf(w, "  Registry:  %s\n", r.RegistryPath)
	if r.DBPurged {
		fmt.Fprintf(w, "  DB purged: %s\n", r.DBPath)
	}
	if len(r.ReapedProcesses) > 0 {
		fmt.Fprintln(w)
		if dryRun {
			fmt.Fprintln(w, "Would signal:")
		} else {
			fmt.Fprintln(w, "Reaped processes:")
		}
		for _, p := range r.ReapedProcesses {
			line := fmt.Sprintf("  pid=%-7d %-14s", p.PID, p.Command)
			switch {
			case dryRun:
				// no signal column on a dry-run row
			case p.Signal == "kill":
				line += " SIGKILL"
			case p.Signal == "term":
				line += " SIGTERM"
			}
			if p.Note != "" {
				line += " (" + p.Note + ")"
			}
			fmt.Fprintln(w, strings.TrimRight(line, " "))
		}
	}
	if r.ProcessScanNote != "" {
		fmt.Fprintln(w)
		fmt.Fprintf(w, "Process scan: %s\n", r.ProcessScanNote)
	}
	return nil
}

func printWorktreeShow(w io.Writer, r *worktreeShowResult) error {
	fmt.Fprintf(w, "Source:   %s\n", r.Source)
	if r.ManifestPath != "" {
		fmt.Fprintf(w, "Manifest: %s\n", r.ManifestPath)
	}
	fmt.Fprintf(w, "DB:       %s%s\n", r.DBPath, dbScopeSuffix(r.DBPath))
	fmt.Fprintf(w, "API:      %s\n", r.APIAddr)
	if r.Manifest != nil {
		fmt.Fprintf(w, "Slug:     %s\n", r.Manifest.Identity.Slug)
		if !r.Manifest.Identity.CreatedAt.IsZero() {
			fmt.Fprintf(w, "Created:  %s\n", localTime(r.Manifest.Identity.CreatedAt))
		}
		if r.Manifest.Identity.Worktree != "" {
			fmt.Fprintf(w, "Worktree: %s\n", r.Manifest.Identity.Worktree)
		}
	}
	if r.RegistryEntry != nil {
		fmt.Fprintf(w, "Registry: %s (port=%d)\n", r.RegistryEntry.Path, r.RegistryEntry.APIPort)
	}
	return nil
}

// worktreeListResult is the structured payload for `bacio worktree list`.
type worktreeListResult struct {
	RegistryPath string                  `json:"registry_path"`
	Entries      []worktreeListEntry     `json:"entries"`
}

// worktreeListEntry decorates a registry row with a `present` flag so
// the user can see at a glance which manifests still exist on disk.
type worktreeListEntry struct {
	wtenv.RegistryEntry
	ManifestPresent bool `json:"manifest_present"`
}

func newWorktreeListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List every worktree manifest registered under ~/.bacio/worktrees.yaml",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			reg, err := wtenv.ReadRegistry("")
			if err != nil {
				return err
			}
			path, err := wtenv.RegistryPath("")
			if err != nil {
				return err
			}
			out := &worktreeListResult{RegistryPath: path}
			for _, e := range reg.Worktrees {
				entry := worktreeListEntry{RegistryEntry: e}
				if _, err := os.Stat(filepath.Join(e.Path, wtenv.DefaultManifestFilename)); err == nil {
					entry.ManifestPresent = true
				}
				out.Entries = append(out.Entries, entry)
			}
			if opts.output == outputJSON {
				return emit(out)
			}
			return printWorktreeList(os.Stdout, out)
		},
	}
}

func printWorktreeList(w io.Writer, r *worktreeListResult) error {
	if len(r.Entries) == 0 {
		fmt.Fprintln(w, "(no worktree manifests registered)")
		fmt.Fprintf(w, "Registry: %s\n", r.RegistryPath)
		return nil
	}
	fmt.Fprintf(w, "Registry: %s\n", r.RegistryPath)
	for _, e := range r.Entries {
		mark := " "
		if !e.ManifestPresent {
			mark = "!"
		}
		fmt.Fprintf(w, "  %s %-32s port=%-5d %s\n", mark, e.Slug, e.APIPort, e.Path)
	}
	for _, e := range r.Entries {
		if !e.ManifestPresent {
			fmt.Fprintf(w, "\n! %s has no manifest at %s (path moved or deleted; run `bacio worktree rm` to reap)\n",
				e.Slug, filepath.Join(e.Path, wtenv.DefaultManifestFilename))
		}
	}
	return nil
}

// reapedProcess reports one process discovered listening on a torn-down
// worktree's API port (BACI-93). Signal is "term", "kill", or "" (a
// dry-run / would-signal entry, or a non-bacio listener left alone).
type reapedProcess struct {
	PID     int    `json:"pid"`
	Command string `json:"command"`
	Signal  string `json:"signal"`
	Killed  bool   `json:"killed"`
	Note    string `json:"note,omitempty"`
}

// worktreeRmResult is the structured payload for `bacio worktree rm`.
type worktreeRmResult struct {
	ManifestPath    string          `json:"manifest_path,omitempty"`
	RegistryPath    string          `json:"registry_path"`
	Slug            string          `json:"slug,omitempty"`
	DBPurged        bool            `json:"db_purged"`
	DBPath          string          `json:"db_path,omitempty"`
	APIPort         int             `json:"api_port,omitempty"`
	ReapedProcesses []reapedProcess `json:"reaped_processes,omitempty"`
	ProcessScanNote string          `json:"process_scan_note,omitempty"`
}

// procReapGrace is how long `worktree rm` waits for a SIGTERM'd
// process to exit before escalating to SIGKILL.
const procReapGrace = 3 * time.Second

// listenersOnPortFn is the discovery seam for the process reap — a
// package var so worktree_test.go can stub it without spawning real
// listeners. Production points it at procfind.ListenersOnPort.
var listenersOnPortFn = procfind.ListenersOnPort

// reapProcSignalTerm / reapProcSignalKill / reapProcAlive are the
// signalling seams, stubbable the same way. Production wraps procfind.
var (
	reapProcSignalTerm = procfind.SignalTerm
	reapProcSignalKill = procfind.SignalKill
	reapProcAlive      = procfind.Alive
)

// isBacioComm reports whether a discovered listener's command names a
// bacio binary — `bacio` or `bacio-desktop`. `worktree rm` only ever
// signals a process whose command passes this check, so a stranger
// that merely grabbed the worktree's port is never killed. Matches on
// the basename because the discovery tool may report an absolute path.
func isBacioComm(comm string) bool {
	base := filepath.Base(strings.TrimSpace(comm))
	return base == "bacio" || base == "bacio-desktop"
}

// reapWorktreeProcesses discovers processes listening on the worktree's
// API port and signals the bacio ones. It mutates res in place
// (ReapedProcesses / ProcessScanNote). When dryRun is set it only
// records the would-signal entries and signals nothing. Errors from
// discovery are non-fatal — the teardown should still proceed — so
// they are recorded as a note rather than returned.
func reapWorktreeProcesses(res *worktreeRmResult, port int, dryRun bool) {
	if port == 0 {
		res.ProcessScanNote = "manifest has no API port allocated; nothing to reap"
		return
	}
	if port == wtenv.DefaultAPIPort {
		res.ProcessScanNote = fmt.Sprintf("manifest API port is the legacy default %d; refusing to signal processes on the shared port", wtenv.DefaultAPIPort)
		return
	}
	listeners, err := listenersOnPortFn(port)
	if err != nil {
		res.ProcessScanNote = fmt.Sprintf("could not scan for processes on port %d: %v; check and kill manually", port, err)
		return
	}
	self := os.Getpid()
	for _, l := range listeners {
		rp := reapedProcess{PID: l.PID, Command: l.Command}
		switch {
		case l.PID == self:
			// Defensive: `worktree rm` doesn't bind the port, but
			// never signal ourselves if a future caller does.
			rp.Note = "this process; left running"
		case !isBacioComm(l.Command):
			rp.Note = "not a bacio process; left running"
		case dryRun:
			// Would-signal entry: no Signal, not Killed.
		default:
			signalReapedProcess(&rp)
		}
		res.ReapedProcesses = append(res.ReapedProcesses, rp)
	}
}

// signalReapedProcess performs the SIGTERM→grace→SIGKILL dance against
// one matched bacio process, recording the outcome on rp.
func signalReapedProcess(rp *reapedProcess) {
	if err := reapProcSignalTerm(rp.PID); err != nil {
		rp.Note = fmt.Sprintf("SIGTERM failed: %v", err)
		// The process may already be gone — treat that as success.
		if !reapProcAlive(rp.PID) {
			rp.Signal = "term"
			rp.Killed = true
			rp.Note = "process already gone"
		}
		return
	}
	rp.Signal = "term"
	deadline := time.Now().Add(procReapGrace)
	for time.Now().Before(deadline) {
		if !reapProcAlive(rp.PID) {
			rp.Killed = true
			return
		}
		time.Sleep(300 * time.Millisecond)
	}
	if !reapProcAlive(rp.PID) {
		rp.Killed = true
		return
	}
	// Survived the grace window — escalate.
	if err := reapProcSignalKill(rp.PID); err != nil {
		rp.Note = fmt.Sprintf("survived SIGTERM grace; SIGKILL failed: %v", err)
		return
	}
	rp.Signal = "kill"
	rp.Killed = true
	rp.Note = "survived SIGTERM grace; sent SIGKILL"
}

func newWorktreeRmCmd() *cobra.Command {
	var (
		pathArg       string
		confirm       string
		purgeDB       bool
		keepProcesses bool
		rawJSON       string
	)
	cmd := &cobra.Command{
		Use:   "rm [path]",
		Short: "Remove a worktree manifest + its registry entry",
		Long: `Remove environment-config.yaml from the given worktree root (defaults
to the current dir) and drop its row from ~/.bacio/worktrees.yaml.

` + "`confirm`" + ` must equal the manifest's slug — same friction as
` + "`bacio repo rm`" + ` so an agent can't blow away the manifest by
accident. Use --purge-db to also delete the worktree's SQLite DB.

By default rm also reaps any bacio process (bacio api / bacio web /
bacio-desktop) still listening on the manifest's API port — SIGTERM,
then SIGKILL after a short grace — so a torn-down worktree can't leave
an orphan holding the shared ui_leader lease. Pass --keep-processes to
skip that step. --dry-run lists the PIDs it would signal without
touching anything. A bacio channel process has no HTTP listener and is
not reaped; processes on the legacy default port 5320 are never
signalled.`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if inRemoteMode() {
				return fmt.Errorf("bacio worktree rm: not supported in remote mode (touches the local filesystem)")
			}
			payload, err := parseJSONInput(cmd, args, rawJSON, "confirm", "purge-db", "keep-processes")
			if err != nil {
				return err
			}
			in := inputs.WorktreeRmInput{
				Path:          pathArg,
				Confirm:       confirm,
				PurgeDB:       purgeDB,
				KeepProcesses: keepProcesses,
			}
			if len(args) == 1 {
				in.Path = args[0]
			}
			if payload != nil {
				in = inputs.WorktreeRmInput{}
				dec := json.NewDecoder(strings.NewReader(string(payload)))
				dec.DisallowUnknownFields()
				if err := dec.Decode(&in); err != nil {
					return fmt.Errorf("decode --json: %w", err)
				}
			}
			if in.Path == "" {
				cwd, err := os.Getwd()
				if err != nil {
					return err
				}
				in.Path = cwd
			}
			root, err := filepath.Abs(in.Path)
			if err != nil {
				return err
			}
			manifestPath := filepath.Join(root, wtenv.DefaultManifestFilename)
			m, err := wtenv.LoadManifest(manifestPath)
			if err != nil {
				return fmt.Errorf("load manifest at %s: %w", manifestPath, err)
			}
			if strings.TrimSpace(in.Confirm) == "" || in.Confirm != m.Identity.Slug {
				return fmt.Errorf("confirm value %q does not match manifest slug %q; pass --confirm %s (or {\"confirm\":%q} in --json) to proceed", in.Confirm, m.Identity.Slug, m.Identity.Slug, m.Identity.Slug)
			}
			regPath, err := wtenv.RegistryPath("")
			if err != nil {
				return err
			}
			res := &worktreeRmResult{
				ManifestPath: manifestPath,
				RegistryPath: regPath,
				Slug:         m.Identity.Slug,
				APIPort:      m.Allocations.APIPort,
			}
			// Reap port-bound processes (BACI-93) before any
			// filesystem mutation, so a failure here leaves the
			// manifest intact for a retry. Skipped on --keep-processes;
			// in --dry-run it only lists the would-signal PIDs.
			if !in.KeepProcesses {
				reapWorktreeProcesses(res, m.Allocations.APIPort, opts.dryRun)
			}
			if in.PurgeDB {
				db := m.Allocations.DBPath
				if !filepath.IsAbs(db) {
					db = filepath.Join(root, db)
				}
				// A shared-DB manifest (the default since BACI-87) pins
				// the global ~/.bacio/db.sqlite. Purging it would wipe
				// every project's issues — refuse, and name the path so
				// the user sees why.
				if shared, err := wtenv.DefaultDBPath(""); err == nil && filepath.Clean(db) == filepath.Clean(shared) {
					return fmt.Errorf("--purge-db refused: manifest db_path is the shared store %s (this worktree has DB isolation off); drop --purge-db, or re-init with --isolate-db if you meant a per-worktree DB", shared)
				}
				res.DBPath = db
				res.DBPurged = true
			}
			if opts.dryRun {
				return emitDryRun(res)
			}
			if err := os.Remove(manifestPath); err != nil && !errors.Is(err, fs.ErrNotExist) {
				return err
			}
			reg, err := wtenv.ReadRegistry("")
			if err != nil {
				return err
			}
			reg.Remove(root)
			if err := wtenv.WriteRegistry("", reg); err != nil {
				return err
			}
			if in.PurgeDB && res.DBPath != "" {
				if err := os.Remove(res.DBPath); err != nil && !errors.Is(err, fs.ErrNotExist) {
					return fmt.Errorf("purge db: %w", err)
				}
			}
			return emit(res)
		},
	}
	cmd.Flags().StringVar(&confirm, "confirm", "", "confirmation token (must equal the manifest slug)")
	cmd.Flags().BoolVar(&purgeDB, "purge-db", false, "also delete the worktree's SQLite DB file")
	cmd.Flags().BoolVar(&keepProcesses, "keep-processes", false, "do not reap bacio processes listening on the worktree's API port")
	addInputFlag(cmd, &rawJSON)
	return cmd
}

// dbScopeSuffix labels a resolved DB path for human-readable worktree
// output: the shared ~/.bacio/db.sqlite store vs an isolated
// per-worktree DB (BACI-87). Empty when the home dir can't be resolved
// — a label is a nicety, not worth failing the command over.
func dbScopeSuffix(dbPath string) string {
	shared, err := wtenv.DefaultDBPath("")
	if err != nil {
		return ""
	}
	if filepath.Clean(dbPath) == filepath.Clean(shared) {
		return "  (shared)"
	}
	return "  (isolated)"
}

// envLookupWithFlag returns an env-lookup func that swaps in the
// global --env flag value for BACIO_ENV when set. Mirrors what
// resolveEnv does so the read-only `worktree show` honours the same
// override surface as every mutating verb.
func envLookupWithFlag() func(string) string {
	if opts.envPath == "" {
		return os.Getenv
	}
	return func(k string) string {
		if k == wtenv.EnvVar {
			return opts.envPath
		}
		return os.Getenv(k)
	}
}
