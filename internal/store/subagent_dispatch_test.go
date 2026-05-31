package store

import (
	"testing"
	"time"
)

func TestNormalizeClaudeAgentID(t *testing.T) {
	cases := map[string]string{
		"a79b9dc411ab02ff6":       "a79b9dc411ab02ff6",
		"A79B9DC411AB02FF6":       "a79b9dc411ab02ff6", // case-folded
		"agent-a79b9dc411ab02ff6": "a79b9dc411ab02ff6", // slug prefix stripped
		"  a79b9dc4 ":             "a79b9dc4",           // trimmed
		"":                        "",
	}
	for in, want := range cases {
		if got := NormalizeClaudeAgentID(in); got != want {
			t.Errorf("NormalizeClaudeAgentID(%q) = %q, want %q", in, got, want)
		}
	}
}

// TestBindSubagentDispatch_RoundTrip asserts the binding write/read pair joins
// through the normalize seam: a header-shaped agent id (bare hex) resolves a
// binding written from a slug-shaped one (agent-<hex>), an unknown id misses,
// and a re-bind overwrites in place.
func TestBindSubagentDispatch_RoundTrip(t *testing.T) {
	s := newTestStore(t)
	now := time.Now()

	// Written with the slug-shaped form; read with the bare header form.
	if err := s.BindSubagentDispatch("agent-A79B9DC411AB02FF6", 42, "sess-1", now); err != nil {
		t.Fatalf("bind: %v", err)
	}
	got, err := s.DispatchIDByClaudeAgentID("a79b9dc411ab02ff6")
	if err != nil {
		t.Fatalf("lookup: %v", err)
	}
	if got == nil || *got != 42 {
		t.Fatalf("lookup = %v, want 42 (normalize seam should bridge slug↔header forms)", got)
	}

	// Unknown id → nil, no error.
	if got, err := s.DispatchIDByClaudeAgentID("does-not-exist"); err != nil || got != nil {
		t.Errorf("unknown lookup = (%v, %v), want (nil, nil)", got, err)
	}

	// Empty id → nil, no error (both sides).
	if err := s.BindSubagentDispatch("", 99, "sess-x", now); err != nil {
		t.Errorf("empty bind should no-op: %v", err)
	}
	if got, err := s.DispatchIDByClaudeAgentID(""); err != nil || got != nil {
		t.Errorf("empty lookup = (%v, %v), want (nil, nil)", got, err)
	}

	// Re-bind overwrites in place (one binding per subagent).
	if err := s.BindSubagentDispatch("a79b9dc411ab02ff6", 77, "sess-1", now.Add(time.Second)); err != nil {
		t.Fatalf("rebind: %v", err)
	}
	got, err = s.DispatchIDByClaudeAgentID("a79b9dc411ab02ff6")
	if err != nil {
		t.Fatalf("lookup after rebind: %v", err)
	}
	if got == nil || *got != 77 {
		t.Errorf("after rebind = %v, want 77", got)
	}
}
