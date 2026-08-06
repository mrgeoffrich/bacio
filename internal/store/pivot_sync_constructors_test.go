package store

import (
	"database/sql"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/mrgeoffrich/bacio/internal/model"
)

// Tests for the three constructors the sync importer needed and that
// only the store can own: CreatePhantomWorkspace, InsertPhantomRepoTx,
// CreateKanbanColumnFromSync + DeleteKanbanColumnByUUID.

func TestCreatePhantomWorkspacePreservesUUIDAndStaysPathless(t *testing.T) {
	s := newTestStore(t)
	const uuid = "0191f0d2-1111-7000-8000-aaaaaaaaaaaa"

	ws, err := s.CreatePhantomWorkspace(uuid, "WKSP", "Team notes")
	if err != nil {
		t.Fatalf("CreatePhantomWorkspace: %v", err)
	}
	if ws.UUID != uuid {
		t.Errorf("uuid = %q, want the caller's %q — a minted uuid would fork the identity the sync repo carries", ws.UUID, uuid)
	}
	if !ws.IsWorkspace() {
		t.Errorf("kind = %q, want workspace", ws.Kind)
	}
	if ws.Path != "" {
		t.Errorf("path = %q, want empty", ws.Path)
	}
	if ws.RemoteURL != "" {
		t.Errorf("remote_url = %q, want empty", ws.RemoteURL)
	}

	// Unlike CreateWorkspace, it must NOT bootstrap: an imported prefix
	// gets its features and lanes from the sync repo, and a local
	// default set would collide with the incoming records.
	cols, err := s.ListKanbanColumns(ws.ID)
	if err != nil {
		t.Fatalf("list lanes: %v", err)
	}
	if len(cols) != 0 {
		t.Errorf("got %d bootstrapped lanes, want 0", len(cols))
	}

	if _, err := s.CreatePhantomWorkspace("", "WKS2", "x"); err == nil {
		t.Error("empty uuid should be refused")
	}
	if _, err := s.CreatePhantomWorkspace("u", "", "x"); err == nil {
		t.Error("empty prefix should be refused")
	}
}

