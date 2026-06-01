package inputs

// ProxyReparseInput is the payload for `bacio proxy reparse --json` (BACI-321) —
// the manual escape hatch over the leader-gated backfill sweep that reparses
// dispatch-correlated Anthropic captures the live recorder path missed into
// proxy_messages. With no fields set it sweeps every eligible dispatch (the same
// work the controller does once a minute). Dispatch scopes the run to one job's
// captures (the dispatch_id the capture correlates on). RetryFailed (BACI-323)
// clears parse_failed_at on the still-unparsed captures in scope before the
// reparse, so dispatches the parser previously gave up on (e.g. the pre-fix
// marker-collision failures) backfill once the parser is fixed — without it those
// rows stay skipped forever. Rebuild requests the destructive partial-gap rebuild
// (delete-from-gap-onward + replay); it is reserved on the surface but not
// implemented in v1 — passing it errors cleanly with a "not yet implemented"
// message, so the flag is wired without shipping the destructive path.
type ProxyReparseInput struct {
	Dispatch    *int64 `json:"dispatch,omitempty"`
	RetryFailed bool   `json:"retry_failed,omitempty"`
	Rebuild     bool   `json:"rebuild,omitempty"`
}
