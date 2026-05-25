package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/mrgeoffrich/bacio/internal/client"
	"github.com/mrgeoffrich/bacio/internal/store"
)

// BACI-126b — issue-group claim gate.
//
// When the caller resolves to an agent identity (via the
// .bacio/agents.json `claude_pid → identity` mapping), every
// `bacio issue *` verb (plus the adjacent key-targeted groups
// `comment`, `tag`, `pr`, `link`, `unlink` — confirmed in scope by the
// ticket's open-question resolution) requires the agent to hold an
// open claim in the local registry. For key-targeted verbs the
// targeted key must be one the agent has claimed — drift caught at
// the call site instead of at PR review. Humans (`actor() == "user"`)
// are exempt entirely.
//
// Resolution order at gate time:
//
//  1. `agentIdentityForProcess()` returns "" → human caller → pass.
//  2. `sessionIDForProcess()` resolves the session id for this
//     `claude` process's pid. Empty (no register has happened) → the
//     channel is mid-setup; pass through with an informative-but-soft
//     error rather than silently letting writes through.
//  3. `store.OpenClaimsForSession(sid)` enumerates open claims.
//  4. For key-bearing verbs, the targeted key (parsed from the first
//     positional that looks like a canonical key, or from the JSON
//     payload's `issue_key` / `key` / `from`+`a` field) must intersect
//     the held-claims set. Verbs without a key (issue list / add /
//     next / peek) pass as long as the session holds *any* open claim.
//
// The gate is a `PersistentPreRunE` on each affected cobra group so
// the rule lives in one place and the per-verb RunE stays unchanged.

// claimPolicy names how the gate combines the extracted keys with the
// held-claims set. The zero value (policyAll) is the original BACI-126b
// behaviour: every extracted key must be in the held-claims set, so
// drift ("agent operates on a ticket it doesn't own") fails loud.
// policyAny relaxes that for verbs whose secondary key is referenced
// rather than mutated — BACI-170 set this on `link` / `unlink` so an
// agent claimed on either side of a relation can run the verb. The
// relaxation is intentionally symmetric (not directional on `from`/`a`)
// so a reviewer agent claimed on the secondary side can back-fill the
// relation without the human needing to re-run.
type claimPolicy int

const (
	policyAll claimPolicy = iota // every extracted key must be held (default)
	policyAny                    // at least one extracted key must be held (link / unlink)
)

// keyExtractor names how to find the issue key (or keys, for link)
// the gated verb targets. fromArgs is the positional slot to read;
// negative means "no key from positionals". fromJSONField is the JSON
// field name; empty means "no key from JSON". secondaryArg / secondaryJSONField
// cover the link / unlink shape (two keys). policy picks the
// combination rule the gate applies once both slots are extracted.
type keyExtractor struct {
	fromArgs           int
	secondaryArgs      int
	fromJSONField      string
	secondaryJSONField string
	policy             claimPolicy
}

// keyExtractors lists, per cobra-command path, how to resolve the
// targeted issue key(s). Verbs not in the table are treated as
// any-claim-suffices (issue list / add / next / peek). The path is the
// fully-qualified command name with spaces — matches cobra's
// CommandPath() output minus the root binary name.
var keyExtractors = map[string]keyExtractor{
	"issue show":      {fromArgs: 0},
	"issue brief":     {fromArgs: 0},
	"issue edit":      {fromArgs: 0, fromJSONField: "key"},
	"issue state":     {fromArgs: 0, fromJSONField: "key"},
	"issue assign":    {fromArgs: 0, fromJSONField: "key"},
	"issue unassign":  {fromArgs: 0, fromJSONField: "key"},
	"issue rm":        {fromArgs: 0, fromJSONField: "key"},
	"issue archive":   {fromArgs: 0, fromJSONField: "key"},
	"issue unarchive": {fromArgs: 0, fromJSONField: "key"},

	"comment add":  {fromArgs: 0, fromJSONField: "issue_key"},
	"comment rm":   {fromArgs: 0, fromJSONField: "issue_key"},
	"comment list": {fromArgs: 0},

	"tag add": {fromArgs: 0, fromJSONField: "issue_key"},
	"tag rm":  {fromArgs: 0, fromJSONField: "issue_key"},

	"pr attach": {fromArgs: 0, fromJSONField: "issue_key"},
	"pr detach": {fromArgs: 0, fromJSONField: "issue_key"},
	"pr list":   {fromArgs: 0},

	// link / unlink take two keys. Per BACI-170 the gate accepts a claim
	// on EITHER side — the secondary key is being referenced (the
	// relation's symmetric backlink), not mutated, so requiring a claim
	// on both would block legitimate planning / reviewer work. Other
	// gated verbs keep the default policyAll because they really do
	// mutate the targeted ticket.
	"link":   {fromArgs: 0, secondaryArgs: 2, fromJSONField: "from", secondaryJSONField: "to", policy: policyAny},
	"unlink": {fromArgs: 0, secondaryArgs: 1, fromJSONField: "a", secondaryJSONField: "b", policy: policyAny},
}

