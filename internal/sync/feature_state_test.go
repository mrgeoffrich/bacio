// BACI-199 sync round-trip tests for the feature state + state_manual
// pair. Mirrors TestImport_RoundTripsArchivedAt's two-machine shape:
// mutate on A, export+import into B, verify each field landed; flip
// back and re-import to confirm the no-op-on-default path is also
// hash-stable.
package sync

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mrgeoffrich/bacio/internal/store"
)

// TestImport_RoundTripsFeatureState exercises both the insert path
// (B has no prior row) and the update path (B already has the row
// at active/0 from a prior import) for the BACI-199 columns.
func TestImport_RoundTripsFeatureState(t *testing.T) {
	a, uuids := seedExportFixture(t)

	// Flip the seed feature to `done` / state_manual=1 on machine A.
	// Bump updated_at so the LWW gate inside applyFeatures lets the
	// change through on import.
	if _, err := a.DB.Exec(
		`UPDATE features SET state = 'done', state_manual = 1, updated_at = '2026-05-15 09:00:00' WHERE uuid = ?`,
		uuids["feat"]); err != nil {
		t.Fatalf("flip feat state on A: %v", err)
	}

	dirA := t.TempDir()
	if _, err := (&Engine{Store: a}).Export(context.Background(), dirA); err != nil {
		t.Fatalf("export A: %v", err)
	}

	// Confirm the emitter wrote both keys to the YAML — the
	// "byte-stable hash" guarantee in the doc comment depends on this
	// pair appearing exactly when non-default.
	featPath := filepath.Join(dirA, "repos", "MINI", "features", "auth-rewrite", "feature.yaml")
	raw, err := os.ReadFile(featPath)
	if err != nil {
		t.Fatalf("read feature.yaml: %v", err)
	}
	if !strings.Contains(string(raw), `state: "done"`) {
		t.Fatalf("feature.yaml missing 'state: \"done\"'; body:\n%s", raw)
	}
	if !strings.Contains(string(raw), "state_manual: true") {
		t.Fatalf("feature.yaml missing 'state_manual: true'; body:\n%s", raw)
	}

	// Fresh B picks the values up on insert.
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
	if string(featB.State) != "done" {
		t.Fatalf("feature state did not round-trip; got %q, want done", featB.State)
	}
	if !featB.StateManual {
		t.Fatalf("feature state_manual did not round-trip; got false, want true")
	}

	// Flip back to active + clear sticky bit on A, re-export, re-import
	// — the update branch of applyFeatures must also follow.
	if _, err := a.DB.Exec(
		`UPDATE features SET state = 'active', state_manual = 0, updated_at = '2026-05-20 09:00:00' WHERE uuid = ?`,
		uuids["feat"]); err != nil {
		t.Fatalf("reset feat state on A: %v", err)
	}
	dirA2 := t.TempDir()
	if _, err := (&Engine{Store: a}).Export(context.Background(), dirA2); err != nil {
		t.Fatalf("re-export A: %v", err)
	}
	if _, err := (&Engine{Store: b}).Import(context.Background(), dirA2); err != nil {
		t.Fatalf("re-import into B: %v", err)
	}
	featB2, _ := b.GetFeatureByUUID(uuids["feat"])
	if string(featB2.State) != "active" {
		t.Fatalf("feature state reset did not round-trip; got %q, want active", featB2.State)
	}
	if featB2.StateManual {
		t.Fatalf("feature state_manual reset did not round-trip; got true, want false")
	}
}

// TestExport_FeatureStateOmitsDefaults locks in the no-churn-on-legacy
// guarantee: a feature with state='active' / state_manual=false (the
// column defaults seeded by the schema CREATE TABLE) emits a
// feature.yaml that contains NEITHER `state:` NOR `state_manual:`. A
// future emitter change that breaks the omitempty guards on either
// field would fail this test loudly.
func TestExport_FeatureStateOmitsDefaults(t *testing.T) {
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
	if strings.Contains(string(raw), "state:") {
		t.Fatalf("feature.yaml carries 'state:' for a default-state feature; body:\n%s", raw)
	}
	if strings.Contains(string(raw), "state_manual") {
		t.Fatalf("feature.yaml carries 'state_manual' for a default-state feature; body:\n%s", raw)
	}
}
