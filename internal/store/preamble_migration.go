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

// oldDispatchPreambleBACI76Typo is the verbatim _dispatch_preamble body
// bacio shipped between BACI-76 and BACI-80. It is identical to the
// post-BACI-80 default except that it told the supervisor to run the
// retired `bacio install-agents` (plural) verb in two places. BACI-79
// consolidated setup into `bacio install-agent` (singular); the plural
// form errors with `unknown command`. Embedded frozen so the BACI-80
// refresh migration can byte-compare a stored preamble against it and
// replace it in place when the user never customised the row.
//
//go:embed migrationdata/old_dispatch_preamble_baci76.txt
var oldDispatchPreambleBACI76Typo string

// oldDispatchPreambleBACI80 is the verbatim _dispatch_preamble body
// bacio shipped between BACI-80 and BACI-85 — the BACI-76 wrapper with
// the `install-agents` plural typo corrected, but before BACI-85 added
// the `mcp__bacio__attach_transcript` step. Embedded frozen so the
// BACI-85 refresh migration can byte-compare a stored preamble against
// it and replace it in place when the user never customised the row.
//
//go:embed migrationdata/old_dispatch_preamble_baci80.txt
var oldDispatchPreambleBACI80 string

// oldDispatchPreambleBACI85 is the verbatim _dispatch_preamble body
// bacio shipped between BACI-85 and BACI-103 — the BACI-80 wrapper with
// the `mcp__bacio__attach_transcript` step added, but before BACI-103
// replaced the free-form `prompt` argument with a fixed verbatim stub.
// Embedded frozen so the BACI-103 refresh migration can byte-compare a
// stored preamble against it and replace it in place when the user
// never customised the row.
//
//go:embed migrationdata/old_dispatch_preamble_baci85.txt
var oldDispatchPreambleBACI85 string

// oldDispatchPreambleBACI103 is the verbatim _dispatch_preamble body
// bacio shipped between BACI-103 and the XML-stub change — the
// fixed-verbatim Task-prompt stub spelled with `Ticket: …`, `Mode: …`,
// `Subagent: …` colon-lines and an appended `Dispatch ID: …` line.
// After the XML-stub change, the stub is emitted as `<issue_id>…</issue_id>`,
// `<mode>…</mode>`, `<subagent_type>…</subagent_type>` and the appended
// tag is `<dispatch_id>…</dispatch_id>`. Embedded frozen so the
// refresh migration can byte-compare a stored preamble against it and
// replace it in place when the user never customised the row.
//
//go:embed migrationdata/old_dispatch_preamble_baci103.txt
var oldDispatchPreambleBACI103 string

