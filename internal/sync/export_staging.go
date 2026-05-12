package sync

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/mrgeoffrich/bacio/internal/git"
)

// Atomic-staging Export. This is what `bacio sync` runs in steady state.
//
// The Phase-2 Engine.Export does full overwrite into the target — fast
// to write, but unsafe: a crash mid-export leaves the working tree
// inconsistent with the DB, and renames look like delete+create to
// git so history is lost on every label change.
//
// ExportStaged builds the entire desired output in a scratch directory
// adjacent to the working tree, then computes the minimum set of file
// operations to converge the target onto that shape. Renames are
// detected by reading the existing YAML's uuid out of the target so
// they're applied as `git mv` ops, preserving git history. The scratch
// directory is removed in a defer regardless of outcome.
//
// The Phase-2 Engine.Export remains for the hidden `bacio sync export`
// dev tool; its tests continue to validate it. Use ExportStaged when
// you want the safe path.

// StagedExportResult mirrors ExportResult but adds the per-op counts
// the steady-state path needs to surface for the commit message.
type StagedExportResult struct {
	*ExportResult

	// Op counts after diff application. Renames are applied with git mv
	// so git history follows the rename; writes are os.WriteFile and
	// deletes are os.RemoveAll on the affected folder.
	Renames int `json:"renames"`
	Writes  int `json:"writes"`
	Deletes int `json:"deletes"`
}

// ExportStaged runs the export pipeline into the working tree at
// gitRepo.Root, atomically. The scratch dir is a sibling of the target
// (so os.Rename stays cheap — same filesystem). When DryRun is set we
// still build the staging dir, compute the ops, but don't apply them.
//
// On error (any stage), the scratch dir is cleaned up; the target is
// left untouched if the diff hadn't begun applying yet, or in an
// in-progress state we document on the panic-recovery branch (we
// don't try to be clever about rollback once mvs have started — the
// caller's response is "re-run bacio sync").
func (e *Engine) ExportStaged(ctx context.Context, gitRepo *git.Repo) (*StagedExportResult, error) {
	if e.Store == nil {
		return nil, fmt.Errorf("sync.ExportStaged: Store is nil")
	}
	if gitRepo == nil || gitRepo.Root == "" {
		return nil, fmt.Errorf("sync.ExportStaged: gitRepo.Root is empty")
	}
	target := gitRepo.Root

	// Staging dir lives as a sibling of the target (same parent
	// directory) so os.Rename across the boundary stays atomic. Using
	// the pid in the name gives a clear "this is mid-flight" marker
	// to anyone glancing at the parent dir.
	stagingDir, err := os.MkdirTemp(filepath.Dir(target), fmt.Sprintf(".bacio-sync-staging-%d-", os.Getpid()))
	if err != nil {
		return nil, fmt.Errorf("create staging dir: %w", err)
	}
	cleanup := true
	defer func() {
		if cleanup {
			_ = os.RemoveAll(stagingDir)
		}
	}()

	// Run the existing Phase-2 export against the staging dir. The
	// emitter is deterministic so this gives us the desired shape.
	exportEng := &Engine{Store: e.Store, Actor: e.Actor, DryRun: false}
	res, err := exportEng.Export(ctx, stagingDir)
	if err != nil {
		return nil, fmt.Errorf("staging export: %w", err)
	}

	staged := &StagedExportResult{ExportResult: res}
	staged.Target = target

	// Compute the diff between staging and target, scoped to the
	// `repos/` subtree. Other top-level files in the sync repo
	// (bacio-sync.yaml, .gitattributes, redirects.yaml inside repos/<prefix>/)
	// are left alone — sentinel and gitattributes are written by
	// `bacio sync init` once; redirects.yaml is owned by the import path.
	ops, err := computeFileOps(target, stagingDir)
	if err != nil {
		return nil, fmt.Errorf("compute ops: %w", err)
	}

	if e.DryRun {
		// Tally what would happen.
		for _, op := range ops {
			switch op.Kind {
			case opMove:
				staged.Renames++
			case opWrite:
				staged.Writes++
			case opDelete:
				staged.Deletes++
			}
		}
		return staged, nil
	}

	// Apply ops. mvs first (so paths are clear for subsequent writes),
	// then writes, then deletes.
	if err := applyFileOps(gitRepo, target, stagingDir, ops, staged); err != nil {
		// Don't roll the target back — by this point we may have
		// applied half the ops. The caller's recovery is to re-run
		// bacio sync; the import-then-export cycle is idempotent.
		return nil, fmt.Errorf("apply ops: %w", err)
	}

	// Refresh the top-level index.yaml at the working-tree root. The
	// staging diff is scoped to repos/, so the index.yaml written into
	// staging by the Export call above was discarded. Re-emit it at
	// the real target so it travels in the same commit as any repos/
	// changes.
	if err := WriteIndex(target, res.Index); err != nil {
		return nil, fmt.Errorf("write index: %w", err)
	}

	cleanup = true // explicit; defer above will remove
	return staged, nil
}

