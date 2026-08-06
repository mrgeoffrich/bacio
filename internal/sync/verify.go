package sync

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Verify is the diagnostic counterpart to Import — it walks a sync
// repo's working tree and reports structural and integrity issues
// without ever touching a DB. The contract is "no per-record problem
// is fatal": Verify keeps walking, accumulates findings, and only
// returns a Go error when something stops the walk dead (sync repo
// missing, filesystem unreadable). Per-record problems become entries
// in VerifyResult.Errors / Warnings.
//
// Findings are split into two buckets so users see structural issues
// (parse errors, uuid collisions, orphan comments) separately from
// soft problems (dangling cross-references, body-hash drift) — the
// design doc calls these out as distinct severity tiers and the
// `bacio sync verify` text output renders them under separate headings.

// VerifyResult is the structured summary of one Verify pass. The
// kind-counts at the top let users sanity-check that Verify saw what
// they expected ("did it pick up my new repo?"), while Errors /
// Warnings carry the actionable findings.
type VerifyResult struct {
	SyncRepo string `json:"sync_repo"`

	Repos     int `json:"repos"`
	Features  int `json:"features"`
	Issues    int `json:"issues"`
	Documents int `json:"documents"`
	Comments  int `json:"comments"`

	// Errors are structural problems that should fail verify (parse
	// errors, uuid collisions, orphan comment files, repo-uuid
	// inconsistencies, redirect cycles). Non-empty Errors → exit
	// non-zero from the CLI wrapper.
	Errors []VerifyIssue `json:"errors,omitempty"`

	// Warnings are soft problems that don't necessarily indicate a
	// broken sync repo (dangling cross-references, body-hash drift).
	// They surface to the user but don't change exit status.
	Warnings []VerifyIssue `json:"warnings,omitempty"`
}

// VerifyIssue is one finding. Path is sync-repo-relative (slash
// separated, matching the layout doc). Related is optional — used by
// uuid-collision findings to point at the second offending file, by
// dangling-reference findings to identify the missing target uuid,
// and so on.
type VerifyIssue struct {
	Kind    string   `json:"kind"`
	Path    string   `json:"path,omitempty"`
	Detail  string   `json:"detail"`
	Related []string `json:"related,omitempty"`
}

// Verify-issue Kind values. Each value reads as the failing check's
// short name; the renderer groups by Kind for the human-friendly
// summary so users can spot e.g. "all hash mismatches" at once.
const (
	VerifyKindParseError       = "parse_error"
	VerifyKindUUIDCollision    = "uuid_collision"
	VerifyKindDanglingRef      = "dangling_reference"
	VerifyKindCaseCollision    = "case_collision"
	VerifyKindRedirectCycle    = "redirect_cycle"
	VerifyKindRedirectChainCap = "redirect_chain_too_long"
	VerifyKindOrphanComment    = "orphan_comment"
	VerifyKindHashMismatch     = "hash_mismatch"
	VerifyKindRepoInconsistent = "repo_inconsistent"
)

// redirectChainCap caps the number of hops we'll follow before
// declaring a chain "runaway". Real chains in v1 should never exceed
// a handful of entries; 100 is far above any plausible legitimate
// length and small enough that a true cycle gets caught quickly.
const redirectChainCap = 100

// Verify walks `syncRepoRoot` (the working tree of an bacio-sync-yaml
// sync repo) and returns the structured result. Returns a Go error
// only for "couldn't even start" — sync repo missing, filesystem
// unreadable. Per-record problems land in VerifyResult.Errors /
// Warnings and are normal output.
//
// Verify never opens a DB. It operates purely on the YAML +
// description files under repos/, and the redirects.yaml / repo.yaml
// metadata. The DB is the moving target — sync repo content is the
// frozen artifact this command audits.
func (e *Engine) Verify(ctx context.Context, syncRepoRoot string) (*VerifyResult, error) {
	if syncRepoRoot == "" {
		return nil, fmt.Errorf("sync.Verify: syncRepoRoot is empty")
	}
	// Sentinel must be present — Verify is "is this sync repo
	// internally consistent?", not "is this random folder of YAML
	// shaped like a sync repo?". The CLI command wraps an explicit
	// requireSyncRepoMode() check too, but defending the engine
	// independently keeps it usable from tests / future tooling.
	if !IsSyncRepo(syncRepoRoot) {
		return nil, fmt.Errorf("sync.Verify: %s is not a sync repo (no bacio-sync.yaml at root)", syncRepoRoot)
	}

	res := &VerifyResult{SyncRepo: syncRepoRoot}

	// Structural pass: walk every repo folder, parse every YAML,
	// build the in-memory record set we'll cross-check against.
	scan, scanIssues := verifyScan(ctx, syncRepoRoot)
	res.Errors = append(res.Errors, scanIssues...)

	// Roll up counts.
	res.Repos = len(scan.repos)
	for _, sr := range scan.repos {
		res.Features += len(sr.features)
		res.Issues += len(sr.issues)
		res.Documents += len(sr.documents)
		for _, si := range sr.issues {
			res.Comments += len(si.comments)
		}
	}

	// uuid uniqueness across all kinds — collisions across kinds are
	// just as fatal as same-kind collisions because the import path
	// keys sync_state on uuid alone.
	res.Errors = append(res.Errors, checkUUIDUniqueness(scan)...)

	// Cross-reference resolution. Every {label, uuid} pair must
	// resolve by uuid to a record we just scanned.
	res.Warnings = append(res.Warnings, checkCrossReferences(scan)...)

	// Case-insensitive folder collisions. Driven by paths.go's
	// existing helper; one finding per offending group.
	res.Errors = append(res.Errors, checkCaseCollisions(syncRepoRoot, scan)...)

	// Redirects: chain termination + cap.
	res.Errors = append(res.Errors, checkRedirectChains(scan)...)

	// repo.yaml internal consistency: one uuid per prefix; prefix
	// matches folder name.
	res.Errors = append(res.Errors, checkRepoConsistency(scan)...)

	// Comment .yaml/.md sibling pairs.
	res.Errors = append(res.Errors, checkCommentPairs(scan)...)

	// Body hashes match content. Phased separately so users can see
	// "structural problems X, hash drift Y" as two distinct counts.
	res.Warnings = append(res.Warnings, checkBodyHashes(scan)...)

	// Stable ordering keeps the output diff-friendly across runs.
	sortVerifyIssues(res.Errors)
	sortVerifyIssues(res.Warnings)
	return res, nil
}

