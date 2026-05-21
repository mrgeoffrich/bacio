package schema

import "testing"

// TestRegistryHasSyncBackground locks in that the BACI-89
// `settings.sync-background` toggle is published in the schema
// registry — `bacio schema show settings.sync-background` and
// `GET /schema/settings.sync-background` both depend on this row, per
// the agent-CLI principle that every mutating verb is schema-reachable.
func TestRegistryHasSyncBackground(t *testing.T) {
	all := All()
	if _, ok := all["settings.sync-background"]; !ok {
		t.Fatal("schema registry is missing settings.sync-background")
	}
	// Every entry must carry a non-empty short description and a
	// worked example.
	for _, e := range Registry {
		if e.Name != "settings.sync-background" {
			continue
		}
		if e.Short == "" {
			t.Error("settings.sync-background has an empty Short")
		}
		if e.Example == nil {
			t.Error("settings.sync-background has no Example")
		}
		return
	}
	t.Fatal("settings.sync-background not found in Registry slice")
}
