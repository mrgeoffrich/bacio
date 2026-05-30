package model

import (
	"regexp"
	"strings"
	"testing"
	"time"
)

func TestParseDispatchMode(t *testing.T) {
	cases := []struct {
		in      string
		want    DispatchMode
		wantErr bool
	}{
		{"", "", false},
		{"  ", "", false},
		{"plan", DispatchModePlan, false},
		{"implement", DispatchModeImplement, false},
		{"review", DispatchModeReview, false},
		{"ship", DispatchModeShip, false},
		{"fix_review", DispatchModeFixReview, false},
		{"scope", DispatchMode(BuiltinTemplateScope), false},
		{" plan ", DispatchModePlan, false},
		// User-created slugs validate too (the slug-shape check is
		// orthogonal to whether the template exists — a deleted
		// template's historical dispatch keeps the slug verbatim).
		{"spike", "spike", false},
		{"post-mortem", "post-mortem", false},
		// Shape violations.
		{"Plan", "", true},                  // uppercase
		{"plan!", "", true},                 // punctuation
		{"-plan", "", true},                 // leading hyphen
		{strings.Repeat("a", 61), "", true}, // too long
	}
	for _, c := range cases {
		got, err := ParseDispatchMode(c.in)
		if c.wantErr {
			if err == nil {
				t.Errorf("ParseDispatchMode(%q): want error, got %q", c.in, got)
			}
			continue
		}
		if err != nil {
			t.Errorf("ParseDispatchMode(%q): unexpected error %v", c.in, err)
		}
		if got != c.want {
			t.Errorf("ParseDispatchMode(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestSubagentTypeForTemplate(t *testing.T) {
	cases := map[string]string{
		"plan":               "bacio-plan-worker",
		"design":             "bacio-design-worker",
		"implement":          "bacio-implement-worker",
		"review":             "bacio-review-worker",
		"ship":               "bacio-ship-worker",
		"fix_review":         "bacio-fix-review-worker",
		"scope":              "bacio-scope-worker",
		"my-custom":          "bacio-my-custom-worker",
		"my_custom_stage":    "bacio-my-custom-stage-worker",
		"_dispatch_preamble": "bacio--dispatch-preamble-worker",
	}
	for slug, want := range cases {
		if got := SubagentTypeForTemplate(slug); got != want {
			t.Errorf("SubagentTypeForTemplate(%q) = %q, want %q", slug, got, want)
		}
	}
	// Every built-in dispatchable slug must round-trip into a valid
	// Claude Code agent name ([a-z0-9-] only).
	valid := regexp.MustCompile(`^[a-z0-9-]+$`)
	for _, slug := range []string{
		BuiltinTemplateScope, BuiltinTemplateResearch, BuiltinTemplatePlan, BuiltinTemplateDesign, BuiltinTemplateImplement,
		BuiltinTemplateReview, BuiltinTemplateShip, BuiltinTemplateFixReview,
	} {
		if name := SubagentTypeForTemplate(slug); !valid.MatchString(name) {
			t.Errorf("SubagentTypeForTemplate(%q) = %q — not a valid agent name", slug, name)
		}
	}
}

// TestBuiltinTemplateScope is the BACI-164 guard for the new `scope`
// built-in: the slug must round-trip through ParseDispatchMode, carry a
// non-empty label / action label, gate to [StateTodo], render a
// non-empty default body that survives include expansion (no leftover
// `{{` tokens), and resolve to the canonical `bacio-scope-worker`
// subagent type.
func TestBuiltinTemplateScope(t *testing.T) {
	if got, err := ParseDispatchMode("scope"); err != nil || string(got) != BuiltinTemplateScope {
		t.Errorf("ParseDispatchMode(\"scope\") = (%q, %v), want (%q, nil)", got, err, BuiltinTemplateScope)
	}
	if got := BuiltinTemplateLabel(BuiltinTemplateScope); got != "Scoping" {
		t.Errorf("BuiltinTemplateLabel(scope) = %q, want %q", got, "Scoping")
	}
	if got := BuiltinTemplateActionLabel(BuiltinTemplateScope); got != "Scope" {
		t.Errorf("BuiltinTemplateActionLabel(scope) = %q, want %q", got, "Scope")
	}
	body := DefaultPromptBodyForBuiltinSlug(BuiltinTemplateScope)
	if strings.TrimSpace(body) == "" {
		t.Fatal("DefaultPromptBodyForBuiltinSlug(scope) is empty — every built-in needs a shipped default")
	}
	if strings.Contains(body, "{{") {
		t.Errorf("DefaultPromptBodyForBuiltinSlug(scope) still contains a {{...}} token after include expansion:\n%s", body)
	}
	if got := SubagentTypeForTemplate(BuiltinTemplateScope); got != "bacio-scope-worker" {
		t.Errorf("SubagentTypeForTemplate(scope) = %q, want %q", got, "bacio-scope-worker")
	}
	// The slug must appear in the canonical lifecycle list so the seed
	// step and `restore-defaults` know about it.
	var found bool
	for _, s := range BuiltinTemplateSlugs() {
		if s == BuiltinTemplateScope {
			found = true
			break
		}
	}
	if !found {
		t.Error("BuiltinTemplateSlugs() does not include the scope slug")
	}
}

func TestComposeDispatchPayload(t *testing.T) {
	cases := []struct {
		name     string
		preamble string
		stub     DispatchStub
		note     string
		want     string
	}{
		// Untyped dispatch (no mode) — payload is just the note.
		{"empty everything", "", DispatchStub{}, "", ""},
		{"note only", "", DispatchStub{}, "just a note", "just a note"},
		{"note trimmed, no mode", "Delegate.", DispatchStub{}, "  trimmed  ", "trimmed"},
		{"preamble dropped without mode", "Delegate.", DispatchStub{}, "just a note", "just a note"},

		// Typed dispatch — preamble + stub.
		{
			"preamble + stub",
			"Spawn the per-mode subagent.",
			DispatchStub{IssueKey: "BACI-10", Mode: "implement", SubagentType: "bacio-implement-worker"},
			"",
			"Spawn the per-mode subagent.\n\n<issue_id>BACI-10</issue_id>\n<mode>implement</mode>\n<subagent_type>bacio-implement-worker</subagent_type>",
		},
		{
			"preamble + stub + note",
			"Delegate.",
			DispatchStub{IssueKey: "BACI-10", Mode: "plan", SubagentType: "bacio-plan-worker"},
			"extra context",
			"Delegate.\n\n<issue_id>BACI-10</issue_id>\n<mode>plan</mode>\n<subagent_type>bacio-plan-worker</subagent_type>\n\nextra context",
		},
		// A preamble that trims to "" behaves identically to no preamble.
		{
			"whitespace-only preamble",
			"   \n  ",
			DispatchStub{IssueKey: "BACI-10", Mode: "plan", SubagentType: "bacio-plan-worker"},
			"",
			"<issue_id>BACI-10</issue_id>\n<mode>plan</mode>\n<subagent_type>bacio-plan-worker</subagent_type>",
		},
		// BACI-226: <base_branch> rides between <mode> and
		// <subagent_type> so the supervisor copies it verbatim into
		// the worker's Task prompt alongside <issue_id> and <mode>.
		{
			"stub with base_branch=main (resolver fallback)",
			"Delegate.",
			DispatchStub{IssueKey: "BACI-10", Mode: "plan", BaseBranch: "main", SubagentType: "bacio-plan-worker"},
			"",
			"Delegate.\n\n<issue_id>BACI-10</issue_id>\n<mode>plan</mode>\n<base_branch>main</base_branch>\n<subagent_type>bacio-plan-worker</subagent_type>",
		},
		{
			"stub with base_branch=feat/X (feature)",
			"Delegate.",
			DispatchStub{IssueKey: "BACI-10", Mode: "plan", BaseBranch: "feat/X", SubagentType: "bacio-plan-worker"},
			"",
			"Delegate.\n\n<issue_id>BACI-10</issue_id>\n<mode>plan</mode>\n<base_branch>feat/X</base_branch>\n<subagent_type>bacio-plan-worker</subagent_type>",
		},
		// An empty BaseBranch omits the tag entirely so the existing
		// per-feature-branch-less invariant rides unchanged.
		{
			"stub with empty base_branch omits the tag",
			"Delegate.",
			DispatchStub{IssueKey: "BACI-10", Mode: "plan", SubagentType: "bacio-plan-worker"},
			"",
			"Delegate.\n\n<issue_id>BACI-10</issue_id>\n<mode>plan</mode>\n<subagent_type>bacio-plan-worker</subagent_type>",
		},
	}
	for _, c := range cases {
		if got := ComposeDispatchPayload(c.preamble, c.stub, c.note); got != c.want {
			t.Errorf("%s: ComposeDispatchPayload(%q, %+v, %q) = %q, want %q", c.name, c.preamble, c.stub, c.note, got, c.want)
		}
	}
}

func TestRenderPromptTemplate(t *testing.T) {
	vars := map[string]string{
		"issue_id":    "BACI-10",
		"issue_title": "Customisable prompt templates",
		"repo_prefix": "BACI",
	}
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"no tokens", "Just plain text.", "Just plain text."},
		{"single token", "Work on {{issue_id}}.", "Work on BACI-10."},
		{"repeated token", "{{issue_id}} {{issue_id}}", "BACI-10 BACI-10"},
		{"all tokens", "{{repo_prefix}}: {{issue_id}} — {{issue_title}}", "BACI: BACI-10 — Customisable prompt templates"},
		{"whitespace in braces", "Work on {{ issue_id }}.", "Work on BACI-10."},
		{"unknown token passthrough", "Hello {{nope}} world", "Hello {{nope}} world"},
		{"unterminated braces", "dangling {{issue_id", "dangling {{issue_id"},
		{"empty string", "", ""},
	}
	for _, c := range cases {
		if got := RenderPromptTemplate(c.in, vars); got != c.want {
			t.Errorf("%s: RenderPromptTemplate(%q) = %q, want %q", c.name, c.in, got, c.want)
		}
	}
}

// TestBuiltinTemplateActionLabel covers the BACI-67 imperative-label
// map. Every built-in slug (except the reserved _dispatch_preamble row
// which never reaches a dropdown) should have an explicit imperative
// override; non-built-in slugs fall back to the derivation rule.
func TestBuiltinTemplateActionLabel(t *testing.T) {
	cases := map[string]string{
		BuiltinTemplateScope:     "Scope",
		BuiltinTemplateResearch:  "Research",
		BuiltinTemplatePlan:      "Plan",
		BuiltinTemplateDesign:    "Design",
		BuiltinTemplateImplement: "Implement",
		BuiltinTemplateReview:    "Review",
		BuiltinTemplateShip:      "Ship",
		BuiltinTemplateFixReview: "Fix review",
	}
	for slug, want := range cases {
		if got := BuiltinTemplateActionLabel(slug); got != want {
			t.Errorf("BuiltinTemplateActionLabel(%q) = %q, want %q", slug, got, want)
		}
	}
	// The reserved preamble row has no state-gate and never reaches the
	// dropdown — by design it has no entry in the action_label map.
	if got := BuiltinTemplateActionLabel(BuiltinTemplatePreamble); got != "" {
		t.Errorf("BuiltinTemplateActionLabel(preamble) = %q, want \"\"", got)
	}
	// Non-built-in slug — empty, derivation runs on the stored Name.
	if got := BuiltinTemplateActionLabel("spike"); got != "" {
		t.Errorf("BuiltinTemplateActionLabel(\"spike\") = %q, want \"\"", got)
	}
}

// TestDeriveActionLabel covers the gerund → imperative fallback the UI
// surfaces use when no action_label override is stored on a template.
// Anchored on the built-in gerunds plus edge cases the rule must
// behave reasonably for.
func TestDeriveActionLabel(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		// Built-in gerunds — the seed step overrides these explicitly,
		// but the derivation rule must produce the same answer for
		// user-created templates that mirror the convention.
		{"Planning", "Planning", "Plan"},
		{"Designing", "Designing", "Design"},
		{"Implementing", "Implementing", "Implement"},
		{"Reviewing", "Reviewing", "Review"},
		{"Shipping", "Shipping", "Ship"},
		// Capitalisation preserved on the kept prefix.
		{"shipping (lowercase)", "shipping", "ship"},
		// Already-imperative — passes through.
		{"already imperative", "Plan", "Plan"},
		{"single word non-gerund", "Spike", "Spike"},
		// "ying" edge case: consonant+y verbs like "Spy" → "Spying".
		{"spying → Spy", "Spying", "Spy"},
		// Short names that happen to end in "ing" but aren't really
		// gerunds: don't mangle them.
		{"king (too short, ambiguous)", "King", "King"},
		{"sing (4-char, too short to safely strip)", "Sing", "Sing"},
		{"ring (4-char, too short to safely strip)", "Ring", "Ring"},
		// Whitespace is trimmed.
		{"whitespace", "  Planning  ", "Plan"},
		{"empty", "", ""},
		{"only whitespace", "   ", ""},
		// Non-English / unusual passes through unchanged.
		{"non-English", "Mañana", "Mañana"},
		// Multi-word names: derivation runs on the full string, so a
		// trailing gerund still trims. Users can override via
		// action_label for any awkward case.
		{"multi-word doubled-consonant gerund", "Sprint planning", "Sprint plan"},
		{"multi-word plain gerund", "Code reviewing", "Code review"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := DeriveActionLabel(c.in); got != c.want {
				t.Errorf("DeriveActionLabel(%q) = %q, want %q", c.in, got, c.want)
			}
		})
	}
}

