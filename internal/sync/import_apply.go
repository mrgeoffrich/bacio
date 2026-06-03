package sync

import (
	"database/sql"
	"errors"
	"fmt"
	"os"
	"sort"
	"time"

	"github.com/mrgeoffrich/bacio/internal/model"
	"github.com/mrgeoffrich/bacio/internal/store"
)

// applyFeatures applies phase 3 to features.
func (e *Engine) applyFeatures(tx *sql.Tx, sr *scannedRepo, repo *model.Repo, res *ImportResult) error {
	now := time.Now().UTC()
	// Sort by uuid for deterministic processing order — important
	// for byte-stable test runs and reproducible audit logs.
	uuids := mapKeys(sr.Features)
	sort.Strings(uuids)
	for _, uuid := range uuids {
		sf := sr.Features[uuid]
		hash := contentHashFeature(sf)
		// BACI-199: state/state_manual round-trip. nil pointer on the
		// parsed side decodes as the column default `active` / 0; an
		// explicit value in feature.yaml overrides. ParseFeatureState
		// runs at the apply boundary so a malformed value loudly fails
		// the whole import rather than falling through to the schema
		// CHECK and surfacing as a less-readable "constraint failed".
		incomingState := model.FeatureStateActive
		if sf.Parsed.State != nil && *sf.Parsed.State != "" {
			parsed, perr := model.ParseFeatureState(*sf.Parsed.State)
			if perr != nil {
				return fmt.Errorf("feature %s state: %w", sf.Parsed.Slug, perr)
			}
			incomingState = parsed
		}
		incomingStateManual := sf.Parsed.StateManual
		// BACI-333: collect_handoffs defaults ON (1). A nil pointer in
		// feature.yaml (the common case — the key is only emitted when
		// OFF) decodes as ON; an explicit value overrides.
		incomingCollectHandoffs := true
		if sf.Parsed.CollectHandoffs != nil {
			incomingCollectHandoffs = *sf.Parsed.CollectHandoffs
		}
		var existingID int64
		var existingSlug, existingTitle, existingDescription, existingEmoji, existingState string
		var existingStateManual, existingCollectHandoffs int64
		var existingUpdatedAt time.Time
		var existingArchivedAt sql.NullTime
		err := tx.QueryRow(
			`SELECT id, slug, title, description, emoji, state, state_manual, collect_handoffs, updated_at, archived_at FROM features WHERE uuid = ?`,
			uuid,
		).Scan(&existingID, &existingSlug, &existingTitle, &existingDescription, &existingEmoji, &existingState, &existingStateManual, &existingCollectHandoffs, &existingUpdatedAt, &existingArchivedAt)
		if errors.Is(err, sql.ErrNoRows) {
			// Insert. archived_at round-trips per BACI-68; sync is the
			// source of truth across machines so an archived row on one
			// machine becomes archived on the other when first imported.
			// emoji (BACI-172) round-trips the same way — sync owns the
			// glyph across machines. state + state_manual (BACI-199)
			// likewise.
			if _, err := tx.Exec(
				`INSERT INTO features (uuid, repo_id, slug, title, description, emoji, state, state_manual, collect_handoffs, created_at, updated_at, archived_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
				uuid, repo.ID, sf.Parsed.Slug, sf.Parsed.Title, sf.Description, sf.Parsed.Emoji,
				string(incomingState), boolToInt(incomingStateManual), boolToInt(incomingCollectHandoffs),
				sqliteTimestamp(sf.Parsed.CreatedAt), sqliteTimestamp(sf.Parsed.UpdatedAt),
				nullableSqliteTimestamp(sf.Parsed.ArchivedAt),
			); err != nil {
				return fmt.Errorf("insert feature %s: %w", sf.Parsed.Slug, err)
			}
			res.Inserted++
			if err := e.markSyncedTx(tx, uuid, store.SyncKindFeature, hash, now); err != nil {
				return err
			}
			continue
		}
		if err != nil {
			return err
		}
		// Last-writer-wins (BACI-5): if the remote YAML is older than
		// the local row, preserve local and surface a skip event. We
		// leave sync_state untouched so the next import re-evaluates;
		// the export phase on this run writes the newer local out.
		if sf.Parsed.UpdatedAt.Before(existingUpdatedAt) {
			res.Skipped++
			res.SkippedStale = append(res.SkippedStale, SkippedStaleEntry{
				Kind:          "feature",
				UUID:          uuid,
				Label:         existingSlug,
				LocalUpdated:  existingUpdatedAt.UTC().Format(time.RFC3339),
				RemoteUpdated: sf.Parsed.UpdatedAt.UTC().Format(time.RFC3339),
			})
			continue
		}
		// Update if any field differs. archived_at is compared as a
		// nullable timestamp so flipping the flag in either direction
		// triggers a write. state + state_manual (BACI-199) join the
		// same predicate.
		incomingStateManualInt := boolToInt(incomingStateManual)
		incomingCollectHandoffsInt := boolToInt(incomingCollectHandoffs)
		if existingSlug != sf.Parsed.Slug || existingTitle != sf.Parsed.Title || existingDescription != sf.Description ||
			existingEmoji != sf.Parsed.Emoji ||
			existingState != string(incomingState) ||
			existingStateManual != incomingStateManualInt ||
			existingCollectHandoffs != incomingCollectHandoffsInt ||
			!nullableTimeEqual(existingArchivedAt, sf.Parsed.ArchivedAt) {
			if _, err := tx.Exec(
				`UPDATE features SET slug = ?, title = ?, description = ?, emoji = ?, state = ?, state_manual = ?, collect_handoffs = ?, updated_at = ?, archived_at = ? WHERE id = ?`,
				sf.Parsed.Slug, sf.Parsed.Title, sf.Description, sf.Parsed.Emoji,
				string(incomingState), incomingStateManualInt, incomingCollectHandoffsInt,
				sqliteTimestamp(sf.Parsed.UpdatedAt),
				nullableSqliteTimestamp(sf.Parsed.ArchivedAt), existingID,
			); err != nil {
				return fmt.Errorf("update feature %s: %w", sf.Parsed.Slug, err)
			}
			res.Updated++
		} else {
			res.NoOp++
		}
		if err := e.markSyncedTx(tx, uuid, store.SyncKindFeature, hash, now); err != nil {
			return err
		}
	}
	return nil
}

// boolToInt converts a Go bool to the int64 used by SQLite's
// state_manual column (0|1). Local helper to keep the apply paths
// readable.
func boolToInt(b bool) int64 {
	if b {
		return 1
	}
	return 0
}

// applyIssues applies phase 3 to issues in two passes. Pass 1
// inserts/updates the issue rows themselves so every uuid mentioned
// in any other issue's `relations` block is already resolvable in
// the DB. Pass 2 walks the same set and applies the side-data:
// tags, PRs, and relations (the last of which is what required the
// split — a single-pass apply would mark perfectly-fine
// inter-issue links as dangling because the target hadn't been
// inserted yet).
//
// Feature references can stay in pass 1: applyFeatures already ran
// before applyIssues, so feature uuids are guaranteed resolvable.
func (e *Engine) applyIssues(tx *sql.Tx, sr *scannedRepo, repo *model.Repo, res *ImportResult) error {
	now := time.Now().UTC()
	uuids := mapKeys(sr.Issues)
	sort.Strings(uuids)
	// Track which issues were inserted (vs updated) so the
	// next_issue_number bump only fires when the issue is new.
	idByUUID := make(map[string]int64, len(uuids))
	// Track which uuids the LWW gate skipped in pass 1 so pass 2
	// leaves their side-data alone too — otherwise tags/PRs/relations
	// would be reverted while the row body was preserved, which is
	// inconsistent and undoes part of the LWW guarantee.
	skipped := make(map[string]bool, len(uuids))

	// Pass 1: insert/update issue rows.
	for _, uuid := range uuids {
		si := sr.Issues[uuid]
		var featureID *int64
		if si.Parsed.Feature != nil && si.Parsed.Feature.UUID != "" {
			id, err := e.featureIDByUUIDTx(tx, si.Parsed.Feature.UUID)
			if err == nil {
				featureID = &id
			} else if errors.Is(err, store.ErrNotFound) {
				res.Dangling = append(res.Dangling, DanglingRef{
					From:        fmt.Sprintf("%s-%d", repo.Prefix, si.Parsed.Number),
					FromUUID:    uuid,
					Kind:        "feature",
					TargetLabel: si.Parsed.Feature.Label,
					TargetUUID:  si.Parsed.Feature.UUID,
				})
			} else {
				return err
			}
		}

		var (
			existingID             int64
			existingNumber         int64
			existingFeatureID      sql.NullInt64
			existingTitle          string
			existingDescription    string
			existingState          string
			existingAssignee       string
			existingCustomerImpact string
			existingUpdatedAt      time.Time
			existingArchivedAt     sql.NullTime
		)
		err := tx.QueryRow(
			`SELECT id, number, feature_id, title, description, state, assignee, customer_impact, updated_at, archived_at FROM issues WHERE uuid = ?`,
			uuid,
		).Scan(&existingID, &existingNumber, &existingFeatureID, &existingTitle, &existingDescription, &existingState, &existingAssignee, &existingCustomerImpact, &existingUpdatedAt, &existingArchivedAt)
		if errors.Is(err, sql.ErrNoRows) {
			res2, err := tx.Exec(
				`INSERT INTO issues (uuid, repo_id, number, feature_id, title, description, state, assignee, customer_impact, created_at, updated_at, archived_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
				uuid, repo.ID, si.Parsed.Number, nullableInt64(featureID),
				si.Parsed.Title, si.Description, si.Parsed.State, si.Parsed.Assignee, si.Parsed.CustomerImpact,
				sqliteTimestamp(si.Parsed.CreatedAt), sqliteTimestamp(si.Parsed.UpdatedAt),
				nullableSqliteTimestamp(si.Parsed.ArchivedAt),
			)
			if err != nil {
				return fmt.Errorf("insert issue %s: %w", uuid, err)
			}
			id, _ := res2.LastInsertId()
			idByUUID[uuid] = id
			res.Inserted++
		} else if err != nil {
			return err
		} else if si.Parsed.UpdatedAt.Before(existingUpdatedAt) {
			// Last-writer-wins (BACI-5): remote YAML is older than the
			// local row. Preserve local body, tags, PRs, relations; the
			// export phase will write the newer local content out.
			skipped[uuid] = true
			idByUUID[uuid] = existingID
			res.Skipped++
			res.SkippedStale = append(res.SkippedStale, SkippedStaleEntry{
				Kind:          "issue",
				UUID:          uuid,
				Label:         fmt.Sprintf("%s-%d", repo.Prefix, existingNumber),
				LocalUpdated:  existingUpdatedAt.UTC().Format(time.RFC3339),
				RemoteUpdated: si.Parsed.UpdatedAt.UTC().Format(time.RFC3339),
			})
		} else {
			// Skip the UPDATE if every field is already equal — this
			// keeps a re-import of an unchanged sync repo reporting
			// `noop` rather than `updated`. Side-data (tags, PRs,
			// relations) is checked separately in pass 2; we don't
			// gate the update on those because pass 2 deletes-and-
			// reinserts them wholesale and is its own no-op when the
			// sets match.
			changed := existingNumber != si.Parsed.Number ||
				!nullableEqualInt64(existingFeatureID, featureID) ||
				existingTitle != si.Parsed.Title ||
				existingDescription != si.Description ||
				existingState != si.Parsed.State ||
				existingAssignee != si.Parsed.Assignee ||
				existingCustomerImpact != si.Parsed.CustomerImpact ||
				!nullableTimeEqual(existingArchivedAt, si.Parsed.ArchivedAt)
			if changed {
				if _, err := tx.Exec(
					`UPDATE issues SET number = ?, feature_id = ?, title = ?, description = ?, state = ?, assignee = ?, customer_impact = ?, updated_at = ?, archived_at = ? WHERE id = ?`,
					si.Parsed.Number, nullableInt64(featureID),
					si.Parsed.Title, si.Description, si.Parsed.State, si.Parsed.Assignee, si.Parsed.CustomerImpact,
					sqliteTimestamp(si.Parsed.UpdatedAt), nullableSqliteTimestamp(si.Parsed.ArchivedAt), existingID,
				); err != nil {
					return fmt.Errorf("update issue %s: %w", uuid, err)
				}
				res.Updated++
			} else {
				res.NoOp++
			}
			idByUUID[uuid] = existingID
		}
	}

	// Pass 2: side-data (tags, PRs, relations). Every issue uuid
	// referenced by another issue's relations now has a row, so
	// resolveRelationsTx won't trip a false dangling.
	for _, uuid := range uuids {
		if skipped[uuid] {
			// LWW skipped this row in pass 1; leave its side-data
			// intact and don't bump sync_state — the export phase will
			// emit the newer local content and the next round-trip
			// will close the loop.
			continue
		}
		si := sr.Issues[uuid]
		issueID := idByUUID[uuid]
		hash := contentHashIssue(si)
		if err := e.replaceIssueTagsTx(tx, issueID, si.Parsed.Tags); err != nil {
			return fmt.Errorf("replace tags for %s: %w", uuid, err)
		}
		if err := e.replacePRsTx(tx, issueID, si.Parsed.PRs); err != nil {
			return fmt.Errorf("replace prs for %s: %w", uuid, err)
		}
		if err := e.replaceRelationsTx(tx, issueID, uuid, repo.Prefix, si.Parsed.Number, si.Parsed.Relations, res); err != nil {
			return fmt.Errorf("replace relations for %s: %w", uuid, err)
		}
		// BACI-144: pass 2's wipe-and-rewrite of issue_tags /
		// issue_pull_requests / issue_relations fires the schema
		// triggers, which bump issues.updated_at to CURRENT_TIMESTAMP.
		// That overwrites the YAML's UpdatedAt that pass 1 just wrote,
		// breaking LWW for the next round-trip (an imported row would
		// suddenly look "fresh" on the next compare). Re-stamp here so
		// the in-DB value matches the YAML byte-for-byte.
		if _, err := tx.Exec(
			`UPDATE issues SET updated_at = ? WHERE id = ?`,
			sqliteTimestamp(si.Parsed.UpdatedAt), issueID,
		); err != nil {
			return fmt.Errorf("re-stamp updated_at for %s: %w", uuid, err)
		}
		if err := e.markSyncedTx(tx, uuid, store.SyncKindIssue, hash, now); err != nil {
			return err
		}
	}

	// Bump next_issue_number to the max number we just imported, if
	// higher than the cached value. We do this once per repo,
	// outside the per-issue loop, to avoid bumping repos.updated_at
	// on every iteration.
	maxNumber := int64(0)
	for _, uuid := range uuids {
		if n := sr.Issues[uuid].Parsed.Number; n > maxNumber {
			maxNumber = n
		}
	}
	if maxNumber > 0 {
		var current int64
		if err := tx.QueryRow(`SELECT next_issue_number FROM repos WHERE id = ?`, repo.ID).Scan(&current); err != nil {
			return err
		}
		if maxNumber+1 > current {
			if _, err := tx.Exec(
				`UPDATE repos SET next_issue_number = ?, updated_at = ? WHERE id = ?`,
				maxNumber+1, sqliteTimestamp(now), repo.ID,
			); err != nil {
				return err
			}
		}
	}
	return nil
}

func (e *Engine) replaceIssueTagsTx(tx *sql.Tx, issueID int64, tags []string) error {
	if _, err := tx.Exec(`DELETE FROM issue_tags WHERE issue_id = ?`, issueID); err != nil {
		return err
	}
	stmt, err := tx.Prepare(`INSERT OR IGNORE INTO issue_tags (issue_id, tag) VALUES (?, ?)`)
	if err != nil {
		return err
	}
	defer stmt.Close()
	seen := map[string]bool{}
	for _, t := range tags {
		clean, err := store.NormalizeTag(t)
		if err != nil {
			return err
		}
		if seen[clean] {
			continue
		}
		seen[clean] = true
		if _, err := stmt.Exec(issueID, clean); err != nil {
			return err
		}
	}
	return nil
}

func (e *Engine) replacePRsTx(tx *sql.Tx, issueID int64, urls []string) error {
	if _, err := tx.Exec(`DELETE FROM issue_pull_requests WHERE issue_id = ?`, issueID); err != nil {
		return err
	}
	stmt, err := tx.Prepare(`INSERT OR IGNORE INTO issue_pull_requests (issue_id, url) VALUES (?, ?)`)
	if err != nil {
		return err
	}
	defer stmt.Close()
	for _, u := range urls {
		// We don't validate strictly here — the user could have
		// hand-edited the file. Schema cap is the only hard limit,
		// and the existing ValidatePRURLStrict would reject weird
		// URLs we'd rather just round-trip. The trade-off mirrors
		// the design doc's "files are authoritative" rule.
		if _, err := stmt.Exec(issueID, u); err != nil {
			return err
		}
	}
	return nil
}

func (e *Engine) replaceRelationsTx(tx *sql.Tx, fromID int64, fromUUID, fromPrefix string, fromNumber int64, rels ParsedRelations, res *ImportResult) error {
	if _, err := tx.Exec(`DELETE FROM issue_relations WHERE from_issue_id = ?`, fromID); err != nil {
		return err
	}
	stmt, err := tx.Prepare(`INSERT OR IGNORE INTO issue_relations (from_issue_id, to_issue_id, type) VALUES (?, ?, ?)`)
	if err != nil {
		return err
	}
	defer stmt.Close()
	emit := func(refs []ParsedRef, kind model.RelationType) error {
		for _, r := range refs {
			toID, err := e.issueIDByUUIDTx(tx, r.UUID)
			if err != nil {
				if errors.Is(err, store.ErrNotFound) {
					res.Dangling = append(res.Dangling, DanglingRef{
						From:        fmt.Sprintf("%s-%d", fromPrefix, fromNumber),
						FromUUID:    fromUUID,
						Kind:        string(kind),
						TargetLabel: r.Label,
						TargetUUID:  r.UUID,
					})
					continue
				}
				return err
			}
			if toID == fromID {
				// Self-loop — schema CHECK forbids; skip silently.
				continue
			}
			if _, err := stmt.Exec(fromID, toID, string(kind)); err != nil {
				return err
			}
		}
		return nil
	}
	if err := emit(rels.Blocks, model.RelBlocks); err != nil {
		return err
	}
	if err := emit(rels.RelatesTo, model.RelRelatesTo); err != nil {
		return err
	}
	if err := emit(rels.DuplicateOf, model.RelDuplicateOf); err != nil {
		return err
	}
	return nil
}

func (e *Engine) applyComments(tx *sql.Tx, sr *scannedRepo, res *ImportResult) error {
	now := time.Now().UTC()
	for issUUID, si := range sr.Issues {
		issueID, err := e.issueIDByUUIDTx(tx, issUUID)
		if err != nil {
			// Issue insert failed earlier? skip; we already accounted for it.
			continue
		}
		// Sort comments by uuid for stable processing order.
		sort.Slice(si.Comments, func(i, j int) bool {
			return si.Comments[i].Parsed.UUID < si.Comments[j].Parsed.UUID
		})
		for _, sc := range si.Comments {
			hash := contentHashComment(sc)
			// BACI-131: pull existing eval triple alongside body/author
			// so the update branch knows when only the triple changed.
			// dispatch_id is local-only and never imported — it stays
			// NULL until / unless a local write resolves a new context.
			var (
				existingID     int64
				existingAuthor string
				existingBody   string
				existingEval   bool
				existingSess   string
				existingMode   string
			)
			err := tx.QueryRow(
				`SELECT id, author, body, eval, agent_session_id, mode FROM comments WHERE uuid = ?`,
				sc.Parsed.UUID,
			).Scan(&existingID, &existingAuthor, &existingBody, &existingEval, &existingSess, &existingMode)
			evalInt := 0
			if sc.Parsed.Eval {
				evalInt = 1
			}
			if errors.Is(err, sql.ErrNoRows) {
				// BACI-338: resurrection guard. No DB row but a sync_state
				// row ⇒ this comment was hard-deleted locally (a genuinely
				// new comment arriving from a remote pull has no local
				// sync_state). Removing the on-disk pair + sync_state row
				// here propagates the delete instead of re-inserting it.
				if handled, err := e.guardCommentResurrection(tx, sc.Parsed.UUID, sc.YAMLPath, sc.MDPath, store.SyncKindComment, res); err != nil {
					return err
				} else if handled {
					continue
				}
				if _, err := tx.Exec(
					`INSERT INTO comments (uuid, issue_id, author, body, created_at, eval, agent_session_id, mode)
					 VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
					sc.Parsed.UUID, issueID, sc.Parsed.Author, sc.Body,
					sqliteTimestamp(sc.Parsed.CreatedAt),
					evalInt, sc.Parsed.AgentSessionID, sc.Parsed.Mode,
				); err != nil {
					return fmt.Errorf("insert comment %s: %w", sc.Parsed.UUID, err)
				}
				res.Inserted++
			} else if err != nil {
				return err
			} else if existingAuthor != sc.Parsed.Author ||
				existingBody != sc.Body ||
				existingEval != sc.Parsed.Eval ||
				existingSess != sc.Parsed.AgentSessionID ||
				existingMode != sc.Parsed.Mode {
				if _, err := tx.Exec(
					`UPDATE comments
					 SET author = ?, body = ?, eval = ?, agent_session_id = ?, mode = ?
					 WHERE id = ?`,
					sc.Parsed.Author, sc.Body, evalInt,
					sc.Parsed.AgentSessionID, sc.Parsed.Mode, existingID,
				); err != nil {
					return fmt.Errorf("update comment %s: %w", sc.Parsed.UUID, err)
				}
				res.Updated++
			} else {
				res.NoOp++
			}
			if err := e.markSyncedTx(tx, sc.Parsed.UUID, store.SyncKindComment, hash, now); err != nil {
				return err
			}
		}
	}
	return nil
}

// guardCommentResurrection (BACI-338) closes the missing cell in the
// (DB row × sync_state × seen-on-disk) case table for hard-deletable
// comments. It is called from the apply pass's ErrNoRows branch — the
// scanned comment is on disk but has no DB row. If a sync_state row
// exists for the uuid, the comment was deleted locally (a genuinely
// new comment arriving from a remote pull has no local sync_state), so
// instead of re-inserting the orphaned file we remove the on-disk
// .yaml/.md pair and drop the sync_state row. Export emits nothing for
// it and the deletion propagates to the remote on this same run.
//
// Returns (handled, err): handled=true means the caller must skip the
// insert. handled=false (no sync_state row, or a bootstrap flow) means
// the comment is genuinely new and the caller inserts as before.
//
// Gated by !e.SkipPropagateDeletes so bootstrap flows (clone / attach)
// — where sync_state is not a trustworthy "what was last shared"
// snapshot — never trip it, mirroring why propagateDeletes is gated
// the same way.
//
// File removal is not transactional, the import is. On a DryRun we
// touch nothing and only record the projected DeletionEntry; on a real
// run a later rollback would restore the sync_state row but leave the
// files gone — which self-heals, since the next sync sees "sync_state
// present, file absent" and propagateDeletes drops the row.
func (e *Engine) guardCommentResurrection(tx *sql.Tx, uuid, yamlPath, mdPath, kind string, res *ImportResult) (bool, error) {
	if e.SkipPropagateDeletes {
		return false, nil
	}
	var dummy string
	err := tx.QueryRow(`SELECT uuid FROM sync_state WHERE uuid = ?`, uuid).Scan(&dummy)
	if errors.Is(err, sql.ErrNoRows) {
		// No sync_state ⇒ genuinely new ⇒ let the caller insert.
		return false, nil
	}
	if err != nil {
		return false, err
	}
	res.Deleted = append(res.Deleted, DeletionEntry{Kind: kind, UUID: uuid})
	if e.DryRun {
		// Project the delete without touching files or sync_state — the
		// outer transaction rolls back on dry-run anyway.
		return true, nil
	}
	for _, p := range []string{yamlPath, mdPath} {
		if p == "" {
			continue
		}
		if err := os.Remove(p); err != nil && !os.IsNotExist(err) {
			return false, fmt.Errorf("remove orphaned comment file %s: %w", p, err)
		}
	}
	if _, err := tx.Exec(`DELETE FROM sync_state WHERE uuid = ?`, uuid); err != nil {
		return false, fmt.Errorf("drop sync_state for deleted comment %s: %w", uuid, err)
	}
	return true, nil
}

// applyFeatureComments mirrors applyComments but writes feature-scoped
// rows (BACI-124). Same UPSERT-by-uuid semantics; runs after
// applyFeatures so the FK to features.id is resolvable.
func (e *Engine) applyFeatureComments(tx *sql.Tx, sr *scannedRepo, res *ImportResult) error {
	now := time.Now().UTC()
	featUUIDs := mapKeys(sr.Features)
	sort.Strings(featUUIDs)
	for _, featUUID := range featUUIDs {
		sf := sr.Features[featUUID]
		var featureID int64
		if err := tx.QueryRow(`SELECT id FROM features WHERE uuid = ?`, featUUID).Scan(&featureID); err != nil {
			// Feature insert failed earlier? skip; no point applying its
			// comments without a parent row.
			continue
		}
		sort.Slice(sf.Comments, func(i, j int) bool {
			return sf.Comments[i].Parsed.UUID < sf.Comments[j].Parsed.UUID
		})
		for _, sc := range sf.Comments {
			hash := contentHashFeatureComment(sc)
			// BACI-333: kind defaults to 'note' when the on-disk YAML omits
			// the key (the common case — it's emitted only for handoffs).
			incomingKind := sc.Parsed.Kind
			if incomingKind == "" {
				incomingKind = store.FeatureCommentKindNote
			}
			var (
				existingID     int64
				existingAuthor string
				existingBody   string
				existingKind   string
			)
			err := tx.QueryRow(
				`SELECT id, author, body, kind FROM feature_comments WHERE uuid = ?`,
				sc.Parsed.UUID,
			).Scan(&existingID, &existingAuthor, &existingBody, &existingKind)
			if errors.Is(err, sql.ErrNoRows) {
				// BACI-338: resurrection guard, mirroring applyComments —
				// no DB row but a sync_state row ⇒ deleted locally; remove
				// the on-disk pair + sync_state row instead of re-inserting.
				if handled, err := e.guardCommentResurrection(tx, sc.Parsed.UUID, sc.YAMLPath, sc.MDPath, store.SyncKindFeatureComment, res); err != nil {
					return err
				} else if handled {
					continue
				}
				if _, err := tx.Exec(
					`INSERT INTO feature_comments (uuid, feature_id, author, body, kind, created_at) VALUES (?, ?, ?, ?, ?, ?)`,
					sc.Parsed.UUID, featureID, sc.Parsed.Author, sc.Body, incomingKind,
					sqliteTimestamp(sc.Parsed.CreatedAt),
				); err != nil {
					return fmt.Errorf("insert feature comment %s: %w", sc.Parsed.UUID, err)
				}
				res.Inserted++
			} else if err != nil {
				return err
			} else if existingAuthor != sc.Parsed.Author || existingBody != sc.Body || existingKind != incomingKind {
				if _, err := tx.Exec(
					`UPDATE feature_comments SET author = ?, body = ?, kind = ? WHERE id = ?`,
					sc.Parsed.Author, sc.Body, incomingKind, existingID,
				); err != nil {
					return fmt.Errorf("update feature comment %s: %w", sc.Parsed.UUID, err)
				}
				res.Updated++
			} else {
				res.NoOp++
			}
			if err := e.markSyncedTx(tx, sc.Parsed.UUID, store.SyncKindFeatureComment, hash, now); err != nil {
				return err
			}
		}
	}
	return nil
}

func (e *Engine) applyDocuments(tx *sql.Tx, sr *scannedRepo, repo *model.Repo, res *ImportResult) error {
	now := time.Now().UTC()
	uuids := mapKeys(sr.Documents)
	sort.Strings(uuids)
	for _, uuid := range uuids {
		sd := sr.Documents[uuid]
		hash := contentHashDocument(sd)
		var (
			existingID         int64
			existingFilename   string
			existingType       string
			existingContent    string
			existingSourcePath string
			existingUpdatedAt  time.Time
			existingArchivedAt sql.NullTime
		)
		err := tx.QueryRow(
			`SELECT id, filename, type, content, source_path, updated_at, archived_at FROM documents WHERE uuid = ?`,
			uuid,
		).Scan(&existingID, &existingFilename, &existingType, &existingContent, &existingSourcePath, &existingUpdatedAt, &existingArchivedAt)
		var stale bool
		if errors.Is(err, sql.ErrNoRows) {
			res2, err := tx.Exec(
				`INSERT INTO documents (uuid, repo_id, filename, type, content, size_bytes, source_path, created_at, updated_at, archived_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
				uuid, repo.ID, sd.Parsed.Filename, sd.Parsed.Type, sd.Content, len(sd.Content), sd.Parsed.SourcePath,
				sqliteTimestamp(sd.Parsed.CreatedAt), sqliteTimestamp(sd.Parsed.UpdatedAt),
				nullableSqliteTimestamp(sd.Parsed.ArchivedAt),
			)
			if err != nil {
				return fmt.Errorf("insert document %s: %w", sd.Parsed.Filename, err)
			}
			id, _ := res2.LastInsertId()
			existingID = id
			res.Inserted++
		} else if err != nil {
			return err
		} else if sd.Parsed.UpdatedAt.Before(existingUpdatedAt) {
			// Last-writer-wins (BACI-5): remote YAML is older than the
			// local row. Preserve local body + links; the export phase
			// writes the newer local content out on this run.
			stale = true
			res.Skipped++
			res.SkippedStale = append(res.SkippedStale, SkippedStaleEntry{
				Kind:          "document",
				UUID:          uuid,
				Label:         existingFilename,
				LocalUpdated:  existingUpdatedAt.UTC().Format(time.RFC3339),
				RemoteUpdated: sd.Parsed.UpdatedAt.UTC().Format(time.RFC3339),
			})
		} else {
			// Same noop-vs-update gate as applyFeatures / applyIssues:
			// only run the UPDATE if any salient field actually
			// differs. Doc-link side-data is replaced wholesale below
			// regardless; that's its own no-op when sets match.
			changed := existingFilename != sd.Parsed.Filename ||
				existingType != sd.Parsed.Type ||
				existingContent != sd.Content ||
				existingSourcePath != sd.Parsed.SourcePath ||
				!nullableTimeEqual(existingArchivedAt, sd.Parsed.ArchivedAt)
			if changed {
				if _, err := tx.Exec(
					`UPDATE documents SET filename = ?, type = ?, content = ?, size_bytes = ?, source_path = ?, updated_at = ?, archived_at = ? WHERE id = ?`,
					sd.Parsed.Filename, sd.Parsed.Type, sd.Content, len(sd.Content), sd.Parsed.SourcePath,
					sqliteTimestamp(sd.Parsed.UpdatedAt), nullableSqliteTimestamp(sd.Parsed.ArchivedAt), existingID,
				); err != nil {
					return fmt.Errorf("update document %s: %w", sd.Parsed.Filename, err)
				}
				res.Updated++
			} else {
				res.NoOp++
			}
		}
		if stale {
			// Leave doc_links + sync_state intact for the same LWW
			// reasons as issues' pass-2 skip.
			continue
		}
		// Replace links wholesale.
		if err := e.replaceDocLinksTx(tx, existingID, sd.Parsed.Filename, uuid, sd.Parsed.Links, res); err != nil {
			return fmt.Errorf("replace doc links for %s: %w", sd.Parsed.Filename, err)
		}
		// BACI-144: the wipe-and-rewrite above fires the
		// bump_document_updated_on_link_insert / _delete schema
		// triggers, which bump documents.updated_at to
		// CURRENT_TIMESTAMP. That overwrites the YAML's UpdatedAt that
		// the insert/update branch above just wrote, breaking LWW for
		// the next round-trip. Re-stamp here so the in-DB value matches
		// the YAML byte-for-byte.
		if _, err := tx.Exec(
			`UPDATE documents SET updated_at = ? WHERE id = ?`,
			sqliteTimestamp(sd.Parsed.UpdatedAt), existingID,
		); err != nil {
			return fmt.Errorf("re-stamp updated_at for %s: %w", sd.Parsed.Filename, err)
		}
		if err := e.markSyncedTx(tx, uuid, store.SyncKindDocument, hash, now); err != nil {
			return err
		}
	}
	return nil
}

func (e *Engine) replaceDocLinksTx(tx *sql.Tx, docID int64, docFilename, docUUID string, links []ParsedDocLink, res *ImportResult) error {
	if _, err := tx.Exec(`DELETE FROM document_links WHERE document_id = ?`, docID); err != nil {
		return err
	}
	stmt, err := tx.Prepare(
		`INSERT INTO document_links (document_id, issue_id, feature_id, description) VALUES (?, ?, ?, ?)`,
	)
	if err != nil {
		return err
	}
	defer stmt.Close()
	for _, l := range links {
		switch l.Kind {
		case "issue":
			id, err := e.issueIDByUUIDTx(tx, l.TargetUUID)
			if err != nil {
				if errors.Is(err, store.ErrNotFound) {
					res.Dangling = append(res.Dangling, DanglingRef{
						From:        docFilename,
						FromUUID:    docUUID,
						Kind:        "doc_link",
						TargetLabel: l.TargetLabel,
						TargetUUID:  l.TargetUUID,
					})
					continue
				}
				return err
			}
			if _, err := stmt.Exec(docID, id, nil, ""); err != nil {
				return err
			}
		case "feature":
			id, err := e.featureIDByUUIDTx(tx, l.TargetUUID)
			if err != nil {
				if errors.Is(err, store.ErrNotFound) {
					res.Dangling = append(res.Dangling, DanglingRef{
						From:        docFilename,
						FromUUID:    docUUID,
						Kind:        "doc_link",
						TargetLabel: l.TargetLabel,
						TargetUUID:  l.TargetUUID,
					})
					continue
				}
				return err
			}
			if _, err := stmt.Exec(docID, nil, id, ""); err != nil {
				return err
			}
		default:
			res.Warnings = append(res.Warnings, fmt.Sprintf("unknown doc_link kind %q on %s", l.Kind, docFilename))
		}
	}
	return nil
}
