package sync

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/mrgeoffrich/bacio/internal/store"
)

// scanWorkingTree walks `source/repos/<prefix>` and parses every
// record into memory. Phase 1 of the design doc — filesystem-only,
// no DB writes.
func (e *Engine) scanWorkingTree(ctx context.Context, source string) (*scanResult, error) {
	scan := &scanResult{
		repos: make(map[string]*scannedRepo),
		seenUUIDs: map[string]map[string]struct{}{
			store.SyncKindIssue:          {},
			store.SyncKindFeature:        {},
			store.SyncKindDocument:       {},
			store.SyncKindComment:        {},
			store.SyncKindFeatureComment: {},
			store.SyncKindRepo:           {},
			store.SyncKindDocFolder:      {},
			store.SyncKindKanbanColumn:   {},
		},
	}
	reposRoot := filepath.Join(source, "repos")
	entries, err := os.ReadDir(reposRoot)
	if err != nil {
		if os.IsNotExist(err) {
			// No repos folder — empty scan, not an error. The user
			// might be importing into a fresh sync repo with no
			// data yet.
			return scan, nil
		}
		return nil, fmt.Errorf("read repos dir: %w", err)
	}
	for _, entry := range entries {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if !entry.IsDir() {
			continue
		}
		prefix := entry.Name()
		sr, err := e.scanRepoFolder(source, prefix, scan)
		if err != nil {
			return nil, fmt.Errorf("scan repo %s: %w", prefix, err)
		}
		if sr != nil {
			scan.repos[prefix] = sr
		}
	}
	return scan, nil
}

func (e *Engine) scanRepoFolder(source, prefix string, scan *scanResult) (*scannedRepo, error) {
	folder := RepoFolder(prefix)
	repoYAMLPath := filepath.Join(source, filepath.FromSlash(RepoYAMLFile(prefix)))
	repoBytes, err := os.ReadFile(repoYAMLPath)
	if err != nil {
		if os.IsNotExist(err) {
			// A directory under repos/ without a repo.yaml is
			// suspicious — could be a stray folder. Skip rather
			// than error out, since import is best-effort.
			return nil, nil
		}
		return nil, fmt.Errorf("read repo.yaml: %w", err)
	}
	parsedRepo, err := ParseRepoYAML(repoBytes)
	if err != nil {
		return nil, err
	}
	scan.seenUUIDs[store.SyncKindRepo][parsedRepo.UUID] = struct{}{}

	redirects, err := LoadRedirects(source, prefix)
	if err != nil {
		return nil, fmt.Errorf("load redirects: %w", err)
	}

	sr := &scannedRepo{
		Prefix:        parsedRepo.Prefix,
		Parsed:        parsedRepo,
		Folder:        folder,
		Redirects:     redirects,
		Features:      make(map[string]*scannedFeature),
		Issues:        make(map[string]*scannedIssue),
		Documents:     make(map[string]*scannedDocument),
		DocFolders:    make(map[string]*scannedDocFolder),
		KanbanColumns: make(map[string]*scannedKanbanColumn),
	}

	// Workspace sentinel. Presence marks the prefix as a workspace; a
	// missing file is the overwhelmingly common case (every git repo)
	// and is not an error.
	workspace, err := scanWorkspaceSentinel(source, prefix)
	if err != nil {
		return nil, err
	}
	sr.Workspace = workspace

	// Features.
	if err := e.scanFeatures(source, prefix, sr, scan); err != nil {
		return nil, err
	}
	// Issues (and their comments).
	if err := e.scanIssues(source, prefix, sr, scan); err != nil {
		return nil, err
	}
	// Documents.
	if err := e.scanDocuments(source, prefix, sr, scan); err != nil {
		return nil, err
	}
	// Container records (doc folders, kanban lanes). Sibling subdirs of
	// features/ issues/ docs/ — invisible to an older binary.
	if err := e.scanDocFolders(source, prefix, sr, scan); err != nil {
		return nil, err
	}
	if err := e.scanKanbanColumns(source, prefix, sr, scan); err != nil {
		return nil, err
	}
	return sr, nil
}

// scanWorkspaceSentinel reads repos/<prefix>/workspace.yaml if present.
// Returns (nil, nil) when the file is absent — that is the normal shape
// for a git-backed prefix, not an error.
func scanWorkspaceSentinel(source, prefix string) (*ParsedWorkspace, error) {
	b, err := os.ReadFile(filepath.Join(source, filepath.FromSlash(WorkspaceYAMLFile(prefix))))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("read workspace.yaml: %w", err)
	}
	parsed, err := ParseWorkspaceYAML(b)
	if err != nil {
		return nil, err
	}
	return parsed, nil
}

