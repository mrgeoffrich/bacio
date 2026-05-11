package sync

import (
	"reflect"
	"testing"
	"time"
)

func TestIssueFolder(t *testing.T) {
	got := IssueFolder("MINI", 7)
	want := "repos/MINI/issues/MINI-7"
	if got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}

func TestIssueFolder_LowercasePrefixIsUppercased(t *testing.T) {
	// resolveRepo + AllocatePrefix already canonicalise to uppercase, but
	// we belt-and-brace here so a stray lowercase caller doesn't end up
	// with two different folders for the same repo.
	got := IssueFolder("mini", 3)
	want := "repos/MINI/issues/MINI-3"
	if got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}

func TestFeatureFolder(t *testing.T) {
	got := FeatureFolder("MINI", "auth-rewrite")
	want := "repos/MINI/features/auth-rewrite"
	if got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}

func TestDocumentFolder_FilenameUsedAsIs(t *testing.T) {
	got := DocumentFolder("MINI", "auth-overview.md")
	want := "repos/MINI/docs/auth-overview.md"
	if got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}

func TestDocumentFolder_NFCNormalisation(t *testing.T) {
	// "Café" expressed as e + combining acute (NFD) must become the
	// precomposed é (NFC) on disk.
	const nfd = "Café.md"
	const nfc = "Café.md"
	got := DocumentFolder("MINI", nfd)
	want := "repos/MINI/docs/" + nfc
	if got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}

func TestCommentFile(t *testing.T) {
	at := time.Date(2026, 5, 9, 14, 22, 0, 0, time.UTC)
	uuid := "0190c2a3-7f3a-7b2c-8b21-ffffffffffff"
	yamlPath, mdPath := CommentFile("repos/MINI/issues/MINI-7", at, uuid)
	wantYAML := "repos/MINI/issues/MINI-7/comments/2026-05-09T14-22-00.000Z--" + uuid + ".yaml"
	wantMD := "repos/MINI/issues/MINI-7/comments/2026-05-09T14-22-00.000Z--" + uuid + ".md"
	if yamlPath != wantYAML {
		t.Errorf("yaml: got %q want %q", yamlPath, wantYAML)
	}
	if mdPath != wantMD {
		t.Errorf("md: got %q want %q", mdPath, wantMD)
	}
}

func TestCommentFile_NoColonsInTimestamp(t *testing.T) {
	// Colons in timestamps would break NTFS / case-insensitive FSes;
	// the helper substitutes dashes. Make sure no colons leak through.
	at := time.Date(2026, 5, 9, 14, 22, 33, int(45*time.Millisecond), time.UTC)
	yamlPath, _ := CommentFile("x", at, "u")
	for _, r := range yamlPath {
		if r == ':' {
			t.Fatalf("colon in path: %q", yamlPath)
		}
	}
}

func TestCommentFile_TimestampNormalisedToUTC(t *testing.T) {
	loc, _ := time.LoadLocation("Australia/Sydney")
	at := time.Date(2026, 5, 9, 14, 22, 0, 0, loc) // 04:22 UTC
	yamlPath, _ := CommentFile("x", at, "u")
	want := "x/comments/2026-05-09T04-22-00.000Z--u.yaml"
	if yamlPath != want {
		t.Fatalf("got %q want %q", yamlPath, want)
	}
}

func TestDetectCaseInsensitiveCollisions(t *testing.T) {
	in := []string{
		"auth-overview.md",
		"AUTH-OVERVIEW.md",
		"design-notes.md",
		"Design-Notes.md",
		"unique.md",
	}
	got := DetectCaseInsensitiveCollisions(in)
	if len(got) != 2 {
		t.Fatalf("expected 2 collision groups, got %d: %#v", len(got), got)
	}
	want1 := []string{"auth-overview.md", "AUTH-OVERVIEW.md"}
	want2 := []string{"design-notes.md", "Design-Notes.md"}
	if !reflect.DeepEqual(got["auth-overview.md"], want1) {
		t.Errorf("group 1: got %v want %v", got["auth-overview.md"], want1)
	}
	if !reflect.DeepEqual(got["design-notes.md"], want2) {
		t.Errorf("group 2: got %v want %v", got["design-notes.md"], want2)
	}
}

func TestDetectCaseInsensitiveCollisions_NFC(t *testing.T) {
	// "Café.md" in NFC vs NFD case-folds to the same key.
	in := []string{"café.md", "café.md"} // NFC vs NFD via combining acute
	got := DetectCaseInsensitiveCollisions(in)
	if len(got) != 1 {
		t.Fatalf("expected 1 collision group, got %d: %#v", len(got), got)
	}
}

func TestRepoYAMLFile(t *testing.T) {
	got := RepoYAMLFile("MINI")
	want := "repos/MINI/repo.yaml"
	if got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}

func TestIssueYAMLAndDescription(t *testing.T) {
	folder := IssueFolder("MINI", 7)
	if got, want := IssueYAMLFile(folder), "repos/MINI/issues/MINI-7/issue.yaml"; got != want {
		t.Errorf("yaml: got %q want %q", got, want)
	}
	if got, want := IssueDescriptionFile(folder), "repos/MINI/issues/MINI-7/description.md"; got != want {
		t.Errorf("md: got %q want %q", got, want)
	}
}
