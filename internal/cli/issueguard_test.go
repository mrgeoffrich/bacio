package cli

import (
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

// TestCanonicalIssueKeyRecognisesPrefix locks in BACI-126b's key
// extraction: a canonical PREFIX-N input becomes its uppercase form,
// bare numbers and bare prefixes return "" (the gate skips them — the
// per-verb RunE resolves bare numbers via cwd).
func TestCanonicalIssueKey(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"MINI-42", "MINI-42"},
		{"mini-42", "MINI-42"},
		{"  MINI-42  ", "MINI-42"},
		{"42", ""},
		{"MINI", ""},
		{"", ""},
		{"not-an-issue-key", ""},
	}
	for _, tc := range cases {
		got := canonicalIssueKey(tc.in)
		if got != tc.want {
			t.Errorf("canonicalIssueKey(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// TestSortedKeys verifies the deterministic ordering used in error
// messages — held-claims lists need to be stable across runs.
func TestSortedKeys(t *testing.T) {
	in := map[string]struct{}{"MINI-3": {}, "MINI-1": {}, "MINI-2": {}}
	got := sortedKeys(in)
	want := []string{"MINI-1", "MINI-2", "MINI-3"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("sortedKeys = %v, want %v", got, want)
	}
}

// TestExtractTargetedKeysFromPositionals checks the gate parses the
// targeted issue key from a positional first-arg for single-key verbs,
// and from the configured slots for link/unlink (two-key verbs).
func TestExtractTargetedKeysFromPositionals(t *testing.T) {
	cmd := &cobra.Command{Use: "show"}
	addInputFlag(cmd, new(string))

	cases := []struct {
		name string
		ext  keyExtractor
		args []string
		want []string
	}{
		{
			name: "issue show MINI-42",
			ext:  keyExtractor{fromArgs: 0},
			args: []string{"MINI-42"},
			want: []string{"MINI-42"},
		},
		{
			name: "issue add (no key)",
			ext:  keyExtractor{fromArgs: -1},
			args: []string{},
			want: nil,
		},
		{
			name: "link MINI-1 blocks MINI-2",
			ext:  keyExtractor{fromArgs: 0, secondaryArgs: 2},
			args: []string{"MINI-1", "blocks", "MINI-2"},
			want: []string{"MINI-1", "MINI-2"},
		},
		{
			name: "unlink MINI-1 MINI-2",
			ext:  keyExtractor{fromArgs: 0, secondaryArgs: 1},
			args: []string{"MINI-1", "MINI-2"},
			want: []string{"MINI-1", "MINI-2"},
		},
		{
			name: "bare number not extracted",
			ext:  keyExtractor{fromArgs: 0},
			args: []string{"42"},
			want: nil,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := extractTargetedKeys(cmd, tc.args, tc.ext)
			if err != nil {
				t.Fatalf("extractTargetedKeys: %v", err)
			}
			// Compare ignoring nil vs empty distinction.
			if len(got) != len(tc.want) {
				t.Fatalf("got %v, want %v", got, tc.want)
			}
			if len(got) == 0 {
				return
			}
			sort.Strings(got)
			sort.Strings(tc.want)
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("extractTargetedKeys = %v, want %v", got, tc.want)
			}
		})
	}
}

// TestExtractTargetedKeysFromJSON checks the gate reads the targeted
// issue key from the configured field of a --json payload (priority
// over positionals — `parseJSONInput` rejects mixed input but
// extractTargetedKeys runs before the verb's RunE).
func TestExtractTargetedKeysFromJSON(t *testing.T) {
	cmd := &cobra.Command{Use: "edit"}
	var raw string
	addInputFlag(cmd, &raw)
	cmd.Flags().Set("json", `{"key":"MINI-42","title":"x"}`)

	got, err := extractTargetedKeys(cmd, nil, keyExtractor{fromArgs: -1, fromJSONField: "key"})
	if err != nil {
		t.Fatalf("extractTargetedKeys: %v", err)
	}
	if len(got) != 1 || got[0] != "MINI-42" {
		t.Errorf("got %v, want [MINI-42]", got)
	}
}

// TestExtractTargetedKeysFromJSONLink covers the two-key link payload —
// both `from` and `to` should be returned for the gate to verify.
func TestExtractTargetedKeysFromJSONLink(t *testing.T) {
	cmd := &cobra.Command{Use: "link"}
	var raw string
	addInputFlag(cmd, &raw)
	cmd.Flags().Set("json", `{"from":"MINI-1","type":"blocks","to":"MINI-2"}`)

	got, err := extractTargetedKeys(cmd, nil, keyExtractor{
		fromArgs:           -1,
		secondaryArgs:      -1,
		fromJSONField:      "from",
		secondaryJSONField: "to",
	})
	if err != nil {
		t.Fatalf("extractTargetedKeys: %v", err)
	}
	want := []string{"MINI-1", "MINI-2"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

// TestExtractTargetedKeysIgnoresBareNumberInJSON verifies the gate
// doesn't treat a bare number in the JSON payload as a key — the per-
// verb RunE handles the bare-number shortcut by resolving against
// cwd's repo, but the gate has no cwd context, so it pretends bare
// numbers aren't keys and lets the verb's own validation surface the
// human-CLI shortcut error.
func TestExtractTargetedKeysIgnoresBareNumberInJSON(t *testing.T) {
	cmd := &cobra.Command{Use: "show"}
	var raw string
	addInputFlag(cmd, &raw)
	cmd.Flags().Set("json", `{"key":"42"}`)

	got, err := extractTargetedKeys(cmd, nil, keyExtractor{fromArgs: -1, fromJSONField: "key"})
	if err != nil {
		t.Fatalf("extractTargetedKeys: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("got %v, want empty (bare number must not be treated as a canonical key)", got)
	}
}

// TestKeyExtractorsCoverAllGatedVerbs sanity-checks the verb→extractor
// table covers every key-targeted gated verb. New verbs added to a
// gated group must land here so the gate keeps catching drift.
func TestKeyExtractorsCoverAllGatedVerbs(t *testing.T) {
	expected := []string{
		"issue show", "issue brief", "issue edit", "issue state",
		"issue assign", "issue unassign", "issue rm",
		"issue archive", "issue unarchive",
		"comment add", "comment rm", "comment list",
		"tag add", "tag rm",
		"pr attach", "pr detach", "pr list",
		"link", "unlink",
	}
	for _, name := range expected {
		if _, ok := keyExtractors[name]; !ok {
			t.Errorf("keyExtractors missing entry for %q — verb is in a gated group but has no key-extraction rule", name)
		}
	}
}

// TestRequireClaimForGroupHumanPasses pins the no-op-for-humans case:
// when actor() returns "user" (no .bacio/agents.json entry for this
// process), the gate is a no-op regardless of what the command is.
func TestRequireClaimForGroupHumanPasses(t *testing.T) {
	// agentIdentityForProcess is memoised via sync.Once on procIdentityName,
	// which is "" in this test process unless a sibling test has seeded
	// it. Verify that the test environment is "human" before continuing
	// — if a prior test populated agents.json against our pid, skip
	// loudly so we don't false-pass.
	if agentIdentityForProcess() != "" {
		t.Skip("test process has an agent identity recorded; this case requires a clean human caller")
	}
	cmd := &cobra.Command{Use: "show"}
	addInputFlag(cmd, new(string))
	if err := requireClaimForGroup(cmd, []string{"MINI-42"}); err != nil {
		t.Errorf("requireClaimForGroup returned error for human caller: %v", err)
	}
}

// TestRequireClaimNoOpenClaimErrorWording locks in the exact phrasing
// of the "no open claim" error — the message is the human-facing
// contract for the gate, and an agent's error-grep loop depends on it.
//
// Driven by the fmt.Errorf in requireClaimForGroup; this test exists
// to catch accidental wording drift.
func TestRequireClaimNoOpenClaimErrorWording(t *testing.T) {
	want := "this agent has no open claim"
	want2 := "before running"
	const sample = "bacio: this agent has no open claim — call `bacio agent claim <KEY>` before running `bacio issue show`."
	if !strings.Contains(sample, want) {
		t.Errorf("wording drift: error must contain %q", want)
	}
	if !strings.Contains(sample, want2) {
		t.Errorf("wording drift: error must contain %q", want2)
	}
}

// TestClaimPolicyAllRequiresEveryKey covers the default BACI-126b
// behaviour: holding only one of two targeted keys is refused, and the
// error names the missing one.
func TestClaimPolicyAllRequiresEveryKey(t *testing.T) {
	held := map[string]struct{}{"MINI-1": {}}
	err := checkClaims(held, []string{"MINI-1", "MINI-2"}, policyAll, "bacio issue edit")
	if err == nil {
		t.Fatal("policyAll with one of two keys held should fail")
	}
	if !strings.Contains(err.Error(), "MINI-2") {
		t.Errorf("error should name the missing key MINI-2: %v", err)
	}
	if !strings.Contains(err.Error(), "cannot operate on") {
		t.Errorf("policyAll wording drift; got: %v", err)
	}
}

// TestClaimPolicyAllPassesWhenAllPresent locks in the pass case for
// the default policy.
func TestClaimPolicyAllPassesWhenAllPresent(t *testing.T) {
	held := map[string]struct{}{"MINI-1": {}, "MINI-2": {}}
	if err := checkClaims(held, []string{"MINI-1", "MINI-2"}, policyAll, "bacio link"); err != nil {
		t.Errorf("policyAll with all keys held should pass; got: %v", err)
	}
}

// TestClaimPolicyAnyPassesOnPrimary covers the BACI-170 relaxation:
// for link / unlink, a claim on the primary (from / a) side is
// sufficient.
func TestClaimPolicyAnyPassesOnPrimary(t *testing.T) {
	held := map[string]struct{}{"MINI-1": {}}
	if err := checkClaims(held, []string{"MINI-1", "MINI-2"}, policyAny, "bacio link"); err != nil {
		t.Errorf("policyAny with primary claimed should pass; got: %v", err)
	}
}

// TestClaimPolicyAnyPassesOnSecondary covers the symmetric branch:
// a claim on the secondary (to / b) side also satisfies the gate, so
// a reviewer agent can back-fill the relation from its own ticket.
func TestClaimPolicyAnyPassesOnSecondary(t *testing.T) {
	held := map[string]struct{}{"MINI-2": {}}
	if err := checkClaims(held, []string{"MINI-1", "MINI-2"}, policyAny, "bacio link"); err != nil {
		t.Errorf("policyAny with secondary claimed should pass; got: %v", err)
	}
}

// TestClaimPolicyAnyRefusesWhenNeitherHeld locks in the drift case
// the relaxed gate still catches, and pins the new error wording —
// agents will grep this message to recover gracefully.
func TestClaimPolicyAnyRefusesWhenNeitherHeld(t *testing.T) {
	held := map[string]struct{}{"MINI-3": {}}
	err := checkClaims(held, []string{"MINI-1", "MINI-2"}, policyAny, "bacio link")
	if err == nil {
		t.Fatal("policyAny with neither side claimed should fail")
	}
	if !strings.Contains(err.Error(), "requires a claim on at least one side of the relation") {
		t.Errorf("policyAny error wording drift; got: %v", err)
	}
	if !strings.Contains(err.Error(), "MINI-1") || !strings.Contains(err.Error(), "MINI-2") {
		t.Errorf("policyAny error must list both extracted keys; got: %v", err)
	}
	if !strings.Contains(err.Error(), "MINI-3") {
		t.Errorf("policyAny error must show the held claim so the agent knows what it has; got: %v", err)
	}
}

// TestKeyExtractorsLinkUsesPolicyAny sanity-checks that the link /
// unlink entries declare the relaxed policy. A future refactor that
// drops the policy field on these would silently revert BACI-170.
func TestKeyExtractorsLinkUsesPolicyAny(t *testing.T) {
	for _, name := range []string{"link", "unlink"} {
		ext, ok := keyExtractors[name]
		if !ok {
			t.Errorf("keyExtractors missing %q", name)
			continue
		}
		if ext.policy != policyAny {
			t.Errorf("keyExtractors[%q].policy = %d, want policyAny (%d)", name, ext.policy, policyAny)
		}
	}
}

// TestKeyExtractorsOtherGatedVerbsUsePolicyAll guards against
// accidentally relaxing the gate on a verb that really does mutate
// the targeted ticket.
func TestKeyExtractorsOtherGatedVerbsUsePolicyAll(t *testing.T) {
	for name, ext := range keyExtractors {
		if name == "link" || name == "unlink" {
			continue
		}
		if ext.policy != policyAll {
			t.Errorf("keyExtractors[%q].policy = %d, want policyAll (%d) — only link/unlink should be policyAny", name, ext.policy, policyAll)
		}
	}
}