func TestInsertPhantomRepoTxHoldsTheKindPathInvariant(t *testing.T) {
	s := newTestStore(t)

	tx, err := s.DB.Begin()
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	if err := InsertPhantomRepoTx(tx, "uuid-git", "GITR", "a git repo",
		model.RepoKindGit, "git@example.com:x/y.git", 7,
		"2026-01-04 08:00:00", "2026-01-05 08:00:00"); err != nil {
		t.Fatalf("insert git: %v", err)
	}
	if err := InsertPhantomRepoTx(tx, "uuid-ws", "WKSP", "a workspace",
		model.RepoKindWorkspace, "", 1, "", ""); err != nil {
		t.Fatalf("insert workspace: %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("commit: %v", err)
	}

	git, err := s.GetRepoByUUID("uuid-git")
	if err != nil {
		t.Fatalf("get git: %v", err)
	}
	if !git.IsPhantom() {
		t.Errorf("git row should be a phantom (kind=%q path=%q)", git.Kind, git.Path)
	}
	if git.NextIssueNumber != 7 {
		t.Errorf("next_issue_number = %d, want 7", git.NextIssueNumber)
	}
	if got := git.CreatedAt.UTC().Format("2006-01-02 15:04:05"); got != "2026-01-04 08:00:00" {
		t.Errorf("created_at = %q, want the caller's timestamp", got)
	}

	ws, err := s.GetRepoByUUID("uuid-ws")
	if err != nil {
		t.Fatalf("get workspace: %v", err)
	}
	if !ws.IsWorkspace() || ws.Path != "" {
		t.Errorf("workspace row wrong: kind=%q path=%q", ws.Kind, ws.Path)
	}

	// There is no path parameter, so a pathed workspace is not
	// expressible — but an unknown kind is still rejected outright.
	tx2, err := s.DB.Begin()
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer tx2.Rollback()
	err = InsertPhantomRepoTx(tx2, "uuid-bad", "BADR", "x", model.RepoKind("nonsense"), "", 1, "", "")
	if err == nil || !strings.Contains(err.Error(), "unknown repo kind") {
		t.Errorf("unknown kind should be refused, got %v", err)
	}
	if err := InsertPhantomRepoTx(nil, "u", "P", "x", model.RepoKindGit, "", 1, "", ""); err == nil {
		t.Error("nil tx should be refused")
	}
}

func TestCreateKanbanColumnFromSyncPreservesUUIDPositionAndTimestamps(t *testing.T) {
	s, repo := kanbanFixture(t)
	created := time.Date(2026, 1, 5, 8, 0, 0, 0, time.UTC)
	updated := time.Date(2026, 1, 6, 8, 0, 0, 0, time.UTC)

	// Position 3 with an empty board: written verbatim, not appended at
	// MAX+1, because the authoring peer owns the lane order.
	col, err := s.CreateKanbanColumnFromSync(repo.ID, "uuid-lane", "Doing", 3,
		sql.NullTime{Time: created, Valid: true}, sql.NullTime{Time: updated, Valid: true})
	if err != nil {
		t.Fatalf("CreateKanbanColumnFromSync: %v", err)
	}
	if col.UUID != "uuid-lane" {
		t.Errorf("uuid = %q", col.UUID)
	}
	if col.Position != 3 {
		t.Errorf("position = %d, want the caller's 3", col.Position)
	}
	if !col.CreatedAt.UTC().Equal(created) || !col.UpdatedAt.UTC().Equal(updated) {
		t.Errorf("timestamps not preserved: created=%v updated=%v", col.CreatedAt, col.UpdatedAt)
	}

	if _, err := s.CreateKanbanColumnFromSync(repo.ID, "", "X", 0, sql.NullTime{}, sql.NullTime{}); err == nil {
		t.Error("empty uuid should be refused")
	}
	if _, err := s.CreateKanbanColumnFromSync(repo.ID, "uuid-dupe", "Doing", 4, sql.NullTime{}, sql.NullTime{}); err == nil {
		t.Error("a duplicate lane name in the same repo should be refused")
	}
}

func TestDeleteKanbanColumnByUUIDTakesCardsOffTheBoard(t *testing.T) {
	s, repo := kanbanFixture(t)
	if err := s.BootstrapKanbanColumns(repo.ID); err != nil {
		t.Fatalf("bootstrap: %v", err)
	}
	cols, err := s.ListKanbanColumns(repo.ID)
	if err != nil || len(cols) < 2 {
		t.Fatalf("lanes: %v (%d)", err, len(cols))
	}
	iss, err := s.CreateIssue(repo.ID, nil, "a card", "", model.StateTodo, nil, "", "")
	if err != nil {
		t.Fatalf("create issue: %v", err)
	}
	if err := s.SetIssueKanbanColumn(iss.ID, &cols[1].ID, 0); err != nil {
		t.Fatalf("place card: %v", err)
	}

	if err := s.DeleteKanbanColumnByUUID(cols[1].UUID); err != nil {
		t.Fatalf("DeleteKanbanColumnByUUID: %v", err)
	}
	if _, err := s.GetKanbanColumnByUUID(cols[1].UUID); !errors.Is(err, ErrNotFound) {
		t.Errorf("lane survived the delete: %v", err)
	}

	after, err := s.GetIssueByID(iss.ID)
	if err != nil {
		t.Fatalf("re-read issue: %v", err)
	}
	if after.KanbanColumnID != nil {
		t.Error("the card should have come off the board")
	}
	if after.KanbanPosition != 0 {
		t.Errorf("kanban_position = %d, want 0 — a card off the board must not keep a stale lane index", after.KanbanPosition)
	}
	// The remaining lanes renumber densely, as with a local delete.
	_, positions := columnNames(t, s, repo.ID)
	assertDense(t, positions)

	if err := s.DeleteKanbanColumnByUUID("nope"); !errors.Is(err, ErrNotFound) {
		t.Errorf("unknown uuid should be ErrNotFound, got %v", err)
	}
	if err := s.DeleteKanbanColumnByUUID(""); err == nil {
		t.Error("empty uuid should be refused")
	}
}
