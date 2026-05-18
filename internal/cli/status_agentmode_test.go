package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"strings"
	"testing"
)

// TestBuildStatusReport_AgentModeUnset asserts the BACI-48 status field
// renders correctly when BACIO_AGENT_MODE is not set in the environment:
// EnvVar, empty Value, Active=false. The outside-a-git-repo branch is
// the simplest reachable path that doesn't need a git working tree.
func TestBuildStatusReport_AgentModeUnset(t *testing.T) {
	t.Setenv("BACIO_AGENT_MODE", "scratch")
	if err := os.Unsetenv("BACIO_AGENT_MODE"); err != nil {
		t.Fatalf("unset: %v", err)
	}

	dir := t.TempDir() // no git init — exercises the global-stats branch
	withCwd(t, dir)
	s := openMemStore(t)

	report, err := buildStatusReport(s, testEnv("/db.sqlite"), dir)
	if err != nil {
		t.Fatalf("buildStatusReport: %v", err)
	}
	if report.AgentMode.EnvVar != "BACIO_AGENT_MODE" {
		t.Fatalf("EnvVar = %q, want BACIO_AGENT_MODE", report.AgentMode.EnvVar)
	}
	if report.AgentMode.Value != "" {
		t.Fatalf("Value = %q, want empty (env unset)", report.AgentMode.Value)
	}
	if report.AgentMode.Active {
		t.Fatal("Active = true, want false (env unset)")
	}
}

// TestBuildStatusReport_AgentModeActive covers the activated branch:
// the raw value round-trips, Active reads true. Belt-and-braces against
// a future regression in agentmode.Enabled's accept list.
func TestBuildStatusReport_AgentModeActive(t *testing.T) {
	t.Setenv("BACIO_AGENT_MODE", "1")

	dir := t.TempDir()
	withCwd(t, dir)
	s := openMemStore(t)

	report, err := buildStatusReport(s, testEnv("/db.sqlite"), dir)
	if err != nil {
		t.Fatalf("buildStatusReport: %v", err)
	}
	if report.AgentMode.Value != "1" {
		t.Fatalf("Value = %q, want %q", report.AgentMode.Value, "1")
	}
	if !report.AgentMode.Active {
		t.Fatal("Active = false, want true (env=1)")
	}
}

// TestBuildStatusReport_AgentModeInertWithBadValue covers the
// debugging-affordance case: BACIO_AGENT_MODE is set but to a value
// the restrictive parser refuses (e.g. "yes" or "TRUE"). The raw value
// must round-trip into the JSON so the user can see why their gate
// isn't activating; Active stays false.
func TestBuildStatusReport_AgentModeInertWithBadValue(t *testing.T) {
	t.Setenv("BACIO_AGENT_MODE", "yes")

	dir := t.TempDir()
	withCwd(t, dir)
	s := openMemStore(t)

	report, err := buildStatusReport(s, testEnv("/db.sqlite"), dir)
	if err != nil {
		t.Fatalf("buildStatusReport: %v", err)
	}
	if report.AgentMode.Value != "yes" {
		t.Fatalf("Value = %q, want %q", report.AgentMode.Value, "yes")
	}
	if report.AgentMode.Active {
		t.Fatal("Active = true, want false (\"yes\" is not on the accept list)")
	}
}

// TestPrintStatus_AgentModeText asserts the human-readable status output
// renders BACIO_AGENT_MODE on a dedicated line. Belt-and-braces against
// a future regression that drops the affordance.
func TestPrintStatus_AgentModeText(t *testing.T) {
	cases := []struct {
		name     string
		value    string
		set      bool
		want     string
		wantTags []string
	}{
		{
			name:     "unset",
			set:      false,
			wantTags: []string{"BACIO_AGENT_MODE", "unset", "inert"},
		},
		{
			name:     "active",
			set:      true,
			value:    "1",
			wantTags: []string{"BACIO_AGENT_MODE=1", "active"},
		},
		{
			name:     "inert_bad_value",
			set:      true,
			value:    "yes",
			wantTags: []string{"BACIO_AGENT_MODE=yes", "inert"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if tc.set {
				t.Setenv("BACIO_AGENT_MODE", tc.value)
			} else {
				t.Setenv("BACIO_AGENT_MODE", "scratch")
				if err := os.Unsetenv("BACIO_AGENT_MODE"); err != nil {
					t.Fatalf("unset: %v", err)
				}
			}
			dir := t.TempDir()
			withCwd(t, dir)
			s := openMemStore(t)
			report, err := buildStatusReport(s, testEnv("/db.sqlite"), dir)
			if err != nil {
				t.Fatalf("buildStatusReport: %v", err)
			}
			var buf bytes.Buffer
			if err := printStatus(&buf, report); err != nil {
				t.Fatalf("printStatus: %v", err)
			}
			got := buf.String()
			for _, want := range tc.wantTags {
				if !strings.Contains(got, want) {
					t.Fatalf("printStatus output missing %q\nfull output:\n%s", want, got)
				}
			}
		})
	}
}

// TestStatusReportJSONShape is a regression guard: the agent_mode JSON
// shape is part of the public output contract. Adding fields is fine;
// renaming or dropping breaks consumers (LLM agents reading the JSON).
func TestStatusReportJSONShape(t *testing.T) {
	t.Setenv("BACIO_AGENT_MODE", "1")

	dir := t.TempDir()
	withCwd(t, dir)
	s := openMemStore(t)
	report, err := buildStatusReport(s, testEnv("/db.sqlite"), dir)
	if err != nil {
		t.Fatalf("buildStatusReport: %v", err)
	}
	data, err := json.Marshal(report)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	top := map[string]json.RawMessage{}
	if err := json.Unmarshal(data, &top); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	raw, ok := top["agent_mode"]
	if !ok {
		t.Fatalf("agent_mode field missing from status JSON; got keys=%v", keys(top))
	}
	var am map[string]any
	if err := json.Unmarshal(raw, &am); err != nil {
		t.Fatalf("agent_mode is not an object: %v", err)
	}
	for _, want := range []string{"env_var", "value", "active"} {
		if _, ok := am[want]; !ok {
			t.Fatalf("agent_mode missing field %q; got %+v", want, am)
		}
	}
	if am["env_var"] != "BACIO_AGENT_MODE" {
		t.Fatalf("env_var = %v, want BACIO_AGENT_MODE", am["env_var"])
	}
	if am["active"] != true {
		t.Fatalf("active = %v, want true", am["active"])
	}
}

func keys(m map[string]json.RawMessage) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
