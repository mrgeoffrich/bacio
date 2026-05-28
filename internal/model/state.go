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
	// StateInPipeline / StateToBeShipped are the Pipeline-page columns.
	// A card's column is its issue state: In Pipeline = a chain of
	// dispatch jobs is being run against it by the controller engine;
	// To Be Shipped = the FIFO queue of finished cards waiting to ship.
	// Both are non-terminal. They are appended after the legacy set so
	// every surface that builds columns from AllStates() keeps its
	// existing ordering; the Pipeline UI keys on these states directly.
	StateInPipeline  State = "in_pipeline"
	StateToBeShipped State = "to_be_shipped"
)

var allStates = []State{
	StateTodo, StateInProgress, StateNeedsAction, StateInReview,
	StateDone, StateCancelled, StateInPipeline, StateToBeShipped,
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
