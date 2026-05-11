package sync

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestAppendRedirect_RoundTrip writes one redirect, then another,
// then re-reads the file and verifies both appear in chronological
// order (the AppendRedirect contract sorts on write).
func TestAppendRedirect_RoundTrip(t *testing.T) {
	dir := t.TempDir()
	r1 := Redirect{
		Kind:      "issue",
		Old:       "MINI-7",
		New:       "MINI-12",
		UUID:      "0190c2a3-7f3a-7b2c-8b21-bbbbbbbbbbbb",
		ChangedAt: time.Date(2026, 5, 9, 14, 30, 0, 0, time.UTC),
		Reason:    ReasonLabelCollision,
	}
	r2 := Redirect{
		Kind:      "feature",
		Old:       "auth-rewrite",
		New:       "auth-rewrite-2",
		UUID:      "0190c2a3-7f3a-7b2c-8b21-dddddddddddd",
		ChangedAt: time.Date(2026, 5, 10, 8, 15, 0, 0, time.UTC),
		Reason:    ReasonLabelCollision,
	}
	if err := AppendRedirect(dir, "MINI", r2); err != nil {
		t.Fatalf("append r2: %v", err)
	}
	if err := AppendRedirect(dir, "MINI", r1); err != nil {
		t.Fatalf("append r1: %v", err)
	}
	got, err := LoadRedirects(dir, "MINI")
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("len: got %d, want 2", len(got))
	}
	if got[0].Old != "MINI-7" {
		t.Errorf("first entry: got %q, want MINI-7 (chronological order)", got[0].Old)
	}
	if got[1].Old != "auth-rewrite" {
		t.Errorf("second entry: got %q, want auth-rewrite", got[1].Old)
	}
	// File body should have stable canonical YAML formatting.
	body, _ := os.ReadFile(filepath.Join(dir, "repos", "MINI", "redirects.yaml"))
	if !strings.Contains(string(body), `kind: "issue"`) {
		t.Errorf("yaml body missing expected snippet:\n%s", body)
	}
}

// TestLoadRedirects_MissingFile: calling Load against a target that
// has no redirects.yaml is fine — empty result, no error.
func TestLoadRedirects_MissingFile(t *testing.T) {
	dir := t.TempDir()
	got, err := LoadRedirects(dir, "MINI")
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("len: got %d, want 0", len(got))
	}
}

// TestResolveLabel_SingleHop: A → B yields ResolveLabel("A") == "B".
func TestResolveLabel_SingleHop(t *testing.T) {
	rs := []Redirect{
		{
			Kind: "issue", Old: "MINI-7", New: "MINI-12",
			UUID:      "u1",
			ChangedAt: time.Date(2026, 5, 9, 14, 30, 0, 0, time.UTC),
			Reason:    ReasonLabelCollision,
		},
	}
	got, ok := ResolveLabel(rs, "issue", "MINI-7")
	if !ok || got != "MINI-12" {
		t.Errorf("got (%q, %v), want (MINI-12, true)", got, ok)
	}
	// And: unrelated lookup misses.
	if _, ok := ResolveLabel(rs, "issue", "MINI-99"); ok {
		t.Errorf("unexpected hit on unmapped label")
	}
	// Wrong kind misses too.
	if _, ok := ResolveLabel(rs, "feature", "MINI-7"); ok {
		t.Errorf("unexpected cross-kind hit")
	}
}

// TestResolveLabel_MultiHop: A → B → C resolves to C.
func TestResolveLabel_MultiHop(t *testing.T) {
	rs := []Redirect{
		{
			Kind: "issue", Old: "MINI-7", New: "MINI-12",
			UUID: "u1", ChangedAt: time.Date(2026, 5, 9, 0, 0, 0, 0, time.UTC),
		},
		{
			Kind: "issue", Old: "MINI-12", New: "MINI-99",
			UUID: "u1", ChangedAt: time.Date(2026, 5, 10, 0, 0, 0, 0, time.UTC),
		},
	}
	got, ok := ResolveLabel(rs, "issue", "MINI-7")
	if !ok || got != "MINI-99" {
		t.Errorf("got (%q, %v), want (MINI-99, true)", got, ok)
	}
}

// TestResolveLabel_Cycle: a malformed manual edit produces A → B →
// A. Resolver should bail without spinning.
func TestResolveLabel_Cycle(t *testing.T) {
	rs := []Redirect{
		{
			Kind: "issue", Old: "MINI-7", New: "MINI-12",
			UUID: "u1", ChangedAt: time.Date(2026, 5, 9, 0, 0, 0, 0, time.UTC),
		},
		{
			Kind: "issue", Old: "MINI-12", New: "MINI-7",
			UUID: "u1", ChangedAt: time.Date(2026, 5, 10, 0, 0, 0, 0, time.UTC),
		},
	}
	got, ok := ResolveLabel(rs, "issue", "MINI-7")
	if !ok {
		t.Fatal("ok=false on cycle, want true with truncated chain")
	}
	if got != "MINI-12" && got != "MINI-7" {
		t.Errorf("unexpected resolution %q on cycle", got)
	}
}
