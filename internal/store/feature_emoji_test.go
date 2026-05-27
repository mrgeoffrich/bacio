package store

import (
	"testing"

	"github.com/mrgeoffrich/bacio/internal/model"
)

// TestCreateFeatureWithEmoji exercises the BACI-172 per-feature emoji
// round-trip — set on insert, read back via slug, update via
// UpdateFeature, clear via UpdateFeature with an explicit empty
// pointer.
func TestCreateFeatureWithEmoji(t *testing.T) {
	s := newTestStore(t)
	repo, err := s.CreateRepo("FE", "fe", t.TempDir(), "")
	if err != nil {
		t.Fatalf("create repo: %v", err)
	}
	feat, err := s.CreateFeature(repo.ID, "auth", "Auth", "", "🔐", "")
	if err != nil {
		t.Fatalf("create feature: %v", err)
	}
	if feat.Emoji != "🔐" {
		t.Fatalf("created emoji = %q, want %q", feat.Emoji, "🔐")
	}
	got, err := s.GetFeatureBySlug(repo.ID, "auth")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Emoji != "🔐" {
		t.Fatalf("get emoji = %q, want %q", got.Emoji, "🔐")
	}

	// Update via the pointer-and-presence path.
	newEmoji := "🪲"
	if err := s.UpdateFeature(feat.ID, nil, nil, &newEmoji, nil); err != nil {
		t.Fatalf("update emoji: %v", err)
	}
	got, err = s.GetFeatureBySlug(repo.ID, "auth")
	if err != nil {
		t.Fatalf("get post-update: %v", err)
	}
	if got.Emoji != "🪲" {
		t.Fatalf("post-update emoji = %q, want %q", got.Emoji, "🪲")
	}

	// Clear via explicit empty pointer.
	empty := ""
	if err := s.UpdateFeature(feat.ID, nil, nil, &empty, nil); err != nil {
		t.Fatalf("clear emoji: %v", err)
	}
	got, err = s.GetFeatureBySlug(repo.ID, "auth")
	if err != nil {
		t.Fatalf("get post-clear: %v", err)
	}
	if got.Emoji != "" {
		t.Fatalf("post-clear emoji = %q, want empty", got.Emoji)
	}

	// Multi-cluster rejection at the store boundary.
	bad := "FEATURE"
	if err := s.UpdateFeature(feat.ID, nil, nil, &bad, nil); err == nil {
		t.Fatalf("expected ValidateEmoji rejection for %q, got nil", bad)
	}
}

// TestIssueFeatureEmojiJoin exercises the BACI-172 issue→feature
// denormalisation: the issue scanner reads f.emoji via the
// issueSelect join so cards can render the glyph without a second
// lookup. Issues with no feature, or features with no emoji, see an
// empty FeatureEmoji field — no NULL leakage.
func TestIssueFeatureEmojiJoin(t *testing.T) {
	s := newTestStore(t)
	repo, err := s.CreateRepo("FJ", "fj", t.TempDir(), "")
	if err != nil {
		t.Fatalf("create repo: %v", err)
	}
	feat, err := s.CreateFeature(repo.ID, "auth", "Auth", "", "🔐", "")
	if err != nil {
		t.Fatalf("create feature: %v", err)
	}
	featNoGlyph, err := s.CreateFeature(repo.ID, "ops", "Ops", "", "", "")
	if err != nil {
		t.Fatalf("create feature ops: %v", err)
	}
	issWithEmoji, err := s.CreateIssue(repo.ID, &feat.ID, "Has emoji", "", model.StateTodo, nil, "")
	if err != nil {
		t.Fatalf("create issue with emoji feature: %v", err)
	}
	issEmptyEmoji, err := s.CreateIssue(repo.ID, &featNoGlyph.ID, "Feature but no emoji", "", model.StateTodo, nil, "")
	if err != nil {
		t.Fatalf("create issue with empty emoji feature: %v", err)
	}
	issNoFeature, err := s.CreateIssue(repo.ID, nil, "No feature", "", model.StateTodo, nil, "")
	if err != nil {
		t.Fatalf("create issue without feature: %v", err)
	}

	got1, err := s.GetIssueByID(issWithEmoji.ID)
	if err != nil {
		t.Fatalf("get1: %v", err)
	}
	if got1.FeatureEmoji != "🔐" {
		t.Fatalf("with-emoji issue: FeatureEmoji = %q, want %q", got1.FeatureEmoji, "🔐")
	}
	got2, err := s.GetIssueByID(issEmptyEmoji.ID)
	if err != nil {
		t.Fatalf("get2: %v", err)
	}
	if got2.FeatureEmoji != "" {
		t.Fatalf("empty-emoji feature issue: FeatureEmoji = %q, want empty", got2.FeatureEmoji)
	}
	got3, err := s.GetIssueByID(issNoFeature.ID)
	if err != nil {
		t.Fatalf("get3: %v", err)
	}
	if got3.FeatureEmoji != "" {
		t.Fatalf("no-feature issue: FeatureEmoji = %q, want empty", got3.FeatureEmoji)
	}
}
