package model

import (
	"fmt"
	"strings"
)

type State string

const (
	StateBacklog    State = "backlog"
	StateTodo       State = "todo"
	StateInProgress State = "in_progress"
	StateInReview   State = "in_review"
	StateDone       State = "done"
	StateCancelled  State = "cancelled"
	StateDuplicate  State = "duplicate"
)

var allStates = []State{
	StateBacklog, StateTodo, StateInProgress, StateInReview,
	StateDone, StateCancelled, StateDuplicate,
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
