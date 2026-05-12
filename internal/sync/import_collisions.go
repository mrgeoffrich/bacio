package sync

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/mrgeoffrich/bacio/internal/model"
)

// resolveCollisions handles phase 2 — for each kind, if an incoming
// label maps to one uuid and the local DB already has a row using
// that label with a *different* uuid, the local row gives up the
// label per the "already-in-git wins" rule.
func (e *Engine) resolveCollisions(tx *sql.Tx, source string, sr *scannedRepo, repo *model.Repo, res *ImportResult) error {
	now := time.Now().UTC()
	// Issues: collide on `(repo_id, number)`.
	incomingNumbers := map[int64]string{}
	for uuid, si := range sr.Issues {
		incomingNumbers[si.Parsed.Number] = uuid
	}
	if err := e.resolveIssueCollisions(tx, source, sr, repo, incomingNumbers, res, now); err != nil {
		return err
	}
	// Features: collide on `(repo_id, slug)`.
	incomingSlugs := map[string]string{}
	for uuid, sf := range sr.Features {
		incomingSlugs[sf.Parsed.Slug] = uuid
	}
	if err := e.resolveFeatureCollisions(tx, source, sr, repo, incomingSlugs, res, now); err != nil {
		return err
	}
	// Documents: collide on `(repo_id, filename)`.
	incomingFilenames := map[string]string{}
	for uuid, sd := range sr.Documents {
		incomingFilenames[sd.Parsed.Filename] = uuid
	}
	if err := e.resolveDocumentCollisions(tx, source, sr, repo, incomingFilenames, res, now); err != nil {
		return err
	}
	return nil
}

func (e *Engine) resolveIssueCollisions(tx *sql.Tx, source string, sr *scannedRepo, repo *model.Repo, incoming map[int64]string, res *ImportResult, now time.Time) error {
	for number, incomingUUID := range incoming {
		var (
			existingUUID string
			existingID   int64
		)
		err := tx.QueryRow(
			`SELECT uuid, id FROM issues WHERE repo_id = ? AND number = ?`,
			repo.ID, number,
		).Scan(&existingUUID, &existingID)
		if errors.Is(err, sql.ErrNoRows) {
			continue
		}
		if err != nil {
			return err
		}
		if existingUUID == incomingUUID {
			continue // same record, no collision
		}
		// Local row owns the number but isn't the just-imported
		// uuid — it must be local-only. Reallocate.
		newNumber, err := e.nextIssueNumberTx(tx, repo.ID, sr, incoming)
		if err != nil {
			return err
		}
		if _, err := tx.Exec(
			`UPDATE issues SET number = ?, updated_at = ? WHERE id = ?`,
			newNumber, sqliteTimestamp(now), existingID,
		); err != nil {
			return err
		}
		// Bump cached counter so subsequent local creates don't reuse.
		if _, err := tx.Exec(
			`UPDATE repos SET next_issue_number = MAX(next_issue_number, ? + 1), updated_at = ? WHERE id = ?`,
			newNumber, sqliteTimestamp(now), repo.ID,
		); err != nil {
			return err
		}
		oldLabel := fmt.Sprintf("%s-%d", repo.Prefix, number)
		newLabel := fmt.Sprintf("%s-%d", repo.Prefix, newNumber)
		// Append redirect.
		if !e.DryRun {
			r := Redirect{
				Kind:      "issue",
				Old:       oldLabel,
				New:       newLabel,
				UUID:      existingUUID,
				ChangedAt: now,
				Reason:    ReasonLabelCollision,
			}
			if err := AppendRedirect(source, repo.Prefix, r); err != nil {
				res.Warnings = append(res.Warnings, fmt.Sprintf("append redirect for %s: %v", oldLabel, err))
			}
		}
		res.Renumbered = append(res.Renumbered, RenumberEntry{
			Prefix:    repo.Prefix,
			UUID:      existingUUID,
			OldNumber: number,
			NewNumber: newNumber,
		})
	}
	return nil
}

// nextIssueNumberTx returns the smallest unused issue number for
// repo, skipping any number that's also in `incoming` (the imported
// set). Without that skip, the renumber could land on a value that
// the very next-applied incoming issue is about to claim.
func (e *Engine) nextIssueNumberTx(tx *sql.Tx, repoID int64, sr *scannedRepo, incoming map[int64]string) (int64, error) {
	var n sql.NullInt64
	if err := tx.QueryRow(`SELECT MAX(number) FROM issues WHERE repo_id = ?`, repoID).Scan(&n); err != nil {
		return 0, err
	}
	candidate := int64(1)
	if n.Valid {
		candidate = n.Int64 + 1
	}
	for {
		// Avoid colliding with another incoming issue.
		if _, taken := incoming[candidate]; !taken {
			return candidate, nil
		}
		candidate++
	}
}

