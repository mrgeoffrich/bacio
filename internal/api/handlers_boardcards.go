package api

// BACI-60: composite kanban-cards endpoint. Mirrors the
// agentcards handler — the desktop's BoardService.ListCards already
// assembled a BoardCard from issue rows + open claims + (newly added
// in BACI-60) sessions + dispatches + todos + prompt templates;
// boardcards.Assemble is the shared assembler so the web bundle can
// fetch the same wire shape over REST instead of reshaping raw
// model.Issue rows client-side (which would miss the new
// ActiveVerb / TodosDone / TodosTotal fields that don't live on
// the Issue at all).
//
// Only the single-repo variant is wired today — the web bundle's
// "all repos" pseudo-board is gated behind a v2 follow-up
// (see api.http.ts:listCards).

import (
	"net/http"

	"github.com/mrgeoffrich/bacio/internal/boardcards"
	"github.com/mrgeoffrich/bacio/internal/client"
)

func (d deps) handleBoardCardsListRepo(w http.ResponseWriter, r *http.Request) {
	repo, ok := resolveRepoFromPath(w, r, d.store)
	if !ok {
		return
	}
	// BACI-68: per-call ?include_archived=1 OR the display.show_archived
	// global setting opt the caller in to archived rows. The per-call
	// query wins over the setting in either direction (true OR true is
	// the only path to true, so we OR them) — the CLI does the same on
	// `bacio issue list --include-archived`.
	includeArchived := r.URL.Query().Get("include_archived") == "true" || r.URL.Query().Get("include_archived") == "1"
	if !includeArchived {
		v, _ := d.store.GetDisplayShowArchived()
		includeArchived = v
	}
	c := client.NewLocalFromStore(d.store, ActorFromContext(r.Context()))
	defer c.Close()
	cards, err := boardcards.Assemble(r.Context(), c, repo, includeArchived)
	if err != nil {
		status, code := statusForError(err)
		writeError(w, status, code, err.Error(), nil)
		return
	}
	if cards == nil {
		cards = []boardcards.BoardCard{}
	}
	writeJSON(w, http.StatusOK, cards)
}
