// Package inputio holds JSON-input helpers shared between the CLI and the
// HTTP API. Both surfaces decode the same inputs.*Input structs and need
// identical strict-decode + presence-map semantics.
package inputio

import (
	"bytes"
	"encoding/json"
	"fmt"
)

// DecodeStrict parses raw JSON into *T using DisallowUnknownFields. It also
// returns a presence map of the top-level keys actually written by the
// caller, so edit-style commands can distinguish "field omitted" from
// "field set to empty/null".
func DecodeStrict[T any](raw []byte) (*T, map[string]json.RawMessage, error) {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 {
		return nil, nil, fmt.Errorf("empty JSON payload")
	}
	var keys map[string]json.RawMessage
	if err := json.Unmarshal(raw, &keys); err != nil {
		return nil, nil, fmt.Errorf("parse JSON: %w", err)
	}
	var v T
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&v); err != nil {
		return nil, nil, fmt.Errorf("decode JSON: %w", err)
	}
	return &v, keys, nil
}
