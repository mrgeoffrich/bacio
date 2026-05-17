package api

// HTTP parity for the prompt-template + state-gate verbs. Mirrors the
// four schemas already registered (`settings.template.set`,
// `settings.template.reset`, `settings.template.states.set`,
// `settings.template.states.reset`). Same store calls + validators +
// audit log as the CLI / local backend — only the transport differs.
//
// Audit ops match local: `prompt_template.update`/`prompt_template.reset`
// and `prompt_states.update`/`prompt_states.reset`. These rows are
// global (no RepoID), same as the local backend writes them.

import (
	"fmt"
	"net/http"
	"strings"

	"github.com/mrgeoffrich/bacio/internal/cli/inputs"
	"github.com/mrgeoffrich/bacio/internal/client"
	"github.com/mrgeoffrich/bacio/internal/inputio"
	"github.com/mrgeoffrich/bacio/internal/model"
	"github.com/mrgeoffrich/bacio/internal/store"
)

// PromptTemplateBody is the per-mode response shape for PUT/DELETE on
// /settings/templates/{mode}. Body is the post-call resolved value (the
// stored override, or the built-in default when no override is set).
type PromptTemplateBody struct {
	Mode string `json:"mode"`
	Body string `json:"body"`
}

// PromptTemplateFullDTO mirrors the desktop's PromptTemplateDTO — a
// single row of the typed CRUD list at GET /settings/templates/full,
// and the response for the typed add / rename / delete / restore
// endpoints. Snake-case JSON to match the rest of the api conventions;
// the web bundle reshapes to camelCase in api.http.ts.
//
// Slug is the storage / CLI identifier; Mode is kept as an alias of
// Slug so a frontend that still keys by "mode" keeps working. Body /
// AllowedStates are the persisted values; Default / DefaultStates are
// the embedded built-in defaults for the slug (empty for user-created
// templates). IsDefault / StatesAreDefault report whether the persisted
// value still matches the built-in default — used by the UI's "reset"
// affordances.
type PromptTemplateFullDTO struct {
	Slug                    string   `json:"slug"`
	Mode                    string   `json:"mode"`
	Label                   string   `json:"label"`
	Body                    string   `json:"body"`
	Default                 string   `json:"default"`
	IsBuiltin               bool     `json:"is_builtin"`
	IsDefault               bool     `json:"is_default"`
	AllowedStates           []string `json:"allowed_states"`
	DefaultStates           []string `json:"default_states"`
	StatesAreDefault        bool     `json:"states_are_default"`
	ConcurrencyLimit        int      `json:"concurrency_limit"`
	DefaultConcurrencyLimit int      `json:"default_concurrency_limit"`
	ConcurrencyIsDefault    bool     `json:"concurrency_is_default"`
}

