package model

import (
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

func TestComposeDispatchPayload(t *testing.T) {
	cases := []struct {
		name     string
		template string
		vars     map[string]string
		note     string
		want     string
	}{
		{"empty everything", "", nil, "", ""},
		{"note only", "", nil, "just a note", "just a note"},
		{"template only", "Implement {{issue_id}}.", map[string]string{"issue_id": "BACI-10"}, "", "Implement BACI-10."},
		{"template + note", "Implement {{issue_id}}.", map[string]string{"issue_id": "BACI-10"}, "watch the migration", "Implement BACI-10.\n\nwatch the migration"},
		{"note trimmed", "Plan {{issue_id}}.", map[string]string{"issue_id": "BACI-10"}, "  trimmed  ", "Plan BACI-10.\n\ntrimmed"},
		{"template trimmed", "  Plan {{issue_id}}.  ", map[string]string{"issue_id": "BACI-10"}, "", "Plan BACI-10."},
	}
	for _, c := range cases {
		if got := ComposeDispatchPayload(c.template, c.vars, c.note); got != c.want {
			t.Errorf("%s: ComposeDispatchPayload(%q, %v, %q) = %q, want %q", c.name, c.template, c.vars, c.note, got, c.want)
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

func TestSessionWaiting(t *testing.T) {
	t0 := time.Date(2026, 5, 16, 9, 0, 0, 0, time.UTC)
	released := t0.Add(time.Hour)

	cases := []struct {
		name        string
		claims      []*AgentClaim
		needsAction map[string]bool
		wantOK      bool
		wantKey     string
	}{
		{"no claims", nil, map[string]bool{"BACI-1": true}, false, ""},
		{"open claim but issue not in needs_action", []*AgentClaim{
			{IssueKey: "BACI-1", ClaimedAt: t0},
		}, map[string]bool{"BACI-2": true}, false, ""},
		{"open claim on needs_action issue", []*AgentClaim{
			{IssueKey: "BACI-1", ClaimedAt: t0},
		}, map[string]bool{"BACI-1": true}, true, "BACI-1"},
		{"released claim ignored", []*AgentClaim{
			{IssueKey: "BACI-1", ClaimedAt: t0, ReleasedAt: &released},
		}, map[string]bool{"BACI-1": true}, false, ""},
		{"nil entries skipped", []*AgentClaim{
			nil,
			{IssueKey: "BACI-1", ClaimedAt: t0},
		}, map[string]bool{"BACI-1": true}, true, "BACI-1"},
		{"newest waiting claim wins", []*AgentClaim{
			{IssueKey: "BACI-1", ClaimedAt: t0},
			{IssueKey: "BACI-2", ClaimedAt: t0.Add(time.Minute)},
		}, map[string]bool{"BACI-1": true, "BACI-2": true}, true, "BACI-2"},
		{"non-waiting newer claim doesn't mask waiting older claim", []*AgentClaim{
			{IssueKey: "BACI-1", ClaimedAt: t0},
			{IssueKey: "BACI-2", ClaimedAt: t0.Add(time.Minute)},
		}, map[string]bool{"BACI-1": true}, true, "BACI-1"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			gotOK, gotKey := SessionWaiting(c.claims, c.needsAction)
			if gotOK != c.wantOK || gotKey != c.wantKey {
				t.Errorf("SessionWaiting() = (%v, %q), want (%v, %q)", gotOK, gotKey, c.wantOK, c.wantKey)
			}
		})
	}
}

func TestSessionLiveness(t *testing.T) {
	now := time.Date(2026, 5, 15, 12, 0, 0, 0, time.UTC)
	ended := now.Add(-time.Hour)

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
	}
	for _, c := range cases {
		if got := SessionLiveness(c.sess, now); got != c.want {
			t.Errorf("SessionLiveness(%s) = %q, want %q", c.name, got, c.want)
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
