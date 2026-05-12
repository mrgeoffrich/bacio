package sync

import (
	"database/sql"
	"errors"
	"fmt"
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
		var existingID int64
		var existingSlug, existingTitle, existingDescription string
		err := tx.QueryRow(
			`SELECT id, slug, title, description FROM features WHERE uuid = ?`,
			uuid,
		).Scan(&existingID, &existingSlug, &existingTitle, &existingDescription)
		if errors.Is(err, sql.ErrNoRows) {
			// Insert.
			if _, err := tx.Exec(
				`INSERT INTO features (uuid, repo_id, slug, title, description, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?)`,
				uuid, repo.ID, sf.Parsed.Slug, sf.Parsed.Title, sf.Description,
				sqliteTimestamp(sf.Parsed.CreatedAt), sqliteTimestamp(sf.Parsed.UpdatedAt),
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
		// Update if any field differs.
		if existingSlug != sf.Parsed.Slug || existingTitle != sf.Parsed.Title || existingDescription != sf.Description {
			if _, err := tx.Exec(
				`UPDATE features SET slug = ?, title = ?, description = ?, updated_at = ? WHERE id = ?`,
				sf.Parsed.Slug, sf.Parsed.Title, sf.Description, sqliteTimestamp(sf.Parsed.UpdatedAt), existingID,
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
			existingID          int64
			existingNumber      int64
			existingFeatureID   sql.NullInt64
			existingTitle       string
			existingDescription string
			existingState       string
			existingAssignee    string
		)
		err := tx.QueryRow(
			`SELECT id, number, feature_id, title, description, state, assignee FROM issues WHERE uuid = ?`,
			uuid,
		).Scan(&existingID, &existingNumber, &existingFeatureID, &existingTitle, &existingDescription, &existingState, &existingAssignee)
		if errors.Is(err, sql.ErrNoRows) {
			res2, err := tx.Exec(
				`INSERT INTO issues (uuid, repo_id, number, feature_id, title, description, state, assignee, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
				uuid, repo.ID, si.Parsed.Number, nullableInt64(featureID),
				si.Parsed.Title, si.Description, si.Parsed.State, si.Parsed.Assignee,
				sqliteTimestamp(si.Parsed.CreatedAt), sqliteTimestamp(si.Parsed.UpdatedAt),
			)
			if err != nil {
				return fmt.Errorf("insert issue %s: %w", uuid, err)
			}
			id, _ := res2.LastInsertId()
			idByUUID[uuid] = id
			res.Inserted++
		} else if err != nil {
			return err
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
				existingAssignee != si.Parsed.Assignee
			if changed {
				if _, err := tx.Exec(
					`UPDATE issues SET number = ?, feature_id = ?, title = ?, description = ?, state = ?, assignee = ?, updated_at = ? WHERE id = ?`,
					si.Parsed.Number, nullableInt64(featureID),
					si.Parsed.Title, si.Description, si.Parsed.State, si.Parsed.Assignee,
					sqliteTimestamp(si.Parsed.UpdatedAt), existingID,
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
			var (
				existingID     int64
				existingAuthor string
				existingBody   string
			)
			err := tx.QueryRow(
				`SELECT id, author, body FROM comments WHERE uuid = ?`,
				sc.Parsed.UUID,
			).Scan(&existingID, &existingAuthor, &existingBody)
			if errors.Is(err, sql.ErrNoRows) {
				if _, err := tx.Exec(
					`INSERT INTO comments (uuid, issue_id, author, body, created_at) VALUES (?, ?, ?, ?, ?)`,
					sc.Parsed.UUID, issueID, sc.Parsed.Author, sc.Body,
					sqliteTimestamp(sc.Parsed.CreatedAt),
				); err != nil {
					return fmt.Errorf("insert comment %s: %w", sc.Parsed.UUID, err)
				}
				res.Inserted++
			} else if err != nil {
				return err
			} else if existingAuthor != sc.Parsed.Author || existingBody != sc.Body {
				// Update body / author.
				if _, err := tx.Exec(
					`UPDATE comments SET author = ?, body = ? WHERE id = ?`,
					sc.Parsed.Author, sc.Body, existingID,
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
		)
		err := tx.QueryRow(
			`SELECT id, filename, type, content, source_path FROM documents WHERE uuid = ?`,
			uuid,
		).Scan(&existingID, &existingFilename, &existingType, &existingContent, &existingSourcePath)
		if errors.Is(err, sql.ErrNoRows) {
			res2, err := tx.Exec(
				`INSERT INTO documents (uuid, repo_id, filename, type, content, size_bytes, source_path, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
				uuid, repo.ID, sd.Parsed.Filename, sd.Parsed.Type, sd.Content, len(sd.Content), sd.Parsed.SourcePath,
				sqliteTimestamp(sd.Parsed.CreatedAt), sqliteTimestamp(sd.Parsed.UpdatedAt),
			)
			if err != nil {
				return fmt.Errorf("insert document %s: %w", sd.Parsed.Filename, err)
			}
			id, _ := res2.LastInsertId()
			existingID = id
			res.Inserted++
		} else if err != nil {
			return err
		} else {
			// Same noop-vs-update gate as applyFeatures / applyIssues:
			// only run the UPDATE if any salient field actually
			// differs. Doc-link side-data is replaced wholesale below
			// regardless; that's its own no-op when sets match.
			changed := existingFilename != sd.Parsed.Filename ||
				existingType != sd.Parsed.Type ||
				existingContent != sd.Content ||
				existingSourcePath != sd.Parsed.SourcePath
			if changed {
				if _, err := tx.Exec(
					`UPDATE documents SET filename = ?, type = ?, content = ?, size_bytes = ?, source_path = ?, updated_at = ? WHERE id = ?`,
					sd.Parsed.Filename, sd.Parsed.Type, sd.Content, len(sd.Content), sd.Parsed.SourcePath,
					sqliteTimestamp(sd.Parsed.UpdatedAt), existingID,
				); err != nil {
					return fmt.Errorf("update document %s: %w", sd.Parsed.Filename, err)
				}
				res.Updated++
			} else {
				res.NoOp++
			}
		}
		// Replace links wholesale.
		if err := e.replaceDocLinksTx(tx, existingID, sd.Parsed.Filename, uuid, sd.Parsed.Links, res); err != nil {
			return fmt.Errorf("replace doc links for %s: %w", sd.Parsed.Filename, err)
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