// oldDispatchPreambleBACI225 is the verbatim _dispatch_preamble body
// bacio shipped after the XML-stub change but before BACI-226 added
// the `<base_branch>` stub tag — the supervisor was told to copy
// `<issue_id>` and `<mode>` into the worker prompt and append
// `<dispatch_id>`, with no mention of `<base_branch>`. Embedded
// frozen so the BACI-226 refresh migration can byte-compare a stored
// preamble against it and replace it in place when the user never
// customised the row.
//
//go:embed migrationdata/old_dispatch_preamble_baci225.txt
var oldDispatchPreambleBACI225 string

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
	// so compare the old embedded files the same way.
	oldDefault := strings.TrimRight(oldDispatchPreambleBACI52, "\r\n")
	if stored == oldDefault {
		if _, err := db.Exec(
			`UPDATE prompt_templates SET body = ?, updated_at = CURRENT_TIMESTAMP WHERE slug = ?`,
			newDefault, slug,
		); err != nil {
			return err
		}
		slog.Info("bacio: refreshed the _dispatch_preamble body to the current default (spawn per-mode subagent)")
		return nil
	}
	// BACI-80: the BACI-76 default carried a `bacio install-agents`
	// (plural) typo. If the stored body matches that frozen typo'd
	// default, the user never customised it — replace it with the
	// corrected default in place.
	oldTypoDefault := strings.TrimRight(oldDispatchPreambleBACI76Typo, "\r\n")
	if stored == oldTypoDefault {
		if _, err := db.Exec(
			`UPDATE prompt_templates SET body = ?, updated_at = CURRENT_TIMESTAMP WHERE slug = ?`,
			newDefault, slug,
		); err != nil {
			return err
		}
		slog.Info("bacio: refreshed the _dispatch_preamble body to the BACI-80 default (`bacio install-agents` typo fixed)")
		return nil
	}
	// BACI-85: the BACI-80 default did not yet tell the supervisor to
	// call `mcp__bacio__attach_transcript` after Task returns. If the
	// stored body matches that frozen default, the user never
	// customised it — replace it with the BACI-85 default in place.
	oldBACI80Default := strings.TrimRight(oldDispatchPreambleBACI80, "\r\n")
	if stored == oldBACI80Default {
		if _, err := db.Exec(
			`UPDATE prompt_templates SET body = ?, updated_at = CURRENT_TIMESTAMP WHERE slug = ?`,
			newDefault, slug,
		); err != nil {
			return err
		}
		slog.Info("bacio: refreshed the _dispatch_preamble body to the BACI-85 default (attach_transcript step added)")
		return nil
	}
	// BACI-103: the BACI-85 default told the supervisor to compose the
	// Task `prompt` argument free-form. If the stored body matches that
	// frozen default, the user never customised it — replace it with the
	// BACI-103 default that passes a fixed verbatim Task-prompt stub.
	oldBACI85Default := strings.TrimRight(oldDispatchPreambleBACI85, "\r\n")
	if stored == oldBACI85Default {
		if _, err := db.Exec(
			`UPDATE prompt_templates SET body = ?, updated_at = CURRENT_TIMESTAMP WHERE slug = ?`,
			newDefault, slug,
		); err != nil {
			return err
		}
		slog.Info("bacio: refreshed the _dispatch_preamble body to the BACI-103 default (fixed verbatim Task-prompt stub)")
		return nil
	}
	// XML-stub change: the BACI-103 default spelled the Task-prompt stub
	// as `Ticket: …`/`Mode: …`/`Subagent: …` colon-lines. The current
	// default emits each field as an XML tag (`<issue_id>…</issue_id>`,
	// `<mode>…</mode>`, `<subagent_type>…</subagent_type>` and an appended
	// `<dispatch_id>…</dispatch_id>`) so the supervisor can copy each tag
	// verbatim into the Task prompt. If the stored body matches the
	// frozen BACI-103 default, replace it in place.
	oldBACI103Default := strings.TrimRight(oldDispatchPreambleBACI103, "\r\n")
	if stored == oldBACI103Default {
		if _, err := db.Exec(
			`UPDATE prompt_templates SET body = ?, updated_at = CURRENT_TIMESTAMP WHERE slug = ?`,
			newDefault, slug,
		); err != nil {
			return err
		}
		slog.Info("bacio: refreshed the _dispatch_preamble body to the XML-stub default (Task-prompt stub uses <issue_id>/<mode>/<dispatch_id> tags)")
		return nil
	}
	// BACI-226: the XML-stub default did not yet mention the
	// `<base_branch>` stub tag. The new default tells the supervisor
	// to copy that tag verbatim into the worker prompt alongside
	// `<issue_id>` / `<mode>`. If the stored body matches the frozen
	// pre-BACI-226 default, replace it in place.
	oldBACI225Default := strings.TrimRight(oldDispatchPreambleBACI225, "\r\n")
	if stored == oldBACI225Default {
		if _, err := db.Exec(
			`UPDATE prompt_templates SET body = ?, updated_at = CURRENT_TIMESTAMP WHERE slug = ?`,
			newDefault, slug,
		); err != nil {
			return err
		}
		slog.Info("bacio: refreshed the _dispatch_preamble body to the BACI-226 default (<base_branch> stub tag in Task prompt)")
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
