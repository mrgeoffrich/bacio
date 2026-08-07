package store

import (
	"database/sql"
	"errors"

	"github.com/mrgeoffrich/bacio/internal/model"
)

// RepoSettings is the per-repo typed-settings view (BACI-235). Today
// the only column is DefaultFeatureID — the per-repo `default_feature`
// setting that auto-applies to issues created without an explicit
// `feature_slug`. nil = no default (the historical behaviour); a non-
// nil value points at a live feature row in the same repo (the FK is
// ON DELETE SET NULL so the column auto-clears when the referenced
// feature is deleted).
type RepoSettings struct {
	RepoID           int64
	DefaultFeatureID *int64
	// AutoShip (Pipeline) is the per-repo Shipping-column auto-ship
	// toggle. When true the controller auto-ship ticker dispatches a
	// ship-mode agent against the top to_be_shipped card.
	AutoShip bool
	// The two nav-surface gates, RAW — nil means "never written", which
	// resolves to a default that depends on repos.kind. The `…Set`
	// suffix is the warning: these are not the effective values, and a
	// caller that treats nil as false gets the wrong answer for every
	// space nobody has touched. Read the resolved pair through
	// GetRepoSurfaces / ListRepoSurfaces instead; this struct exposes
	// them only because it is the row's faithful shape.
	ShowAgentSurfacesSet *bool
	ShowKanbanSet        *bool
}

// GetRepoSettings reads the per-repo settings row for repoID. Returns
// a zero-valued RepoSettings (with RepoID set) when no row exists —
// the "no per-repo settings have ever been written" case is read as
// "every column is its zero value", not as an error. Matches the
// defensive read pattern of GetDisplayShowArchived / friends.
func (s *Store) GetRepoSettings(repoID int64) (RepoSettings, error) {
	out := RepoSettings{RepoID: repoID}
	var defaultFeatureID sql.NullInt64
	var autoShip int
	var showAgent, showKanban sql.NullBool
	err := s.DB.QueryRow(
		`SELECT default_feature_id, auto_ship, show_agent_surfaces, show_kanban
		   FROM repo_settings WHERE repo_id = ?`,
		repoID,
	).Scan(&defaultFeatureID, &autoShip, &showAgent, &showKanban)
	if errors.Is(err, sql.ErrNoRows) {
		return out, nil
	}
	if err != nil {
		return out, err
	}
	if defaultFeatureID.Valid {
		v := defaultFeatureID.Int64
		out.DefaultFeatureID = &v
	}
	out.AutoShip = autoShip != 0
	out.ShowAgentSurfacesSet = nullableBool(showAgent)
	out.ShowKanbanSet = nullableBool(showKanban)
	return out, nil
}

// nullableBool maps a scanned SQL nullable onto the *bool the resolver
// takes: nil for NULL ("never written"), a pointer to the value
// otherwise.
func nullableBool(v sql.NullBool) *bool {
	if !v.Valid {
		return nil
	}
	b := v.Bool
	return &b
}

// GetRepoSurfaces resolves one space's nav-surface gates against its
// kind. This — not the raw RepoSettings columns — is the per-repo
// reader; see model.ResolveRepoSurfaces for why the raw values are
// never the answer on their own.
//
// The LEFT JOIN is what lets a repo with no repo_settings row at all
// (the common case) still resolve: the columns come back NULL and the
// kind decides.
func (s *Store) GetRepoSurfaces(repoID int64) (model.RepoSurfaces, error) {
	var kind string
	var showAgent, showKanban sql.NullBool
	err := s.DB.QueryRow(
		`SELECT r.kind, rs.show_agent_surfaces, rs.show_kanban
		   FROM repos r
		   LEFT JOIN repo_settings rs ON rs.repo_id = r.id
		  WHERE r.id = ?`,
		repoID,
	).Scan(&kind, &showAgent, &showKanban)
	if err != nil {
		return model.RepoSurfaces{}, err
	}
	return model.ResolveRepoSurfaces(
		model.RepoKind(kind), nullableBool(showAgent), nullableBool(showKanban),
	), nil
}

