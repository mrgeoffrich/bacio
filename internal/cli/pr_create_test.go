package cli

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mrgeoffrich/bacio/internal/cli/inputs"
	"github.com/mrgeoffrich/bacio/internal/ghcli"
	"github.com/mrgeoffrich/bacio/internal/model"
	"github.com/mrgeoffrich/bacio/internal/store"
)

// fakeGH is a stand-in `ghcli.Client` that records every method call
// and returns the canned responses each test sets up. CreatePR can
// either return a successful URL or an error.
type fakeGH struct {
	prsByLabel    map[string][]ghcli.PRSummary
	listErr       error
	createLabelOK bool
	createLabelErr error
	createPRURL   string
	createPRErr   error

	listCalls   int
	labelCalls  int
	createCalls int
	lastExtra   []string
}

func (f *fakeGH) ListPRsByLabel(_ context.Context, label string) ([]ghcli.PRSummary, error) {
	f.listCalls++
	if f.listErr != nil {
		return nil, f.listErr
	}
	return f.prsByLabel[label], nil
}

func (f *fakeGH) CreateLabelIdempotent(_ context.Context, _, _ string) error {
	f.labelCalls++
	if f.createLabelErr != nil {
		return f.createLabelErr
	}
	if !f.createLabelOK {
		return nil
	}
	return nil
}

func (f *fakeGH) CreatePR(_ context.Context, _ string, extra []string) (string, error) {
	f.createCalls++
	f.lastExtra = append([]string(nil), extra...)
	if f.createPRErr != nil {
		return "", f.createPRErr
	}
	return f.createPRURL, nil
}

// setupPRCreateTest stands up an on-disk SQLite store (the wrapper
// also calls openStore for its history-op write, which the in-memory
// client.Open can't share with openClient), seeds a repo + issue,
// installs a fake gh client, and resets the global opts the wrapper
// reads. Returns the issue key and a cleanup function — t.Cleanup
// handles deregistration.
func setupPRCreateTest(t *testing.T, gh *fakeGH) string {
	t.Helper()
	return setupPRCreateTestWith(t, gh, prCreateTestOpts{})
}

// prCreateTestOpts parametrises the BACI-228 base-branch resolver
// inputs for setupPRCreateTestWith. Empty values use the equivalent of
// the legacy setupPRCreateTest (no feature, no per-issue override).
type prCreateTestOpts struct {
	// FeatureBranch, if non-empty, seeds a parent feature with the
	// given branch_name and attaches the issue to it.
	FeatureBranch string
	// IssueBaseBranch, if non-empty, is stamped as the per-issue
	// base_branch override on the seeded issue.
	IssueBaseBranch string
}

func setupPRCreateTestWith(t *testing.T, gh *fakeGH, o prCreateTestOpts) string {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "db.sqlite")
	prevDB := opts.dbPath
	prevDry := opts.dryRun
	opts.dbPath = dbPath
	opts.dryRun = false
	t.Cleanup(func() {
		opts.dbPath = prevDB
		opts.dryRun = prevDry
	})

	s, err := store.Open(dbPath)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	repo, err := s.CreateRepo("MINI", "miniproject", filepath.Join(t.TempDir(), "miniproject"), "")
	if err != nil {
		t.Fatalf("create repo: %v", err)
	}
	var featureID *int64
	if o.FeatureBranch != "" {
		feat, ferr := s.CreateFeature(repo.ID, "auth-rewrite", "Auth rewrite", "", "", o.FeatureBranch)
		if ferr != nil {
			t.Fatalf("create feature: %v", ferr)
		}
		featureID = &feat.ID
	}
	iss, err := s.CreateIssue(repo.ID, featureID, "Login broken on Safari", "", model.StateTodo, nil, o.IssueBaseBranch, "")
	if err != nil {
		t.Fatalf("create issue: %v", err)
	}
	_ = s.Close()

	prev := newGHCli
	newGHCli = func() ghcli.Client { return gh }
	t.Cleanup(func() { newGHCli = prev })

	return iss.Key
}

