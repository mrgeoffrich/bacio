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
// rows stay skipped forever. Rebuild (BACI-325) is the destructive per-dispatch
// rebuild: it deletes the dispatch's proxy_messages rows and replays ALL its
// captures through the current parser, re-classifying every turn — the recovery for
// a dispatch misclassified by a since-fixed parser bug. It REQUIRES Dispatch; a
// global rebuild (Rebuild with no Dispatch) errors cleanly, as an unbounded
// destructive write over the shared store is out of scope.
type ProxyReparseInput struct {
	Dispatch    *int64 `json:"dispatch,omitempty"`
	RetryFailed bool   `json:"retry_failed,omitempty"`
	Rebuild     bool   `json:"rebuild,omitempty"`
}
