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

func newWorktreeInitCmd() *cobra.Command {
	var (
		slug    string
		port    int
		dbPath  string
		force   bool
		rawJSON string
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
  db_path .bacio/db.sqlite (relative to the worktree root)

Fails if a manifest is already present — pass --force to overwrite in
place. Honours --dry-run and --json (see ` + "`bacio schema show worktree.init`" + `).`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if inRemoteMode() {
				return fmt.Errorf("bacio worktree init: not supported in remote mode (touches the local filesystem); run against the local DB instead")
			}
			payload, err := parseJSONInput(cmd, args, rawJSON, "slug", "port", "db-path", "force")
			if err != nil {
				return err
			}
			in := inputs.WorktreeInitInput{
				Slug:   slug,
				Port:   port,
				DBPath: dbPath,
				Force:  force,
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
			info, err := git.Detect(cwd)
			if err != nil {
				return err
			}

			res, err := runWorktreeInit(info.Root, info.Name, in)
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
	cmd.Flags().StringVar(&dbPath, "db-path", "", "SQLite DB path relative to the worktree root (defaults to .bacio/db.sqlite)")
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
	dbPath := strings.TrimSpace(in.DBPath)
	if dbPath == "" {
		dbPath = filepath.Join(".bacio", "db.sqlite")
	}
	if filepath.IsAbs(dbPath) {
		// Absolute paths are accepted as a power-user override (e.g.
		// pointing two worktrees at a shared external DB intentionally),
		// but warn — the standard flow keeps the DB inside the worktree.
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

func printWorktreeShow(w io.Writer, r *worktreeShowResult) error {
	fmt.Fprintf(w, "Source:   %s\n", r.Source)
	if r.ManifestPath != "" {
		fmt.Fprintf(w, "Manifest: %s\n", r.ManifestPath)
	}
	fmt.Fprintf(w, "DB:       %s\n", r.DBPath)
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

// worktreeRmResult is the structured payload for `bacio worktree rm`.
type worktreeRmResult struct {
	ManifestPath string `json:"manifest_path,omitempty"`
	RegistryPath string `json:"registry_path"`
	Slug         string `json:"slug,omitempty"`
	DBPurged     bool   `json:"db_purged"`
	DBPath       string `json:"db_path,omitempty"`
}

func newWorktreeRmCmd() *cobra.Command {
	var (
		pathArg string
		confirm string
		purgeDB bool
		rawJSON string
	)
	cmd := &cobra.Command{
		Use:   "rm [path]",
		Short: "Remove a worktree manifest + its registry entry",
		Long: `Remove environment-config.yaml from the given worktree root (defaults
to the current dir) and drop its row from ~/.bacio/worktrees.yaml.

` + "`confirm`" + ` must equal the manifest's slug — same friction as
` + "`bacio repo rm`" + ` so an agent can't blow away the manifest by
accident. Use --purge-db to also delete the worktree's SQLite DB.`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if inRemoteMode() {
				return fmt.Errorf("bacio worktree rm: not supported in remote mode (touches the local filesystem)")
			}
			payload, err := parseJSONInput(cmd, args, rawJSON, "confirm", "purge-db")
			if err != nil {
				return err
			}
			in := inputs.WorktreeRmInput{
				Path:    pathArg,
				Confirm: confirm,
				PurgeDB: purgeDB,
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
			}
			if in.PurgeDB {
				db := m.Allocations.DBPath
				if !filepath.IsAbs(db) {
					db = filepath.Join(root, db)
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
	addInputFlag(cmd, &rawJSON)
	return cmd
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