func TestPRCreate_RefusesOnOpen(t *testing.T) {
	gh := &fakeGH{
		prsByLabel: map[string][]ghcli.PRSummary{
			"bacio:MINI-1": {{Number: 99, State: "OPEN", URL: "https://github.com/example/mini/pull/99"}},
		},
	}
	key := setupPRCreateTest(t, gh)

	err := runPRCreate(inputs.PRCreateInput{IssueKey: key})
	if err == nil {
		t.Fatal("expected pre-flight refusal, got nil")
	}
	if !strings.Contains(err.Error(), "OPEN") || !strings.Contains(err.Error(), "PR #99") {
		t.Fatalf("error should name the OPEN PR: %v", err)
	}
	if gh.labelCalls != 0 || gh.createCalls != 0 {
		t.Fatalf("refusal must short-circuit before label/create (label=%d create=%d)", gh.labelCalls, gh.createCalls)
	}
}

func TestPRCreate_RefusesOnMerged(t *testing.T) {
	gh := &fakeGH{
		prsByLabel: map[string][]ghcli.PRSummary{
			"bacio:MINI-1": {{Number: 77, State: "MERGED", URL: "https://github.com/example/mini/pull/77"}},
		},
	}
	key := setupPRCreateTest(t, gh)

	err := runPRCreate(inputs.PRCreateInput{IssueKey: key})
	if err == nil {
		t.Fatal("expected pre-flight refusal, got nil")
	}
	if !strings.Contains(err.Error(), "MERGED") {
		t.Fatalf("error should name the MERGED state: %v", err)
	}
	if gh.createCalls != 0 {
		t.Fatal("must not call CreatePR after MERGED refusal")
	}
}

func TestPRCreate_ClosedOnlyAllowsWithWarning(t *testing.T) {
	gh := &fakeGH{
		prsByLabel: map[string][]ghcli.PRSummary{
			"bacio:MINI-1": {{Number: 50, State: "CLOSED", URL: "https://github.com/example/mini/pull/50"}},
		},
		createPRURL: "https://github.com/example/mini/pull/55",
	}
	key := setupPRCreateTest(t, gh)

	if err := runPRCreate(inputs.PRCreateInput{
		IssueKey: key,
		GHArgs:   []string{"--title", "x", "--body", "y"},
	}); err != nil {
		t.Fatalf("CLOSED-only should allow the PR: %v", err)
	}
	if gh.createCalls != 1 {
		t.Fatalf("expected one CreatePR call, got %d", gh.createCalls)
	}
	if gh.labelCalls != 1 {
		t.Fatalf("expected one CreateLabelIdempotent call, got %d", gh.labelCalls)
	}
	// The passthrough args reach gh with `--base main` injected at the
	// head by the wrapper (BACI-228); the caller's tail is preserved.
	want := []string{"--base", "main", "--title", "x", "--body", "y"}
	if len(gh.lastExtra) != len(want) {
		t.Fatalf("passthrough mismatch: got=%v want=%v", gh.lastExtra, want)
	}
	for i := range want {
		if gh.lastExtra[i] != want[i] {
			t.Fatalf("passthrough mismatch at %d: got=%v want=%v", i, gh.lastExtra, want)
		}
	}
}