func sameStringSlice(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// templateDTO maps a store.PromptTemplate to the wire DTO. Mirrors
// desktop/settingsservice.go::dtoForTemplate so both backends emit the
// same shape.
func templateDTO(t *store.PromptTemplate) PromptTemplateFullDTO {
	def := model.DefaultPromptBodyForBuiltinSlug(t.Slug)
	defStates := stateStrings(model.DefaultPromptStatesForBuiltinSlug(t.Slug))
	allowed := stateStrings(t.AllowedStates)
	label := t.Name
	if label == "" {
		label = model.BuiltinTemplateLabel(t.Slug)
		if label == "" {
			label = t.Slug
		}
	}
	defConc := model.DefaultConcurrencyLimit(t.Slug)
	return PromptTemplateFullDTO{
		Slug:                    t.Slug,
		Mode:                    t.Slug,
		Label:                   label,
		Body:                    t.Body,
		Default:                 def,
		IsBuiltin:               t.IsBuiltin,
		IsDefault:               t.IsBuiltin && t.Body == def,
		AllowedStates:           allowed,
		DefaultStates:           defStates,
		StatesAreDefault:        t.IsBuiltin && sameStringSlice(allowed, defStates),
		ConcurrencyLimit:        t.ConcurrencyLimit,
		DefaultConcurrencyLimit: defConc,
		ConcurrencyIsDefault:    t.IsBuiltin && t.ConcurrencyLimit == defConc,
	}
}

// PromptTemplateStates is the per-mode response shape for PUT/DELETE on
// /settings/templates/{mode}/states. States is the post-call resolved
// value (the stored override, or the built-in default).
type PromptTemplateStates struct {
	Mode   string   `json:"mode"`
	States []string `json:"states"`
}

// ---------- list (templates) ----------

func (d deps) handlePromptTemplatesList(w http.ResponseWriter, r *http.Request) {
	all, err := d.store.AllPromptTemplates()
	if err != nil {
		status, code := statusForError(err)
		writeError(w, status, code, err.Error(), nil)
		return
	}
	out := make(map[string]string, len(all))
	for m, body := range all {
		out[string(m)] = body
	}
	writeJSON(w, http.StatusOK, out)
}

// handlePromptTemplatesFullList returns every template's full DTO —
// slug, label, body, allowed_states, is_builtin, plus the embedded
// defaults so the UI can render its reset affordances. BACI-50: the
// web bundle calls this instead of /settings/templates so it doesn't
// have to derive labels client-side.
func (d deps) handlePromptTemplatesFullList(w http.ResponseWriter, r *http.Request) {
	tmpls, err := d.store.ListPromptTemplates()
	if err != nil {
		status, code := statusForError(err)
		writeError(w, status, code, err.Error(), nil)
		return
	}
	out := make([]PromptTemplateFullDTO, 0, len(tmpls))
	for _, t := range tmpls {
		out = append(out, templateDTO(t))
	}
	writeJSON(w, http.StatusOK, out)
}

// ---------- list (states) ----------

func (d deps) handlePromptStatesList(w http.ResponseWriter, r *http.Request) {
	all, err := d.store.AllPromptStates()
	if err != nil {
		status, code := statusForError(err)
		writeError(w, status, code, err.Error(), nil)
		return
	}
	out := make(map[string][]string, len(all))
	for m, states := range all {
		ss := make([]string, len(states))
		for i, st := range states {
			ss[i] = string(st)
		}
		out[string(m)] = ss
	}
	writeJSON(w, http.StatusOK, out)
}

// ---------- set body ----------

func (d deps) handlePromptTemplateSet(w http.ResponseWriter, r *http.Request) {
	mode, ok := parseModePath(w, r)
	if !ok {
		return
	}
	raw, ok := readBody(r, w)
	if !ok {
		return
	}
	parsed, _, err := inputio.DecodeStrict[inputs.SettingsTemplateSetInput](raw)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_input", err.Error(), nil)
		return
	}
	in := *parsed
	if in.Slug != "" && in.Slug != string(mode) {
		writeError(w, http.StatusBadRequest, "invalid_input",
			"slug in body must match URL", map[string]any{"field": "slug"})
		return
	}
	if in.Body == "" {
		writeError(w, http.StatusBadRequest, "invalid_input",
			"body is required; use DELETE to revert a stage to its default",
			map[string]any{"field": "body"})
		return
	}
	if isDryRun(r) {
		// Validate at the store boundary, then short-circuit with the
		// projected resolved body the caller asked for.
		if _, err := d.store.ValidatePromptTemplate(mode, in.Body); err != nil {
			status, code := statusForError(err)
			writeError(w, status, code, err.Error(), nil)
			return
		}
		writeDryRun(w, http.StatusOK, &PromptTemplateBody{Mode: string(mode), Body: in.Body})
		return
	}
	if err := d.store.SetPromptTemplate(mode, in.Body); err != nil {
		status, code := statusForError(err)
		writeError(w, status, code, err.Error(), nil)
		return
	}
	recordOp(d.store, d.logger, model.HistoryEntry{
		Actor:       ActorFromContext(r.Context()),
		Op:          "prompt_template.update",
		Kind:        "app_setting",
		TargetLabel: "prompt_template." + string(mode),
		Details:     "stage=" + string(mode),
	})
	resolved, err := d.store.GetPromptTemplate(mode)
	if err != nil {
		status, code := statusForError(err)
		writeError(w, status, code, err.Error(), nil)
		return
	}
	writeJSON(w, http.StatusOK, &PromptTemplateBody{Mode: string(mode), Body: resolved})
}

