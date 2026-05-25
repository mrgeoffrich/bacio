// BACI-68: the archive lifecycle layer. Issues, features and documents
// each grew a nullable archived_at column doubling as a hidden-flag and
// an audit timestamp. This file holds the auto-sweep — three SQL passes
// run in one transaction by the leader-elected Controller (and by the
// manual `bacio archive sweep` CLI verb) — plus the public ArchiveSweep
// result type.
//
// Ordering matters. Features depend on every-child-archived; documents
// depend on every-linked-parent-archived; both have to see the issue
// archives this tick. So: issues -> features -> documents, inside a
// single tx.
package store

import (
	"fmt"
	"time"
)

// ArchiveSweepInterval is the period the leader's Controller invokes
// ArchiveSweep at. Hourly cadence is independent of the per-tick
// retention window — the window decides which issues are eligible,
// the cadence decides how often we re-check.
const ArchiveSweepInterval = 1 * time.Hour

// DefaultArchiveRetentionDays is the fallback retention window the
// BACI-162 settings layer falls back to when the configured value is
// missing, blank, or out of range. The live value lives in
// app_settings as `archive.retention_days` and is configurable from
// the Settings page.
//
// Exported so non-store callers (the Settings UI, schema docs, the
// CLI defaults) can quote the same number.
const DefaultArchiveRetentionDays = 7

// ArchiveSweepResult is the per-tick summary. Returned by ArchiveSweep
// for logging and the optional `bacio archive sweep` CLI verb. Each
// count is the number of rows whose archived_at was newly stamped on
// this tick (already-archived rows aren't counted).
type ArchiveSweepResult struct {
	IssuesArchived    int64 `json:"issues_archived"`
	FeaturesArchived  int64 `json:"features_archived"`
	DocumentsArchived int64 `json:"documents_archived"`
}

// Total returns the sum of the three counts — useful for the leader
// log line and `bacio archive sweep` text output.
func (r ArchiveSweepResult) Total() int64 {
	return r.IssuesArchived + r.FeaturesArchived + r.DocumentsArchived
}