func TestPRCreate_BacioAttachedRefuses(t *testing.T) {
	// gh returns nothing for the label, but the bacio side has an attached
	// PR — the belt + braces probe must refuse.
	gh := &fakeGH{
		prsByLabel: map[string][]ghcli.PRSummary{},
	}
	key := setupPRCreateTest(t, gh)
	// Attach a PR to the issue via the store so the wrapper picks it up.
	s, err := store.Open(opts.dbPath)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	_, err = s.GetRepoByPrefix("MINI")
	if err != nil {
		t.Fatalf("get repo: %v", err)
	}
	prefix, number, err := store.ParseIssueKey(key)
	if err != nil {
		t.Fatalf("parse key: %v", err)
	}
	iss, err := s.GetIssueByKey(prefix, number)
	if err != nil {
		t.Fatalf("get issue: %v", err)
	}
	if _, err := s.AttachPR(iss.ID, "https://github.com/example/mini/pull/123"); err != nil {
		t.Fatalf("attach: %v", err)
	}
	_ = s.Close()

	err = runPRCreate(inputs.PRCreateInput{IssueKey: key})
	if err == nil {
		t.Fatal("expected refusal when bacio has an attached PR")
	}
	if !strings.Contains(err.Error(), "attached to this issue") {
		t.Fatalf("error should name the bacio attachment: %v", err)
	}
	if gh.createCalls != 0 {
		t.Fatal("must not call CreatePR after bacio-attached refusal")
	}
}

func TestPRCreate_ForceOverridesOpenRefusal(t *testing.T) {
	gh := &fakeGH{
		prsByLabel: map[string][]ghcli.PRSummary{
			"bacio:MINI-1": {{Number: 99, State: "OPEN", URL: "https://github.com/example/mini/pull/99"}},
		},
		createPRURL: "https://github.com/example/mini/pull/100",
	}
	key := setupPRCreateTest(t, gh)

	if err := runPRCreate(inputs.PRCreateInput{
		IssueKey: key,
		Force:    true,
		GHArgs:   []string{"--title", "x", "--body", "y"},
	}); err != nil {
		t.Fatalf("--force must override pre-flight refusal: %v", err)
	}
	if gh.createCalls != 1 {
		t.Fatalf("expected one CreatePR call with --force, got %d", gh.createCalls)
	}
}

func TestPRCreate_DryRunNoSideEffects(t *testing.T) {
	gh := &fakeGH{
		prsByLabel:  map[string][]ghcli.PRSummary{}, // clean
		createPRURL: "https://github.com/example/mini/pull/999",
	}
	key := setupPRCreateTest(t, gh)
	opts.dryRun = true
	t.Cleanup(func() { opts.dryRun = false })

	if err := runPRCreate(inputs.PRCreateInput{
		IssueKey: key,
		GHArgs:   []string{"--title", "x"},
	}); err != nil {
		t.Fatalf("dry-run: %v", err)
	}
	if gh.labelCalls != 0 || gh.createCalls != 0 {
		t.Fatalf("dry-run must skip label/create (label=%d create=%d)", gh.labelCalls, gh.createCalls)
	}
	if gh.listCalls == 0 {
		t.Fatal("dry-run should still run the read-only pre-flight queries")
	}
	// No PR should have been attached either.
	s, err := store.Open(opts.dbPath)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer s.Close()
	_, _ = s.GetRepoByPrefix("MINI")
	prefix, number, parseErr := store.ParseIssueKey(key)
	if parseErr != nil {
		t.Fatalf("parse key: %v", parseErr)
	}
	iss, _ := s.GetIssueByKey(prefix, number)
	prs, err := s.ListPRs(iss.ID)
	if err != nil {
		t.Fatalf("list prs: %v", err)
	}
	if len(prs) != 0 {
		t.Fatalf("dry-run must not attach a PR, got %d", len(prs))
	}
}

