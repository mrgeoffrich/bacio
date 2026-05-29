package store

import (
	"database/sql"
	"errors"
	"sort"
	"strings"

	"github.com/mrgeoffrich/bacio/internal/model"
)

// Generic per-repo KV used by the TUI. Keep keys namespaced (e.g.
// "board.hidden_states") so future toggles can share this table.

func (s *Store) GetTUISetting(repoID int64, key string) (string, error) {
	var v string
	err := s.DB.QueryRow(
		`SELECT value FROM tui_settings WHERE repo_id = ? AND key = ?`,
		repoID, key,
	).Scan(&v)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return v, err
}

func (s *Store) SetTUISetting(repoID int64, key, value string) error {
	_, err := s.DB.Exec(
		`INSERT INTO tui_settings (repo_id, key, value, updated_at)
		 VALUES (?, ?, ?, CURRENT_TIMESTAMP)
		 ON CONFLICT(repo_id, key) DO UPDATE SET
		   value      = excluded.value,
		   updated_at = CURRENT_TIMESTAMP`,
		repoID, key, value,
	)
	return err
}

const boardHiddenKey = "board.hidden_states"

// LoadHiddenStates returns the set of board states the user has hidden in
// this repo. Unknown state names are silently dropped so a future state
// rename doesn't break old saved settings.
func (s *Store) LoadHiddenStates(repoID int64) (map[model.State]bool, error) {
	raw, err := s.GetTUISetting(repoID, boardHiddenKey)
	if err != nil {
		return nil, err
	}
	out := map[model.State]bool{}
	if raw == "" {
		return out, nil
	}
	valid := map[model.State]bool{}
	for _, st := range model.AllStates() {
		valid[st] = true
	}
	for _, name := range strings.Split(raw, ",") {
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}
		st := model.State(name)
		if valid[st] {
			out[st] = true
		}
	}
	return out, nil
}

// SaveHiddenStates persists the hidden set as a comma-separated list of
// canonical state names, sorted for stable storage.
func (s *Store) SaveHiddenStates(repoID int64, hidden map[model.State]bool) error {
	names := make([]string, 0, len(hidden))
	for st, on := range hidden {
		if on {
			names = append(names, string(st))
		}
	}
	sort.Strings(names)
	return s.SetTUISetting(repoID, boardHiddenKey, strings.Join(names, ","))
}

// boardHiddenFeaturesKey is the per-repo KV key for the set of feature
// slugs hidden from the kanban board. Shared by the TUI's feature
// picker (internal/tui/board_pickers.go) and the BACI-177 per-feature
// "Show on board" toggle exposed on the desktop / web Features screen
// — flipping the hide state on any surface is visible to the others
// on the same machine, since all three surfaces read and write this
// same KV row.
const boardHiddenFeaturesKey = "board.hidden_features"

// HiddenFeaturesUnassigned is the sentinel slug stored when the user
// has hidden the "(unassigned)" group — i.e. issues that have no
// feature attached. Real feature slugs in this codebase don't use the
// "__none__" form, so collisions are not a practical concern.
const HiddenFeaturesUnassigned = "__none__"

// LoadHiddenFeatures returns the set of feature slugs the user has
// hidden in the board view. The sentinel HiddenFeaturesUnassigned
// represents the "no feature" group.
func (s *Store) LoadHiddenFeatures(repoID int64) (map[string]bool, error) {
	raw, err := s.GetTUISetting(repoID, boardHiddenFeaturesKey)
	if err != nil {
		return nil, err
	}
	out := map[string]bool{}
	if raw == "" {
		return out, nil
	}
	for _, name := range strings.Split(raw, ",") {
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}
		out[name] = true
	}
	return out, nil
}

// SaveHiddenFeatures persists the hidden set as a comma-separated list
// of slugs, sorted for stable storage.
func (s *Store) SaveHiddenFeatures(repoID int64, hidden map[string]bool) error {
	names := make([]string, 0, len(hidden))
	for slug, on := range hidden {
		if on {
			names = append(names, slug)
		}
	}
	sort.Strings(names)
	return s.SetTUISetting(repoID, boardHiddenFeaturesKey, strings.Join(names, ","))
}

// IsFeatureHiddenOnBoard (BACI-177) reports whether the per-feature
// "Show on board" toggle is currently off for slug in repo — i.e.
// whether the slug is present in the boardHiddenFeaturesKey comma-set.
// Wraps LoadHiddenFeatures so REST / Wails handlers don't reach for
// the map directly. Returns false on an empty set / unknown slug.
func (s *Store) IsFeatureHiddenOnBoard(repoID int64, slug string) (bool, error) {
	hidden, err := s.LoadHiddenFeatures(repoID)
	if err != nil {
		return false, err
	}
	return hidden[slug], nil
}

// SetFeatureHiddenOnBoard (BACI-177) flips the per-feature board-hide
// flag for slug in repo. Idempotent — setting an already-hidden slug
// to hidden (or an already-visible slug to visible) is a no-op write.
// The slug is stored verbatim; callers should resolve it against a
// known feature row before flipping, since the store-of-truth here is
// the per-repo KV, not the features table.
func (s *Store) SetFeatureHiddenOnBoard(repoID int64, slug string, hidden bool) error {
	current, err := s.LoadHiddenFeatures(repoID)
	if err != nil {
		return err
	}
	if hidden {
		if current[slug] {
			return nil
		}
		current[slug] = true
	} else {
		if !current[slug] {
			return nil
		}
		delete(current, slug)
	}
	return s.SaveHiddenFeatures(repoID, current)
}

// backlogCollapsedKey is the per-repo KV key (BACI-288) for the Pipeline
// page's "collapse the Backlog column to a thin rail" display preference.
// Purely client-side chrome the backend never acts on, so it lives in
// tui_settings (like board.hidden_states) rather than repo_settings
// (where auto-ship lives because the controller's ticker reads it).
const backlogCollapsedKey = "pipeline.backlog_collapsed"

// IsBacklogCollapsed (BACI-288) reports whether the Pipeline page's
// Backlog column is collapsed for repo. Defaults to false (expanded).
func (s *Store) IsBacklogCollapsed(repoID int64) (bool, error) {
	raw, err := s.GetTUISetting(repoID, backlogCollapsedKey)
	if err != nil {
		return false, err
	}
	return raw == "1", nil
}

// SetBacklogCollapsed (BACI-288) persists the Pipeline Backlog-column
// collapse preference for repo. Stored as "1" (collapsed) / "" (expanded).
func (s *Store) SetBacklogCollapsed(repoID int64, collapsed bool) error {
	value := ""
	if collapsed {
		value = "1"
	}
	return s.SetTUISetting(repoID, backlogCollapsedKey, value)
}
