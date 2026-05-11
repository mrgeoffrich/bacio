package cli

import (
	"context"
	"errors"
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/mrgeoffrich/bacio/internal/git"
	"github.com/mrgeoffrich/bacio/internal/model"
	"github.com/mrgeoffrich/bacio/internal/store"
	"github.com/mrgeoffrich/bacio/internal/sync"
)

// newSyncCmd registers the `bacio sync` command group. Phase 4 ships the
// full user-facing surface: `bacio sync init`, `bacio sync clone`, and the
// bare `bacio sync` (steady-state). The Phase-2/3 development verbs
// `bacio sync export` and `bacio sync import` stay registered (and hidden)
// — they're still useful for diagnostics and their tests guard
// behaviour we care about.
//
// The parent group is no longer hidden: `bacio help sync` shows it and
// the bootstrap flow.
func newSyncCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "sync",
		Short: "Git-backed sync of the local DB to a shared sync repo",
		Long: `Synchronise the local bacio database with a shared sync repo (a separate
git repo whose working tree carries one folder per project, with YAML
and markdown for each record). Run with no subcommand to do a full
pull → import → export → commit → push cycle.

Two non-overlapping bootstrap flows:
  bacio sync init <local-path> [--remote URL]   # first time setup
  bacio sync clone [<local-path>]               # join an existing sync repo

The Phase-2 dev tools (bacio sync export / import) remain available as
hidden commands for low-level debugging.`,
		RunE: runSync, // bare `bacio sync` runs the steady-state pipeline
	}
	syncRunFlags(cmd)
	cmd.AddCommand(
		newSyncInitCmd(),
		newSyncCloneCmd(),
		newSyncVerifyCmd(),
		newSyncInspectCmd(),
		newSyncExportCmd(),
		newSyncImportCmd(),
	)
	return cmd
}

// requireSyncRepoMode ensures the current working tree is the root of
// an bacio sync repo (bacio-sync.yaml at the working-tree root). Returns
// the absolute path of that root for the caller to pass on to the
// engine. Pairs with the inverse check (`if sync.IsSyncRepo … return
// error`) used by every other sync command.
//
// The error message mirrors the wording used by errSyncRepoMode in
// the opposite direction so users see a consistent pair: one says
// "this is a sync repo", the other says "this isn't".
func requireSyncRepoMode() (string, error) {
	cwd, err := os.Getwd()
	if err != nil {
		return "", err
	}
	info, err := git.Detect(cwd)
	if err != nil {
		return "", fmt.Errorf("this command must run inside an bacio sync repo (bacio-sync.yaml at root): %w", err)
	}
	if !sync.IsSyncRepo(info.Root) {
		return "", fmt.Errorf("this command must run inside an bacio sync repo (bacio-sync.yaml at root); current working tree is %s — run `bacio sync clone` to set one up", info.Root)
	}
	return info.Root, nil
}

// syncRunFlags wires the run-time flags on the bare `bacio sync` command.
// Kept in a helper so the same flag set lives in one place; the cobra
// docs render them off cmd.Flags() either way.
func syncRunFlags(cmd *cobra.Command) {
	cmd.Flags().Bool("no-import", false, "skip the import phase (files → DB)")
	cmd.Flags().Bool("no-export", false, "skip the export phase (DB → files)")
	cmd.Flags().Bool("no-push", false, "do everything but the final git push")
}

