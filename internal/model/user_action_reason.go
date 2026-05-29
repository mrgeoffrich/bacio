package model

import (
	"fmt"
	"strings"
)

// UserActionReasonType is the typed reason an issue is parked in
// `needs_action` — the bucket today screams "the agent is waiting on
// you" uniformly, so the UI can't differentiate "answer a clarifying
// question" from "give the work a once-over". BACI-220 wires the
// `user_question` value so a `mcp__bacio__ask_user_question` call
// stamps the issue with the typed reason; the `user_manual_review`
// value is defined for the follow-up that lets a finished worker ask
// the user to sanity-check the change. The column is NULL whenever the
// issue is not in `needs_action`.
type UserActionReasonType string

const (
	// UserActionReasonQuestion — the agent asked a clarifying question
	// via `mcp__bacio__ask_user_question` and is parked waiting for the
	// answer. Set on the auto-flip path that already moves the linked
	// issue to `needs_action`; cleared when the question is answered or
	// cancelled and the auto-flip walks the issue back to `in_progress`.
	UserActionReasonQuestion UserActionReasonType = "user_question"
	// UserActionReasonManualReview — the agent has finished its work
	// and wants the user to eyeball the result before moving on. Enum
	// value only at this stage; no wiring lands until the follow-up
	// ticket adds the CLI / MCP verb that sets it.
	UserActionReasonManualReview UserActionReasonType = "user_manual_review"
	// UserActionReasonAgentError — the worker running a Pipeline job died
	// on a terminal Anthropic API error (auth / billing / org-not-allowed,
	// or an unclassified failure). The StopFailure hook (BACI-296) tears
	// the chain down and moves the card out of the pipeline to
	// `needs_action` stamped with this reason, so the user is pulled in to
	// fix the underlying account/config problem — there's no point
	// auto-retrying a billing error.
	UserActionReasonAgentError UserActionReasonType = "agent_error"
)

var allUserActionReasons = []UserActionReasonType{
	UserActionReasonQuestion, UserActionReasonManualReview, UserActionReasonAgentError,
}

// AllUserActionReasons returns the full enum slice; the returned slice
// is a copy so callers can't mutate the package-internal source.
func AllUserActionReasons() []UserActionReasonType {
	return append([]UserActionReasonType(nil), allUserActionReasons...)
}

// ParseUserActionReason accepts the canonical underscore form
// (`user_question`, `user_manual_review`) plus the same dash / space
// tolerance the issue state parser applies, so an input like
// `user-question` or `User Manual Review` round-trips to the enum.
func ParseUserActionReason(s string) (UserActionReasonType, error) {
	norm := strings.ToLower(strings.NewReplacer(" ", "_", "-", "_").Replace(strings.TrimSpace(s)))
	for _, r := range allUserActionReasons {
		if string(r) == norm {
			return r, nil
		}
	}
	return "", fmt.Errorf("unknown user_action_reason_type %q (valid: %s)", s, strings.Join(userActionReasonStrings(), ", "))
}

func userActionReasonStrings() []string {
	out := make([]string, len(allUserActionReasons))
	for i, r := range allUserActionReasons {
		out[i] = string(r)
	}
	return out
}