// ArchiveSweep runs the three SQL passes in one transaction:
//
//  1. Issues in (done, cancelled) whose terminal_at is older than the
//     configured retention window and that aren't already archived.
//     BACI-162 makes the window configurable (archive.retention_days);
//     BACI-138's terminal_at column is the clock anchor (NOT
//     updated_at — editing a closed issue's tags no longer resets the
//     retention countdown). When archive.auto_enabled is false the
//     issue pass is skipped entirely.
//  2. Features whose every child issue is archived (and the feature
//     had at least one child) that aren't already archived.
//  3. Documents whose every linked parent (issue and/or feature) is
//     archived (and the doc had at least one link) that aren't
//     already archived. Docs with zero links are NOT orphans — they
//     were never attached, so the sweep leaves them alone.
//
// The cascade passes (2 + 3) run regardless of archive.auto_enabled:
// they cascade off per-entity archived_at writes, so a manually
// archived issue should still cascade to its feature and docs.
//
// Idempotent. Safe to run on a quiet DB (every pass affects zero rows)
// and safe to run concurrently with manual archive verbs (the WHERE
// clauses already exclude `archived_at IS NOT NULL`, and the per-row
// updates are atomic).
//
// The sweep itself does NOT emit per-row history audit rows — auto-
// archive is mechanical janitor work like the prune and matcher loops,
// so flooding the audit log with per-row entries every hour would be
// noise. Manual archive verbs DO emit history entries (under the
// resolved actor), per the agent-CLI principles. Since BACI-160 gap 2,
// both surfaces (the manual `bacio archive sweep` verb and the
// leader-driven controller.ArchiveSweepIfLeader tick) also emit ONE
// `archive.sweep` summary row per non-empty sweep — distinct from the
// "no per-row" guarantee.
func (s *Store) ArchiveSweep() (ArchiveSweepResult, error) {
	autoEnabled, err := s.GetArchiveAutoEnabled()
	if err != nil {
		return ArchiveSweepResult{}, fmt.Errorf("read archive.auto_enabled: %w", err)
	}
	retentionDays, err := s.GetArchiveRetentionDays()
	if err != nil {
		return ArchiveSweepResult{}, fmt.Errorf("read archive.retention_days: %w", err)
	}

	tx, err := s.DB.Begin()
	if err != nil {
		return ArchiveSweepResult{}, err
	}
	defer tx.Rollback()

	var res ArchiveSweepResult

	// Pass 1: issues whose terminal_at is older than the configured
	// retention window. Skipped entirely when auto_enabled=false — the
	// cascade passes (2 + 3) still run so a manually-archived issue
	// cascades to its feature / docs as expected.
	if autoEnabled {
		// `terminal_at < datetime('now','-N days')` is the eligibility
		// predicate. The IS NOT NULL guard is defensive — state IN
		// ('done','cancelled') already implies terminal_at IS NOT NULL
		// post-BACI-138, but stating both clarifies intent and protects
		// against a future state-vs-terminal_at drift.
		ageWindowExpr := fmt.Sprintf("datetime('now', '-%d days')", retentionDays)
		issueQ := `
			UPDATE issues
			   SET archived_at = CURRENT_TIMESTAMP
			 WHERE archived_at IS NULL
			   AND state IN ('done','cancelled')
			   AND terminal_at IS NOT NULL
			   AND terminal_at < ` + ageWindowExpr
		r, err := tx.Exec(issueQ)
		if err != nil {
			return ArchiveSweepResult{}, fmt.Errorf("archive issues: %w", err)
		}
		res.IssuesArchived, _ = r.RowsAffected()
	}

	// Pass 2: features whose every child issue is archived and that
	// had at least one child. The inner SELECT groups by feature_id
	// and SUMs `archived_at IS NULL` — if the sum is zero, every
	// child is archived. `HAVING COUNT(*) > 0` is implicit (the
	// GROUP BY excludes feature_id IS NULL and a feature with no
	// children never appears in the inner result), but stated for
	// clarity in the comment: the brief explicitly excludes
	// childless features from the auto-archive path.
	r, err := tx.Exec(`
		UPDATE features
		   SET archived_at = CURRENT_TIMESTAMP
		 WHERE archived_at IS NULL
		   AND id IN (
		     SELECT feature_id FROM issues
		      WHERE feature_id IS NOT NULL
		      GROUP BY feature_id
		     HAVING SUM(CASE WHEN archived_at IS NULL THEN 1 ELSE 0 END) = 0
		   )`)
	if err != nil {
		return ArchiveSweepResult{}, fmt.Errorf("archive features: %w", err)
	}
	res.FeaturesArchived, _ = r.RowsAffected()

	// Pass 3: docs whose every linked parent is archived. The LEFT
	// JOINs preserve link rows whose issue or feature side is NULL
	// (one of the two FKs is set, the other isn't — the
	// document_links CHECK enforces XOR). The SUM-CASE pair counts
	// non-archived parents: if both sums are zero, every parent on
	// every link is archived. `GROUP BY dl.document_id` plus the
	// `id IN (...)` shape limits the sweep to docs that have at
	// least one link (a doc with zero links never appears in the
	// inner result), which matches the brief: "Docs with zero links
	// are NOT orphans — they were never attached, so the sweep
	// leaves them alone".
	r, err = tx.Exec(`
		UPDATE documents
		   SET archived_at = CURRENT_TIMESTAMP
		 WHERE archived_at IS NULL
		   AND id IN (
		     SELECT dl.document_id
		       FROM document_links dl
		       LEFT JOIN issues   i ON dl.issue_id   = i.id
		       LEFT JOIN features f ON dl.feature_id = f.id
		      GROUP BY dl.document_id
		     HAVING SUM(CASE WHEN i.archived_at IS NULL AND dl.issue_id   IS NOT NULL THEN 1 ELSE 0 END)
		          + SUM(CASE WHEN f.archived_at IS NULL AND dl.feature_id IS NOT NULL THEN 1 ELSE 0 END) = 0
		   )`)
	if err != nil {
		return ArchiveSweepResult{}, fmt.Errorf("archive documents: %w", err)
	}
	res.DocumentsArchived, _ = r.RowsAffected()

	if err := tx.Commit(); err != nil {
		return ArchiveSweepResult{}, err
	}
	return res, nil
}
