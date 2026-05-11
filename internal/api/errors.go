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
	} {
		if strings.Contains(msg, frag) {
			return http.StatusBadRequest, "invalid_input"
		}
	}
	return http.StatusInternalServerError, "internal"
}
