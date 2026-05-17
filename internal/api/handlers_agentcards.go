package api

// BACI-50: composite Agents endpoint. The desktop's BoardService.ListAgents
// already assembled an AgentCard from five reads (sessions, todos bulk,
// dispatches per-repo, issues per-repo, per-session claims); the
// assembly now lives in internal/agentcards so this handler can serve
// the same shape to a browser.
//
// Two variants mirror the existing agent-registry routes:
//   - GET /repos/{prefix}/agents/cards — one repo
//   - GET /agents/cards               — every repo

import (
	"net/http"

	"github.com/mrgeoffrich/bacio/internal/agentcards"
	"github.com/mrgeoffrich/bacio/internal/client"
)

func (d deps) handleAgentCardsListRepo(w http.ResponseWriter, r *http.Request) {
	repo, ok := resolveRepoFromPath(w, r, d.store)
	if !ok {
		return
	}
	c := client.NewLocalFromStore(d.store, ActorFromContext(r.Context()))
	defer c.Close()
	cards, err := agentcards.Assemble(r.Context(), c, repo)
	if err != nil {
		status, code := statusForError(err)
		writeError(w, status, code, err.Error(), nil)
		return
	}
	if cards == nil {
		cards = []agentcards.AgentCard{}
	}
	writeJSON(w, http.StatusOK, cards)
}

func (d deps) handleAgentCardsListAll(w http.ResponseWriter, r *http.Request) {
	c := client.NewLocalFromStore(d.store, ActorFromContext(r.Context()))
	defer c.Close()
	cards, err := agentcards.Assemble(r.Context(), c, nil)
	if err != nil {
		status, code := statusForError(err)
		writeError(w, status, code, err.Error(), nil)
		return
	}
	if cards == nil {
		cards = []agentcards.AgentCard{}
	}
	writeJSON(w, http.StatusOK, cards)
}