func (e *Engine) scanFeatures(source, prefix string, sr *scannedRepo, scan *scanResult) error {
	dir := filepath.Join(source, "repos", prefix, "features")
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("read features dir: %w", err)
	}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		slug := entry.Name()
		folder := FeatureFolder(prefix, slug)
		yamlBytes, err := os.ReadFile(filepath.Join(source, filepath.FromSlash(FeatureYAMLFile(folder))))
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return fmt.Errorf("read feature %s: %w", slug, err)
		}
		parsed, err := ParseFeatureYAML(yamlBytes)
		if err != nil {
			return fmt.Errorf("parse feature %s: %w", slug, err)
		}
		body, err := readBody(filepath.Join(source, filepath.FromSlash(FeatureDescriptionFile(folder))))
		if err != nil {
			return err
		}
		sf := &scannedFeature{
			Parsed:      parsed,
			Folder:      folder,
			Description: string(body),
			BodyHash:    ContentHash(body),
		}
		// BACI-124 feature comments. Mirrors the issue-comment scan path
		// — same YAML / MD pair, rooted at <featureFolder>/comments/.
		commentsDir := filepath.Join(source, filepath.FromSlash(folder), "comments")
		commentEntries, err := os.ReadDir(commentsDir)
		if err == nil {
			for _, ce := range commentEntries {
				if ce.IsDir() {
					continue
				}
				name := ce.Name()
				if !strings.HasSuffix(name, ".yaml") {
					continue
				}
				yamlPath := filepath.Join(commentsDir, name)
				mdPath := strings.TrimSuffix(yamlPath, ".yaml") + ".md"
				cBytes, err := os.ReadFile(yamlPath)
				if err != nil {
					return fmt.Errorf("read feature comment %s: %w", name, err)
				}
				cParsed, err := ParseCommentYAML(cBytes)
				if err != nil {
					return fmt.Errorf("parse feature comment %s: %w", name, err)
				}
				cBody, err := readBody(mdPath)
				if err != nil {
					return err
				}
				sf.Comments = append(sf.Comments, &scannedFeatureComment{
					Parsed:   cParsed,
					YAMLPath: yamlPath,
					MDPath:   mdPath,
					Body:     string(cBody),
					BodyHash: ContentHash(cBody),
				})
				scan.seenUUIDs[store.SyncKindFeatureComment][cParsed.UUID] = struct{}{}
			}
		} else if !os.IsNotExist(err) {
			return fmt.Errorf("read feature comments dir: %w", err)
		}
		sr.Features[parsed.UUID] = sf
		scan.seenUUIDs[store.SyncKindFeature][parsed.UUID] = struct{}{}
	}
	return nil
}

func (e *Engine) scanIssues(source, prefix string, sr *scannedRepo, scan *scanResult) error {
	dir := filepath.Join(source, "repos", prefix, "issues")
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("read issues dir: %w", err)
	}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		label := entry.Name()
		// Issue folder names look like MINI-7. Number is parsed
		// from issue.yaml (the YAML is authoritative); the folder
		// name is just an aid.
		_ = label
		var folder = "repos/" + prefix + "/issues/" + label
		yamlBytes, err := os.ReadFile(filepath.Join(source, filepath.FromSlash(IssueYAMLFile(folder))))
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return fmt.Errorf("read issue %s: %w", label, err)
		}
		parsed, err := ParseIssueYAML(yamlBytes)
		if err != nil {
			return fmt.Errorf("parse issue %s: %w", label, err)
		}
		body, err := readBody(filepath.Join(source, filepath.FromSlash(IssueDescriptionFile(folder))))
		if err != nil {
			return err
		}
		si := &scannedIssue{
			Parsed:      parsed,
			Folder:      folder,
			Description: string(body),
			BodyHash:    ContentHash(body),
		}
		// Comments.
		commentsDir := filepath.Join(source, filepath.FromSlash(folder), "comments")
		commentEntries, err := os.ReadDir(commentsDir)
		if err == nil {
			for _, ce := range commentEntries {
				if ce.IsDir() {
					continue
				}
				name := ce.Name()
				if !strings.HasSuffix(name, ".yaml") {
					continue
				}
				yamlPath := filepath.Join(commentsDir, name)
				mdPath := strings.TrimSuffix(yamlPath, ".yaml") + ".md"
				cBytes, err := os.ReadFile(yamlPath)
				if err != nil {
					return fmt.Errorf("read comment %s: %w", name, err)
				}
				cParsed, err := ParseCommentYAML(cBytes)
				if err != nil {
					return fmt.Errorf("parse comment %s: %w", name, err)
				}
				cBody, err := readBody(mdPath)
				if err != nil {
					return err
				}
				si.Comments = append(si.Comments, &scannedComment{
					Parsed:   cParsed,
					YAMLPath: yamlPath,
					MDPath:   mdPath,
					Body:     string(cBody),
					BodyHash: ContentHash(cBody),
				})
				scan.seenUUIDs[store.SyncKindComment][cParsed.UUID] = struct{}{}
			}
		} else if !os.IsNotExist(err) {
			return fmt.Errorf("read comments dir: %w", err)
		}
		sr.Issues[parsed.UUID] = si
		scan.seenUUIDs[store.SyncKindIssue][parsed.UUID] = struct{}{}
	}
	return nil
}

