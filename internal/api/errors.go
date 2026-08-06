package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/mrgeoffrich/bacio/internal/store"
)

type errorBody struct {
	Error   string         `json:"error"`
	Code    string         `json:"code"`
	Details map[string]any `json:"details,omitempty"`
}

func writeError(w http.ResponseWriter, status int, code, msg string, details map[string]any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(errorBody{Error: msg, Code: code, Details: details})
}

// statusForError maps a store/decoder error onto an HTTP status + machine
// code. The string match for UNIQUE constraints is necessary because
// modernc.org/sqlite surfaces them as text rather than a typed sentinel.
func statusForError(err error) (int, string) {
	if err == nil {
		return http.StatusOK, ""
	}
	if errors.Is(err, store.ErrNotFound) {
		return http.StatusNotFound, "not_found"
	}
	// Pivot doc-folder refusals. All three describe a well-formed request
	// the *current tree shape* forbids — a sibling name already taken, a
	// move that would make a folder its own ancestor, a nesting past
	// MaxDocFolderDepth — so they are conflicts, not malformed input. The
	// name collision in particular is the store's typed replacement for
	// the UNIQUE-constraint text matched further down, and must land on
	// the same 409 that text would have.
	if errors.Is(err, store.ErrDocFolderExists) ||
		errors.Is(err, store.ErrDocFolderCycle) ||
		errors.Is(err, store.ErrDocFolderTooDeep) {
		return http.StatusConflict, "conflict"
	}
	// A uuid that resolves to another repo's row is reported as "not
	// found" rather than "forbidden": the per-repo routes must not
	// confirm that a row exists elsewhere. Same rule as
	// resolveIssueOnRepo. The handlers pre-resolve and 404 before the
	// client is reached, so this is the defence-in-depth arm.
	if errors.Is(err, store.ErrDocFolderOtherRepo) {
		return http.StatusNotFound, "not_found"
	}
	var maxErr *http.MaxBytesError
	if errors.As(err, &maxErr) {
		return http.StatusRequestEntityTooLarge, "payload_too_large"
	}
	var syntaxErr *json.SyntaxError
	if errors.As(err, &syntaxErr) {
		return http.StatusBadRequest, "invalid_input"
	}
	var typeErr *json.UnmarshalTypeError
	if errors.As(err, &typeErr) {
		return http.StatusBadRequest, "invalid_input"
	}
	msg := err.Error()
	if strings.Contains(msg, "UNIQUE constraint failed") {
		return http.StatusConflict, "conflict"
	}
	// store.CreateKanbanColumn / client.CreateWorkspace turn a taken lane
	// name and a taken repo prefix into friendly messages that no longer
	// carry the UNIQUE text. Both are collisions with an existing row.
	if kanbanNameCollision(err) || strings.Contains(msg, "is already in use") {
		return http.StatusConflict, "conflict"
	}
	// The client's cross-repo guards for lanes and issues are plain
	// fmt.Errorf values (only the doc-folder side has a sentinel). Same
	// non-leaking 404 as store.ErrDocFolderOtherRepo above.
	if strings.Contains(msg, "is not in repo ") {
		return http.StatusNotFound, "not_found"
	}
	if strings.Contains(msg, "unknown field") {
		return http.StatusBadRequest, "invalid_input"
	}
	// Validator errors from internal/store/validate.go are plain fmt.Errorf
	// values without a sentinel; pattern-match the recognisable phrases so a
	// bad title/body/url/slug surfaces as 400 instead of 500.
	for _, frag := range []string{
		"is required",
		"is not valid UTF-8",
		"too long",
		"disallowed control character",
		"must not contain",
		"must use http",
		"must include a host",
		"must be kebab-case",
		"is not allowed",
		"must not have leading",
		"invalid URL",
		"unknown state",
		"unknown relation",
		"invalid issue key",
		"invalid issue reference",
		"cannot be empty",
		"must not contain '/'",
		// client.MoveDocumentToFolder / MoveIssueToKanbanColumn on a
		// negative position — the one numeric input the pivot routes take.
		"position must be >=",
	} {
		if strings.Contains(msg, frag) {
			return http.StatusBadRequest, "invalid_input"
		}
	}
	return http.StatusInternalServerError, "internal"
}

// kanbanNameCollision reports the store's duplicate-lane refusal.
// store.CreateKanbanColumn deliberately replaces the raw
// `UNIQUE constraint failed` text with `kanban column %q already exists
// in this repo`, so there is no sentinel to match on and the phrase is
// the contract. Kept in one place so the status mapping and the
// field-attribution helper can't drift apart.
func kanbanNameCollision(err error) bool {
	return err != nil && strings.Contains(err.Error(), "already exists in this repo")
}
