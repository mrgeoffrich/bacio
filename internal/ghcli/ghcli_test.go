package ghcli

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
)

// stubRunner is a runner that returns canned stdout/stderr/err for
// each call. Implemented as a closure-friendly slice so each test
// can hand it the exact sequence of responses it expects.
type stubRunner struct {
	calls    [][]string
	respond  func(args []string) (string, string, error)
}

func (s *stubRunner) run(_ context.Context, args ...string) (string, string, error) {
	s.calls = append(s.calls, append([]string(nil), args...))
	return s.respond(args)
}

func newStub(respond func(args []string) (string, string, error)) (*Exec, *stubRunner) {
	stub := &stubRunner{respond: respond}
	return newExecWithRunner(stub.run), stub
}

func TestListPRsByLabel_ParsesJSON(t *testing.T) {
	canned := `[
  {"number": 191, "state": "MERGED", "url": "https://github.com/example/bacio/pull/191"},
  {"number": 192, "state": "OPEN",   "url": "https://github.com/example/bacio/pull/192"}
]`
	e, stub := newStub(func(args []string) (string, string, error) {
		return canned, "", nil
	})
	got, err := e.ListPRsByLabel(context.Background(), "bacio:BACI-111")
	if err != nil {
		t.Fatalf("ListPRsByLabel: %v", err)
	}
	want := []PRSummary{
		{Number: 191, State: "MERGED", URL: "https://github.com/example/bacio/pull/191"},
		{Number: 192, State: "OPEN", URL: "https://github.com/example/bacio/pull/192"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("rows mismatch:\n got=%+v\nwant=%+v", got, want)
	}
	// Argv sanity-check: the label and json fields are wired through.
	if len(stub.calls) != 1 {
		t.Fatalf("expected 1 call, got %d", len(stub.calls))
	}
	argv := stub.calls[0]
	if !contains(argv, "--label") || !contains(argv, "bacio:BACI-111") {
		t.Fatalf("argv missing --label bacio:BACI-111: %v", argv)
	}
	if !contains(argv, "number,state,url") {
		t.Fatalf("argv missing JSON projection: %v", argv)
	}
}

func TestListPRsByLabel_EmptyOutput(t *testing.T) {
	e, _ := newStub(func(args []string) (string, string, error) {
		return "", "", nil
	})
	got, err := e.ListPRsByLabel(context.Background(), "bacio:BACI-9")
	if err != nil {
		t.Fatalf("ListPRsByLabel: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("expected empty, got %+v", got)
	}
}

func TestListPRsByLabel_PropagatesRunError(t *testing.T) {
	want := errors.New("boom")
	e, _ := newStub(func(args []string) (string, string, error) {
		return "", "stderr", want
	})
	_, err := e.ListPRsByLabel(context.Background(), "bacio:BACI-9")
	if !errors.Is(err, want) {
		t.Fatalf("expected wrapped %v, got %v", want, err)
	}
}

func TestListPRsByLabel_RejectsEmptyLabel(t *testing.T) {
	e, stub := newStub(func(args []string) (string, string, error) {
		return "", "", nil
	})
	_, err := e.ListPRsByLabel(context.Background(), "  ")
	if err == nil {
		t.Fatal("expected error on empty label")
	}
	if len(stub.calls) != 0 {
		t.Fatalf("runner should not be called on empty label, got %v", stub.calls)
	}
}

func TestCreateLabelIdempotent_PassesForce(t *testing.T) {
	e, stub := newStub(func(args []string) (string, string, error) {
		return "", "", nil
	})
	if err := e.CreateLabelIdempotent(context.Background(), "bacio:BACI-1", "BFD4F2"); err != nil {
		t.Fatalf("CreateLabelIdempotent: %v", err)
	}
	if len(stub.calls) != 1 {
		t.Fatalf("expected 1 call, got %d", len(stub.calls))
	}
	argv := stub.calls[0]
	if !contains(argv, "--force") {
		t.Fatalf("argv missing --force: %v", argv)
	}
	if !contains(argv, "BFD4F2") {
		t.Fatalf("argv missing colour: %v", argv)
	}
	if !contains(argv, "bacio:BACI-1") {
		t.Fatalf("argv missing label name: %v", argv)
	}
}

func TestCreatePR_ExtractsURL(t *testing.T) {
	out := `Creating draft pull request for feat/foo into main in example/bacio
remote:
remote: Create a pull request for 'feat/foo' on GitHub by visiting:
https://github.com/example/bacio/pull/200
`
	e, stub := newStub(func(args []string) (string, string, error) {
		return out, "", nil
	})
	url, err := e.CreatePR(context.Background(), "bacio:BACI-1", []string{"--title", "x", "--body", "y"})
	if err != nil {
		t.Fatalf("CreatePR: %v", err)
	}
	if url != "https://github.com/example/bacio/pull/200" {
		t.Fatalf("URL mismatch: %q", url)
	}
	// --label is injected ahead of the passthrough args.
	argv := stub.calls[0]
	labelIdx := indexOf(argv, "--label")
	if labelIdx == -1 || argv[labelIdx+1] != "bacio:BACI-1" {
		t.Fatalf("argv missing --label bacio:BACI-1: %v", argv)
	}
	titleIdx := indexOf(argv, "--title")
	if titleIdx == -1 {
		t.Fatalf("argv missing passthrough --title: %v", argv)
	}
}

func TestCreatePR_NoURLInOutput(t *testing.T) {
	e, _ := newStub(func(args []string) (string, string, error) {
		return "some text without a URL\n", "", nil
	})
	_, err := e.CreatePR(context.Background(), "bacio:BACI-1", nil)
	if err == nil {
		t.Fatal("expected error on missing URL")
	}
	if !strings.Contains(err.Error(), "could not find PR URL") {
		t.Fatalf("unexpected error text: %v", err)
	}
}

func TestIsUnauthenticated(t *testing.T) {
	cases := []struct {
		in   string
		want bool
	}{
		{"You are not logged into any GitHub hosts. Run gh auth login.", true},
		{"authentication required", true},
		{"random failure", false},
		{"", false},
	}
	for _, c := range cases {
		got := isUnauthenticated(c.in)
		if got != c.want {
			t.Errorf("isUnauthenticated(%q) = %v, want %v", c.in, got, c.want)
		}
	}
}

// contains is a tiny test helper — we don't import slices/Contains
// because this module still targets the project's minimum Go version.
func contains(haystack []string, needle string) bool {
	for _, h := range haystack {
		if h == needle {
			return true
		}
	}
	return false
}

func indexOf(haystack []string, needle string) int {
	for i, h := range haystack {
		if h == needle {
			return i
		}
	}
	return -1
}