// verifyScanResult is Verify's in-memory view of the sync repo. It
// mirrors scanResult from import.go but is independently typed so
// Verify isn't coupled to any change that future import phases might
// make to the import-side structure.
//
// duplicateUUIDs accumulates every (kind, uuid, path) tuple where a
// uuid is reused across records. The scanner deduplicates the
// per-uuid maps as it walks (so cross-reference checks have a unique
// resolution target), but this side channel preserves the duplicates
// so checkUUIDUniqueness can surface them as findings.
type verifyScanResult struct {
	repos          map[string]*verifyScannedRepo // by prefix (folder name)
	duplicateUUIDs []duplicateUUID
}

type duplicateUUID struct {
	uuid     string
	kind     string
	path     string
	firstOf  string // path of the first occurrence we recorded
}

type verifyScannedRepo struct {
	prefix     string
	folder     string // sync-repo-relative
	repoYAML   string // sync-repo-relative path
	parsedRepo *ParsedRepo

	redirects     []Redirect
	redirectsPath string

	features  map[string]*verifyScannedFeature  // by uuid
	issues    map[string]*verifyScannedIssue    // by uuid
	documents map[string]*verifyScannedDocument // by uuid
}

type verifyScannedFeature struct {
	parsed   *ParsedFeature
	folder   string
	yamlPath string
	bodyPath string
	bodyHash string
}

type verifyScannedIssue struct {
	parsed   *ParsedIssue
	folder   string
	yamlPath string
	bodyPath string
	bodyHash string
	comments []*verifyScannedComment
}

type verifyScannedComment struct {
	parsed   *ParsedComment
	yamlPath string
	mdPath   string
	bodyHash string
	// orphanKind is set when one of the {.yaml, .md} sides is
	// missing; checkCommentPairs reads it to surface the
	// orphan-comment finding.
	orphanKind string // "missing_yaml" | "missing_md" | ""
}

type verifyScannedDocument struct {
	parsed      *ParsedDocument
	folder      string
	yamlPath    string
	contentPath string
	contentHash string
}

// verifyScan walks `syncRepoRoot` and returns the populated scan plus
// any parse-error findings encountered along the way. Parse failures
// are collected, not propagated — Verify wants to report all bad
// files, not stop on the first one.
func verifyScan(ctx context.Context, syncRepoRoot string) (*verifyScanResult, []VerifyIssue) {
	scan := &verifyScanResult{repos: map[string]*verifyScannedRepo{}}
	var issues []VerifyIssue

	reposRoot := filepath.Join(syncRepoRoot, "repos")
	entries, err := os.ReadDir(reposRoot)
	if err != nil {
		if os.IsNotExist(err) {
			// Empty sync repo — totally fine, nothing to verify.
			return scan, issues
		}
		issues = append(issues, VerifyIssue{
			Kind:   VerifyKindParseError,
			Path:   "repos",
			Detail: fmt.Sprintf("read repos dir: %v", err),
		})
		return scan, issues
	}
	for _, entry := range entries {
		if err := ctx.Err(); err != nil {
			return scan, issues
		}
		if !entry.IsDir() {
			continue
		}
		prefix := entry.Name()
		sr, repoIssues := verifyScanRepo(syncRepoRoot, prefix, scan)
		issues = append(issues, repoIssues...)
		if sr != nil {
			scan.repos[prefix] = sr
		}
	}
	return scan, issues
}

// recordIfDuplicate inspects the {kind, uuid} maps already populated
// inside `sr` and the cross-prefix `scan.duplicateUUIDs` slice; if
// the uuid is already known, append a duplicateUUID record so
// checkUUIDUniqueness can surface it without losing either side.
// Returns true if the caller should skip inserting the record into
// the per-uuid map (we keep the first occurrence as the canonical
// resolution target).
func recordIfDuplicate(scan *verifyScanResult, kind, uuid, path string, exists bool, existingPath string) bool {
	if !exists {
		return false
	}
	scan.duplicateUUIDs = append(scan.duplicateUUIDs, duplicateUUID{
		uuid:    uuid,
		kind:    kind,
		path:    path,
		firstOf: existingPath,
	})
	return true
}