// ListRepoSurfaces resolves every space's gates in one query, keyed by
// prefix. Bulk-shaped because the only reader is the boards list, which
// needs all of them at once to render the nav — the same reason
// SyncStatuses is bulk-shaped.
func (s *Store) ListRepoSurfaces() (map[string]model.RepoSurfaces, error) {
	rows, err := s.DB.Query(
		`SELECT r.prefix, r.kind, rs.show_agent_surfaces, rs.show_kanban
		   FROM repos r
		   LEFT JOIN repo_settings rs ON rs.repo_id = r.id`,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make(map[string]model.RepoSurfaces)
	for rows.Next() {
		var prefix, kind string
		var showAgent, showKanban sql.NullBool
		if err := rows.Scan(&prefix, &kind, &showAgent, &showKanban); err != nil {
			return nil, err
		}
		out[prefix] = model.ResolveRepoSurfaces(
			model.RepoKind(kind), nullableBool(showAgent), nullableBool(showKanban),
		)
	}
	return out, rows.Err()
}

// SetRepoShowAgentSurfaces writes the per-space "Agent Mode" gate.
// Upsert, same shape as SetRepoAutoShip — note the DO UPDATE names only
// this column, so writing one setting never clobbers a sibling.
func (s *Store) SetRepoShowAgentSurfaces(repoID int64, enabled bool) error {
	_, err := s.DB.Exec(
		`INSERT INTO repo_settings (repo_id, show_agent_surfaces, updated_at)
		 VALUES (?, ?, CURRENT_TIMESTAMP)
		 ON CONFLICT(repo_id) DO UPDATE SET
		   show_agent_surfaces = excluded.show_agent_surfaces,
		   updated_at          = CURRENT_TIMESTAMP`,
		repoID, boolBit(enabled),
	)
	return err
}

// SetRepoShowKanban writes the per-space "Show Kanban Board" gate.
func (s *Store) SetRepoShowKanban(repoID int64, enabled bool) error {
	_, err := s.DB.Exec(
		`INSERT INTO repo_settings (repo_id, show_kanban, updated_at)
		 VALUES (?, ?, CURRENT_TIMESTAMP)
		 ON CONFLICT(repo_id) DO UPDATE SET
		   show_kanban = excluded.show_kanban,
		   updated_at  = CURRENT_TIMESTAMP`,
		repoID, boolBit(enabled),
	)
	return err
}

// boolBit maps a Go bool onto the 0/1 integer SQLite stores.
func boolBit(b bool) int {
	if b {
		return 1
	}
	return 0
}

// SetRepoAutoShip writes the per-repo Shipping-column auto-ship toggle.
// Upsert so the first write doesn't need a separate INSERT path; mirrors
// SetDefaultFeatureID's shape. Bumps updated_at on every write.
func (s *Store) SetRepoAutoShip(repoID int64, enabled bool) error {
	bit := 0
	if enabled {
		bit = 1
	}
	_, err := s.DB.Exec(
		`INSERT INTO repo_settings (repo_id, auto_ship, updated_at)
		 VALUES (?, ?, CURRENT_TIMESTAMP)
		 ON CONFLICT(repo_id) DO UPDATE SET
		   auto_ship  = excluded.auto_ship,
		   updated_at = CURRENT_TIMESTAMP`,
		repoID, bit,
	)
	return err
}

// SetDefaultFeatureID writes the per-repo default_feature_id column.
// nil clears the column (the "no default" semantic). The caller is
// responsible for verifying that featureID refers to a live feature
// row in the same repo — pass the result of GetFeatureBySlug.ID, not
// a slug. The FK ON DELETE SET NULL handles the post-write cascade if
// the feature is later deleted.
//
// Upsert shape so the first write doesn't need a separate INSERT
// path. updated_at is bumped on every write (including idempotent
// writes of the same value) — a deliberately conservative choice so
// the audit log + UI can show "last touched at" without joining the
// history table.
func (s *Store) SetDefaultFeatureID(repoID int64, featureID *int64) error {
	_, err := s.DB.Exec(
		`INSERT INTO repo_settings (repo_id, default_feature_id, updated_at)
		 VALUES (?, ?, CURRENT_TIMESTAMP)
		 ON CONFLICT(repo_id) DO UPDATE SET
		   default_feature_id = excluded.default_feature_id,
		   updated_at         = CURRENT_TIMESTAMP`,
		repoID, nullableInt(featureID),
	)
	return err
}

// ClearDefaultFeature is the explicit "unset the default" form. Same
// effect as SetDefaultFeatureID(repoID, nil); kept as its own method
// so the call site reads naturally and the audit-op naming
// (`repo_setting.update` with details `cleared`) doesn't have to
// re-derive intent from the value.
func (s *Store) ClearDefaultFeature(repoID int64) error {
	return s.SetDefaultFeatureID(repoID, nil)
}