func (e *Engine) resolveFeatureCollisions(tx *sql.Tx, source string, sr *scannedRepo, repo *model.Repo, incoming map[string]string, res *ImportResult, now time.Time) error {
	for slug, incomingUUID := range incoming {
		var (
			existingUUID string
			existingID   int64
		)
		err := tx.QueryRow(
			`SELECT uuid, id FROM features WHERE repo_id = ? AND slug = ?`,
			repo.ID, slug,
		).Scan(&existingUUID, &existingID)
		if errors.Is(err, sql.ErrNoRows) {
			continue
		}
		if err != nil {
			return err
		}
		if existingUUID == incomingUUID {
			continue
		}
		newSlug, err := e.nextFreeSlugTx(tx, repo.ID, slug, incoming)
		if err != nil {
			return err
		}
		if _, err := tx.Exec(
			`UPDATE features SET slug = ?, updated_at = ? WHERE id = ?`,
			newSlug, sqliteTimestamp(now), existingID,
		); err != nil {
			return err
		}
		if !e.DryRun {
			r := Redirect{
				Kind:      "feature",
				Old:       slug,
				New:       newSlug,
				UUID:      existingUUID,
				ChangedAt: now,
				Reason:    ReasonLabelCollision,
			}
			if err := AppendRedirect(source, repo.Prefix, r); err != nil {
				res.Warnings = append(res.Warnings, fmt.Sprintf("append redirect for %s: %v", slug, err))
			}
		}
		res.Renamed = append(res.Renamed, RenameEntry{
			Kind:   "feature",
			Prefix: repo.Prefix,
			UUID:   existingUUID,
			Old:    slug,
			New:    newSlug,
		})
	}
	return nil
}

// nextFreeSlugTx returns base-N (the smallest N >= 2) such that no
// feature in the repo uses it and it's not in `incoming`.
func (e *Engine) nextFreeSlugTx(tx *sql.Tx, repoID int64, base string, incoming map[string]string) (string, error) {
	for n := 2; ; n++ {
		candidate := fmt.Sprintf("%s-%d", base, n)
		if _, taken := incoming[candidate]; taken {
			continue
		}
		var existing int64
		err := tx.QueryRow(
			`SELECT id FROM features WHERE repo_id = ? AND slug = ?`,
			repoID, candidate,
		).Scan(&existing)
		if errors.Is(err, sql.ErrNoRows) {
			return candidate, nil
		}
		if err != nil {
			return "", err
		}
	}
}

func (e *Engine) resolveDocumentCollisions(tx *sql.Tx, source string, sr *scannedRepo, repo *model.Repo, incoming map[string]string, res *ImportResult, now time.Time) error {
	for filename, incomingUUID := range incoming {
		var (
			existingUUID string
			existingID   int64
		)
		err := tx.QueryRow(
			`SELECT uuid, id FROM documents WHERE repo_id = ? AND filename = ?`,
			repo.ID, filename,
		).Scan(&existingUUID, &existingID)
		if errors.Is(err, sql.ErrNoRows) {
			continue
		}
		if err != nil {
			return err
		}
		if existingUUID == incomingUUID {
			continue
		}
		newFilename, err := e.nextFreeFilenameTx(tx, repo.ID, filename, incoming)
		if err != nil {
			return err
		}
		if _, err := tx.Exec(
			`UPDATE documents SET filename = ?, updated_at = ? WHERE id = ?`,
			newFilename, sqliteTimestamp(now), existingID,
		); err != nil {
			return err
		}
		if !e.DryRun {
			r := Redirect{
				Kind:      "document",
				Old:       filename,
				New:       newFilename,
				UUID:      existingUUID,
				ChangedAt: now,
				Reason:    ReasonLabelCollision,
			}
			if err := AppendRedirect(source, repo.Prefix, r); err != nil {
				res.Warnings = append(res.Warnings, fmt.Sprintf("append redirect for %s: %v", filename, err))
			}
		}
		res.Renamed = append(res.Renamed, RenameEntry{
			Kind:   "document",
			Prefix: repo.Prefix,
			UUID:   existingUUID,
			Old:    filename,
			New:    newFilename,
		})
	}
	return nil
}

// nextFreeFilenameTx suffixes the basename with -N before the
// extension, e.g. auth-overview.md → auth-overview-2.md.
func (e *Engine) nextFreeFilenameTx(tx *sql.Tx, repoID int64, base string, incoming map[string]string) (string, error) {
	dot := strings.LastIndex(base, ".")
	stem, ext := base, ""
	if dot > 0 {
		stem, ext = base[:dot], base[dot:]
	}
	for n := 2; ; n++ {
		candidate := fmt.Sprintf("%s-%d%s", stem, n, ext)
		if _, taken := incoming[candidate]; taken {
			continue
		}
		var existing int64
		err := tx.QueryRow(
			`SELECT id FROM documents WHERE repo_id = ? AND filename = ?`,
			repoID, candidate,
		).Scan(&existing)
		if errors.Is(err, sql.ErrNoRows) {
			return candidate, nil
		}
		if err != nil {
			return "", err
		}
	}
}