func verifyScanRepo(syncRepoRoot, prefix string, scan *verifyScanResult) (*verifyScannedRepo, []VerifyIssue) {
	var issues []VerifyIssue
	folder := RepoFolder(prefix)
	repoYAML := RepoYAMLFile(prefix)
	repoPath := filepath.Join(syncRepoRoot, filepath.FromSlash(repoYAML))
	repoBytes, err := os.ReadFile(repoPath)
	if err != nil {
		if os.IsNotExist(err) {
			// A folder under repos/ without a repo.yaml — surface as
			// a parse-error so the user knows. Don't try to scan
			// further; we have no prefix to attribute records to.
			issues = append(issues, VerifyIssue{
				Kind:   VerifyKindParseError,
				Path:   repoYAML,
				Detail: "repo.yaml is missing under repos/" + prefix,
			})
			return nil, issues
		}
		issues = append(issues, VerifyIssue{
			Kind:   VerifyKindParseError,
			Path:   repoYAML,
			Detail: fmt.Sprintf("read: %v", err),
		})
		return nil, issues
	}
	parsedRepo, err := ParseRepoYAML(repoBytes)
	if err != nil {
		issues = append(issues, VerifyIssue{
			Kind:   VerifyKindParseError,
			Path:   repoYAML,
			Detail: err.Error(),
		})
		return nil, issues
	}

	sr := &verifyScannedRepo{
		prefix:        prefix,
		folder:        folder,
		repoYAML:      repoYAML,
		parsedRepo:    parsedRepo,
		features:      map[string]*verifyScannedFeature{},
		issues:        map[string]*verifyScannedIssue{},
		documents:     map[string]*verifyScannedDocument{},
		redirectsPath: RedirectsFile(prefix),
	}

	// Redirects (best-effort: parse errors here are findings, not
	// fatal).
	redirectsAbs := filepath.Join(syncRepoRoot, filepath.FromSlash(sr.redirectsPath))
	if rb, err := os.ReadFile(redirectsAbs); err == nil {
		parsed, perr := ParseRedirectsYAML(rb)
		if perr != nil {
			issues = append(issues, VerifyIssue{
				Kind:   VerifyKindParseError,
				Path:   sr.redirectsPath,
				Detail: perr.Error(),
			})
		} else {
			sr.redirects = make([]Redirect, len(parsed))
			for i, p := range parsed {
				sr.redirects[i] = Redirect{
					Kind: p.Kind, Old: p.Old, New: p.New,
					UUID: p.UUID, ChangedAt: p.ChangedAt, Reason: p.Reason,
				}
			}
		}
	} else if !os.IsNotExist(err) {
		issues = append(issues, VerifyIssue{
			Kind:   VerifyKindParseError,
			Path:   sr.redirectsPath,
			Detail: fmt.Sprintf("read: %v", err),
		})
	}

	issues = append(issues, verifyScanFeatures(syncRepoRoot, prefix, sr, scan)...)
	issues = append(issues, verifyScanIssues(syncRepoRoot, prefix, sr, scan)...)
	issues = append(issues, verifyScanDocuments(syncRepoRoot, prefix, sr, scan)...)

	return sr, issues
}

func verifyScanFeatures(syncRepoRoot, prefix string, sr *verifyScannedRepo, scan *verifyScanResult) []VerifyIssue {
	var issues []VerifyIssue
	dir := filepath.Join(syncRepoRoot, "repos", prefix, "features")
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		issues = append(issues, VerifyIssue{
			Kind:   VerifyKindParseError,
			Path:   joinSlash("repos", prefix, "features"),
			Detail: fmt.Sprintf("read features dir: %v", err),
		})
		return issues
	}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		slug := entry.Name()
		folder := FeatureFolder(prefix, slug)
		yamlPath := FeatureYAMLFile(folder)
		bodyPath := FeatureDescriptionFile(folder)
		yamlAbs := filepath.Join(syncRepoRoot, filepath.FromSlash(yamlPath))
		yamlBytes, err := os.ReadFile(yamlAbs)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			issues = append(issues, VerifyIssue{
				Kind: VerifyKindParseError, Path: yamlPath,
				Detail: fmt.Sprintf("read: %v", err),
			})
			continue
		}
		parsed, err := ParseFeatureYAML(yamlBytes)
		if err != nil {
			issues = append(issues, VerifyIssue{
				Kind: VerifyKindParseError, Path: yamlPath,
				Detail: err.Error(),
			})
			continue
		}
		bodyAbs := filepath.Join(syncRepoRoot, filepath.FromSlash(bodyPath))
		bodyHash := hashBodyOrEmpty(bodyAbs)
		if existing, ok := sr.features[parsed.UUID]; ok {
			recordIfDuplicate(scan, "feature", parsed.UUID, yamlPath, true, existing.yamlPath)
			continue
		}
		sr.features[parsed.UUID] = &verifyScannedFeature{
			parsed:   parsed,
			folder:   folder,
			yamlPath: yamlPath,
			bodyPath: bodyPath,
			bodyHash: bodyHash,
		}
	}
	return issues
}

