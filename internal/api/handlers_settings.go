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

	"github.com/mrgeoffrich/bacio/internal/cli/inputs"
	"github.com/mrgeoffrich/bacio/internal/inputio"
	"github.com/mrgeoffrich/bacio/internal/model"
)

// PromptTemplateBody is the per-mode response shape for PUT/DELETE on
// /settings/templates/{mode}. Body is the post-call resolved value (the
// stored override, or the built-in default when no override is set).
type PromptTemplateBody struct {
	Mode string `json:"mode"`
	Body string `json:"body"`
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
