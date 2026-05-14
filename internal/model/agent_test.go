package model

import (
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
		{"refactor", "", true},
		{"Plan", "", true}, // case-sensitive, like ParseDispatchStatus
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

func TestDefaultPromptTemplate(t *testing.T) {
	for _, m := range AllDispatchModes() {
		if DefaultPromptTemplate(m) == "" {
			t.Errorf("DefaultPromptTemplate(%q) is empty — every stage needs a shipped default", m)
		}
	}
	if got := DefaultPromptTemplate(""); got != "" {
		t.Errorf("DefaultPromptTemplate(\"\") = %q, want empty", got)
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