func verifyScanIssues(syncRepoRoot, prefix string, sr *verifyScannedRepo, scan *verifyScanResult) []VerifyIssue {
	var issues []VerifyIssue
	dir := filepath.Join(syncRepoRoot, "repos", prefix, "issues")
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		issues = append(issues, VerifyIssue{
			Kind:   VerifyKindParseError,
			Path:   joinSlash("repos", prefix, "issues"),
			Detail: fmt.Sprintf("read issues dir: %v", err),
		})
		return issues
	}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		label := entry.Name()
		folder := joinSlash("repos", prefix, "issues", label)
		yamlPath := IssueYAMLFile(folder)
		bodyPath := IssueDescriptionFile(folder)
		yamlAbs := filepath.Join(syncRepoRoot, filepath.FromSlash(yamlPath))
		yamlBytes, err := os.ReadFile(yamlAbs)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			issues = append(issues, VerifyIssue{
				Kind: VerifyKindParseError, Path: yamlPath,
				Detail: fmt.Sprintf("read: %v", err),
			})
			continue
		}
		parsed, err := ParseIssueYAML(yamlBytes)
		if err != nil {
			issues = append(issues, VerifyIssue{
				Kind: VerifyKindParseError, Path: yamlPath,
				Detail: err.Error(),
			})
			continue
		}
		bodyAbs := filepath.Join(syncRepoRoot, filepath.FromSlash(bodyPath))
		bodyHash := hashBodyOrEmpty(bodyAbs)
		si := &verifyScannedIssue{
			parsed:   parsed,
			folder:   folder,
			yamlPath: yamlPath,
			bodyPath: bodyPath,
			bodyHash: bodyHash,
		}
		// Comments. Pair .yaml ↔ .md by basename; emit orphan
		// findings for either-side-only entries.
		commentsDir := filepath.Join(syncRepoRoot, filepath.FromSlash(folder), "comments")
		commentIssues := verifyScanComments(commentsDir, folder, si, scan)
		issues = append(issues, commentIssues...)
		if existing, ok := sr.issues[parsed.UUID]; ok {
			recordIfDuplicate(scan, "issue", parsed.UUID, yamlPath, true, existing.yamlPath)
			continue
		}
		sr.issues[parsed.UUID] = si
	}
	return issues
}

func verifyScanComments(commentsDir, issueFolder string, si *verifyScannedIssue, scan *verifyScanResult) []VerifyIssue {
	var issues []VerifyIssue
	entries, err := os.ReadDir(commentsDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		issues = append(issues, VerifyIssue{
			Kind:   VerifyKindParseError,
			Path:   issueFolder + "/comments",
			Detail: fmt.Sprintf("read comments dir: %v", err),
		})
		return issues
	}
	// Index by basename (sans extension) so the .yaml/.md siblings
	// can be paired up.
	type pair struct {
		yaml string
		md   string
	}
	pairs := map[string]*pair{}
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		if strings.HasSuffix(name, ".yaml") {
			base := strings.TrimSuffix(name, ".yaml")
			p := pairs[base]
			if p == nil {
				p = &pair{}
				pairs[base] = p
			}
			p.yaml = name
		} else if strings.HasSuffix(name, ".md") {
			base := strings.TrimSuffix(name, ".md")
			p := pairs[base]
			if p == nil {
				p = &pair{}
				pairs[base] = p
			}
			p.md = name
		}
		// Other extensions are tolerated silently — verify is
		// strict but not paranoid; if a user dropped a README in
		// there, leave it alone.
	}
	bases := make([]string, 0, len(pairs))
	for b := range pairs {
		bases = append(bases, b)
	}
	sort.Strings(bases)
	for _, base := range bases {
		p := pairs[base]
		yamlPath := issueFolder + "/comments/" + base + ".yaml"
		mdPath := issueFolder + "/comments/" + base + ".md"
		c := &verifyScannedComment{
			yamlPath: yamlPath,
			mdPath:   mdPath,
		}
		switch {
		case p.yaml == "" && p.md != "":
			c.orphanKind = "missing_yaml"
			si.comments = append(si.comments, c)
			continue
		case p.yaml != "" && p.md == "":
			c.orphanKind = "missing_md"
			si.comments = append(si.comments, c)
			continue
		}
		yamlAbs := filepath.Join(commentsDir, p.yaml)
		yamlBytes, err := os.ReadFile(yamlAbs)
		if err != nil {
			issues = append(issues, VerifyIssue{
				Kind: VerifyKindParseError, Path: yamlPath,
				Detail: fmt.Sprintf("read: %v", err),
			})
			continue
		}
		parsed, err := ParseCommentYAML(yamlBytes)
		if err != nil {
			issues = append(issues, VerifyIssue{
				Kind: VerifyKindParseError, Path: yamlPath,
				Detail: err.Error(),
			})
			continue
		}
		c.parsed = parsed
		c.bodyHash = hashBodyOrEmpty(filepath.Join(commentsDir, p.md))
		si.comments = append(si.comments, c)
	}
	return issues
}