// ---------- reset body ----------

func (d deps) handlePromptTemplateReset(w http.ResponseWriter, r *http.Request) {
	mode, ok := parseModePath(w, r)
	if !ok {
		return
	}
	if isDryRun(r) {
		writeDryRun(w, http.StatusOK, &PromptTemplateBody{
			Mode: string(mode),
			Body: model.DefaultPromptTemplate(mode),
		})
		return
	}
	if err := d.store.SetPromptTemplate(mode, ""); err != nil {
		status, code := statusForError(err)
		writeError(w, status, code, err.Error(), nil)
		return
	}
	recordOp(d.store, d.logger, model.HistoryEntry{
		Actor:       ActorFromContext(r.Context()),
		Op:          "prompt_template.reset",
		Kind:        "app_setting",
		TargetLabel: "prompt_template." + string(mode),
		Details:     "stage=" + string(mode),
	})
	writeJSON(w, http.StatusOK, &PromptTemplateBody{
		Mode: string(mode),
		Body: model.DefaultPromptTemplate(mode),
	})
}

// ---------- set states ----------

func (d deps) handlePromptStatesSet(w http.ResponseWriter, r *http.Request) {
	mode, ok := parseModePath(w, r)
	if !ok {
		return
	}
	raw, ok := readBody(r, w)
	if !ok {
		return
	}
	parsed, _, err := inputio.DecodeStrict[inputs.SettingsTemplateStatesSetInput](raw)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_input", err.Error(), nil)
		return
	}
	in := *parsed
	if in.Slug != "" && in.Slug != string(mode) {
		writeError(w, http.StatusBadRequest, "invalid_input",
			"slug in body must match URL", map[string]any{"field": "slug"})
		return
	}
	if len(in.States) == 0 {
		writeError(w, http.StatusBadRequest, "invalid_input",
			"states is required; use DELETE to revert a stage to its default",
			map[string]any{"field": "states"})
		return
	}
	states := make([]model.State, len(in.States))
	for i, s := range in.States {
		states[i] = model.State(s)
	}
	if isDryRun(r) {
		clean, err := d.store.ValidatePromptStates(mode, states)
		if err != nil {
			status, code := statusForError(err)
			writeError(w, status, code, err.Error(), nil)
			return
		}
		writeDryRun(w, http.StatusOK, &PromptTemplateStates{
			Mode: string(mode), States: stateStrings(clean),
		})
		return
	}
	if err := d.store.SetPromptStates(mode, states); err != nil {
		status, code := statusForError(err)
		writeError(w, status, code, err.Error(), nil)
		return
	}
	recordOp(d.store, d.logger, model.HistoryEntry{
		Actor:       ActorFromContext(r.Context()),
		Op:          "prompt_states.update",
		Kind:        "app_setting",
		TargetLabel: "prompt_states." + string(mode),
		Details:     "stage=" + string(mode),
	})
	resolved, err := d.store.GetPromptStates(mode)
	if err != nil {
		status, code := statusForError(err)
		writeError(w, status, code, err.Error(), nil)
		return
	}
	writeJSON(w, http.StatusOK, &PromptTemplateStates{
		Mode: string(mode), States: stateStrings(resolved),
	})
}

// ---------- reset states ----------

