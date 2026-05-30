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

// TestApplyBacioHooksKeepsAllPostToolUseGroups is the multi-row
// regression guard for the PostToolUse event. BACI-147 added the
// `set-title` row alongside the original task-list mirror; BACI-159
// added a third heartbeat row with an empty matcher (it must fire on
// every supervisor tool call, not only TaskCreate / register).
// applyBacioHooks must land all three groups under PostToolUse in a
// single pass, not let a subsequent iteration clobber the earlier
// ones. Re-runs apply twice in a row to also catch a drift where the
// second run drops a just-written group.
func TestApplyBacioHooksKeepsAllPostToolUseGroups(t *testing.T) {
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
		if len(bacioGroups) != 3 {
			t.Fatalf("pass %d: PostToolUse bacio-owned groups = %d, want 3", pass, len(bacioGroups))
		}

		// Each matched bacio group is identified by the command marker:
		// post-tool-use → TaskCreate|TaskUpdate, set-title →
		// mcp__bacio__register, post-tool-use-heartbeat → no matcher.
		// Group lookups by command so a regression that drops one
		// (e.g. a refactor that rewrites both with the same matcher)
		// fails loudly here.
		byCommand := map[string]map[string]any{}
		for _, g := range bacioGroups {
			raw := string(mustJSON(t, g))
			switch {
			case strings.Contains(raw, "bacio hook post-tool-use-heartbeat"):
				byCommand["post-tool-use-heartbeat"] = g
			case strings.Contains(raw, "bacio hook set-title"):
				byCommand["set-title"] = g
			case strings.Contains(raw, "bacio hook post-tool-use"):
				byCommand["post-tool-use"] = g
			}
		}
		for _, want := range []string{"post-tool-use", "set-title", "post-tool-use-heartbeat"} {
			if _, ok := byCommand[want]; !ok {
				t.Fatalf("pass %d: missing PostToolUse group for command %q", pass, want)
			}
		}
		if m, _ := byCommand["post-tool-use"]["matcher"].(string); m != "TaskCreate|TaskUpdate" {
			t.Fatalf("pass %d: post-tool-use matcher = %q, want TaskCreate|TaskUpdate", pass, m)
		}
		if m, _ := byCommand["set-title"]["matcher"].(string); m != "mcp__bacio__register" {
			t.Fatalf("pass %d: set-title matcher = %q, want mcp__bacio__register", pass, m)
		}
		// The heartbeat group MUST NOT carry a matcher — it has to
		// fire on every supervisor tool call so a long Task-spawned
		// subagent run keeps bumping the parent's last_seen_at.
		if _, hasMatcher := byCommand["post-tool-use-heartbeat"]["matcher"]; hasMatcher {
			t.Fatalf("pass %d: post-tool-use-heartbeat must not carry a matcher (got %v)",
				pass, byCommand["post-tool-use-heartbeat"]["matcher"])
		}
	}
}

// TestInstallHooks_StopFailureRow pins the BACI-296 wiring: after apply,
// .claude/settings.json carries a matcher-less `bacio hook stop-failure`
// group under the StopFailure event, and a second apply is idempotent
// (exactly one such group, not stacked).
func TestInstallHooks_StopFailureRow(t *testing.T) {
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
				StopFailure []map[string]any `json:"StopFailure"`
			} `json:"hooks"`
		}
		if err := readJSON(t, path, &settings); err != nil {
			t.Fatalf("pass %d read: %v", pass, err)
		}
		var bacioGroups []map[string]any
		for _, g := range settings.Hooks.StopFailure {
			if strings.Contains(string(mustJSON(t, g)), "bacio hook stop-failure") {
				bacioGroups = append(bacioGroups, g)
			}
		}
		if len(bacioGroups) != 1 {
			t.Fatalf("pass %d: StopFailure stop-failure groups = %d, want 1 (idempotent)", pass, len(bacioGroups))
		}
		// Event-typed: must not carry a matcher.
		if _, hasMatcher := bacioGroups[0]["matcher"]; hasMatcher {
			t.Fatalf("pass %d: stop-failure must not carry a matcher (got %v)", pass, bacioGroups[0]["matcher"])
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
// BACI-49 + BACI-301 contract: the post-install activation banner
// mentions BACIO_AGENT_MODE, the recommended launch command (with the
// two dangerously-* flags and the injected reverse-proxy env), and the
// "inert" reminder so a user who just ran install-agent sees one
// copy-paste line that wires up the env var, the per-tool approval
// waiver, the native-channels transport, and the proxy endpoint.
func TestInstallAgentActivationBannerIncludesEnvVar(t *testing.T) {
	const endpoint = "http://127.0.0.1:5320/anthropic"
	var buf strings.Builder
	printActivationBanner(&buf, endpoint, "")
	got := buf.String()
	for _, want := range []string{
		"BACIO_AGENT_MODE",
		"BACIO_AGENT_MODE=1 ANTHROPIC_BASE_URL=http://127.0.0.1:5320/anthropic ENABLE_TOOL_SEARCH=true claude --dangerously-skip-permissions --dangerously-load-development-channels server:bacio",
		"inert",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("activation banner missing %q; got:\n%s", want, got)
		}
	}
}