func verifyScanDocuments(syncRepoRoot, prefix string, sr *verifyScannedRepo, scan *verifyScanResult) []VerifyIssue {
	var issues []VerifyIssue
	dir := filepath.Join(syncRepoRoot, "repos", prefix, "docs")
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		issues = append(issues, VerifyIssue{
			Kind:   VerifyKindParseError,
			Path:   joinSlash("repos", prefix, "docs"),
			Detail: fmt.Sprintf("read docs dir: %v", err),
		})
		return issues
	}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		filename := entry.Name()
		folder := DocumentFolder(prefix, filename)
		yamlPath := DocumentYAMLFile(folder)
		contentPath := DocumentContentFile(folder, filename)
		yamlAbs := filepath.Join(syncRepoRoot, filepath.FromSlash(yamlPath))
		yamlBytes, err := os.ReadFile(yamlAbs)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			issues = append(issues, VerifyIssue{
				Kind: VerifyKindParseError, Path: yamlPath,
				Detail: fmt.Sprintf("read: %v", err),
			})
			continue
		}
		parsed, err := ParseDocumentYAML(yamlBytes)
		if err != nil {
			issues = append(issues, VerifyIssue{
				Kind: VerifyKindParseError, Path: yamlPath,
				Detail: err.Error(),
			})
			continue
		}
		// Hash the resolved body: content<ext> when present, the
		// legacy content.md fallback for documents synced before
		// BACI-102 (resolveDocBody picks whichever exists).
		contentAbs := resolveDocBody(syncRepoRoot, folder, filename)
		contentHash := hashBodyOrEmpty(contentAbs)
		if existing, ok := sr.documents[parsed.UUID]; ok {
			recordIfDuplicate(scan, "document", parsed.UUID, yamlPath, true, existing.yamlPath)
			continue
		}
		sr.documents[parsed.UUID] = &verifyScannedDocument{
			parsed:      parsed,
			folder:      folder,
			yamlPath:    yamlPath,
			contentPath: contentPath,
			contentHash: contentHash,
		}
	}
	return issues
}

// hashBodyOrEmpty reads a body file, normalises line endings, and
// returns the canonical content hash. A missing file hashes to the
// canonical hash of the empty body — matches what Phase 3's
// scanWorkingTree does, so the body-hash check below stays consistent
// with what Import sees.
func hashBodyOrEmpty(absPath string) string {
	b, err := os.ReadFile(absPath)
	if err != nil {
		if os.IsNotExist(err) {
			return ContentHash([]byte{})
		}
		// Unreadable but present — return empty so the hash check
		// later flags it as drift; the parse-error pass already
		// reports the read failure.
		return ""
	}
	return ContentHash(NormalizeBody(b))
}

// joinSlash joins slash-separated components for a sync-repo-relative
// path. We can't use filepath.Join because it produces backslashes on
// Windows; the design contract is "all VerifyIssue.Path values are
// slash-separated".
func joinSlash(parts ...string) string {
	return strings.Join(parts, "/")
}

// checkUUIDUniqueness scans every record across every repo and emits
// one finding per uuid collision. Duplicates surface from two
// places:
//
//   1. The scanner's `duplicateUUIDs` side channel — records that
//      reused a uuid within the same kind in the same prefix. The
//      per-uuid map deduplicated, but the side channel preserved
//      both paths so we can report them.
//   2. A cross-prefix / cross-kind walk — the per-uuid maps live
//      under their owning prefix, so a uuid that appears under two
//      different prefixes (or once as an issue and once as a doc)
//      doesn't show up in #1.
//
// The first occurrence's path becomes Path; the second's becomes the
// single Related entry, so the user can grep both files at once.
func checkUUIDUniqueness(scan *verifyScanResult) []VerifyIssue {
	type seen struct {
		path string
		kind string
	}
	first := map[string]seen{}
	var out []VerifyIssue
	add := func(uuid, kind, path string) {
		if existing, ok := first[uuid]; ok {
			out = append(out, VerifyIssue{
				Kind:    VerifyKindUUIDCollision,
				Path:    existing.path,
				Detail:  fmt.Sprintf("uuid %s appears in both %s (%s) and %s (%s)", uuid, existing.path, existing.kind, path, kind),
				Related: []string{path},
			})
			return
		}
		first[uuid] = seen{path: path, kind: kind}
	}
	prefixes := mapKeys(scan.repos)
	sort.Strings(prefixes)
	for _, prefix := range prefixes {
		sr := scan.repos[prefix]
		add(sr.parsedRepo.UUID, "repo", sr.repoYAML)
		featUUIDs := mapKeys(sr.features)
		sort.Strings(featUUIDs)
		for _, u := range featUUIDs {
			add(u, "feature", sr.features[u].yamlPath)
		}
		issUUIDs := mapKeys(sr.issues)
		sort.Strings(issUUIDs)
		for _, u := range issUUIDs {
			si := sr.issues[u]
			add(u, "issue", si.yamlPath)
			for _, c := range si.comments {
				if c.parsed == nil {
					continue
				}
				add(c.parsed.UUID, "comment", c.yamlPath)
			}
		}
		docUUIDs := mapKeys(sr.documents)
		sort.Strings(docUUIDs)
		for _, u := range docUUIDs {
			add(u, "document", sr.documents[u].yamlPath)
		}
	}
	for _, dup := range scan.duplicateUUIDs {
		out = append(out, VerifyIssue{
			Kind:    VerifyKindUUIDCollision,
			Path:    dup.firstOf,
			Detail:  fmt.Sprintf("uuid %s appears in both %s and %s (%s)", dup.uuid, dup.firstOf, dup.path, dup.kind),
			Related: []string{dup.path},
		})
	}
	return out
}