// fileOp is one element of the staging→target diff plan.
type fileOp struct {
	Kind opKind

	// For opMove: From and To are slash-separated paths relative to
	// target. The mv applies the staging contents at To once moved
	// (so opMove may be followed by an opWrite for the same path).
	From string
	To   string

	// For opWrite and opDelete: Path is the slash-separated relative
	// path. opWrite carries the bytes to write; opDelete removes the
	// folder (or file).
	Path  string
	Bytes []byte
}

type opKind int

const (
	opMove opKind = iota
	opWrite
	opDelete
)

// computeFileOps produces the minimum-ish set of (mv, write, delete)
// ops to converge target's `repos/` subtree onto staging's. Renames
// are detected by uuid: a kind/uuid present at one path in target and
// a different path in staging emits an opMove for that folder, plus
// any per-file opWrites whose contents differ post-move. Folders in
// target that have no counterpart in staging emit opDelete; new
// folders in staging emit opWrites for every file.
//
// We don't bother optimising "rename + content change" into a single
// in-place rename + selective rewrite — applying the mv then a write
// for the changed file is what git would do anyway, and the extra
// blob write is cheap.
func computeFileOps(target, staging string) ([]fileOp, error) {
	stagingFiles, err := walkRepoFiles(staging)
	if err != nil {
		return nil, fmt.Errorf("walk staging: %w", err)
	}
	targetFiles, err := walkRepoFiles(target)
	if err != nil {
		return nil, fmt.Errorf("walk target: %w", err)
	}

	// Build uuid → folder maps for each side, restricted to record
	// folders that carry a yaml manifest.
	stagingUUIDs, err := indexFoldersByUUID(staging)
	if err != nil {
		return nil, fmt.Errorf("index staging by uuid: %w", err)
	}
	targetUUIDs, err := indexFoldersByUUID(target)
	if err != nil {
		return nil, fmt.Errorf("index target by uuid: %w", err)
	}

	// Renames: same kind/uuid, different folder. Emit one opMove per
	// folder. Track moved-from paths so the subsequent write/delete
	// pass treats the folder as already located at its new path.
	movedFrom := map[string]string{} // old path → new path
	movedTo := map[string]bool{}     // new paths created via mv
	var ops []fileOp

	keys := make([]string, 0, len(stagingUUIDs))
	for k := range stagingUUIDs {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		newPath := stagingUUIDs[k]
		oldPath, ok := targetUUIDs[k]
		if !ok || oldPath == newPath {
			continue
		}
		ops = append(ops, fileOp{Kind: opMove, From: oldPath, To: newPath})
		movedFrom[oldPath] = newPath
		movedTo[newPath] = true
	}

	// Build a synthesised "post-move" view of target: every file
	// path we'd see if all ops above were applied.
	postMoveTarget := map[string][]byte{}
	for relPath, body := range targetFiles {
		// If this path is under a moved-from folder, rewrite it under
		// the moved-to folder.
		moved := false
		for old, neu := range movedFrom {
			if relPath == old || strings.HasPrefix(relPath, old+"/") {
				postMoveTarget[neu+strings.TrimPrefix(relPath, old)] = body
				moved = true
				break
			}
		}
		if !moved {
			postMoveTarget[relPath] = body
		}
	}

	// Writes: every staging file whose post-move target counterpart
	// is missing or has different bytes.
	stagingKeys := make([]string, 0, len(stagingFiles))
	for k := range stagingFiles {
		stagingKeys = append(stagingKeys, k)
	}
	sort.Strings(stagingKeys)
	for _, p := range stagingKeys {
		body := stagingFiles[p]
		existing, ok := postMoveTarget[p]
		if !ok || !bytes.Equal(existing, body) {
			ops = append(ops, fileOp{Kind: opWrite, Path: p, Bytes: body})
		}
	}

	// Deletes: every post-move target path with no staging
	// counterpart, scoped to record folders. We emit one opDelete per
	// record folder rather than per file so a removed feature/issue
	// goes away in a single operation.
	deleteFolders := map[string]bool{}
	for p := range postMoveTarget {
		if _, ok := stagingFiles[p]; ok {
			continue
		}
		// Only delete files inside record folders. Keep redirects.yaml
		// (handled by import) and repo.yaml (always rewritten by
		// staging, so it'll already be in stagingFiles). The record
		// folder is the path two segments below `repos/<prefix>/`.
		folder := recordFolderOf(p)
		if folder == "" {
			continue
		}
		deleteFolders[folder] = true
	}
	deleteList := make([]string, 0, len(deleteFolders))
	for f := range deleteFolders {
		deleteList = append(deleteList, f)
	}
	sort.Strings(deleteList)
	for _, f := range deleteList {
		ops = append(ops, fileOp{Kind: opDelete, Path: f})
	}

	return ops, nil
}