// requireClaimForGroup is the PersistentPreRunE wired onto the gated
// command groups. It returns nil for humans (so existing UX is
// untouched), runs the gate for agent callers, and fails the cobra
// command with a clear error when the agent has no open claim or is
// operating on the wrong issue.
//
// dry-run is intentionally NOT exempt — the spirit of the gate is to
// catch drift before any side effect, including a rehearsal that
// resolves the wrong ticket.
func requireClaimForGroup(cmd *cobra.Command, args []string) error {
	identity := agentIdentityForProcess()
	if identity == "" {
		return nil // human caller — gate is a no-op
	}
	sid := sessionIDForProcess()
	if sid == "" {
		// Agent identity recorded but no session id yet — the channel
		// is mid-setup, or the SessionStart hook never fired. Soft-fail
		// with an actionable message rather than silently letting
		// writes through.
		return fmt.Errorf("bacio: agent %q has no recorded session id — wait for the bacio channel's `register` to complete before running `%s`",
			identity, cmd.CommandPath())
	}
	// The gate is CLI-only — the API doesn't auto-bind a session id to
	// a request, so a `--remote` invocation has no session context to
	// gate against. Skip the gate in that mode rather than wedging
	// agent traffic against a misconfigured `--remote` endpoint.
	if inRemoteMode() {
		return nil
	}
	// Look up open claims. A store error here is fatal — we'd rather
	// fail loud than misroute the gate.
	c, err := openClient()
	if err != nil {
		return err
	}
	defer c.Close()
	claims, err := c.OpenClaimsForSession(context.Background(), sid)
	if err != nil {
		if errors.Is(err, client.ErrLocalOnly) {
			return nil // remote mode already filtered above; defensive belt-and-braces
		}
		return fmt.Errorf("bacio: gate lookup for session %s: %w", sid, err)
	}
	if len(claims) == 0 {
		return fmt.Errorf("bacio: this agent has no open claim — call `bacio agent claim <KEY>` before running `%s`",
			cmd.CommandPath())
	}
	held := make(map[string]struct{}, len(claims))
	for _, c := range claims {
		held[c] = struct{}{}
	}
	ext, ok := keyExtractors[strings.TrimPrefix(cmd.CommandPath(), cmd.Root().Name()+" ")]
	if !ok {
		// Verb is in a gated group but takes no issue key (issue list /
		// add / next / peek). Any open claim suffices.
		return nil
	}
	// Resolve the targeted key(s) from positionals or the --json payload.
	keys, err := extractTargetedKeys(cmd, args, ext)
	if err != nil {
		return err
	}
	if len(keys) == 0 {
		// Verb supports a key but neither --json nor a canonical-form
		// positional supplied one (the per-verb RunE will reject the
		// call with its own clearer error). Pass through — the
		// any-claim test already ran above.
		return nil
	}
	return checkClaims(held, keys, ext.policy, cmd.CommandPath())
}

// checkClaims is the policy step extracted from requireClaimForGroup
// so it can be unit-tested without standing up an agent session.
// `held` is the set of issue keys the calling agent has open claims on;
// `keys` is what the verb is targeting (already canonicalised by
// extractTargetedKeys); `policy` picks the combination rule; `cmdPath`
// is interpolated into the error message so the operator sees which
// verb refused.
func checkClaims(held map[string]struct{}, keys []string, policy claimPolicy, cmdPath string) error {
	heldList := sortedKeys(held)
	switch policy {
	case policyAny:
		// At least one extracted key must be in held — the relaxed
		// rule for `link` / `unlink`. The error names both sides of
		// the relation so the agent knows which extra ticket would
		// satisfy the gate.
		for _, k := range keys {
			if _, ok := held[k]; ok {
				return nil
			}
		}
		return fmt.Errorf("bacio: this agent is claimed against %s; `%s` requires a claim on at least one side of the relation (got %s)",
			strings.Join(heldList, ", "), cmdPath, strings.Join(keys, ", "))
	default:
		// policyAll — every extracted key must be held (the original
		// BACI-126b behaviour, used by every verb except link/unlink).
		for _, k := range keys {
			if _, ok := held[k]; !ok {
				return fmt.Errorf("bacio: this agent is claimed against %s; cannot operate on %s without an open claim on it",
					strings.Join(heldList, ", "), k)
			}
		}
		return nil
	}
}

