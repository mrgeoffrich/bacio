package cli

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
)

// TestBacioHookGroupMatcher locks in the matcher contract: the four
// event-typed hooks never carry a matcher (the field is omitted), and
// the two tool-call hooks always carry one — PostToolUse the
// pipe-alternation literal `TaskCreate|TaskUpdate` (BACI-60: Claude
// Code 2.1 renamed TodoWrite into the Task* family), PreToolUse
// `Write|Edit` (BACI-116: the worktree-confinement guard). A drift here
// silently breaks the relevant hook because Claude Code matches the
// entry against every tool call.
func TestBacioHookGroupMatcher(t *testing.T) {
	wantMatcher := map[string]string{
		"PostToolUse": "TaskCreate|TaskUpdate",
		"PreToolUse":  "Write|Edit",
	}
	for _, ev := range bacioHookEvents {
		grp := bacioHookGroup(ev.Subcommand, ev.Matcher)
		_, hasMatcher := grp["matcher"]
		if want, ok := wantMatcher[ev.Event]; ok {
			if !hasMatcher {
				t.Fatalf("%s group is missing matcher", ev.Event)
			}
			if grp["matcher"].(string) != want {
				t.Fatalf("%s matcher = %q, want %q", ev.Event, grp["matcher"], want)
			}
		} else if hasMatcher {
			t.Fatalf("%s group should not carry a matcher (got %q)", ev.Event, grp["matcher"])
		}
	}
}

// TestApplyBacioHooksWritesPostToolUseMatcher roundtrips the apply
// step into a temp settings.json and confirms the on-disk JSON carries
// the matcher exactly once on PostToolUse and nowhere else. Catches a
// future regression where the JSON shape changes underfoot.
func TestApplyBacioHooksWritesPostToolUseMatcher(t *testing.T) {
	dir := t.TempDir()
	path := dir + "/settings.json"
	top, _, err := planBacioHooks(path)
	if err != nil {
		t.Fatalf("plan: %v", err)
	}
	if err := applyBacioHooks(path, top); err != nil {
		t.Fatalf("apply: %v", err)
	}
	got := map[string]json.RawMessage{}
	if err := readJSON(t, path, &got); err != nil {
		t.Fatalf("read: %v", err)
	}
	hooks := map[string]json.RawMessage{}
	if err := json.Unmarshal(got["hooks"], &hooks); err != nil {
		t.Fatalf("hooks unmarshal: %v", err)
	}
	for _, ev := range bacioHookEvents {
		raw, ok := hooks[ev.Event]
		if !ok {
			t.Fatalf("missing event %q", ev.Event)
		}
		var groups []map[string]any
		if err := json.Unmarshal(raw, &groups); err != nil {
			t.Fatalf("%s groups unmarshal: %v", ev.Event, err)
		}
		// Find bacio's own group via its command marker — non-bacio
		// hooks on the same event would be passed through untouched.
		var bacioGroup map[string]any
		for _, g := range groups {
			if strings.Contains(string(mustJSON(t, g)), bacioHookMarker) {
				bacioGroup = g
				break
			}
		}
		if bacioGroup == nil {
			t.Fatalf("%s: no bacio-owned group present", ev.Event)
		}
		matcher, hasMatcher := bacioGroup["matcher"]
		switch ev.Matcher {
		case "":
			if hasMatcher {
				t.Fatalf("%s group should not carry a matcher (got %v)", ev.Event, matcher)
			}
		default:
			if !hasMatcher {
				t.Fatalf("%s group should carry matcher %q", ev.Event, ev.Matcher)
			}
			if got := matcher.(string); got != ev.Matcher {
				t.Fatalf("%s matcher = %q, want %q", ev.Event, got, ev.Matcher)
			}
		}
	}
}

func mustJSON(t *testing.T, v any) []byte {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return b
}

func readJSON(t *testing.T, path string, out any) error {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	return json.Unmarshal(data, out)
}

// TestInstallAgentActivationBannerIncludesEnvVar pins the BACI-48 +
// BACI-49 contract: the post-install activation banner mentions
// BACIO_AGENT_MODE, the recommended launch command, and the two
// dangerously-* flags BACI-49 added so a user who just ran
// install-agent sees one copy-paste line that wires up the env var,
// the per-tool approval waiver, and the native-channels transport.
func TestInstallAgentActivationBannerIncludesEnvVar(t *testing.T) {
	var buf strings.Builder
	printActivationBanner(&buf)
	got := buf.String()
	for _, want := range []string{
		"BACIO_AGENT_MODE",
		"BACIO_AGENT_MODE=1 claude --dangerously-skip-permissions --dangerously-load-development-channels server:bacio",
		"inert",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("activation banner missing %q; got:\n%s", want, got)
		}
	}
}
