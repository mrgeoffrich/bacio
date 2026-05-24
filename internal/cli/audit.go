package cli

import (
	"fmt"
	"os"
	"strings"

	"github.com/mrgeoffrich/bacio/internal/model"
	"github.com/mrgeoffrich/bacio/internal/store"
)

// actor returns the name stamped on audit-log rows for CLI mutations.
// An agent-driven call resolves via .bacio/agents.json (PID → identity);
// everything else lands as the literal "user", a placeholder until real
// auth lands. The OS-username fallback was deliberately removed — too
// easy for an LLM that saw a `--user` flag in help text to mis-attribute.
func actor() string {
	if id := agentIdentityForProcess(); id != "" {
		if clean, err := store.ValidateActor(id); err == nil {
			return clean
		}
	}
	return "user"
}

// recordOp writes an audit-log entry. Failures are reported on stderr but
// never fail the user-visible command — losing a log row is preferable to
// rolling back the work the user just asked for.
func recordOp(s *store.Store, e model.HistoryEntry) {
	if e.Actor == "" {
		e.Actor = actor()
	}
	if err := s.RecordHistory(e); err != nil {
		fmt.Fprintln(os.Stderr, "bacio: warning: failed to record history:", err)
	}
}

// repoSnapshot fills the repo-related history fields from a model.Repo,
// returning a partially-populated entry the caller can extend.
func repoSnapshot(repo *model.Repo) model.HistoryEntry {
	if repo == nil {
		return model.HistoryEntry{}
	}
	id := repo.ID
	return model.HistoryEntry{
		RepoID:     &id,
		RepoPrefix: repo.Prefix,
	}
}

// updatedFieldList returns "updated <a>,<b>" for a fixed set of "did this
// field get touched?" booleans. Used to summarise patch-style mutations
// in audit entries.
func updatedFieldList(fields map[string]bool) string {
	var parts []string
	for name, touched := range fields {
		if touched {
			parts = append(parts, name)
		}
	}
	if len(parts) == 0 {
		return ""
	}
	return "updated " + strings.Join(parts, ",")
}
