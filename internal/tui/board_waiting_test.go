package tui

import (
	"testing"

	"github.com/mrgeoffrich/bacio/internal/boardcards"
	"github.com/mrgeoffrich/bacio/internal/model"
)

// TestWaitingStateLabelTUI — BACI-145. Locks in the wording rendered
// on the bottom row of a waiting card, mirroring the React surface's
// desktop/frontend/src/lib/waitingLabels.ts helper. The Go string and
// the TS string MUST stay in lockstep: edit them together.
func TestWaitingStateLabelTUI(t *testing.T) {
	tests := []struct {
		name string
		ws   *boardcards.WaitingState
		want string
	}{
		{"nil", nil, ""},
		{
			"no agent",
			&boardcards.WaitingState{Kind: boardcards.WaitingNoAgent},
			"Waiting for an available agent",
		},
		{
			"blocked by cap with named verb",
			&boardcards.WaitingState{Kind: boardcards.WaitingBlockedByMode, Mode: model.DispatchMode("ship"), ActionLabel: "Ship"},
			"Waiting on Ship job to finish",
		},
		{
			"blocked by cap, missing verb falls back",
			&boardcards.WaitingState{Kind: boardcards.WaitingBlockedByMode},
			"Waiting on prior job to finish",
		},
		{
			"delivered with named verb",
			&boardcards.WaitingState{Kind: boardcards.WaitingDelivered, Mode: model.DispatchMode("ship"), ActionLabel: "Ship"},
			"Worker has the Ship job",
		},
		{
			"delivered, missing verb falls back",
			&boardcards.WaitingState{Kind: boardcards.WaitingDelivered},
			"Worker is on this job",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := waitingStateLabelTUI(tc.ws); got != tc.want {
				t.Errorf("waitingStateLabelTUI(%+v) = %q, want %q", tc.ws, got, tc.want)
			}
		})
	}
}