func (d deps) handlePromptStatesReset(w http.ResponseWriter, r *http.Request) {
	mode, ok := parseModePath(w, r)
	if !ok {
		return
	}
	if isDryRun(r) {
		writeDryRun(w, http.StatusOK, &PromptTemplateStates{
			Mode: string(mode), States: stateStrings(model.DefaultPromptStates(mode)),
		})
		return
	}
	if err := d.store.SetPromptStates(mode, nil); err != nil {
		status, code := statusForError(err)
		writeError(w, status, code, err.Error(), nil)
		return
	}
	recordOp(d.store, d.logger, model.HistoryEntry{
		Actor:       ActorFromContext(r.Context()),
		Op:          "prompt_states.reset",
		Kind:        "app_setting",
		TargetLabel: "prompt_states." + string(mode),
		Details:     "stage=" + string(mode),
	})
	writeJSON(w, http.StatusOK, &PromptTemplateStates{
		Mode: string(mode), States: stateStrings(model.DefaultPromptStates(mode)),
	})
}

// parseModePath pulls {mode} from the URL and validates it's a concrete
// dispatch stage. Rejects the untyped mode ("") — every settings verb
// names a stage. Returns ok=false after writing the error response.
func parseModePath(w http.ResponseWriter, r *http.Request) (model.DispatchMode, bool) {
	raw := r.PathValue("mode")
	mode, err := model.ParseDispatchMode(raw)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_input", err.Error(),
			map[string]any{"field": "mode"})
		return "", false
	}
	if mode == "" {
		writeError(w, http.StatusBadRequest, "invalid_input",
			"a job stage is required (plan, implement, review, ship, fix_review)",
			map[string]any{"field": "mode"})
		return "", false
	}
	return mode, true
}

// stateStrings renders a []model.State as a []string in order, never
// nil — JSON consumers always see an array. Mirrors statesToStrings in
// internal/cli/settings.go (kept here so the api package doesn't take a
// CLI import).
func stateStrings(states []model.State) []string {
	out := make([]string, len(states))
	for i, st := range states {
		out[i] = string(st)
	}
	return out
}

// ---------- board preferences (BACI-47/D) ----------
//
// One scalar global flag today — board.hide_empty_columns. The desktop
// app was the only writer until the web bundle wanted parity; serving
// it from REST means the browser can drive the same toggle.

// BoardPreferencesOut is the response shape for GET/PUT
// /settings/board-preferences. Keep the underscore form on the wire so
// it matches every other JSON field in this package; the desktop's
// reshape into the camelCase BoardPreferencesDTO lives in api.http.ts.
type BoardPreferencesOut struct {
	HideEmptyColumns bool `json:"hide_empty_columns"`
}

// boardPreferencesIn is the strict-decoded input for PUT.
type boardPreferencesIn struct {
	HideEmptyColumns bool `json:"hide_empty_columns"`
}

func (d deps) handleBoardPreferencesGet(w http.ResponseWriter, r *http.Request) {
	hide, err := d.store.GetBoardHideEmptyColumns()
	if err != nil {
		status, code := statusForError(err)
		writeError(w, status, code, err.Error(), nil)
		return
	}
	writeJSON(w, http.StatusOK, &BoardPreferencesOut{HideEmptyColumns: hide})
}

func (d deps) handleBoardPreferencesSet(w http.ResponseWriter, r *http.Request) {
	raw, ok := readBody(r, w)
	if !ok {
		return
	}
	parsed, _, err := inputio.DecodeStrict[boardPreferencesIn](raw)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_input", err.Error(), nil)
		return
	}
	if isDryRun(r) {
		writeDryRun(w, http.StatusOK, &BoardPreferencesOut{HideEmptyColumns: parsed.HideEmptyColumns})
		return
	}
	if err := d.store.SetBoardHideEmptyColumns(parsed.HideEmptyColumns); err != nil {
		status, code := statusForError(err)
		writeError(w, status, code, err.Error(), nil)
		return
	}
	// Consistent with the prompt-template REST verbs which all audit.
	// The local-path desktop writer doesn't audit today; this asymmetry
	// is fine — the REST surface is where multi-client traffic shows up
	// and history is most useful.
	recordOp(d.store, d.logger, model.HistoryEntry{
		Actor:       ActorFromContext(r.Context()),
		Op:          "board_pref.update",
		Kind:        "app_setting",
		TargetLabel: "board.hide_empty_columns",
		Details:     fmt.Sprintf("hide_empty_columns=%t", parsed.HideEmptyColumns),
	})
	writeJSON(w, http.StatusOK, &BoardPreferencesOut{HideEmptyColumns: parsed.HideEmptyColumns})
}

