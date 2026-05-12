package model

import (
	"fmt"
	"strings"
)

type State string

const (
	StateTodo        State = "todo"
	StateInProgress  State = "in_progress"
	// StateNeedsAction parks an issue while an LLM agent is waiting on
	// the user for input — the assignee stays, but the column signals
	// that human attention (not more agent work) is the next step.
	StateNeedsAction State = "needs_action"
	StateInReview    State = "in_review"
	StateDone        State = "done"
	StateCancelled   State = "cancelled"
)

var allStates = []State{
	StateTodo, StateInProgress, StateNeedsAction, StateInReview,
	StateDone, StateCancelled,
}

func AllStates() []State { return append([]State(nil), allStates...) }

// ParseState accepts "in-progress", "in progress", "in_progress", "InProgress", etc.
func ParseState(s string) (State, error) {
	norm := strings.ToLower(strings.NewReplacer(" ", "_", "-", "_").Replace(strings.TrimSpace(s)))
	for _, st := range allStates {
		if string(st) == norm {
			return st, nil
		}
	}
	return "", fmt.Errorf("unknown state %q (valid: %s)", s, strings.Join(stateStrings(), ", "))
}

func stateStrings() []string {
	out := make([]string, len(allStates))
	for i, s := range allStates {
		out[i] = string(s)
	}
	return out
}
