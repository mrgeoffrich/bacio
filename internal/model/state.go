package model

import (
	"fmt"
	"strings"
)

type State string

const (
	StateTodo      State = "todo"
	StateInReview  State = "in_review"
	StateDone      State = "done"
	StateCancelled State = "cancelled"
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
	StateTodo, StateInReview,
	StateDone, StateCancelled, StateInPipeline, StateToBeShipped,
}

func AllStates() []State { return append([]State(nil), allStates...) }

// boardColumnStates are the columns the legacy state-kanban renders (the
// TUI board and the pre-deprecation web issues board). The two Pipeline
// states are deliberately excluded — those cards live on the Pipeline
// page, not the kanban — but they remain in allStates so ParseState
// still accepts the full enum.
var boardColumnStates = []State{
	StateTodo, StateInReview,
	StateDone, StateCancelled,
}

// BoardColumnStates returns the states the legacy column-kanban renders,
// excluding the Pipeline columns (in_pipeline / to_be_shipped).
func BoardColumnStates() []State { return append([]State(nil), boardColumnStates...) }

// ReleaseFallbackState is the state a claim-only `bacio agent release`
// (no explicit --state) lands an issue in, given its current state. The
// Pipeline is now the way work flows, so the legacy in_progress /
// needs_action states are gone and a claim is a focus marker that no
// longer moves an off-pipeline card's state. A release therefore never
// moves an off-pipeline card either — it stays exactly where it was,
// and the engine (for pipeline cards) or a human (for everything else)
// is the only thing that advances it. This is the single place that
// decides where a state-neutral release leaves the card:
//
//   - Engine-governed cards (in_pipeline / to_be_shipped) keep their
//     state: the controller engine owns their progression, and the
//     store's engine-governed-state guard would no-op a processing-state
//     write regardless.
//   - Every other card keeps its current state too — there is no resting
//     "an agent finished a pass" column anymore; the card simply stays
//     put. Returning the current state keeps the dry-run projection
//     honest (no phantom move).
func ReleaseFallbackState(current State) State {
	return current
}

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