func TestPRCreate_JSONPathEquivalent(t *testing.T) {
	gh := &fakeGH{
		prsByLabel:  map[string][]ghcli.PRSummary{},
		createPRURL: "https://github.com/example/mini/pull/200",
	}
	key := setupPRCreateTest(t, gh)

	// Same payload via the JSON-driven path: same end-state.
	if err := runPRCreate(inputs.PRCreateInput{
		IssueKey: key,
		GHArgs:   []string{"--title", "json", "--body", "json"},
	}); err != nil {
		t.Fatalf("json-path: %v", err)
	}
	if gh.createCalls != 1 {
		t.Fatalf("expected one CreatePR call, got %d", gh.createCalls)
	}
	// Issue should now have the PR attached.
	s, err := store.Open(opts.dbPath)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer s.Close()
	_, _ = s.GetRepoByPrefix("MINI")
	prefix, number, parseErr := store.ParseIssueKey(key)
	if parseErr != nil {
		t.Fatalf("parse key: %v", parseErr)
	}
	iss, _ := s.GetIssueByKey(prefix, number)
	prs, err := s.ListPRs(iss.ID)
	if err != nil {
		t.Fatalf("list prs: %v", err)
	}
	if len(prs) != 1 || prs[0].URL != gh.createPRURL {
		t.Fatalf("expected PR %q attached, got %+v", gh.createPRURL, prs)
	}
}

