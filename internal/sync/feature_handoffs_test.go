// BACI-333 sync round-trip tests for the per-feature collect_handoffs
// opt-out and the feature-comment kind discriminator. Mirrors
// feature_state_test.go's two-machine shape: mutate on A, export+import
// into B, verify each field landed. The polarity is inverted from every
// other synced feature flag (default is ON/1), so the churn-guard test
// asserts a default feature emits NEITHER key.
package sync

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mrgeoffrich/bacio/internal/store"
)

// TestImport_RoundTripsCollectHandoffs flips collect_handoffs OFF on A,
// confirms the emitter wrote the key, and that B picks up the OFF value
// on import (insert path).
func TestImport_RoundTripsCollectHandoffs(t *testing.T) {
	a, uuids := seedExportFixture(t)

	if _, err := a.DB.Exec(
		`UPDATE features SET collect_handoffs = 0, updated_at = '2026-05-15 09:00:00' WHERE uuid = ?`,
		uuids["feat"]); err != nil {
		t.Fatalf("flip collect_handoffs on A: %v", err)
	}

	dirA := t.TempDir()
	if _, err := (&Engine{Store: a}).Export(context.Background(), dirA); err != nil {
		t.Fatalf("export A: %v", err)
	}

	featPath := filepath.Join(dirA, "repos", "MINI", "features", "auth-rewrite", "feature.yaml")
	raw, err := os.ReadFile(featPath)
	if err != nil {
		t.Fatalf("read feature.yaml: %v", err)
	}
	// The key is emitted only when OFF, and only as `false`.
	if !strings.Contains(string(raw), "collect_handoffs: false") {
		t.Fatalf("feature.yaml missing 'collect_handoffs: false'; body:\n%s", raw)
	}

	b, err := store.Open(":memory:")
	if err != nil {
		t.Fatalf("open b: %v", err)
	}
	t.Cleanup(func() { b.Close() })
	if _, err := (&Engine{Store: b}).Import(context.Background(), dirA); err != nil {
		t.Fatalf("import into B: %v", err)
	}
	featB, err := b.GetFeatureByUUID(uuids["feat"])
	if err != nil {
		t.Fatalf("get feat B: %v", err)
	}
	if featB.CollectHandoffs {
		t.Fatalf("collect_handoffs did not round-trip; got true, want false")
	}

	// Flip back ON on A, re-export, re-import — the update branch must
	// follow back to the default.
	if _, err := a.DB.Exec(
		`UPDATE features SET collect_handoffs = 1, updated_at = '2026-05-20 09:00:00' WHERE uuid = ?`,
		uuids["feat"]); err != nil {
		t.Fatalf("reset collect_handoffs on A: %v", err)
	}
	dirA2 := t.TempDir()
	if _, err := (&Engine{Store: a}).Export(context.Background(), dirA2); err != nil {
		t.Fatalf("re-export A: %v", err)
	}
	if _, err := (&Engine{Store: b}).Import(context.Background(), dirA2); err != nil {
		t.Fatalf("re-import into B: %v", err)
	}
	featB2, _ := b.GetFeatureByUUID(uuids["feat"])
	if !featB2.CollectHandoffs {
		t.Fatalf("collect_handoffs reset did not round-trip; got false, want true")
	}
}

// TestExport_CollectHandoffsOmitsDefault is the inverted-polarity
// churn-guard: a feature with collect_handoffs=1 (the schema default)
// emits a feature.yaml that does NOT carry the key, so the majority of
// features stay byte-identical to the pre-BACI-333 shape and the next
// sync doesn't churn every feature.yaml.
func TestExport_CollectHandoffsOmitsDefault(t *testing.T) {
	a, _ := seedExportFixture(t)
	dirA := t.TempDir()
	if _, err := (&Engine{Store: a}).Export(context.Background(), dirA); err != nil {
		t.Fatalf("export A: %v", err)
	}
	featPath := filepath.Join(dirA, "repos", "MINI", "features", "auth-rewrite", "feature.yaml")
	raw, err := os.ReadFile(featPath)
	if err != nil {
		t.Fatalf("read feature.yaml: %v", err)
	}
	if strings.Contains(string(raw), "collect_handoffs") {
		t.Fatalf("feature.yaml carries 'collect_handoffs' for a default feature; body:\n%s", raw)
	}
}

// TestRoundTripsFeatureCommentKind exercises the kind discriminator: a
// handoff comment exported from A is re-imported into B with kind
// preserved, while a default 'note' comment's YAML omits the key.
func TestRoundTripsFeatureCommentKind(t *testing.T) {
	a, uuids := seedExportFixture(t)
	featA, err := a.GetFeatureByUUID(uuids["feat"])
	if err != nil {
		t.Fatalf("get feat A: %v", err)
	}
	// A handoff comment (kind discriminator set) and a plain note.
	if _, err := a.CreateFeatureComment(featA.ID, "agent-bob", "handoff body", store.FeatureCommentKindHandoff); err != nil {
		t.Fatalf("create handoff: %v", err)
	}
	if _, err := a.CreateFeatureComment(featA.ID, "alice", "note body", store.FeatureCommentKindNote); err != nil {
		t.Fatalf("create note: %v", err)
	}

	dirA := t.TempDir()
	if _, err := (&Engine{Store: a}).Export(context.Background(), dirA); err != nil {
		t.Fatalf("export A: %v", err)
	}

	// Across all comment YAML files, exactly one carries `kind: "handoff"`
	// and none carries `kind: "note"` (the note default is omitted).
	commentsDir := filepath.Join(dirA, "repos", "MINI", "features", "auth-rewrite", "comments")
	entries, err := os.ReadDir(commentsDir)
	if err != nil {
		t.Fatalf("read comments dir: %v", err)
	}
	handoffKeys, noteKeys := 0, 0
	for _, e := range entries {
		if !strings.HasSuffix(e.Name(), ".yaml") {
			continue
		}
		raw, err := os.ReadFile(filepath.Join(commentsDir, e.Name()))
		if err != nil {
			t.Fatalf("read %s: %v", e.Name(), err)
		}
		if strings.Contains(string(raw), `kind: "handoff"`) {
			handoffKeys++
		}
		if strings.Contains(string(raw), `kind: "note"`) {
			noteKeys++
		}
	}
	if handoffKeys != 1 {
		t.Fatalf("expected exactly one handoff kind key, got %d", handoffKeys)
	}
	if noteKeys != 0 {
		t.Fatalf("expected note kind to be omitted, got %d keys", noteKeys)
	}

	// Round-trip into B; the handoff comment's kind survives.
	b, err := store.Open(":memory:")
	if err != nil {
		t.Fatalf("open b: %v", err)
	}
	t.Cleanup(func() { b.Close() })
	if _, err := (&Engine{Store: b}).Import(context.Background(), dirA); err != nil {
		t.Fatalf("import into B: %v", err)
	}
	featB, err := b.GetFeatureByUUID(uuids["feat"])
	if err != nil {
		t.Fatalf("get feat B: %v", err)
	}
	cs, err := b.ListFeatureComments(featB.ID)
	if err != nil {
		t.Fatalf("list comments B: %v", err)
	}
	var sawHandoff, sawNote bool
	for _, c := range cs {
		switch c.Kind {
		case store.FeatureCommentKindHandoff:
			sawHandoff = true
		case store.FeatureCommentKindNote:
			sawNote = true
		default:
			t.Fatalf("unexpected kind %q", c.Kind)
		}
	}
	if !sawHandoff || !sawNote {
		t.Fatalf("kinds did not round-trip: handoff=%v note=%v (comments: %+v)", sawHandoff, sawNote, cs)
	}
}