// checkCrossReferences walks every cross-reference uuid in every
// record and emits a warning for each one that doesn't resolve to a
// scanned record. The check is global (issues can reference
// cross-repo features/docs in principle); we look up uuids in the
// flat allUUIDs map.
func checkCrossReferences(scan *verifyScanResult) []VerifyIssue {
	all := buildAllUUIDsByKind(scan)
	var out []VerifyIssue
	prefixes := mapKeys(scan.repos)
	sort.Strings(prefixes)
	for _, prefix := range prefixes {
		sr := scan.repos[prefix]
		// Issues: feature ref + relations.
		issUUIDs := mapKeys(sr.issues)
		sort.Strings(issUUIDs)
		for _, u := range issUUIDs {
			si := sr.issues[u]
			if si.parsed.Feature != nil && si.parsed.Feature.UUID != "" {
				if _, ok := all["feature"][si.parsed.Feature.UUID]; !ok {
					out = append(out, VerifyIssue{
						Kind:    VerifyKindDanglingRef,
						Path:    si.yamlPath,
						Detail:  fmt.Sprintf("feature ref %q (uuid=%s) does not resolve to any feature", si.parsed.Feature.Label, si.parsed.Feature.UUID),
						Related: []string{si.parsed.Feature.UUID},
					})
				}
			}
			out = append(out, danglingForRelations(si.yamlPath, "blocks", si.parsed.Relations.Blocks, all["issue"])...)
			out = append(out, danglingForRelations(si.yamlPath, "relates_to", si.parsed.Relations.RelatesTo, all["issue"])...)
			out = append(out, danglingForRelations(si.yamlPath, "duplicate_of", si.parsed.Relations.DuplicateOf, all["issue"])...)
		}
		// Documents: links.
		docUUIDs := mapKeys(sr.documents)
		sort.Strings(docUUIDs)
		for _, u := range docUUIDs {
			sd := sr.documents[u]
			for _, link := range sd.parsed.Links {
				bucket, ok := all[link.Kind]
				if !ok {
					out = append(out, VerifyIssue{
						Kind:    VerifyKindDanglingRef,
						Path:    sd.yamlPath,
						Detail:  fmt.Sprintf("doc link kind %q is not 'issue' or 'feature' (target=%q uuid=%s)", link.Kind, link.TargetLabel, link.TargetUUID),
						Related: []string{link.TargetUUID},
					})
					continue
				}
				if _, found := bucket[link.TargetUUID]; !found {
					out = append(out, VerifyIssue{
						Kind:    VerifyKindDanglingRef,
						Path:    sd.yamlPath,
						Detail:  fmt.Sprintf("doc link to %s %q (uuid=%s) does not resolve", link.Kind, link.TargetLabel, link.TargetUUID),
						Related: []string{link.TargetUUID},
					})
				}
			}
		}
	}
	return out
}

func danglingForRelations(path, kind string, refs []ParsedRef, issues map[string]struct{}) []VerifyIssue {
	var out []VerifyIssue
	for _, r := range refs {
		if _, ok := issues[r.UUID]; ok {
			continue
		}
		out = append(out, VerifyIssue{
			Kind:    VerifyKindDanglingRef,
			Path:    path,
			Detail:  fmt.Sprintf("%s ref %q (uuid=%s) does not resolve to any issue", kind, r.Label, r.UUID),
			Related: []string{r.UUID},
		})
	}
	return out
}

// buildAllUUIDsByKind returns {kind → {uuid → struct{}}} so the
// reference resolver can look up by kind without scanning all
// records.
func buildAllUUIDsByKind(scan *verifyScanResult) map[string]map[string]struct{} {
	out := map[string]map[string]struct{}{
		"feature":  {},
		"issue":    {},
		"document": {},
		"repo":     {},
		"comment":  {},
	}
	for _, sr := range scan.repos {
		out["repo"][sr.parsedRepo.UUID] = struct{}{}
		for u := range sr.features {
			out["feature"][u] = struct{}{}
		}
		for u, si := range sr.issues {
			out["issue"][u] = struct{}{}
			for _, c := range si.comments {
				if c.parsed != nil {
					out["comment"][c.parsed.UUID] = struct{}{}
				}
			}
		}
		for u := range sr.documents {
			out["document"][u] = struct{}{}
		}
	}
	return out
}