// ---------- typed prompt-template CRUD (BACI-50) ----------
//
// These mirror the four `bacio settings template ...` verbs and the
// matching client.Client methods. The store calls live behind the
// localClient so the audit-row Op names stay consistent with the CLI
// path: template.create / template.rename / template.delete /
// template.restore_defaults. Delete lives at
// /settings/templates/{slug}/row to keep the existing
// DELETE /settings/templates/{mode} body-reset endpoint working —
// that endpoint resets a built-in's body without removing the row.

// parseSlugPath pulls {slug} from the URL and validates it as a
// dispatch slug. Distinct from parseModePath because the typed CRUD
// surface accepts user-created slugs (not just the built-in modes).
func parseSlugPath(w http.ResponseWriter, r *http.Request) (string, bool) {
	raw := strings.TrimSpace(r.PathValue("slug"))
	slug, err := store.ValidatePromptTemplateSlug(raw)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_input", err.Error(),
			map[string]any{"field": "slug"})
		return "", false
	}
	return slug, true
}

func (d deps) handlePromptTemplateAdd(w http.ResponseWriter, r *http.Request) {
	raw, ok := readBody(r, w)
	if !ok {
		return
	}
	parsed, _, err := inputio.DecodeStrict[inputs.SettingsTemplateAddInput](raw)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_input", err.Error(), nil)
		return
	}
	c := client.NewLocalFromStore(d.store, ActorFromContext(r.Context()))
	defer c.Close()
	t, err := c.AddPromptTemplate(r.Context(), *parsed, isDryRun(r))
	if err != nil {
		status, code := statusForError(err)
		writeError(w, status, code, err.Error(), nil)
		return
	}
	if isDryRun(r) {
		writeDryRun(w, http.StatusCreated, templateDTO(t))
		return
	}
	writeJSON(w, http.StatusCreated, templateDTO(t))
}

func (d deps) handlePromptTemplateRename(w http.ResponseWriter, r *http.Request) {
	slug, ok := parseSlugPath(w, r)
	if !ok {
		return
	}
	raw, ok := readBody(r, w)
	if !ok {
		return
	}
	parsed, _, err := inputio.DecodeStrict[inputs.SettingsTemplateRenameInput](raw)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_input", err.Error(), nil)
		return
	}
	in := *parsed
	// URL slug is authoritative; the body's "slug" field is optional
	// and must match if present (mirrors the body-set endpoint's
	// behaviour).
	if in.Slug != "" && in.Slug != slug {
		writeError(w, http.StatusBadRequest, "invalid_input",
			"slug in body must match URL", map[string]any{"field": "slug"})
		return
	}
	in.Slug = slug
	c := client.NewLocalFromStore(d.store, ActorFromContext(r.Context()))
	defer c.Close()
	t, err := c.RenamePromptTemplate(r.Context(), in, isDryRun(r))
	if err != nil {
		status, code := statusForError(err)
		writeError(w, status, code, err.Error(), nil)
		return
	}
	if isDryRun(r) {
		writeDryRun(w, http.StatusOK, templateDTO(t))
		return
	}
	writeJSON(w, http.StatusOK, templateDTO(t))
}

func (d deps) handlePromptTemplateDelete(w http.ResponseWriter, r *http.Request) {
	slug, ok := parseSlugPath(w, r)
	if !ok {
		return
	}
	c := client.NewLocalFromStore(d.store, ActorFromContext(r.Context()))
	defer c.Close()
	t, err := c.DeletePromptTemplate(r.Context(),
		inputs.SettingsTemplateRmInput{Slug: slug}, isDryRun(r))
	if err != nil {
		status, code := statusForError(err)
		writeError(w, status, code, err.Error(), nil)
		return
	}
	if isDryRun(r) {
		writeDryRun(w, http.StatusOK, templateDTO(t))
		return
	}
	writeJSON(w, http.StatusOK, templateDTO(t))
}