// extractTargetedKeys reads the issue key(s) the verb is targeting,
// in priority order: --json field first, then the relevant positional.
// Returns the unique-canonical-key set so the caller can compare to
// held claims with a map lookup.
func extractTargetedKeys(cmd *cobra.Command, args []string, ext keyExtractor) ([]string, error) {
	keys := map[string]struct{}{}

	// JSON path first — when the caller supplied --json, the
	// positionals are absent (rejectMixedInput enforces this in the
	// RunE), so we read the structured payload instead.
	if flag := cmd.Flags().Lookup(jsonInputFlag); flag != nil && flag.Changed {
		raw, err := readJSONInput(flag.Value.String())
		if err != nil {
			return nil, err
		}
		if len(raw) > 0 {
			// Parse loosely — we only need a handful of fields, and an
			// unparseable payload will be rejected by DecodeStrict in
			// the verb's own RunE, so we don't need to surface decode
			// errors here.
			var m map[string]any
			if err := json.Unmarshal(raw, &m); err == nil {
				if ext.fromJSONField != "" {
					if v, ok := stringField(m, ext.fromJSONField); ok {
						keys[v] = struct{}{}
					}
				}
				if ext.secondaryJSONField != "" {
					if v, ok := stringField(m, ext.secondaryJSONField); ok {
						keys[v] = struct{}{}
					}
				}
			}
		}
	}

	// Positional path — used when --json isn't set. The slot index
	// names the arg's position; we tolerate short args (the per-verb
	// RunE will error on those with its own clearer message).
	if ext.fromArgs >= 0 && ext.fromArgs < len(args) {
		if k := canonicalIssueKey(args[ext.fromArgs]); k != "" {
			keys[k] = struct{}{}
		}
	}
	if ext.secondaryArgs > 0 && ext.secondaryArgs < len(args) {
		if k := canonicalIssueKey(args[ext.secondaryArgs]); k != "" {
			keys[k] = struct{}{}
		}
	}
	return sortedKeys(keys), nil
}

// canonicalIssueKey returns the canonical PREFIX-N form of an input
// string if it looks like one, else "". Used by the gate to decide
// whether a positional argument is an issue key worth checking
// against the held-claims set (a bare number, e.g. `42`, falls back
// to the human-CLI shortcut and the gate skips it — the verb's own
// repoForIssueKey will resolve it relative to cwd).
func canonicalIssueKey(s string) string {
	s = strings.TrimSpace(s)
	if !strings.Contains(s, "-") {
		return ""
	}
	if _, _, err := store.ParseIssueKey(s); err != nil {
		return ""
	}
	return strings.ToUpper(s)
}

// stringField reads m[name] as a string (only — non-string fields
// don't satisfy the gate, so we ignore them rather than coerce). Bare
// numbers and other non-canonical inputs produce "" from
// canonicalIssueKey; the second return is false in that case so the
// caller doesn't insert an empty string into the keys map.
func stringField(m map[string]any, name string) (string, bool) {
	v, ok := m[name]
	if !ok {
		return "", false
	}
	s, ok := v.(string)
	if !ok || strings.TrimSpace(s) == "" {
		return "", false
	}
	canonical := canonicalIssueKey(s)
	if canonical == "" {
		return "", false
	}
	return canonical, true
}

// sortedKeys returns the keys of m as a sorted, deduplicated slice —
// used both to surface the held claim set in error messages and to
// turn the per-extraction key map into a stable slice.
func sortedKeys(m map[string]struct{}) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	// Insertion-sort over a small set (always <= a handful in practice).
	for i := 1; i < len(out); i++ {
		for j := i; j > 0 && out[j-1] > out[j]; j-- {
			out[j-1], out[j] = out[j], out[j-1]
		}
	}
	return out
}