// runSync is the steady-state `bacio sync`. Resolves the project,
// .bacio/config.yaml, and the local sync-clone path, then hands off to
// sync.Engine.Run.
func runSync(cmd *cobra.Command, _ []string) error {
	if inRemoteMode() {
		return fmt.Errorf("bacio sync: not supported in remote mode (operates on the local DB only)")
	}

	noImport, _ := cmd.Flags().GetBool("no-import")
	noExport, _ := cmd.Flags().GetBool("no-export")
	noPush, _ := cmd.Flags().GetBool("no-push")

	cwd, err := os.Getwd()
	if err != nil {
		return err
	}
	info, err := git.Detect(cwd)
	if err != nil {
		return err
	}
	if sync.IsSyncRepo(info.Root) {
		return fmt.Errorf("bacio sync runs from inside a project repo, not a sync repo (%s)", info.Root)
	}

	cfg, err := sync.ReadProjectConfig(info.Root)
	if err != nil {
		if errors.Is(err, sync.ErrNoConfig) {
			return fmt.Errorf("no .bacio/config.yaml found at %s; run 'bacio sync init' or 'bacio sync clone' first", info.Root)
		}
		return err
	}
	if cfg.Sync.Remote == "" {
		return fmt.Errorf("%s/.bacio/config.yaml has no sync.remote set", info.Root)
	}

	s, err := openStore()
	if err != nil {
		return err
	}
	defer s.Close()

	rec, err := s.GetSyncRemote(cfg.Sync.Remote)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return fmt.Errorf("no local clone of sync remote %s; run 'bacio sync clone' first", cfg.Sync.Remote)
		}
		return err
	}
	syncRepo, err := git.Open(rec.LocalPath)
	if err != nil {
		return fmt.Errorf("local sync clone at %s is missing or unreadable: %w", rec.LocalPath, err)
	}

	dbPath := opts.dbPath
	if dbPath == "" {
		def, _ := store.DefaultPath()
		dbPath = def
	}
	release, err := sync.AcquireSyncLock(dbPath)
	if err != nil {
		return err
	}
	defer release() //nolint:errcheck — best-effort lock release

	eng := &sync.Engine{Store: s, Actor: actor(), DryRun: opts.dryRun}
	res, err := eng.Run(context.Background(), info.Root, syncRepo, sync.RunOptions{
		NoImport: noImport,
		NoExport: noExport,
		NoPush:   noPush,
		DryRun:   opts.dryRun,
	})
	if err != nil {
		return err
	}

	// Audit + last_sync_at bookkeeping for non-dryrun runs.
	if !opts.dryRun {
		recordOp(s, model.HistoryEntry{
			Op:      "sync.run",
			Kind:    "repo",
			Details: syncRunDetails(res),
		})
		if err := s.MarkSyncCompleted(cfg.Sync.Remote); err != nil {
			fmt.Fprintln(os.Stderr, "bacio: warning: failed to update last_sync_at:", err)
		}
		// Per-event audit ops mirror Phase 3's recordSyncImportOps.
		if res.Import != nil {
			recordSyncImportOps(s, res.Import)
		}
	}

	out := syncRunResult{RunResult: res}
	if opts.dryRun {
		return emitDryRun(out)
	}
	return emit(out)
}

// syncRunDetails formats a one-line summary of a RunResult for the
// audit log. Counts come straight from the embedded import / export
// results.
func syncRunDetails(res *sync.RunResult) string {
	if res == nil {
		return ""
	}
	parts := ""
	if res.Import != nil {
		parts += fmt.Sprintf("inserted=%d updated=%d noop=%d ",
			res.Import.Inserted, res.Import.Updated, res.Import.NoOp)
	}
	if res.Export != nil && res.Export.ExportResult != nil {
		parts += fmt.Sprintf("renames=%d writes=%d deletes=%d ",
			res.Export.Renames, res.Export.Writes, res.Export.Deletes)
	}
	if res.Commit != "" {
		parts += "commit=" + res.Commit
	}
	if res.Pushed {
		parts += " pushed=true"
	}
	return parts
}

// syncRunResult wraps *sync.RunResult so renderText can dispatch a
// text renderer for the steady-state command without leaking sync.*
// types into the output package.
type syncRunResult struct {
	*sync.RunResult
}