// promptTemplateRestoreResponse is the POST /settings/templates/restore-builtins
// response. Restored lists the slugs that were freshly re-seeded
// (empty array when every built-in was already present); Templates is
// the full list after the call so the UI can replace its state in one
// shot — mirrors desktop SettingsService.RestoreBuiltinPromptTemplates.
type promptTemplateRestoreResponse struct {
	Restored  []string                `json:"restored"`
	Templates []PromptTemplateFullDTO `json:"templates"`
}

func (d deps) handlePromptTemplateRestore(w http.ResponseWriter, r *http.Request) {
	c := client.NewLocalFromStore(d.store, ActorFromContext(r.Context()))
	defer c.Close()
	restored, err := c.RestoreBuiltinPromptTemplates(r.Context(), isDryRun(r))
	if err != nil {
		status, code := statusForError(err)
		writeError(w, status, code, err.Error(), nil)
		return
	}
	if restored == nil {
		restored = []string{}
	}
	// List after the call so the response carries the post-state — the
	// UI replaces its template list with this and renders.
	tmpls, err := d.store.ListPromptTemplates()
	if err != nil {
		status, code := statusForError(err)
		writeError(w, status, code, err.Error(), nil)
		return
	}
	out := make([]PromptTemplateFullDTO, 0, len(tmpls))
	for _, t := range tmpls {
		out = append(out, templateDTO(t))
	}
	resp := &promptTemplateRestoreResponse{Restored: restored, Templates: out}
	if isDryRun(r) {
		writeDryRun(w, http.StatusOK, resp)
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

// ---------- set concurrency (BACI-51) ----------

// PromptTemplateConcurrency is the per-mode response shape for
// PUT /settings/templates/{mode}/concurrency — the post-call resolved
// concurrency_limit (the matcher's per-(repo, mode) in-flight cap;
// 0 = unlimited).
type PromptTemplateConcurrency struct {
	Mode             string `json:"mode"`
	ConcurrencyLimit int    `json:"concurrency_limit"`
}

type promptTemplateConcurrencyIn struct {
	ConcurrencyLimit int `json:"concurrency_limit"`
}

func (d deps) handlePromptTemplateConcurrencySet(w http.ResponseWriter, r *http.Request) {
	mode, ok := parseModePath(w, r)
	if !ok {
		return
	}
	raw, ok := readBody(r, w)
	if !ok {
		return
	}
	parsed, _, err := inputio.DecodeStrict[promptTemplateConcurrencyIn](raw)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_input", err.Error(), nil)
		return
	}
	if isDryRun(r) {
		// Validate without writing — same shape every other dry-run path uses.
		cleaned, err := store.ValidateConcurrencyLimit(parsed.ConcurrencyLimit)
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid_input", err.Error(), nil)
			return
		}
		writeDryRun(w, http.StatusOK, &PromptTemplateConcurrency{
			Mode: string(mode), ConcurrencyLimit: cleaned,
		})
		return
	}
	updated, err := d.store.SetPromptTemplateConcurrencyLimit(string(mode), parsed.ConcurrencyLimit)
	if err != nil {
		status, code := statusForError(err)
		writeError(w, status, code, err.Error(), nil)
		return
	}
	recordOp(d.store, d.logger, model.HistoryEntry{
		Actor:       ActorFromContext(r.Context()),
		Op:          "template.set_concurrency",
		Kind:        "app_setting",
		TargetID:    &updated.ID,
		TargetLabel: "prompt_template:" + updated.Slug,
		Details:     fmt.Sprintf("slug=%s, concurrency_limit=%d", updated.Slug, updated.ConcurrencyLimit),
	})
	writeJSON(w, http.StatusOK, &PromptTemplateConcurrency{
		Mode: string(mode), ConcurrencyLimit: updated.ConcurrencyLimit,
	})
}
