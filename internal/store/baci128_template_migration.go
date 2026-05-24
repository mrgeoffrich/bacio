package store

import (
	"database/sql"
	_ "embed"
	"log/slog"
	"strings"

	"github.com/mrgeoffrich/bacio/internal/model"
)

// BACI-128 — refresh the per-mode dispatch templates (and the
// dispatch preamble) when the stored body byte-matches the
// pre-BACI-128 embedded default. The new default adds a one-line
// instruction telling the worker to pass `issue_id: <issue_id>` in
// the `mcp__bacio__ask_user_question` call, and renames the
// `attach_transcript` example arg from `issue_key` to `issue_id`.
//
// A user who has customised a template (the stored body no longer
// byte-matches the previous default) keeps their override. The
// trade-off — they don't pick up the new sentence automatically —
// is preferable to silently rewriting a personalised body. They
// can opt in with `bacio settings template reset <slug>`.
//
//go:embed migrationdata/baci128_prev_templates/_dispatch_preamble.txt
var prevTemplatePreambleBACI128 string

//go:embed migrationdata/baci128_prev_templates/plan.txt
var prevTemplatePlanBACI128 string

//go:embed migrationdata/baci128_prev_templates/design.txt
var prevTemplateDesignBACI128 string

//go:embed migrationdata/baci128_prev_templates/implement.txt
var prevTemplateImplementBACI128 string

//go:embed migrationdata/baci128_prev_templates/review.txt
var prevTemplateReviewBACI128 string

//go:embed migrationdata/baci128_prev_templates/ship.txt
var prevTemplateShipBACI128 string

//go:embed migrationdata/baci128_prev_templates/fix_review.txt
var prevTemplateFixReviewBACI128 string

// baci128PrevDefaults maps each affected built-in slug to the body
// it shipped with before BACI-128. The map is the migration's
// equality check: a stored body that byte-matches the value is an
// untouched default and can be safely rewritten in place. Trailing
// CR/LF is stripped to match how `loadDefaultPromptBodies` stores
// the new embedded body (TrimRight \r\n) — keeps the two sides
// comparable.
var baci128PrevDefaults = map[string]string{
	model.BuiltinTemplatePreamble:  strings.TrimRight(prevTemplatePreambleBACI128, "\r\n"),
	model.BuiltinTemplatePlan:      strings.TrimRight(prevTemplatePlanBACI128, "\r\n"),
	model.BuiltinTemplateDesign:    strings.TrimRight(prevTemplateDesignBACI128, "\r\n"),
	model.BuiltinTemplateImplement: strings.TrimRight(prevTemplateImplementBACI128, "\r\n"),
	model.BuiltinTemplateReview:    strings.TrimRight(prevTemplateReviewBACI128, "\r\n"),
	model.BuiltinTemplateShip:      strings.TrimRight(prevTemplateShipBACI128, "\r\n"),
	model.BuiltinTemplateFixReview: strings.TrimRight(prevTemplateFixReviewBACI128, "\r\n"),
}

// refreshAskUserQuestionTemplates is the BACI-128 one-shot migration
// of every affected built-in template body. For each slug:
//
//   - If the row is missing, nothing to do — migratePromptTemplates'
//     first-time seed inserted the new default, or the user has
//     deleted the row (`bacio settings template rm <slug>` is their
//     deliberate choice; we don't resurrect deleted rows).
//   - If the stored body byte-matches the frozen pre-BACI-128
//     default, the user never customised it: replace it with the
//     current embedded default in place.
//   - Otherwise the user has customised the row (or it is already
//     the new default): leave it untouched.
//
// Idempotent: a second run sees the new default stored, the
// equality check against the OLD default fails, and it is a no-op.
//
// Logging is at info-level per slug so an operator who runs `bacio
// status` or tails the log can see exactly which built-ins were
// refreshed on first launch after upgrading.
func refreshAskUserQuestionTemplates(db *sql.DB) error {
	for slug, oldDefault := range baci128PrevDefaults {
		newDefault := model.DefaultPromptBodyForBuiltinSlug(slug)
		if newDefault == "" {
			// Belt-and-braces: should never happen — every slug
			// in baci128PrevDefaults is a built-in, and the
			// embed step panics at package init on a missing
			// file. Defensive guard so a future restructure
			// can't silently no-op the migration.
			continue
		}
		var stored string
		err := db.QueryRow(`SELECT body FROM prompt_templates WHERE slug = ?`, slug).Scan(&stored)
		if err == sql.ErrNoRows {
			continue
		}
		if err != nil {
			return err
		}
		if stored == newDefault {
			continue // already on the new default.
		}
		if stored != oldDefault {
			continue // user customised — leave alone.
		}
		if _, err := db.Exec(
			`UPDATE prompt_templates SET body = ?, updated_at = CURRENT_TIMESTAMP WHERE slug = ?`,
			newDefault, slug,
		); err != nil {
			return err
		}
		slog.Info("bacio: refreshed prompt template to current default (BACI-128: issue_id required on ask_user_question, attach_transcript arg renamed)",
			"slug", slug)
	}
	return nil
}