func (e *Engine) scanDocuments(source, prefix string, sr *scannedRepo, scan *scanResult) error {
	dir := filepath.Join(source, "repos", prefix, "docs")
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("read docs dir: %w", err)
	}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		filename := entry.Name()
		folder := DocumentFolder(prefix, filename)
		yamlBytes, err := os.ReadFile(filepath.Join(source, filepath.FromSlash(DocumentYAMLFile(folder))))
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return fmt.Errorf("read doc %s: %w", filename, err)
		}
		parsed, err := ParseDocumentYAML(yamlBytes)
		if err != nil {
			return fmt.Errorf("parse doc %s: %w", filename, err)
		}
		body, err := readBody(resolveDocBody(source, folder, filename))
		if err != nil {
			return err
		}
		sr.Documents[parsed.UUID] = &scannedDocument{
			Parsed:      parsed,
			Folder:      folder,
			Content:     string(body),
			ContentHash: ContentHash(body),
		}
		scan.seenUUIDs[store.SyncKindDocument][parsed.UUID] = struct{}{}
	}
	return nil
}

// scanDocFolders walks repos/<prefix>/folders/<uuid>/folder.yaml.
// Mirrors scanDocuments: a missing subdir is fine (every pre-pivot sync
// repo), a directory without its manifest is skipped, and a manifest
// that fails to parse aborts the import loudly.
//
// The folder segment is the record's uuid, but the YAML is
// authoritative — a hand-renamed directory still imports under the uuid
// its manifest declares, and the next export writes it back at the
// canonical path.
func (e *Engine) scanDocFolders(source, prefix string, sr *scannedRepo, scan *scanResult) error {
	dir := filepath.Join(source, "repos", prefix, DocFoldersSubdir)
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("read folders dir: %w", err)
	}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		folder := DocFolderFolder(prefix, entry.Name())
		yamlBytes, err := os.ReadFile(filepath.Join(source, filepath.FromSlash(DocFolderYAMLFile(folder))))
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return fmt.Errorf("read doc folder %s: %w", entry.Name(), err)
		}
		parsed, err := ParseDocFolderYAML(yamlBytes)
		if err != nil {
			return fmt.Errorf("parse doc folder %s: %w", entry.Name(), err)
		}
		if parsed.UUID == "" {
			return fmt.Errorf("doc folder %s: folder.yaml has no uuid", entry.Name())
		}
		sr.DocFolders[parsed.UUID] = &scannedDocFolder{Parsed: parsed, Folder: folder}
		scan.seenUUIDs[store.SyncKindDocFolder][parsed.UUID] = struct{}{}
	}
	return nil
}

// scanKanbanColumns walks repos/<prefix>/kanban/<uuid>/column.yaml.
// Mirrors scanDocFolders exactly.
func (e *Engine) scanKanbanColumns(source, prefix string, sr *scannedRepo, scan *scanResult) error {
	dir := filepath.Join(source, "repos", prefix, KanbanColumnsSubdir)
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("read kanban dir: %w", err)
	}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		folder := KanbanColumnFolder(prefix, entry.Name())
		yamlBytes, err := os.ReadFile(filepath.Join(source, filepath.FromSlash(KanbanColumnYAMLFile(folder))))
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return fmt.Errorf("read kanban column %s: %w", entry.Name(), err)
		}
		parsed, err := ParseKanbanColumnYAML(yamlBytes)
		if err != nil {
			return fmt.Errorf("parse kanban column %s: %w", entry.Name(), err)
		}
		if parsed.UUID == "" {
			return fmt.Errorf("kanban column %s: column.yaml has no uuid", entry.Name())
		}
		sr.KanbanColumns[parsed.UUID] = &scannedKanbanColumn{Parsed: parsed, Folder: folder}
		scan.seenUUIDs[store.SyncKindKanbanColumn][parsed.UUID] = struct{}{}
	}
	return nil
}

// resolveDocBody returns the absolute on-disk path of a document body:
// content<ext> derived from the document's filename (BACI-102), falling
// back to the legacy content.md when the extension-derived file is
// absent — documents synced before BACI-102 always wrote content.md
// regardless of the real extension. When both are absent the primary
// (extension-derived) path is returned and readBody yields an empty
// body. The fallback keeps pre-BACI-102 non-markdown documents readable
// without a migration.
func resolveDocBody(syncRepoRoot, folder, filename string) string {
	primary := filepath.Join(syncRepoRoot, filepath.FromSlash(DocumentContentFile(folder, filename)))
	if _, err := os.Stat(primary); err == nil {
		return primary
	}
	legacy := filepath.Join(syncRepoRoot, filepath.FromSlash(LegacyDocumentContentFile(folder)))
	if _, err := os.Stat(legacy); err == nil {
		return legacy
	}
	return primary
}

// readBody reads a body sibling and normalises line endings to LF. A
// missing file is fine — empty body. Caller hashes the result.
func readBody(path string) ([]byte, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return []byte{}, nil
		}
		return nil, fmt.Errorf("read body: %w", err)
	}
	return NormalizeBody(b), nil
}