// newSyncInitCmd handles `bacio sync init <local-path> [--remote URL]`.
// Refuses to bootstrap against a remote that already has commits; the
// caller is expected to use `bacio sync clone` for that case.
func newSyncInitCmd() *cobra.Command {
	var remote string
	cmd := &cobra.Command{
		Use:   "init <local-path>",
		Short: "Create a new sync repo and seed it with the project's data",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if inRemoteMode() {
				return fmt.Errorf("bacio sync init: not supported in remote mode (operates on the local DB only)")
			}
			cwd, err := os.Getwd()
			if err != nil {
				return err
			}
			info, err := git.Detect(cwd)
			if err != nil {
				return err
			}
			if sync.IsSyncRepo(info.Root) {
				return fmt.Errorf("bacio sync init runs from inside a project repo, not a sync repo (%s)", info.Root)
			}

			s, err := openStore()
			if err != nil {
				return err
			}
			defer s.Close()

			eng := &sync.Engine{Store: s, Actor: actor(), DryRun: opts.dryRun}
			res, err := eng.InitSyncRepo(context.Background(), info.Root, sync.InitOptions{
				LocalPath: args[0],
				Remote:    remote,
			})
			if err != nil {
				return err
			}
			if !opts.dryRun {
				recordOp(s, model.HistoryEntry{
					Op:      "sync.init",
					Kind:    "repo",
					Details: fmt.Sprintf("local=%s remote=%s commit=%s pushed=%v", res.LocalPath, res.Remote, res.CommitSHA, res.Pushed),
				})
			}
			out := syncInitResult{InitResult: res}
			if opts.dryRun {
				return emitDryRun(out)
			}
			return emit(out)
		},
	}
	cmd.Flags().StringVar(&remote, "remote", "", "git URL of the remote sync repo (sets up origin and writes .bacio/config.yaml)")
	return cmd
}

// newSyncCloneCmd handles `bacio sync clone [<local-path>]
// [--allow-renumber] [--dry-run]`. Without `--allow-renumber`, errors
// with a preview when the local DB has data that would be renumbered.
func newSyncCloneCmd() *cobra.Command {
	var allowRenumber bool
	cmd := &cobra.Command{
		Use:   "clone [<local-path>]",
		Short: "Join an existing sync repo and import its data into the local DB",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if inRemoteMode() {
				return fmt.Errorf("bacio sync clone: not supported in remote mode (operates on the local DB only)")
			}
			cwd, err := os.Getwd()
			if err != nil {
				return err
			}
			info, err := git.Detect(cwd)
			if err != nil {
				return err
			}
			if sync.IsSyncRepo(info.Root) {
				return fmt.Errorf("bacio sync clone runs from inside a project repo, not a sync repo (%s)", info.Root)
			}

			cfg, err := sync.ReadProjectConfig(info.Root)
			if err != nil {
				if errors.Is(err, sync.ErrNoConfig) {
					return fmt.Errorf("no .bacio/config.yaml at %s; ask the project owner to run 'bacio sync init --remote <url>' first", info.Root)
				}
				return err
			}
			if cfg.Sync.Remote == "" {
				return fmt.Errorf("%s/.bacio/config.yaml has no sync.remote set", info.Root)
			}

			s, err := openStore()
			if err != nil {
				return err
			}
			defer s.Close()

			localPath := ""
			if len(args) == 1 {
				localPath = args[0]
			}
			eng := &sync.Engine{Store: s, Actor: actor(), DryRun: opts.dryRun}
			res, err := eng.CloneSyncRepo(context.Background(), info.Root, sync.CloneOptions{
				LocalPath:     localPath,
				Remote:        cfg.Sync.Remote,
				AllowRenumber: allowRenumber,
				DryRun:        opts.dryRun,
			})
			if err != nil {
				// Even on error, emit the preview if the engine populated one.
				if res != nil && res.PreviewCollisions != nil {
					_ = emit(syncCloneResult{CloneResult: res})
				}
				return err
			}
			if !opts.dryRun {
				recordOp(s, model.HistoryEntry{
					Op:      "sync.clone",
					Kind:    "repo",
					Details: fmt.Sprintf("local=%s remote=%s", res.LocalPath, res.Remote),
				})
				if res.Import != nil {
					recordSyncImportOps(s, res.Import)
				}
			}
			out := syncCloneResult{CloneResult: res}
			if opts.dryRun {
				return emitDryRun(out)
			}
			return emit(out)
		},
	}
	cmd.Flags().BoolVar(&allowRenumber, "allow-renumber", false, "permit local rows to be renumbered/renamed to resolve collisions")
	return cmd
}

