package cli

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"strings"
	"testing"
)

// TestChannelSourceErrlogRoutesThroughSlog pins the BACI-120 wiring:
// channelSource.errlog must route its error events through the
// configured slog.Logger (so they land in the BACI-73 per-process log
// file alongside the rest of the channel's diagnostics), and must be
// a no-op when the logger is nil (the test-construction path that
// builds a channelSource directly without going through RunE).
func TestChannelSourceErrlogRoutesThroughSlog(t *testing.T) {
	t.Run("nil logger is a no-op", func(t *testing.T) {
		src := &channelSource{} // logger left nil
		// Must not panic.
		src.errlog("drain session", "session_id", "test-sess", "err", "boom")
	})

	t.Run("emits a structured error event", func(t *testing.T) {
		var buf bytes.Buffer
		// JSON handler so attribute assertions are stable across runs;
		// matches the shape buildChannelLogger wires for the live binary.
		logger := slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelInfo})).
			With("component", "channel", "channel_pid", 99999)
		src := &channelSource{logger: logger}

		src.errlog("drain session", "session_id", "abc-123", "err", "io: read on closed file")

		out := buf.String()
		if !strings.Contains(out, "\n") {
			t.Fatalf("expected at least one log line, got: %q", out)
		}
		// JSON handler emits one event per line; parse the first.
		line := strings.SplitN(strings.TrimSpace(out), "\n", 2)[0]
		var ev map[string]any
		if err := json.Unmarshal([]byte(line), &ev); err != nil {
			t.Fatalf("event was not JSON: %v\n%s", err, line)
		}
		if got := ev["level"]; got != "ERROR" {
			t.Fatalf("level: got %v, want ERROR", got)
		}
		if got := ev["msg"]; got != "drain session" {
			t.Fatalf("msg: got %v, want %q", got, "drain session")
		}
		if got := ev["session_id"]; got != "abc-123" {
			t.Fatalf("session_id attr: got %v, want abc-123", got)
		}
		if got := ev["err"]; got != "io: read on closed file" {
			t.Fatalf("err attr: got %v, want %q", got, "io: read on closed file")
		}
		// Inherited component / channel_pid attrs are what makes the
		// log file useful to operators — pin them so a refactor that
		// drops the With() call gets caught here, not in the field.
		if got := ev["component"]; got != "channel" {
			t.Fatalf("component attr: got %v, want channel", got)
		}
		if got, ok := ev["channel_pid"].(float64); !ok || got != 99999 {
			t.Fatalf("channel_pid attr: got %v, want 99999", ev["channel_pid"])
		}
	})
}
