package api

import (
	"log/slog"
	"net/http"
	"net/url"

	"github.com/mrgeoffrich/bacio/internal/proxy"
	"github.com/mrgeoffrich/bacio/internal/store"
)

// deps is the unexported handler-context bag. Each handler is a method on
// deps so it can reach the store and config without pulling globals.
//
// leader is the UI leader-election goroutine — nil when newRouter is used
// in tests that don't go through Server.Run. GET /leader reads its cached
// state; everything else ignores it.
type deps struct {
	store  *store.Store
	opts   Options
	logger *slog.Logger
	leader *apiLeaderService
}

func newRouter(d deps) http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("GET /healthz", d.handleHealthz)
	mux.HandleFunc("GET /version", d.handleVersion)
	mux.HandleFunc("GET /leader", d.handleLeader)

	mux.HandleFunc("GET /schema", d.handleSchemaAll)
	mux.HandleFunc("GET /schema/list", d.handleSchemaList)
	mux.HandleFunc("GET /schema/{name}", d.handleSchemaShow)

	mux.HandleFunc("GET /repos", d.handleReposList)
	mux.HandleFunc("POST /repos", d.handleReposCreate)
	mux.HandleFunc("GET /repos/{prefix}", d.handleReposShow)
	mux.HandleFunc("DELETE /repos/{prefix}", d.handleReposDelete)
	mux.HandleFunc("POST /repos/{prefix}/link", d.handleRepoLink)

	mux.HandleFunc("GET /repos/{prefix}/features", d.handleFeaturesList)
	mux.HandleFunc("POST /repos/{prefix}/features", d.handleFeatureCreate)
	// BACI-177: per-feature "Show on board" toggle. The "hidden"
	// literal segment is more specific than "{slug}" so ServeMux
	// disambiguates without conflict. The PUT lives at
	// /features/{slug}/hide for symmetry with /archive + /unarchive.
	mux.HandleFunc("GET /repos/{prefix}/features/hidden", d.handleFeaturesHidden)
	mux.HandleFunc("GET /repos/{prefix}/features/{slug}", d.handleFeatureShow)
	mux.HandleFunc("PATCH /repos/{prefix}/features/{slug}", d.handleFeatureEdit)
	mux.HandleFunc("DELETE /repos/{prefix}/features/{slug}", d.handleFeatureDelete)
	mux.HandleFunc("PUT /repos/{prefix}/features/{slug}/hide", d.handleFeatureHide)
	// BACI-199: per-feature three-state column. PUT to align with the
	// issue-side `/issues/{key}/state` shape; archive / unarchive
	// remain orthogonal under /archive + /unarchive.
	mux.HandleFunc("PUT /repos/{prefix}/features/{slug}/state", d.handleFeatureState)
	// BACI-250: per-feature auto-close toggle — the sticky `state_manual`
	// bit decoupled from the state-flip path. Body: {"enabled": bool}.
	mux.HandleFunc("PUT /repos/{prefix}/features/{slug}/auto-close", d.handleFeatureAutoClose)
	mux.HandleFunc("GET /repos/{prefix}/features/{slug}/plan", d.handleFeaturePlan)
	mux.HandleFunc("GET /repos/{prefix}/features/{slug}/next", d.handleFeatureNextPeek)
	mux.HandleFunc("POST /repos/{prefix}/features/{slug}/next", d.handleFeatureNextClaim)
	mux.HandleFunc("GET /repos/{prefix}/features/{slug}/comments", d.handleFeatureCommentsList)
	mux.HandleFunc("POST /repos/{prefix}/features/{slug}/comments", d.handleFeatureCommentAdd)
	mux.HandleFunc("DELETE /repos/{prefix}/features/{slug}/comments/{uuid}", d.handleFeatureCommentDelete)

	mux.HandleFunc("GET /repos/{prefix}/issues", d.handleIssuesList)
	mux.HandleFunc("POST /repos/{prefix}/issues", d.handleIssueCreate)
	mux.HandleFunc("GET /repos/{prefix}/issues/{key}", d.handleIssueShow)
	mux.HandleFunc("GET /repos/{prefix}/issues/{key}/brief", d.handleIssueBrief)
	mux.HandleFunc("PATCH /repos/{prefix}/issues/{key}", d.handleIssueEdit)
	mux.HandleFunc("DELETE /repos/{prefix}/issues/{key}", d.handleIssueDelete)
	mux.HandleFunc("PUT /repos/{prefix}/issues/{key}/state", d.handleIssueState)
	mux.HandleFunc("PUT /repos/{prefix}/issues/{key}/assignee", d.handleIssueAssign)
	mux.HandleFunc("DELETE /repos/{prefix}/issues/{key}/assignee", d.handleIssueUnassign)

	// Pipeline page: card ordering, process assignment, and the engine
	// controls (manual Start / Stop, auto drive-mode, ship hand-off) plus
	// the per-repo auto-ship toggle. Job control verbs sit under
	// .../jobs/{start,stop} (more specific literals than any {…} segment,
	// so ServeMux disambiguates cleanly). The per-job re-run sits a level
	// deeper at .../jobs/{seq}/rerun (BACI-291) — the trailing `rerun`
	// literal keeps it distinct from the literal start/stop verbs.
	mux.HandleFunc("PUT /repos/{prefix}/issues/{key}/reorder", d.handleIssueReorder)
	mux.HandleFunc("POST /repos/{prefix}/issues/{key}/process", d.handleIssueProcess)
	mux.HandleFunc("PUT /repos/{prefix}/issues/{key}/process/tail", d.handleIssueProcessEdit)
	mux.HandleFunc("POST /repos/{prefix}/issues/{key}/process/reset", d.handleIssueProcessReset)
	mux.HandleFunc("GET /repos/{prefix}/issues/{key}/jobs", d.handleIssueJobs)
	mux.HandleFunc("POST /repos/{prefix}/issues/{key}/jobs/start", d.handleIssueJobStart)
	mux.HandleFunc("POST /repos/{prefix}/issues/{key}/jobs/stop", d.handleIssueJobStop)
	mux.HandleFunc("POST /repos/{prefix}/issues/{key}/jobs/{seq}/rerun", d.handleIssueJobRerun)
	mux.HandleFunc("PUT /repos/{prefix}/issues/{key}/engine-mode", d.handleIssueEngineMode)
	mux.HandleFunc("POST /repos/{prefix}/issues/{key}/ship", d.handleIssueShip)
	mux.HandleFunc("GET /repos/{prefix}/auto-ship", d.handleRepoAutoShipGet)
	mux.HandleFunc("PUT /repos/{prefix}/auto-ship", d.handleRepoAutoShip)
	// BACI-288: per-repo Pipeline Backlog-column collapse preference
	// (tui_settings `pipeline.backlog_collapsed`). Purely client-side
	// chrome — GET seeds the rail state, PUT persists it (honouring
	// ?dry_run) and audits a repo_setting.update row on change.
	mux.HandleFunc("GET /repos/{prefix}/backlog-collapsed", d.handleRepoBacklogCollapsedGet)
	mux.HandleFunc("PUT /repos/{prefix}/backlog-collapsed", d.handleRepoBacklogCollapsed)

	mux.HandleFunc("GET /repos/{prefix}/issues/{key}/comments", d.handleCommentsList)
	mux.HandleFunc("POST /repos/{prefix}/issues/{key}/comments", d.handleCommentAdd)
	mux.HandleFunc("DELETE /repos/{prefix}/issues/{key}/comments/{uuid}", d.handleCommentDelete)

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

	// BACI-187: shipping-log popover — list of recently-done issues,
	// newest-first, sibling of /history. Per-repo only; cross-repo is
	// deliberately out of scope (matches the rest of the surface).
	// BACI-221 reshapes the list response to {rows, total} and adds
	// /shipped/count for the Pipeline Shipping-column pill's 10s
	// scope-count poll. The literal "count" segment is more specific
	// than the bare /shipped route, so ServeMux disambiguates without a
	// conflicting pattern.
	mux.HandleFunc("GET /repos/{prefix}/shipped", d.handleShippedList)
	mux.HandleFunc("GET /repos/{prefix}/shipped/count", d.handleShippedCount)

	// BACI-287: agent→user notification bell — the read + mark-read side
	// of the global (cross-repo) notification bell. The write side is
	// channel-only (the send_user_notification MCP tool), so there is no
	// POST-create route. Cross-repo like /history; the literal "count" /
	// "read-all" segments are more specific than {id}, so ServeMux
	// disambiguates without a conflicting pattern.
	mux.HandleFunc("GET /notifications", d.handleNotificationsList)
	mux.HandleFunc("GET /notifications/count", d.handleNotificationsCount)
	mux.HandleFunc("GET /notifications/{id}", d.handleNotificationShow)
	mux.HandleFunc("POST /notifications/{id}/read", d.handleNotificationRead)
	mux.HandleFunc("POST /notifications/read-all", d.handleNotificationsReadAll)

	// BACI-301: the reverse-proxy forwarding listener. Mounted
	// unconditionally (the agent needs the pipe whether it launched
	// `bacio api` or `bacio web`) and auth-exempt (see auth() in
	// middleware.go) — agent traffic carries its own Anthropic auth, not
	// bacio's bearer token. Empty ProxyUpstream selects the Anthropic
	// default; a malformed explicit value is logged and falls back to the
	// default rather than panicking (the value is internal config).
	proxyLogger := d.logger
	if proxyLogger == nil {
		proxyLogger = slog.Default()
	}
	upstreamRaw := d.opts.ProxyUpstream
	if upstreamRaw == "" {
		upstreamRaw = proxy.DefaultUpstream
	}
	upstreamURL, err := url.Parse(upstreamRaw)
	if err != nil || upstreamURL.Host == "" {
		proxyLogger.Error("invalid proxy upstream — falling back to default",
			"upstream", upstreamRaw, "err", err)
		upstreamURL, _ = url.Parse(proxy.DefaultUpstream)
	}
	mux.Handle(proxy.PathPrefix+"/", proxy.New(upstreamURL, proxyLogger))

	// Web UI bundle (BACI-30, gated by BACI-72): serve the browser-deployed
	// React build at /ui/, with a 301 from the unslashed /ui to keep the
	// SPA's base path consistent. The bundle is embedded at compile time
	// via root.WebUIFS — see internal/api/static.go. `bacio web` sets
	// MountUI=true; `bacio api` leaves it false so an API-only deployment
	// returns 404 on /ui/.
	if d.opts.MountUI {
		mux.HandleFunc("GET /ui", d.handleUIRedirect)
		mux.HandleFunc("GET /ui/", d.handleUI)
	}

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
	// BACI-286: push a user→agent steer message at a busy session. The
	// channel serving that session drains + pushes it as a
	// `<channel kind="message">` tag at the worker's next turn boundary.
	// NOT a dispatch — no matcher, no ack lifecycle.
	mux.HandleFunc("POST /agents/sessions/{session_id}/messages", d.handleSessionMessageSend)
	// BACI-45: read the session's TodoWrite mirror. Writes happen via the
	// post-tool-use hook only — no POST surface here.
	mux.HandleFunc("GET /agents/sessions/{session_id}/todos", d.handleAgentSessionTodos)
	// BACI-53 ask_user_question rows. Same shape rules as /todos:
	// session-scoped GETs, plus per-question answer/cancel mutations
	// addressable by primary key. The literal "open" segment is more
	// specific than the bare list endpoint, so ServeMux disambiguates.
	mux.HandleFunc("GET /agents/sessions/{session_id}/questions", d.handleSessionQuestionsList)
	mux.HandleFunc("GET /agents/sessions/{session_id}/questions/open", d.handleSessionQuestionsOpen)
	mux.HandleFunc("GET /agents/questions/{id}", d.handleQuestionShow)
	mux.HandleFunc("POST /agents/questions/{id}/answer", d.handleQuestionAnswer)
	mux.HandleFunc("POST /agents/questions/{id}/cancel", d.handleQuestionCancel)
	mux.HandleFunc("POST /agents/dispatches/{id}/ack", d.handleAgentDispatchAck)
	mux.HandleFunc("POST /agents/dispatches/{id}/cancel", d.handleAgentDispatchCancel)
	// BACI-190 rescue: post a `from="bacio-rescue"` channel event to an
	// idle supervisor session, asking it to handle a dead worker's
	// stranded worktree inline. Eligibility (status pending/delivered,
	// target session ended, real per-mode creator) is enforced by
	// client.CreateRescueDispatch — the handler just shapes the response.
	mux.HandleFunc("POST /agents/dispatches/{id}/rescue", d.handleAgentDispatchRescue)
	mux.HandleFunc("GET /agents/claims/open", d.handleAgentClaimsOpen)
	// BACI-50: composite Agents endpoint — one assembled AgentCard per
	// session, with claims + dispatches + todos hydrated server-side
	// so the browser does one round trip per refresh.
	mux.HandleFunc("GET /repos/{prefix}/agents/cards", d.handleAgentCardsListRepo)
	mux.HandleFunc("GET /agents/cards", d.handleAgentCardsListAll)
	// BACI-60: composite kanban-cards endpoint — same pattern as
	// /agents/cards but for the Board view. Web mode used to reshape
	// raw /repos/{prefix}/issues client-side, which couldn't see the
	// ActiveVerb / TodosDone / TodosTotal fields the desktop now
	// computes server-side. Only the per-repo route is wired; the
	// "all repos" pseudo-board is still gated to desktop only.
	mux.HandleFunc("GET /repos/{prefix}/cards", d.handleBoardCardsListRepo)
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
	// BACI-51 spinner-as-cancel UI read: returns the active queued /
	// pending / delivered dispatch on an issue, or 404 when none. Used
	// by the desktop + (future) web cancel button to resolve the
	// dispatch id without exposing dispatch internals through the card
	// DTO.
	mux.HandleFunc("GET /repos/{prefix}/issues/{key}/waiting-dispatch", d.handleIssueWaitingDispatch)

	// Prompt templates (BACI-36). Global app_settings — no /repos/{prefix}
	// scope. GET-all + PUT/DELETE per-mode body endpoints.
	mux.HandleFunc("GET /settings/templates", d.handlePromptTemplatesList)
	mux.HandleFunc("PUT /settings/templates/{mode}", d.handlePromptTemplateSet)
	mux.HandleFunc("DELETE /settings/templates/{mode}", d.handlePromptTemplateReset)
	// Typed prompt-template CRUD over REST (BACI-50): the four `bacio
	// settings template add/rename/rm/restore-defaults` verbs plus the
	// /full list endpoint that returns the rich DTO the desktop renders
	// from. Delete lives at /{slug}/row to coexist with the existing
	// body-reset endpoint at /{mode}. The literal "restore-builtins"
	// segment is more specific than "{slug}" so ServeMux disambiguates
	// for free.
	mux.HandleFunc("GET /settings/templates/full", d.handlePromptTemplatesFullList)
	mux.HandleFunc("POST /settings/templates", d.handlePromptTemplateAdd)
	mux.HandleFunc("POST /settings/templates/restore-builtins", d.handlePromptTemplateRestore)
	mux.HandleFunc("POST /settings/templates/{slug}/rename", d.handlePromptTemplateRename)
	mux.HandleFunc("DELETE /settings/templates/{slug}/row", d.handlePromptTemplateDelete)
	// BACI-51 per-template concurrency limit. PUT mirrors the body /
	// state-gate PUTs; no DELETE because "unlimited" is just
	// concurrency_limit=0 — the caller PUTs 0 to revert.
	mux.HandleFunc("PUT /settings/templates/{mode}/concurrency", d.handlePromptTemplateConcurrencySet)
	// BACI-67 per-template action_label override. PUT sets the
	// imperative; DELETE clears (the UI then derives from Name).
	mux.HandleFunc("PUT /settings/templates/{mode}/action-label", d.handlePromptTemplateActionLabelSet)
	mux.HandleFunc("DELETE /settings/templates/{mode}/action-label", d.handlePromptTemplateActionLabelDelete)

	// BACI-89 background sync. GET /sync (cross-repo) + GET
	// /repos/{prefix}/sync (per-repo) report live sync status —
	// last_sync_at, last_error, in_progress, configured — so the web
	// UI's Sync badge reflects real state instead of a hardcoded
	// false. /settings/sync-preferences is the background-sync
	// opt-out toggle, mirroring /settings/board-preferences.
	//
	// BACI-107 adds the inverse, registry-shaped read at GET
	// /sync/repos — one entry per sync_remotes row with its project
	// members nested, plus a sibling list of tracked project repos
	// with no sync config. The literal "repos" segment under /sync is
	// more specific than the bare /sync handler so ServeMux
	// disambiguates without a conflicting pattern.
	mux.HandleFunc("GET /sync", d.handleSyncStatusList)
	mux.HandleFunc("GET /sync/repos", d.handleSyncRegistryList)
	mux.HandleFunc("GET /repos/{prefix}/sync", d.handleSyncStatusGet)
	// BACI-110: HTTP equivalent of `bacio sync init` / `bacio sync clone`.
	// Three modes (init / clone / attach) — see handleSyncSetup. Lives
	// alongside the GET status route so the desktop / web setup form has
	// a single per-repo sync URL surface to talk to.
	mux.HandleFunc("POST /repos/{prefix}/sync/setup", d.handleSyncSetup)
	mux.HandleFunc("GET /settings/sync-preferences", d.handleSyncPreferencesGet)
	mux.HandleFunc("PUT /settings/sync-preferences", d.handleSyncPreferencesSet)

	// BACI-68 archive lifecycle. Per-entity archive / unarchive on
	// issues, features, documents — flip archived_at without going
	// through a PATCH (the verb shape matches the CLI's `bacio issue
	// archive` family and the audit log records the op verb cleanly).
	// `/archive/sweep` runs the same three SQL passes the leader-
	// elected Controller runs hourly. `/settings/display-preferences`
	// holds the display.show_archived global toggle alongside the
	// other /settings/... routes.
	mux.HandleFunc("POST /repos/{prefix}/issues/{key}/archive", d.handleIssueArchive)
	mux.HandleFunc("POST /repos/{prefix}/issues/{key}/unarchive", d.handleIssueUnarchive)
	mux.HandleFunc("POST /repos/{prefix}/features/{slug}/archive", d.handleFeatureArchive)
	mux.HandleFunc("POST /repos/{prefix}/features/{slug}/unarchive", d.handleFeatureUnarchive)
	mux.HandleFunc("POST /repos/{prefix}/documents/{filename}/archive", d.handleDocumentArchive)
	mux.HandleFunc("POST /repos/{prefix}/documents/{filename}/unarchive", d.handleDocumentUnarchive)
	mux.HandleFunc("POST /archive/sweep", d.handleArchiveSweep)
	mux.HandleFunc("GET /settings/display-preferences", d.handleDisplayPreferencesGet)
	mux.HandleFunc("PUT /settings/display-preferences", d.handleDisplayPreferencesSet)
	mux.HandleFunc("GET /settings/archive-preferences", d.handleArchivePreferencesGet)
	mux.HandleFunc("PUT /settings/archive-preferences", d.handleArchivePreferencesSet)

	// BACI-240 ui.shipped_sfx global toggle — the Pipeline Shipping
	// column's Shipped pill's ka-ching SFX gate. Sits alongside the
	// display / archive routes,
	// same single-boolean pattern (the wire payload is {shipped_sfx: bool}
	// rather than a generic "value" to leave room for future audio toggles
	// without breaking the existing field name).
	mux.HandleFunc("GET /settings/audio-preferences", d.handleAudioPreferencesGet)
	mux.HandleFunc("PUT /settings/audio-preferences", d.handleAudioPreferencesSet)

	// BACI-312 global ui.timezone setting — the user's IANA zone name.
	// GET reads ({timezone: "<name>"|""}); PUT validates the IANA-name
	// shape and persists it ({timezone: "Australia/Sydney"}). The browser
	// uses the stored zone to compute local-midnight for the Shipped
	// pill's "Today" cutoff (the server stays tz-agnostic).
	mux.HandleFunc("GET /settings/timezone-preferences", d.handleTimezonePreferencesGet)
	mux.HandleFunc("PUT /settings/timezone-preferences", d.handleTimezonePreferencesSet)

	// BACI-235 per-repo default_feature. GET reads the current
	// setting ({feature: <Feature|null>}); PUT sets it ({slug:
	// "<slug>"}); DELETE clears it. The FK on default_feature_id is
	// ON DELETE SET NULL so the column auto-clears when the
	// referenced feature is deleted.
	mux.HandleFunc("GET /repos/{prefix}/settings/default-feature", d.handleDefaultFeatureGet)
	mux.HandleFunc("PUT /repos/{prefix}/settings/default-feature", d.handleDefaultFeatureSet)
	mux.HandleFunc("DELETE /repos/{prefix}/settings/default-feature", d.handleDefaultFeatureDelete)

	// BACI-248: per-repo board-hidden-states KV (tui_settings
	// `board.hidden_states`). Mirrors the BACI-177 features/hidden
	// per-key shape so the desktop / web Per-repository Settings
	// pane reads both board-hide endpoints with one transport
	// pattern. GET returns the current sorted slice; PUT replaces
	// the persisted set verbatim (the request body is the full new
	// set, not a delta) and audits when the set actually changes.
	mux.HandleFunc("GET /repos/{prefix}/board/hidden-states", d.handleBoardHiddenStatesGet)
	mux.HandleFunc("PUT /repos/{prefix}/board/hidden-states", d.handleBoardHiddenStatesSet)

	// Outermost first: panic recovery wraps everything so a bug in any
	// later layer still returns a 500 envelope. The CORS middleware
	// sits *outside* auth so a cross-origin preflight (OPTIONS) is
	// answered before the bearer-token check would reject it.
	var h http.Handler = mux
	h = bodyCap(h, 4<<20)
	h = auth(h, d.opts.Token)
	h = actorMiddleware(h)
	h = cors(h, d.opts.CORSOrigins)
	h = requestLog(h, d.logger)
	h = recoverPanic(h, d.logger)
	return h
}