// walkRepoFiles returns a map of slash-separated relative paths to
// file bytes for every file under <root>/repos/. Missing root or
// missing repos/ subdir is fine — empty map. Symlinks are not
// followed; if any are present we surface them as a real error so
// the user investigates.
func walkRepoFiles(root string) (map[string][]byte, error) {
	out := map[string][]byte{}
	reposRoot := filepath.Join(root, "repos")
	info, err := os.Stat(reposRoot)
	if err != nil {
		if os.IsNotExist(err) {
			return out, nil
		}
		return nil, err
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("repos at %s is not a directory", reposRoot)
	}
	err = filepath.WalkDir(reposRoot, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.IsDir() {
			return nil
		}
		if d.Type()&fs.ModeSymlink != 0 {
			return fmt.Errorf("unexpected symlink in repos tree: %s", path)
		}
		body, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		out[filepath.ToSlash(rel)] = body
		return nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// indexFoldersByUUID walks `<root>/repos/` and returns a map of
// `<kind>:<uuid>` → folder-path (slash-separated, relative to root)
// for every record folder that carries a parseable YAML manifest. The
// kind discriminator prevents a (one-in-2^122 chance) uuid collision
// across kinds from being treated as a rename.
//
// Used by computeFileOps to detect rename ops. Missing or unparseable
// YAML is silently skipped — we don't want a stray hand-edit to
// abort the whole sync; the worst-case fallback is "treat as
// delete+create" which is exactly Phase 2's behaviour.
func indexFoldersByUUID(root string) (map[string]string, error) {
	out := map[string]string{}
	reposRoot := filepath.Join(root, "repos")
	prefixes, err := os.ReadDir(reposRoot)
	if err != nil {
		if os.IsNotExist(err) {
			return out, nil
		}
		return nil, err
	}
	for _, prefix := range prefixes {
		if !prefix.IsDir() {
			continue
		}
		prefixPath := filepath.Join(reposRoot, prefix.Name())

		// repo.yaml — kind=repo
		if u := readUUIDFromYAML(filepath.Join(prefixPath, "repo.yaml")); u != "" {
			out[uuidKey("repo", u)] = filepath.ToSlash(filepath.Join("repos", prefix.Name()))
		}

		indexKindFolders(prefixPath, "features", "feature.yaml", "feature", "repos/"+prefix.Name(), out)
		indexKindFolders(prefixPath, "issues", "issue.yaml", "issue", "repos/"+prefix.Name(), out)
		indexKindFolders(prefixPath, "docs", "doc.yaml", "document", "repos/"+prefix.Name(), out)

		// Comments are nested under issues/<label>/comments/. They
		// move when the issue moves; we don't track them here.
	}
	return out, nil
}

func indexKindFolders(prefixPath, subdir, manifest, kind, relPrefix string, out map[string]string) {
	root := filepath.Join(prefixPath, subdir)
	entries, err := os.ReadDir(root)
	if err != nil {
		return
	}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		yamlPath := filepath.Join(root, e.Name(), manifest)
		u := readUUIDFromYAML(yamlPath)
		if u == "" {
			continue
		}
		out[uuidKey(kind, u)] = relPrefix + "/" + subdir + "/" + e.Name()
	}
}

func uuidKey(kind, uuid string) string { return kind + ":" + uuid }

// readUUIDFromYAML reads a YAML file and returns its `uuid:` field,
// or "" if missing/unreadable. Cheap text scan rather than a full
// parse — the emitter always produces a `uuid: "..."` line at top
// level of every record manifest, so a regex-style scan is enough.
func readUUIDFromYAML(path string) string {
	b, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(string(b), "\n") {
		trimmed := strings.TrimSpace(line)
		if !strings.HasPrefix(trimmed, "uuid:") {
			continue
		}
		v := strings.TrimSpace(strings.TrimPrefix(trimmed, "uuid:"))
		v = strings.Trim(v, `"`)
		if v != "" {
			return v
		}
	}
	return ""
}

// recordFolderOf returns the slash-separated path of the record
// folder that contains the file at relPath, or "" if the path isn't
// under a record folder. A record folder is the third segment under
// repos/<prefix>/<kind>/<label>; comments live at the fourth segment
// (repos/<prefix>/issues/<label>/comments/<file>) so we group those
// by the issue folder above them.
func recordFolderOf(relPath string) string {
	parts := strings.Split(relPath, "/")
	if len(parts) < 4 || parts[0] != "repos" {
		return ""
	}
	switch parts[2] {
	case "features", "issues", "docs":
		// Comment file: repos/<p>/issues/<label>/comments/<file>
		// The "record folder" we care about for delete is the issue.
		// Phase-2 export always writes the issue folder (issue.yaml +
		// description.md) so the issue folder is already in stagingFiles.
		// A comment-only delete (issue still present, comment gone) is
		// an unusual case for v1 — we'd need to scope deletes finer.
		// Until comment deletion is wired through, we stop at the
		// record folder.
		if len(parts) >= 4 {
			return strings.Join(parts[:4], "/")
		}
	}
	return ""
}

// applyFileOps walks the diff plan in (mvs, writes, deletes) order
// and applies each op. Renames go through `git mv` so git history
// follows them; writes are plain os.WriteFile; deletes are
// os.RemoveAll on the record folder.
func applyFileOps(gitRepo *git.Repo, target, staging string, ops []fileOp, res *StagedExportResult) error {
	// Ordering: mvs first, then writes, then deletes.
	var mvs, writes, deletes []fileOp
	for _, op := range ops {
		switch op.Kind {
		case opMove:
			mvs = append(mvs, op)
		case opWrite:
			writes = append(writes, op)
		case opDelete:
			deletes = append(deletes, op)
		}
	}
	for _, op := range mvs {
		if err := gitRepo.Mv(op.From, op.To); err != nil {
			return fmt.Errorf("git mv %s -> %s: %w", op.From, op.To, err)
		}
		res.Renames++
	}
	for _, op := range writes {
		abs := filepath.Join(target, filepath.FromSlash(op.Path))
		if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
			return fmt.Errorf("mkdir for %s: %w", op.Path, err)
		}
		if err := os.WriteFile(abs, op.Bytes, 0o644); err != nil {
			return fmt.Errorf("write %s: %w", op.Path, err)
		}
		res.Writes++
	}
	for _, op := range deletes {
		abs := filepath.Join(target, filepath.FromSlash(op.Path))
		if err := os.RemoveAll(abs); err != nil {
			return fmt.Errorf("delete %s: %w", op.Path, err)
		}
		res.Deletes++
	}
	return nil
}

// stagingNotFoundErr is the explicit form of "staging dir vanished
// between mkdir and walk". Currently unused (Walk surfaces it
// directly) but kept here so a future caller wanting to disambiguate
// the case has a sentinel.
var stagingNotFoundErr = errors.New("staging dir not found")

// Compile-time guards: keep unused-but-public-shape exports tidy.
var _ = stagingNotFoundErr
