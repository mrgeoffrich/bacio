package cli

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
)

// TestBacioHookGroupMatcher locks in the matcher contract: the four
// event-typed hooks never carry a matcher (the field is omitted), and
// each tool-call subcommand always carries the literal scoped to its
// handler — `post-tool-use` → `TaskCreate|TaskUpdate` (BACI-60: Claude
// Code 2.1 renamed TodoWrite into the Task* family), `set-title` →
// `mcp__bacio__register` (BACI-147: only fire on a successful
// `register` MCP call), `pre-tool-use` → `Write|Edit|Bash` (BACI-116:
// worktree-confinement guard; BACI-134: widened to cover Bash for
// the sqlite3 confinement guard). A drift here silently breaks the
// relevant hook because Claude Code matches the entry against every
// tool call.
func TestBacioHookGroupMatcher(t *testing.T) {
	// Keyed by subcommand so two PostToolUse entries carrying distinct
	// matchers are both covered (BACI-147 added the second one).
	wantMatcher := map[string]string{
		"post-tool-use": "TaskCreate|TaskUpdate",
		"set-title":     "mcp__bacio__register",
		"pre-tool-use":  "Write|Edit|Bash",
	}
	for _, ev := range bacioHookEvents {
		grp := bacioHookGroup(ev.Subcommand, ev.Matcher)
		_, hasMatcher := grp["matcher"]
		if want, ok := wantMatcher[ev.Subcommand]; ok {
			if !hasMatcher {
				t.Fatalf("%s/%s group is missing matcher", ev.Event, ev.Subcommand)
			}
			if grp["matcher"].(string) != want {
				t.Fatalf("%s/%s matcher = %q, want %q", ev.Event, ev.Subcommand, grp["matcher"], want)
			}
		} else if hasMatcher {
			t.Fatalf("%s/%s group should not carry a matcher (got %q)", ev.Event, ev.Subcommand, grp["matcher"])
		}
	}
}

// TestApplyBacioHooksWritesPostToolUseMatcher roundtrips the apply
// step into a temp settings.json and confirms the on-disk JSON carries
// the matcher per bacio-owned group: every event-typed entry has none,
// and each tool-call entry carries the literal scoped to its handler.
// Catches a future regression where the JSON shape changes underfoot.
//
// PostToolUse carries two bacio groups (BACI-147 added set-title
// alongside the existing post-tool-use mirror), so groups are located
// by command marker rather than by "first bacio-owned group on the
// event".
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
		// Find the bacio-owned group for THIS subcommand — multiple
		// bacio groups can share one event (PostToolUse since BACI-147).
		wantCommand := "bacio hook " + ev.Subcommand
		var bacioGroup map[string]any
		for _, g := range groups {
			if strings.Contains(string(mustJSON(t, g)), wantCommand) {
				bacioGroup = g
				break
			}
		}
		if bacioGroup == nil {
			t.Fatalf("%s/%s: no bacio-owned group present", ev.Event, ev.Subcommand)
		}
		matcher, hasMatcher := bacioGroup["matcher"]
		switch ev.Matcher {
		case "":
			if hasMatcher {
				t.Fatalf("%s/%s group should not carry a matcher (got %v)", ev.Event, ev.Subcommand, matcher)
			}
		default:
			if !hasMatcher {
				t.Fatalf("%s/%s group should carry matcher %q", ev.Event, ev.Subcommand, ev.Matcher)
			}
			if got := matcher.(string); got != ev.Matcher {
				t.Fatalf("%s/%s matcher = %q, want %q", ev.Event, ev.Subcommand, got, ev.Matcher)
			}
		}
	}
}

// TestApplyBacioHooksKeepsBothPostToolUseGroups is the BACI-147
// regression guard for the multi-group case: two bacio rows share the
// PostToolUse event (post-tool-use + set-title); applyBacioHooks must
// land both groups under PostToolUse in a single pass, not let the
// second iteration clobber the first one's write. Re-runs apply twice
// in a row to also catch a drift where the second run drops the
// just-written group.
func TestApplyBacioHooksKeepsBothPostToolUseGroups(t *testing.T) {
	dir := t.TempDir()
	path := dir + "/settings.json"
	for pass := 1; pass <= 2; pass++ {
		top, _, err := planBacioHooks(path)
		if err != nil {
			t.Fatalf("pass %d plan: %v", pass, err)
		}
		if err := applyBacioHooks(path, top); err != nil {
			t.Fatalf("pass %d apply: %v", pass, err)
		}

		var settings struct {
			Hooks struct {
				PostToolUse []map[string]any `json:"PostToolUse"`
			} `json:"hooks"`
		}
		if err := readJSON(t, path, &settings); err != nil {
			t.Fatalf("pass %d read: %v", pass, err)
		}

		var bacioGroups []map[string]any
		for _, g := range settings.Hooks.PostToolUse {
			if strings.Contains(string(mustJSON(t, g)), bacioHookMarker) {
				bacioGroups = append(bacioGroups, g)
			}
		}
		if len(bacioGroups) != 2 {
			t.Fatalf("pass %d: PostToolUse bacio-owned groups = %d, want 2", pass, len(bacioGroups))
		}

		// Each bacio group must carry its own distinct matcher — the
		// task-list mirror's TaskCreate|TaskUpdate and BACI-147's
		// mcp__bacio__register. The two are sentinels: a regression
		// that drops one of them or rewrites both with the same matcher
		// fails here loudly.
		gotMatchers := map[string]bool{}
		for _, g := range bacioGroups {
			m, _ := g["matcher"].(string)
			gotMatchers[m] = true
		}
		for _, want := range []string{"TaskCreate|TaskUpdate", "mcp__bacio__register"} {
			if !gotMatchers[want] {
				t.Fatalf("pass %d: missing PostToolUse matcher %q (got %v)", pass, want, gotMatchers)
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