// syncInitResult wraps *sync.InitResult for the renderText switch.
type syncInitResult struct {
	*sync.InitResult
}

// syncCloneResult wraps *sync.CloneResult for the renderText switch.
type syncCloneResult struct {
	*sync.CloneResult
}

// newSyncVerifyCmd handles `bacio sync verify`. Must run inside a sync
// repo. Walks the working tree, prints (or JSON-emits) the
// VerifyResult, and exits non-zero on any errors (warnings don't
// affect exit status). Filesystem-only — never opens the local DB.
func newSyncVerifyCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "verify",
		Short: "Check sync-repo on-disk consistency (run from inside a sync repo)",
		Long: `Walks the current sync repo and reports parse failures, uuid
collisions, dangling cross-references, case-insensitive folder
collisions, redirect-chain cycles, orphan comment files, and
body-hash drift.

Errors fail the command (exit non-zero); warnings (dangling refs,
hash drift) print but don't change exit status.

Run from inside the sync repo's working tree.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if inRemoteMode() {
				return fmt.Errorf("bacio sync verify: not supported in remote mode (operates on the local sync repo only)")
			}
			root, err := requireSyncRepoMode()
			if err != nil {
				return err
			}
			eng := &sync.Engine{}
			res, err := eng.Verify(context.Background(), root)
			if err != nil {
				return err
			}
			if err := emit(syncVerifyResult{VerifyResult: res}); err != nil {
				return err
			}
			if len(res.Errors) > 0 {
				return fmt.Errorf("verify: %d error(s) found", len(res.Errors))
			}
			return nil
		},
	}
	return cmd
}

// syncVerifyResult wraps *sync.VerifyResult so renderText can
// dispatch on the type. JSON emission uses the embedded pointer's
// own json tags.
type syncVerifyResult struct {
	*sync.VerifyResult
}

// newSyncInspectCmd handles `bacio sync inspect <prefix>` and its
// variants. Read-only browsing of one prefix's records, useful for
// debugging sync without touching the project repo's DB.
func newSyncInspectCmd() *cobra.Command {
	var (
		issue    string
		feature  string
		document string
	)
	cmd := &cobra.Command{
		Use:   "inspect <prefix>",
		Short: "Browse a sync repo's records read-only (run from inside a sync repo)",
		Long: `Prints either a per-prefix summary (record counts plus recent
renumbers/renames) or one specific record's parsed YAML and body.

Run from inside the sync repo's working tree.

