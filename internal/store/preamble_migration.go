package store

import (
	"database/sql"
	_ "embed"
	"log/slog"
	"strings"

	"github.com/mrgeoffrich/bacio/internal/model"
)

// oldDispatchPreambleBACI52 is the verbatim _dispatch_preamble body
// bacio shipped before BACI-76 (the BACI-52 delegation wrapper that
// told the supervisor to spawn `general-purpose` and paste the brief
// after a `---` separator). It is embedded frozen so the BACI-76
// refresh migration can byte-compare a stored preamble against it: if
// the user never customised the row, the stored body equals this and
// the migration safely replaces it with the new default; if it
// differs, the user customised it and the migration leaves the row
// alone with a warning.
//
//go:embed migrationdata/old_dispatch_preamble_baci52.txt
var oldDispatchPreambleBACI52 string

// refreshDispatchPreamble is the BACI-76 one-time migration of the
// stored _dispatch_preamble body. Before BACI-76 the preamble told the
// supervisor to spawn `general-purpose` and paste the full brief; after
// BACI-76 it tells the supervisor to spawn the per-mode custom subagent
// and pass only a tiny stub.
//
//   - If the row is missing, nothing to do — backfillDispatchPreamble
//     (which runs first in migrate) already inserted the new default.
//   - If the stored body byte-matches the old embedded default, the
//     user never customised it: replace it with the new embedded
//     default in place.
//   - If it differs, the user customised it (or it is already the new
//     default): leave it untouched. When it is neither default and
//     still mentions `general-purpose` we log a one-line warning so the
//     user knows their custom preamble is stale.
//
// Idempotent: a second run sees the new default stored, the equality
// check against the OLD default fails, and it is a no-op.
func refreshDispatchPreamble(db *sql.DB) error {
	slug := model.BuiltinTemplatePreamble
	var stored string
	err := db.QueryRow(`SELECT body FROM prompt_templates WHERE slug = ?`, slug).Scan(&stored)
	if err == sql.ErrNoRows {
		return nil // backfillDispatchPreamble handled the insert.
	}
	if err != nil {
		return err
	}
	newDefault := model.DefaultPromptBodyForBuiltinSlug(slug)
	if stored == newDefault {
		return nil // already on the new default.
	}
	// loadDefaultPromptBodies stores bodies with trailing \r\n stripped,
	// so compare the old embedded file the same way.
	oldDefault := strings.TrimRight(oldDispatchPreambleBACI52, "\r\n")
	if stored == oldDefault {
		if _, err := db.Exec(
			`UPDATE prompt_templates SET body = ?, updated_at = CURRENT_TIMESTAMP WHERE slug = ?`,
			newDefault, slug,
		); err != nil {
			return err
		}
		slog.Info("bacio: refreshed the _dispatch_preamble body to the BACI-76 default (spawn per-mode subagent)")
		return nil
	}
	// Customised body — leave it, but warn: it may still say
	// `general-purpose`, which BACI-76 retired.
	if strings.Contains(stored, "general-purpose") {
		slog.Warn("bacio: your customised dispatch preamble still tells the supervisor to spawn `general-purpose`; " +
			"BACI-76 moved the per-mode brief into custom subagents — review it against the new default " +
			"(`bacio settings template reset _dispatch_preamble` takes ours, then `bacio install-agent`)")
	}
	return nil
}
