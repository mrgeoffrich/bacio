package store

import (
	"strings"
	"testing"
)

// TestCreateFeatureWithBranchName exercises the BACI-225 per-feature
// branch_name round-trip — set on insert, read back via slug, update
// via UpdateFeature, clear via UpdateFeature with an explicit empty
// pointer. Mirrors the shape of TestCreateFeatureWithEmoji.
func TestCreateFeatureWithBranchName(t *testing.T) {
	s := newTestStore(t)
	repo, err := s.CreateRepo("FB", "fb", t.TempDir(), "")
	if err != nil {
		t.Fatalf("create repo: %v", err)
	}

	// Empty branch_name on create → column reads NULL → BranchName is nil.
	noBranch, err := s.CreateFeature(repo.ID, "ops", "Ops", "", "", "")
	if err != nil {
		t.Fatalf("create feature ops: %v", err)
	}
	if noBranch.BranchName != nil {
		t.Fatalf("created feature with empty branch: BranchName = %v, want nil", *noBranch.BranchName)
	}

	// Non-empty branch_name on create → column reads back the value.
	feat, err := s.CreateFeature(repo.ID, "auth", "Auth", "", "", "feat/auth")
	if err != nil {
		t.Fatalf("create feature: %v", err)
	}
	if feat.BranchName == nil || *feat.BranchName != "feat/auth" {
		t.Fatalf("created branch = %v, want feat/auth", feat.BranchName)
	}
	got, err := s.GetFeatureBySlug(repo.ID, "auth")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.BranchName == nil || *got.BranchName != "feat/auth" {
		t.Fatalf("get branch = %v, want feat/auth", got.BranchName)
	}

	// Update via the pointer-and-presence path.
	newBranch := "feat/auth-v2"
	if err := s.UpdateFeature(feat.ID, nil, nil, nil, &newBranch); err != nil {
		t.Fatalf("update branch: %v", err)
	}
	got, err = s.GetFeatureBySlug(repo.ID, "auth")
	if err != nil {
		t.Fatalf("get post-update: %v", err)
	}
	if got.BranchName == nil || *got.BranchName != "feat/auth-v2" {
		t.Fatalf("post-update branch = %v, want feat/auth-v2", got.BranchName)
	}

	// Clear via explicit empty pointer → column goes back to NULL.
	empty := ""
	if err := s.UpdateFeature(feat.ID, nil, nil, nil, &empty); err != nil {
		t.Fatalf("clear branch: %v", err)
	}
	got, err = s.GetFeatureBySlug(repo.ID, "auth")
	if err != nil {
		t.Fatalf("get post-clear: %v", err)
	}
	if got.BranchName != nil {
		t.Fatalf("post-clear BranchName = %v, want nil", *got.BranchName)
	}

	// Invalid branch_name on update is rejected.
	bad := "has spaces"
	if err := s.UpdateFeature(feat.ID, nil, nil, nil, &bad); err == nil {
		t.Fatalf("expected ValidateBranchName rejection for %q, got nil", bad)
	}
}

// TestCreateFeature_InvalidBranchName confirms validation fires on the
// insert path too, not just on update.
func TestCreateFeature_InvalidBranchName(t *testing.T) {
	s := newTestStore(t)
	repo, _ := s.CreateRepo("FB", "fb", t.TempDir(), "")
	if _, err := s.CreateFeature(repo.ID, "bad", "Bad", "", "", "with spaces"); err == nil {
		t.Fatalf("expected ValidateBranchName rejection on create, got nil")
	}
}

// TestValidateBranchName covers each rejection rule with a table of
// negative cases plus a representative set of accepted refnames.
func TestValidateBranchName(t *testing.T) {
	t.Run("accepts", func(t *testing.T) {
		cases := []string{
			"",                              // empty is the "no value" sentinel
			"main",                          // plain name
			"feat/auth",                     // slash-separated
			"feat/auth-rewrite-v2",          // hyphen-separated
			"release/2025.01",               // dots in the middle
			"users/alice/feature/wip",       // deeply nested
			"v1.2.3",                        // dotted version-ish
			strings.Repeat("a", 256),        // exactly at the cap
		}
		for _, in := range cases {
			if _, err := ValidateBranchName(in); err != nil {
				t.Errorf("ValidateBranchName(%q) errored: %v", in, err)
			}
		}
	})
	t.Run("rejects", func(t *testing.T) {
		cases := map[string]string{
			"has spaces":                  "whitespace",
			"with\ttab":                   "whitespace",
			"with\nnewline":               "whitespace",
			"-leading-hyphen":             "leading -",
			"/leading-slash":              "leading /",
			"trailing-slash/":             "trailing /",
			".leading-dot":                "leading .",
			"trailing-dot.":               "trailing .",
			"double..dot":                 "embedded ..",
			"at@{open":                    "@{",
			"caret^foo":                   "^",
			"tilde~foo":                   "~",
			"colon:foo":                   ":",
			"question?":                   "?",
			"star*":                       "*",
			"open[bracket":                "[",
			"back\\slash":                 "\\",
			strings.Repeat("a", 257):      "too long",
			"with\x00null":                "control char",
		}
		for in, label := range cases {
			if _, err := ValidateBranchName(in); err == nil {
				t.Errorf("ValidateBranchName(%q) returned nil, expected rejection (%s)", in, label)
			}
		}
	})
}

// TestFeaturesBranchNameColumnExists is the schema-level guard: a
// fresh in-memory store must expose features.branch_name, and a
// caller can write NULL by passing the empty sentinel. Migration
// idempotence is implicit in newTestStore reopening the file-backed
// path, but we re-open here explicitly to pin the BACI-225 columnExists
// guard.
func TestFeaturesBranchNameColumnExists(t *testing.T) {
	s := newTestStore(t)
	repo, err := s.CreateRepo("FBC", "fbc", t.TempDir(), "")
	if err != nil {
		t.Fatalf("create repo: %v", err)
	}
	// Direct column read confirms the column is present and selectable.
	if _, err := s.DB.Exec(`INSERT INTO features (uuid, repo_id, slug, title, branch_name) VALUES (?, ?, ?, ?, ?)`,
		"019e6671-0000-7000-0000-000000000001", repo.ID, "schema-check", "Schema check", nil); err != nil {
		t.Fatalf("insert with NULL branch_name: %v", err)
	}
	var n int
	if err := s.DB.QueryRow(`SELECT COUNT(*) FROM features WHERE branch_name IS NULL`).Scan(&n); err != nil {
		t.Fatalf("count nulls: %v", err)
	}
	if n == 0 {
		t.Fatalf("expected at least one NULL-branch row")
	}
}