func TestDefaultPromptBodyForBuiltinSlug(t *testing.T) {
	for _, slug := range BuiltinTemplateSlugs() {
		if DefaultPromptBodyForBuiltinSlug(slug) == "" {
			t.Errorf("DefaultPromptBodyForBuiltinSlug(%q) is empty — every built-in needs a shipped default", slug)
		}
	}
	// User-created slugs (or empty / unknown) have no embedded body.
	if got := DefaultPromptBodyForBuiltinSlug(""); got != "" {
		t.Errorf("DefaultPromptBodyForBuiltinSlug(\"\") = %q, want empty", got)
	}
	if got := DefaultPromptBodyForBuiltinSlug("spike"); got != "" {
		t.Errorf("DefaultPromptBodyForBuiltinSlug(\"spike\") = %q, want empty (not a built-in)", got)
	}
}

func TestSessionLiveness(t *testing.T) {
	now := time.Date(2026, 5, 15, 12, 0, 0, 0, time.UTC)
	ended := now.Add(-time.Hour)
	errored := now.Add(-time.Minute)

	cases := []struct {
		name string
		sess *AgentSession
		want string
	}{
		{"nil", nil, "ended"},
		{"ended", &AgentSession{LastSeenAt: now, EndedAt: &ended}, "ended"},
		{"fresh", &AgentSession{LastSeenAt: now.Add(-time.Minute)}, "active"},
		{"on threshold", &AgentSession{LastSeenAt: now.Add(-AgentLivenessThreshold)}, "active"},
		{"past threshold", &AgentSession{LastSeenAt: now.Add(-AgentLivenessThreshold - time.Second)}, "idle"},
		// BACI-296: errored supersedes active/idle but never ended.
		{"errored over active", &AgentSession{LastSeenAt: now.Add(-time.Minute), ErroredAt: &errored}, "errored"},
		{"errored over idle", &AgentSession{LastSeenAt: now.Add(-AgentLivenessThreshold - time.Hour), ErroredAt: &errored}, "errored"},
		{"ended beats errored", &AgentSession{LastSeenAt: now, EndedAt: &ended, ErroredAt: &errored}, "ended"},
	}
	for _, c := range cases {
		if got := SessionLiveness(c.sess, now); got != c.want {
			t.Errorf("SessionLiveness(%s) = %q, want %q", c.name, got, c.want)
		}
	}
}

