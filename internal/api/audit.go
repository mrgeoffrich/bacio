package api

import (
	"log/slog"

	"github.com/mrgeoffrich/bacio/internal/model"
	"github.com/mrgeoffrich/bacio/internal/store"
)

// recordOp writes an audit-log entry. Failures are logged and swallowed —
// losing one history row is preferable to rolling back the work the
// caller just asked for. Mirrors internal/cli/audit.go but takes the
// actor explicitly (read from request context by the handler) rather
// than reaching into a CLI global.
func recordOp(s *store.Store, logger *slog.Logger, e model.HistoryEntry) {
	if e.Actor == "" {
		e.Actor = defaultActor
	}
	if err := s.RecordHistory(e); err != nil {
		logger.Warn("failed to record history", "err", err, "op", e.Op)
	}
}
