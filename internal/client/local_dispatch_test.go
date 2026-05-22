package client

import (
	"strings"
	"testing"
)

// TestBuildSetupDispatchPayload locks in BACI-98: the channel-emitted
// setup-dispatch payload pre-fills the real session_id and no longer
// carries a "branch" placeholder — the SessionStart hook resolves the
// branch, so the agent never supplies it via the register tool.
func TestBuildSetupDispatchPayload(t *testing.T) {
	payload := buildSetupDispatchPayload("sess-xyz")
	if !strings.Contains(payload, `"session_id": "sess-xyz"`) {
		t.Fatalf("payload missing the substituted session_id: %q", payload)
	}
	if strings.Contains(payload, "branch") {
		t.Fatalf("payload should no longer mention branch: %q", payload)
	}
}
