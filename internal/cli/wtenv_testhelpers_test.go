package cli

import (
	"log/slog"

	"github.com/mrgeoffrich/bacio/internal/logging"
	"github.com/mrgeoffrich/bacio/internal/wtenv"
)

// testEnv builds a wtenv.Resolved suitable for unit tests that need to
// drive buildStatusReport with a fixed DB path. Keeps the legacy
// default API addr so test assertions on the API row stay stable.
func testEnv(dbPath string) wtenv.Resolved {
	return wtenv.Resolved{
		Source:  wtenv.SourceDefault,
		DBPath:  dbPath,
		APIAddr: wtenv.DefaultAPIAddr,
	}
}

// testLog builds a logging.Resolved suitable for unit tests that need
// to drive buildStatusReport with a default logging configuration.
// Mirrors the default-fallback shape so test assertions on the Log row
// stay stable.
func testLog() logging.Resolved {
	return logging.Resolved{
		Source:      logging.SourceDefault,
		Dir:         "/tmp/bacio-logs",
		Level:       slog.LevelInfo,
		LevelSource: logging.SourceDefault,
	}
}