// checkCaseCollisions runs paths.go's DetectCaseInsensitiveCollisions
// over every kind of folder under each repo. We don't rely on the
// filesystem's own case-sensitivity (a Linux dev box is case
// sensitive even though macOS / Windows aren't), so the check works
// regardless of where verify runs.
func checkCaseCollisions(syncRepoRoot string, scan *verifyScanResult) []VerifyIssue {
	var out []VerifyIssue
	prefixes := mapKeys(scan.repos)
	sort.Strings(prefixes)
	for _, prefix := range prefixes {
		// Each kind gets its own collision check — features and
		// documents share a parent (repos/<prefix>/) only at the
		// path level, but their case-collision concerns are kind
		// local (no human writes both `feature/foo` and `docs/Foo`
		// expecting them to collide).
		// The two container subdirs join the list even though their
		// folder segments are uuids (which can't case-collide in
		// practice): a hand-created or hand-renamed directory still
		// gets the same scrutiny as every other record kind.
		for _, sub := range []string{"features", "issues", "docs", DocFoldersSubdir, KanbanColumnsSubdir} {
			dir := filepath.Join(syncRepoRoot, "repos", prefix, sub)
			entries, err := os.ReadDir(dir)
			if err != nil {
				continue
			}
			names := make([]string, 0, len(entries))
			for _, e := range entries {
				if e.IsDir() {
					names = append(names, e.Name())
				}
			}
			groups := DetectCaseInsensitiveCollisions(names)
			keys := mapKeys(groups)
			sort.Strings(keys)
			for _, key := range keys {
				members := groups[key]
				sort.Strings(members)
				out = append(out, VerifyIssue{
					Kind:    VerifyKindCaseCollision,
					Path:    joinSlash("repos", prefix, sub),
					Detail:  fmt.Sprintf("%s folders collide case-insensitively: %s", sub, strings.Join(members, ", ")),
					Related: members,
				})
			}
		}
	}
	return out
}

// checkRedirectChains walks each repo's redirects.yaml entries
// kind-by-kind and walks forward from every `old` label to detect
// runaway chains. The cap (redirectChainCap) catches both true cycles
// and degenerate length, attributed to the chain's starting entry.
func checkRedirectChains(scan *verifyScanResult) []VerifyIssue {
	var out []VerifyIssue
	prefixes := mapKeys(scan.repos)
	sort.Strings(prefixes)
	for _, prefix := range prefixes {
		sr := scan.repos[prefix]
		if len(sr.redirects) == 0 {
			continue
		}
		// Group by kind so the chain walks within one kind only —
		// a redirect from `kind=issue old=MINI-7` is unrelated to a
		// `kind=feature old=MINI-7`.
		byKind := map[string][]Redirect{}
		for _, r := range sr.redirects {
			byKind[r.Kind] = append(byKind[r.Kind], r)
		}
		kinds := mapKeys(byKind)
		sort.Strings(kinds)
		for _, kind := range kinds {
			// Walk from each `old` value: if that label is also a
			// `new` for an earlier hop, follow forward; cap on
			// hops; flag cycles via a seen-label set.
			rs := byKind[kind]
			// Index `old` → list of redirects with that old (the
			// resolver uses earliest-by-changed-at, but for cycle
			// detection we only need any forward arc).
			oldIdx := map[string][]int{}
			for i, r := range rs {
				oldIdx[r.Old] = append(oldIdx[r.Old], i)
			}
			starts := mapKeys(oldIdx)
			sort.Strings(starts)
			for _, start := range starts {
				_, hops, cyc := walkChain(rs, oldIdx, start)
				if cyc {
					out = append(out, VerifyIssue{
						Kind:   VerifyKindRedirectCycle,
						Path:   sr.redirectsPath,
						Detail: fmt.Sprintf("redirect chain (kind=%s) starting at %q forms a cycle", kind, start),
					})
					continue
				}
				if hops >= redirectChainCap {
					out = append(out, VerifyIssue{
						Kind:   VerifyKindRedirectChainCap,
						Path:   sr.redirectsPath,
						Detail: fmt.Sprintf("redirect chain (kind=%s) starting at %q exceeded %d hops; possible runaway chain", kind, start, redirectChainCap),
					})
				}
			}
		}
	}
	return out
}

// walkChain walks forward from `start` following any redirect with
// matching `old`. Returns (finalLabel, hopCount, isCycle). On cycle
// the walk stops on the second visit to a label.
func walkChain(rs []Redirect, oldIdx map[string][]int, start string) (string, int, bool) {
	current := start
	seen := map[string]bool{current: true}
	hops := 0
	for hops < redirectChainCap {
		idxs, ok := oldIdx[current]
		if !ok {
			return current, hops, false
		}
		// Pick the earliest by ChangedAt (stable when multiple share
		// a starting label, which happens after manual edits).
		bestIdx := idxs[0]
		for _, i := range idxs {
			if rs[i].ChangedAt.Before(rs[bestIdx].ChangedAt) {
				bestIdx = i
			}
		}
		next := rs[bestIdx].New
		if seen[next] {
			return current, hops, true
		}
		seen[next] = true
		current = next
		hops++
	}
	return current, hops, false
}