func TestPRCreate_WritesPRCreateHistoryOp(t *testing.T) {
	gh := &fakeGH{
		prsByLabel:  map[string][]ghcli.PRSummary{},
		createPRURL: "https://github.com/example/mini/pull/200",
	}
	key := setupPRCreateTest(t, gh)

	if err := runPRCreate(inputs.PRCreateInput{IssueKey: key, GHArgs: []string{"--title", "t", "--body", "b"}}); err != nil {
		t.Fatalf("runPRCreate: %v", err)
	}
	s, err := store.Open(opts.dbPath)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer s.Close()
	rows, err := s.ListHistory(store.HistoryFilter{Op: "pr.create"})
	if err != nil {
		t.Fatalf("list history: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("expected exactly one pr.create row, got %d", len(rows))
	}
	if !strings.Contains(rows[0].Details, gh.createPRURL) {
		t.Fatalf("pr.create row details should include URL %q: %+v", gh.createPRURL, rows[0])
	}
	// The inner pr.attach row should still exist on top.
	attaches, err := s.ListHistory(store.HistoryFilter{Op: "pr.attach"})
	if err != nil {
		t.Fatalf("list history (attach): %v", err)
	}
	if len(attaches) != 1 {
		t.Fatalf("expected one pr.attach row, got %d", len(attaches))
	}
}

func TestValidateGHArgs_RejectsLabel(t *testing.T) {
	cases := []struct {
		args []string
		wantErr bool
	}{
		{[]string{"--title", "x"}, false},
		{[]string{"--title", "x", "--label", "foo"}, true},
		{[]string{"--label=foo"}, true},
		{nil, false},
	}
	for i, c := range cases {
		err := validateGHArgs(c.args)
		if (err != nil) != c.wantErr {
			t.Errorf("case %d (%v): wantErr=%v got=%v", i, c.args, c.wantErr, err)
		}
	}
}

func TestClassifyPreflight(t *testing.T) {
	// Sanity: an OPEN gh row refuses, a CLOSED-only gh row warns, no
	// rows is silent.
	refusals, warnings := classifyPreflight(
		"bacio:MINI-1",
		[]ghcli.PRSummary{{Number: 1, State: "OPEN", URL: "x"}},
		nil,
	)
	if len(refusals) == 0 {
		t.Fatal("OPEN should refuse")
	}
	if len(warnings) != 0 {
		t.Fatal("OPEN should not warn (refusal dominates)")
	}

	refusals, warnings = classifyPreflight(
		"bacio:MINI-1",
		[]ghcli.PRSummary{{Number: 1, State: "CLOSED", URL: "x"}},
		nil,
	)
	if len(refusals) != 0 {
		t.Fatal("CLOSED-only should not refuse")
	}
	if len(warnings) == 0 {
		t.Fatal("CLOSED-only should warn")
	}

	refusals, warnings = classifyPreflight("bacio:MINI-1", nil, nil)
	if len(refusals) != 0 || len(warnings) != 0 {
		t.Fatalf("clean preflight should be silent, got refusals=%v warnings=%v", refusals, warnings)
	}
}

// extraHasBase reports whether the recorded passthrough args contain
// `--base <branch>` (the wrapper-injected pair). Helper for the
// BACI-228 base-branch injection tests.
func extraHasBase(extra []string, want string) bool {
	for i, a := range extra {
		if a == "--base" && i+1 < len(extra) && extra[i+1] == want {
			return true
		}
	}
	return false
}

// BACI-228: feature-branched issue with no per-issue override → the
// wrapper injects `--base <feature.branch_name>` so the PR opens
// against the feature integration branch, not gh's default.
func TestPRCreate_InjectsBaseFromFeatureBranch(t *testing.T) {
	gh := &fakeGH{
		prsByLabel:  map[string][]ghcli.PRSummary{},
		createPRURL: "https://github.com/example/mini/pull/300",
	}
	key := setupPRCreateTestWith(t, gh, prCreateTestOpts{FeatureBranch: "feat/auth-rewrite"})

	if err := runPRCreate(inputs.PRCreateInput{
		IssueKey: key,
		GHArgs:   []string{"--title", "t", "--body", "b"},
	}); err != nil {
		t.Fatalf("runPRCreate: %v", err)
	}
	if !extraHasBase(gh.lastExtra, "feat/auth-rewrite") {
		t.Fatalf("expected --base feat/auth-rewrite injected, got extra=%v", gh.lastExtra)
	}
}

// BACI-228: issue with override=main, parent feature on feat/X → the
// override wins (this is the terminal "merge feature → main" PR), so
// the wrapper injects `--base main`.
func TestPRCreate_IssueOverrideBeatsFeatureBranch(t *testing.T) {
	gh := &fakeGH{
		prsByLabel:  map[string][]ghcli.PRSummary{},
		createPRURL: "https://github.com/example/mini/pull/301",
	}
	key := setupPRCreateTestWith(t, gh, prCreateTestOpts{
		FeatureBranch:   "feat/auth-rewrite",
		IssueBaseBranch: "main",
	})

	if err := runPRCreate(inputs.PRCreateInput{
		IssueKey: key,
		GHArgs:   []string{"--title", "t", "--body", "b"},
	}); err != nil {
		t.Fatalf("runPRCreate: %v", err)
	}
	if !extraHasBase(gh.lastExtra, "main") {
		t.Fatalf("expected --base main (issue override wins), got extra=%v", gh.lastExtra)
	}
}

// BACI-228: plain main-targeted issue (no feature, no override) — the
// wrapper still injects `--base main` explicitly rather than relying
// on gh's default-branch detection.
func TestPRCreate_InjectsBaseMainForPlainIssue(t *testing.T) {
	gh := &fakeGH{
		prsByLabel:  map[string][]ghcli.PRSummary{},
		createPRURL: "https://github.com/example/mini/pull/302",
	}
	key := setupPRCreateTest(t, gh)

	if err := runPRCreate(inputs.PRCreateInput{
		IssueKey: key,
		GHArgs:   []string{"--title", "t", "--body", "b"},
	}); err != nil {
		t.Fatalf("runPRCreate: %v", err)
	}
	if !extraHasBase(gh.lastExtra, "main") {
		t.Fatalf("expected --base main injected for plain issue, got extra=%v", gh.lastExtra)
	}
}

// BACI-228: caller-passed `--base` on the verbatim tail wins — the
// wrapper must not inject a second `--base`.
func TestPRCreate_CallerBaseOverrideWins(t *testing.T) {
	gh := &fakeGH{
		prsByLabel:  map[string][]ghcli.PRSummary{},
		createPRURL: "https://github.com/example/mini/pull/303",
	}
	key := setupPRCreateTestWith(t, gh, prCreateTestOpts{FeatureBranch: "feat/auth-rewrite"})

	if err := runPRCreate(inputs.PRCreateInput{
		IssueKey: key,
		GHArgs:   []string{"--base", "other/branch", "--title", "t", "--body", "b"},
	}); err != nil {
		t.Fatalf("runPRCreate: %v", err)
	}
	// Count `--base` occurrences — must be exactly one, the caller's.
	bases := 0
	for _, a := range gh.lastExtra {
		if a == "--base" {
			bases++
		}
	}
	if bases != 1 {
		t.Fatalf("expected exactly one --base (caller-supplied) in passthrough, got %d in %v", bases, gh.lastExtra)
	}
	if !extraHasBase(gh.lastExtra, "other/branch") {
		t.Fatalf("expected --base other/branch (caller override) preserved, got extra=%v", gh.lastExtra)
	}
}

// BACI-228: the `--base=foo` shorthand also counts as a caller
// override — the wrapper must not double up.
func TestPRCreate_CallerBaseEqualsFormWins(t *testing.T) {
	gh := &fakeGH{
		prsByLabel:  map[string][]ghcli.PRSummary{},
		createPRURL: "https://github.com/example/mini/pull/304",
	}
	key := setupPRCreateTestWith(t, gh, prCreateTestOpts{FeatureBranch: "feat/auth-rewrite"})

	if err := runPRCreate(inputs.PRCreateInput{
		IssueKey: key,
		GHArgs:   []string{"--base=other/branch", "--title", "t", "--body", "b"},
	}); err != nil {
		t.Fatalf("runPRCreate: %v", err)
	}
	// Wrapper-injected `--base` is the two-token form, so its absence
	// is enough to confirm no injection happened.
	for _, a := range gh.lastExtra {
		if a == "--base" {
			t.Fatalf("expected no wrapper-injected --base when caller passed --base=... form, got extra=%v", gh.lastExtra)
		}
	}
}

func TestGHArgsHaveBase(t *testing.T) {
	cases := []struct {
		args []string
		want bool
	}{
		{nil, false},
		{[]string{"--title", "x"}, false},
		{[]string{"--base", "main"}, true},
		{[]string{"--title", "x", "--base", "feat/y"}, true},
		{[]string{"--base=main"}, true},
		{[]string{"--baseline", "x"}, false}, // prefix-collision guard
	}
	for i, c := range cases {
		if got := ghArgsHaveBase(c.args); got != c.want {
			t.Errorf("case %d (%v): want=%v got=%v", i, c.args, c.want, got)
		}
	}
}

// Sanity: the label has the form `bacio:<canonical-key>` and survives
// a roundtrip through canonicalisation (uppercased PREFIX).
func TestPRCreate_LabelHasCanonicalKey(t *testing.T) {
	gh := &fakeGH{
		prsByLabel:  map[string][]ghcli.PRSummary{},
		createPRURL: "https://github.com/example/mini/pull/1",
	}
	key := setupPRCreateTest(t, gh)
	// Call with lowercase prefix; the wrapper should canonicalise.
	lower := strings.ToLower(key)
	if err := runPRCreate(inputs.PRCreateInput{IssueKey: lower, GHArgs: []string{"--title", "t", "--body", "b"}}); err != nil {
		t.Fatalf("runPRCreate: %v", err)
	}
	// Inspect listCalls: the fake stores by label, and the wrapper
	// should have queried "bacio:MINI-1", not "bacio:mini-1".
	if _, ok := gh.prsByLabel["bacio:"+key]; !ok {
		// The seeded map only had the canonical entry. Just sanity-check
		// the audit row carries the canonical label.
		s, err := store.Open(opts.dbPath)
		if err != nil {
			t.Fatalf("open: %v", err)
		}
		defer s.Close()
		rows, _ := s.ListHistory(store.HistoryFilter{Op: "pr.create"})
		if len(rows) != 1 {
			t.Fatalf("expected one pr.create row, got %d", len(rows))
		}
		want := fmt.Sprintf("label=bacio:%s", key)
		if !strings.Contains(rows[0].Details, want) {
			t.Fatalf("pr.create row should carry canonical label %q: %+v", want, rows[0])
		}
	}
}
