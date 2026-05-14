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
		mode DispatchMode
		note string
		want string
	}{
		{"", "", ""},
		{"", "just a note", "just a note"},
		{DispatchModePlan, "", "Run a planning pass on this issue: produce an implementation plan, don't write code yet."},
		{DispatchModeImplement, "", "Implement this issue end-to-end."},
		{DispatchModeImplement, "watch the migration", "Implement this issue end-to-end.\n\nwatch the migration"},
		{DispatchModePlan, "  trimmed  ", "Run a planning pass on this issue: produce an implementation plan, don't write code yet.\n\ntrimmed"},
	}
	for _, c := range cases {
		if got := ComposeDispatchPayload(c.mode, c.note); got != c.want {
			t.Errorf("ComposeDispatchPayload(%q, %q) = %q, want %q", c.mode, c.note, got, c.want)
		}
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