// checkRepoConsistency verifies that each repo.yaml's `prefix` matches
// the folder name and that no two repos share a uuid (already covered
// by checkUUIDUniqueness, but the message here is more specific).
// Also surfaces empty/blank uuids as inconsistencies.
func checkRepoConsistency(scan *verifyScanResult) []VerifyIssue {
	var out []VerifyIssue
	prefixes := mapKeys(scan.repos)
	sort.Strings(prefixes)
	for _, prefix := range prefixes {
		sr := scan.repos[prefix]
		if sr.parsedRepo.UUID == "" {
			out = append(out, VerifyIssue{
				Kind:   VerifyKindRepoInconsistent,
				Path:   sr.repoYAML,
				Detail: "repo.yaml has empty uuid",
			})
		}
		if sr.parsedRepo.Prefix != prefix {
			out = append(out, VerifyIssue{
				Kind:   VerifyKindRepoInconsistent,
				Path:   sr.repoYAML,
				Detail: fmt.Sprintf("repo.yaml prefix %q does not match folder name %q", sr.parsedRepo.Prefix, prefix),
			})
		}
	}
	return out
}

// checkCommentPairs surfaces every orphan comment file (a .yaml
// without a sibling .md, or vice versa) flagged during the scan.
func checkCommentPairs(scan *verifyScanResult) []VerifyIssue {
	var out []VerifyIssue
	prefixes := mapKeys(scan.repos)
	sort.Strings(prefixes)
	for _, prefix := range prefixes {
		sr := scan.repos[prefix]
		issUUIDs := mapKeys(sr.issues)
		sort.Strings(issUUIDs)
		for _, u := range issUUIDs {
			si := sr.issues[u]
			for _, c := range si.comments {
				switch c.orphanKind {
				case "missing_yaml":
					out = append(out, VerifyIssue{
						Kind:   VerifyKindOrphanComment,
						Path:   c.mdPath,
						Detail: "comment .md has no sibling .yaml",
					})
				case "missing_md":
					out = append(out, VerifyIssue{
						Kind:   VerifyKindOrphanComment,
						Path:   c.yamlPath,
						Detail: "comment .yaml has no sibling .md",
					})
				}
			}
		}
	}
	return out
}

// checkBodyHashes compares each parsed record's stored hash field
// against the recomputed hash of the on-disk body. Drift is a warning
// (not an error) because the round-trip is still correct in the sense
// that import would re-canonicalise the file on next sync — but the
// drift typically signals a manual edit that didn't refresh the hash,
// which is what the user almost always wants to know about.
func checkBodyHashes(scan *verifyScanResult) []VerifyIssue {
	var out []VerifyIssue
	prefixes := mapKeys(scan.repos)
	sort.Strings(prefixes)
	for _, prefix := range prefixes {
		sr := scan.repos[prefix]
		// Features.
		featUUIDs := mapKeys(sr.features)
		sort.Strings(featUUIDs)
		for _, u := range featUUIDs {
			sf := sr.features[u]
			if sf.parsed.DescriptionHash != "" && sf.parsed.DescriptionHash != sf.bodyHash {
				out = append(out, VerifyIssue{
					Kind:   VerifyKindHashMismatch,
					Path:   sf.yamlPath,
					Detail: fmt.Sprintf("description_hash %s does not match %s for %s", sf.parsed.DescriptionHash, sf.bodyHash, sf.bodyPath),
				})
			}
		}
		// Issues.
		issUUIDs := mapKeys(sr.issues)
		sort.Strings(issUUIDs)
		for _, u := range issUUIDs {
			si := sr.issues[u]
			if si.parsed.DescriptionHash != "" && si.parsed.DescriptionHash != si.bodyHash {
				out = append(out, VerifyIssue{
					Kind:   VerifyKindHashMismatch,
					Path:   si.yamlPath,
					Detail: fmt.Sprintf("description_hash %s does not match %s for %s", si.parsed.DescriptionHash, si.bodyHash, si.bodyPath),
				})
			}
			for _, c := range si.comments {
				if c.parsed == nil {
					continue
				}
				if c.parsed.BodyHash != "" && c.parsed.BodyHash != c.bodyHash {
					out = append(out, VerifyIssue{
						Kind:   VerifyKindHashMismatch,
						Path:   c.yamlPath,
						Detail: fmt.Sprintf("body_hash %s does not match %s for %s", c.parsed.BodyHash, c.bodyHash, c.mdPath),
					})
				}
			}
		}
		// Documents.
		docUUIDs := mapKeys(sr.documents)
		sort.Strings(docUUIDs)
		for _, u := range docUUIDs {
			sd := sr.documents[u]
			if sd.parsed.ContentHash != "" && sd.parsed.ContentHash != sd.contentHash {
				out = append(out, VerifyIssue{
					Kind:   VerifyKindHashMismatch,
					Path:   sd.yamlPath,
					Detail: fmt.Sprintf("content_hash %s does not match %s for %s", sd.parsed.ContentHash, sd.contentHash, sd.contentPath),
				})
			}
		}
	}
	return out
}

func sortVerifyIssues(s []VerifyIssue) {
	sort.Slice(s, func(i, j int) bool {
		if s[i].Kind != s[j].Kind {
			return s[i].Kind < s[j].Kind
		}
		if s[i].Path != s[j].Path {
			return s[i].Path < s[j].Path
		}
		return s[i].Detail < s[j].Detail
	})
}