Examples:
  bacio sync inspect MINI
  bacio sync inspect MINI --issue MINI-7
  bacio sync inspect MINI --feature auth-rewrite
  bacio sync inspect MINI --doc auth-overview.md`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if inRemoteMode() {
				return fmt.Errorf("bacio sync inspect: not supported in remote mode (operates on the local sync repo only)")
			}
			root, err := requireSyncRepoMode()
			if err != nil {
				return err
			}
			// At-most-one-target check. Cobra doesn't have native
			// mutex-flag support, so do it ourselves.
			selected := 0
			for _, s := range []string{issue, feature, document} {
				if s != "" {
					selected++
				}
			}
			if selected > 1 {
				return fmt.Errorf("at most one of --issue, --feature, --doc can be set")
			}
			eng := &sync.Engine{}
			res, err := eng.Inspect(context.Background(), root, sync.InspectOptions{
				Prefix:   args[0],
				Issue:    issue,
				Feature:  feature,
				Document: document,
			})
			if err != nil {
				return err
			}
			return emit(syncInspectResult{InspectResult: res})
		},
	}
	cmd.Flags().StringVar(&issue, "issue", "", "show one issue (label, e.g. MINI-7)")
	cmd.Flags().StringVar(&feature, "feature", "", "show one feature (slug)")
	cmd.Flags().StringVar(&document, "doc", "", "show one document (filename)")
	return cmd
}

// syncInspectResult wraps *sync.InspectResult for renderText.
type syncInspectResult struct {
	*sync.InspectResult
}

func newSyncExportCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:    "export <path>",
		Short:  "Export the local DB to a folder as YAML + markdown (diagnostic)",
		Args:   cobra.ExactArgs(1),
		Hidden: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			if inRemoteMode() {
				return fmt.Errorf("bacio sync export: not supported in remote mode (operates on the local DB only)")
			}
			s, err := openStore()
			if err != nil {
				return err
			}
			defer s.Close()

			eng := &sync.Engine{
				Store:  s,
				Actor:  actor(),
				DryRun: opts.dryRun,
			}
			res, err := eng.Export(context.Background(), args[0])
			if err != nil {
				return err
			}
			out := exportResult{ExportResult: res}
			if opts.dryRun {
				return emitDryRun(out)
			}
			return emit(out)
		},
	}
	return cmd
}

// exportResult wraps *sync.ExportResult so we can register a text
// renderer in renderText without leaking sync types into the output
// package's switch. JSON output flows through ExportResult's own
// fields untouched (the embedded pointer's tags do the work).
type exportResult struct {
	*sync.ExportResult
}

// newSyncImportCmd is the hidden Phase-3 inbound counterpart to
// `bacio sync export`. Useful for diagnosing sync issues without going
// through the full `bacio sync` pipeline.
func newSyncImportCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:    "import <path>",
		Short:  "Import a sync-repo folder into the local DB (diagnostic)",
		Args:   cobra.ExactArgs(1),
		Hidden: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			if inRemoteMode() {
				return fmt.Errorf("bacio sync import: not supported in remote mode (operates on the local DB only)")
			}
			s, err := openStore()
			if err != nil {
				return err
			}
			defer s.Close()

			eng := &sync.Engine{
				Store:  s,
				Actor:  actor(),
				DryRun: opts.dryRun,
			}
			res, err := eng.Import(context.Background(), args[0])
			if err != nil {
				return err
			}
			recordSyncImportOps(s, res)
			out := importResult{ImportResult: res}
			if opts.dryRun {
				return emitDryRun(out)
			}
			return emit(out)
		},
	}
	return cmd
}

// importResult mirrors exportResult — a thin wrapper so renderText
// can dispatch on the type without leaking sync.* into the output
// package.
type importResult struct {
	*sync.ImportResult
}

// recordSyncImportOps writes audit-log rows for the side effects of
// an import: one sync.import per invocation, plus per-renumber,
// per-rename, and per-deletion entries. Failures don't fail the
// command (recordOp logs to stderr).
func recordSyncImportOps(s *store.Store, res *sync.ImportResult) {
	if res == nil {
		return
	}
	if opts.dryRun {
		return // dry-run leaves no audit trail; mirrors the rest of bacio
	}
	details := fmt.Sprintf("repos=%d issues=%d features=%d documents=%d comments=%d inserted=%d updated=%d noop=%d",
		res.Repos, res.Issues, res.Features, res.Documents, res.Comments,
		res.Inserted, res.Updated, res.NoOp)
	recordOp(s, model.HistoryEntry{
		Op:      "sync.import",
		Kind:    "repo",
		Details: details,
	})
	for _, r := range res.Renumbered {
		recordOp(s, model.HistoryEntry{
			Op:          "sync.renumber",
			Kind:        "issue",
			TargetLabel: fmt.Sprintf("%s-%d", r.Prefix, r.NewNumber),
			Details:     fmt.Sprintf("from %s-%d (uuid=%s)", r.Prefix, r.OldNumber, r.UUID),
		})
	}
	for _, r := range res.Renamed {
		recordOp(s, model.HistoryEntry{
			Op:          "sync.rename",
			Kind:        r.Kind,
			TargetLabel: r.New,
			Details:     fmt.Sprintf("from %s (uuid=%s)", r.Old, r.UUID),
		})
	}
	for _, d := range res.Deleted {
		recordOp(s, model.HistoryEntry{
			Op:          "sync.delete",
			Kind:        d.Kind,
			TargetLabel: d.Label,
			Details:     fmt.Sprintf("uuid=%s", d.UUID),
		})
	}
}
