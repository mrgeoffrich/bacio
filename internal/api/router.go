package api

import (
	"log/slog"
	"net/http"

	"github.com/mrgeoffrich/bacio/internal/store"
)

// deps is the unexported handler-context bag. Each handler is a method on
// deps so it can reach the store and config without pulling globals.
type deps struct {
	store  *store.Store
	opts   Options
	logger *slog.Logger
}

func newRouter(d deps) http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("GET /healthz", d.handleHealthz)

	mux.HandleFunc("GET /schema", d.handleSchemaAll)
	mux.HandleFunc("GET /schema/list", d.handleSchemaList)
	mux.HandleFunc("GET /schema/{name}", d.handleSchemaShow)

	mux.HandleFunc("GET /repos", d.handleReposList)
	mux.HandleFunc("POST /repos", d.handleReposCreate)
	mux.HandleFunc("GET /repos/{prefix}", d.handleReposShow)
	mux.HandleFunc("DELETE /repos/{prefix}", d.handleReposDelete)

	mux.HandleFunc("GET /repos/{prefix}/features", d.handleFeaturesList)
	mux.HandleFunc("POST /repos/{prefix}/features", d.handleFeatureCreate)
	mux.HandleFunc("GET /repos/{prefix}/features/{slug}", d.handleFeatureShow)
	mux.HandleFunc("PATCH /repos/{prefix}/features/{slug}", d.handleFeatureEdit)
	mux.HandleFunc("DELETE /repos/{prefix}/features/{slug}", d.handleFeatureDelete)
	mux.HandleFunc("GET /repos/{prefix}/features/{slug}/plan", d.handleFeaturePlan)
	mux.HandleFunc("GET /repos/{prefix}/features/{slug}/next", d.handleFeatureNextPeek)
	mux.HandleFunc("POST /repos/{prefix}/features/{slug}/next", d.handleFeatureNextClaim)

	mux.HandleFunc("GET /repos/{prefix}/issues", d.handleIssuesList)
	mux.HandleFunc("POST /repos/{prefix}/issues", d.handleIssueCreate)
	mux.HandleFunc("GET /repos/{prefix}/issues/{key}", d.handleIssueShow)
	mux.HandleFunc("GET /repos/{prefix}/issues/{key}/brief", d.handleIssueBrief)
	mux.HandleFunc("PATCH /repos/{prefix}/issues/{key}", d.handleIssueEdit)
	mux.HandleFunc("DELETE /repos/{prefix}/issues/{key}", d.handleIssueDelete)
	mux.HandleFunc("PUT /repos/{prefix}/issues/{key}/state", d.handleIssueState)
	mux.HandleFunc("PUT /repos/{prefix}/issues/{key}/assignee", d.handleIssueAssign)
	mux.HandleFunc("DELETE /repos/{prefix}/issues/{key}/assignee", d.handleIssueUnassign)

	mux.HandleFunc("GET /repos/{prefix}/issues/{key}/comments", d.handleCommentsList)
	mux.HandleFunc("POST /repos/{prefix}/issues/{key}/comments", d.handleCommentAdd)

	mux.HandleFunc("POST /repos/{prefix}/relations", d.handleRelationCreate)
	mux.HandleFunc("DELETE /repos/{prefix}/relations", d.handleRelationDelete)

	mux.HandleFunc("GET /repos/{prefix}/issues/{key}/pull-requests", d.handlePRsList)
	mux.HandleFunc("POST /repos/{prefix}/issues/{key}/pull-requests", d.handlePRAttach)
	mux.HandleFunc("DELETE /repos/{prefix}/issues/{key}/pull-requests", d.handlePRDetach)

	mux.HandleFunc("POST /repos/{prefix}/issues/{key}/tags", d.handleTagsAdd)
	mux.HandleFunc("DELETE /repos/{prefix}/issues/{key}/tags", d.handleTagsRemove)

	mux.HandleFunc("GET /repos/{prefix}/documents", d.handleDocumentsList)
	mux.HandleFunc("POST /repos/{prefix}/documents", d.handleDocumentCreate)
	mux.HandleFunc("GET /repos/{prefix}/documents/{filename}", d.handleDocumentShow)
	mux.HandleFunc("PUT /repos/{prefix}/documents/{filename}", d.handleDocumentUpsert)
	mux.HandleFunc("PATCH /repos/{prefix}/documents/{filename}", d.handleDocumentEdit)
	mux.HandleFunc("DELETE /repos/{prefix}/documents/{filename}", d.handleDocumentDelete)
	mux.HandleFunc("GET /repos/{prefix}/documents/{filename}/download", d.handleDocumentDownload)
	mux.HandleFunc("POST /repos/{prefix}/documents/{filename}/rename", d.handleDocumentRename)
	mux.HandleFunc("POST /repos/{prefix}/documents/{filename}/links", d.handleDocumentLink)
	mux.HandleFunc("DELETE /repos/{prefix}/documents/{filename}/links", d.handleDocumentUnlink)

	mux.HandleFunc("GET /history", d.handleHistoryAll)
	mux.HandleFunc("GET /repos/{prefix}/history", d.handleHistoryRepo)

	// Agent registry (BACI-34). Local-only data, but reachable over HTTP
	// so a remote frontend can drive the same surface as the desktop.
	// Repo-scoped: register a session, list sessions in a repo, list open
	// claims in a repo. Session-scoped: heartbeat / end / claim / release /
	// show / inbox. Dispatch-scoped: ack. Cross-repo variants of the two
	// lists mirror the /history pattern.
	mux.HandleFunc("POST /repos/{prefix}/agents/sessions", d.handleAgentRegister)
	mux.HandleFunc("GET /repos/{prefix}/agents/sessions", d.handleAgentSessionsListRepo)
	mux.HandleFunc("GET /repos/{prefix}/agents/claims/open", d.handleAgentClaimsOpenRepo)
	mux.HandleFunc("GET /agents/sessions", d.handleAgentSessionsList)
	mux.HandleFunc("GET /agents/sessions/{session_id}", d.handleAgentSessionShow)
	mux.HandleFunc("POST /agents/sessions/{session_id}/heartbeat", d.handleAgentHeartbeat)
	mux.HandleFunc("POST /agents/sessions/{session_id}/end", d.handleAgentEnd)
	mux.HandleFunc("POST /agents/sessions/{session_id}/claims", d.handleAgentClaim)
	mux.HandleFunc("DELETE /agents/sessions/{session_id}/claims", d.handleAgentRelease)
	mux.HandleFunc("GET /agents/sessions/{session_id}/inbox", d.handleAgentInbox)
	mux.HandleFunc("POST /agents/dispatches/{id}/ack", d.handleAgentDispatchAck)
	mux.HandleFunc("GET /agents/claims/open", d.handleAgentClaimsOpen)
	// Dispatch CRUD (BACI-35) rounds out the four dispatch verbs — inbox
	// and ack already shipped with BACI-34. Repo-scoped because a
	// dispatch is always queued against one repo.
	mux.HandleFunc("POST /repos/{prefix}/agents/dispatches", d.handleAgentDispatchCreate)
	mux.HandleFunc("GET /repos/{prefix}/agents/dispatches", d.handleAgentDispatchesList)
	// State-gated auto-pick dispatch (BACI-40): re-check the stage's
	// state-gate against the issue's current state, pick the most-
	// recently-active free agent, and queue the dispatch. Mirrors the
	// desktop per-card action button and the CLI's target-less
	// `bacio agent dispatch <key> --mode <stage>`.
	mux.HandleFunc("POST /repos/{prefix}/issues/{key}/dispatch", d.handleIssueDispatch)

	// Outermost first: panic recovery wraps everything so a bug in any
	// later layer still returns a 500 envelope.
	var h http.Handler = mux
	h = bodyCap(h, 4<<20)
	h = auth(h, d.opts.Token)
	h = actorMiddleware(h)
	h = requestLog(h, d.logger)
	h = recoverPanic(h, d.logger)
	return h
}
