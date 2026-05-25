package model

import (
	"fmt"
	"strings"
)

// FeatureState is the three-state column on features (BACI-199):
// `active` (the default, work in flight), `done` (delivered) and
// `cancelled` (abandoned). Deliberately a separate type from the
// issue-side `State` so a typo can't compile — the issue enum carries
// values (`in_progress`, `needs_action`, ...) that don't exist on a
// feature, and the feature enum may grow values issues don't care
// about. The auto-completion sweep keys off this column plus the
// sibling `state_manual` sticky-bit so a manually-set value is never
// overwritten by the sweep without explicit user action.
type FeatureState string

const (
	FeatureStateActive    FeatureState = "active"
	FeatureStateDone      FeatureState = "done"
	FeatureStateCancelled FeatureState = "cancelled"
)

var allFeatureStates = []FeatureState{
	FeatureStateActive, FeatureStateDone, FeatureStateCancelled,
}

// AllFeatureStates returns a defensive copy of the enum's full set.
func AllFeatureStates() []FeatureState {
	return append([]FeatureState(nil), allFeatureStates...)
}

// ParseFeatureState accepts "done", "Done", "cancelled", "ACTIVE", and
// (for symmetry with model.ParseState) dashed / spaced variants like
// "in-progress" — even though no current value carries a separator,
// the normaliser keeps the two parsers behaviourally aligned.
func ParseFeatureState(s string) (FeatureState, error) {
	norm := strings.ToLower(strings.NewReplacer(" ", "_", "-", "_").Replace(strings.TrimSpace(s)))
	for _, st := range allFeatureStates {
		if string(st) == norm {
			return st, nil
		}
	}
	return "", fmt.Errorf("unknown feature state %q (valid: %s)", s, strings.Join(featureStateStrings(), ", "))
}

func featureStateStrings() []string {
	out := make([]string, len(allFeatureStates))
	for i, s := range allFeatureStates {
		out[i] = string(s)
	}
	return out
}