func TestIsTransientAPIError(t *testing.T) {
	cases := []struct {
		in   string
		want bool
	}{
		{"server_error", true},
		{"rate_limit", true},
		{" server_error ", true}, // trimmed
		{"authentication_failed", false},
		{"billing_error", false},
		{"oauth_org_not_allowed", false},
		{"invalid_request", false},
		{"model_not_found", false},
		{"max_output_tokens", false},
		{"unknown", false}, // conservative default — surface to user
		{"", false},
	}
	for _, c := range cases {
		if got := IsTransientAPIError(c.in); got != c.want {
			t.Errorf("IsTransientAPIError(%q) = %v, want %v", c.in, got, c.want)
		}
	}
}

func TestParseEndReason(t *testing.T) {
	cases := []struct {
		in      string
		want    EndReason
		wantErr bool
	}{
		{"stop", EndReasonStop, false},
		{"clear", EndReasonClear, false},
		{"logout", EndReasonLogout, false},
		{"crash", EndReasonCrash, false},
		{"other", EndReasonOther, false},
		// BACI-57: reaper-written reason for sessions force-ended after
		// failing to ack an idle-check ping.
		{"presumed_dead", EndReasonPresumedDead, false},
		// No dash/space normalisation — these are short identifiers,
		// agents send them verbatim.
		{"presumed-dead", "", true},
		{"unknown", "", true},
		{"", "", true},
	}
	for _, c := range cases {
		got, err := ParseEndReason(c.in)
		if c.wantErr {
			if err == nil {
				t.Errorf("ParseEndReason(%q) = (%q, nil), want error", c.in, got)
			}
			continue
		}
		if err != nil {
			t.Errorf("ParseEndReason(%q) errored unexpectedly: %v", c.in, err)
		}
		if got != c.want {
			t.Errorf("ParseEndReason(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestAnyOpenClaim(t *testing.T) {
	released := time.Date(2026, 5, 15, 10, 0, 0, 0, time.UTC)
	cases := []struct {
		name   string
		claims []*AgentClaim
		want   bool
	}{
		{"empty", nil, false},
		{"all released", []*AgentClaim{{ReleasedAt: &released}, {ReleasedAt: &released}}, false},
		{"one open", []*AgentClaim{{ReleasedAt: &released}, {ReleasedAt: nil}}, true},
		{"all open", []*AgentClaim{{ReleasedAt: nil}, {ReleasedAt: nil}}, true},
		{"nil entry mixed", []*AgentClaim{nil, {ReleasedAt: &released}}, false},
		{"nil entry with open", []*AgentClaim{nil, {ReleasedAt: nil}}, true},
	}
	for _, c := range cases {
		if got := AnyOpenClaim(c.claims); got != c.want {
			t.Errorf("AnyOpenClaim(%s) = %v, want %v", c.name, got, c.want)
		}
	}
}
