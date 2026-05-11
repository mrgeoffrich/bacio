package api

import (
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/mrgeoffrich/bacio/internal/cli/inputs"
	"github.com/mrgeoffrich/bacio/internal/inputio"
	"github.com/mrgeoffrich/bacio/internal/model"
	"github.com/mrgeoffrich/bacio/internal/store"
)

// parseRelType is kept in sync with internal/client/local_misc.go:parseRelType.
// Hyphens or underscores accepted so callers can use either casing.
func parseRelType(s string) (model.RelationType, error) {
	switch strings.ToLower(strings.NewReplacer("-", "_", " ", "_").Replace(strings.TrimSpace(s))) {
	case "blocks":
		return model.RelBlocks, nil
	case "relates_to", "relates":
		return model.RelRelatesTo, nil
	case "duplicate_of", "duplicates":
		return model.RelDuplicateOf, nil
	default:
		return "", fmt.Errorf("unknown relation %q (valid: blocks, relates-to, duplicate-of)", s)
	}
}

// resolveCanonicalIssue is the relation-specific resolver: must be canonical
// PREFIX-N (no bare numbers — agents don't have a "current repo"). Used for
// both legs of link/unlink.
func resolveCanonicalIssue(s *store.Store, raw string) (*model.Issue, error) {
	prefix, num, err := store.ParseIssueKey(raw)
	if err != nil {
		return nil, err
	}
	return s.GetIssueByKey(prefix, num)
}

func (d deps) handleRelationCreate(w http.ResponseWriter, r *http.Request) {
	repo, ok := resolveRepoFromPath(w, r, d.store)
	if !ok {
		return
	}
	raw, ok := readBody(r, w)
	if !ok {
		return
	}
	in, _, err := inputio.DecodeStrict[inputs.LinkInput](raw)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_input", err.Error(), nil)
		return
	}
	if in.From == "" || in.Type == "" || in.To == "" {
		writeError(w, http.StatusBadRequest, "invalid_input", "from, type, and to are required", nil)
		return
	}
	t, err := parseRelType(in.Type)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_input", err.Error(), map[string]any{"field": "type"})
		return
	}
	from, err := resolveCanonicalIssue(d.store, in.From)
	if err != nil {
		status, code := statusForError(err)
		writeError(w, status, code, err.Error(), map[string]any{"field": "from"})
		return
	}
	if from.RepoID != repo.ID {
		writeError(w, http.StatusNotFound, "not_found", "from issue not in this repo", map[string]any{"field": "from"})
		return
	}
	to, err := resolveCanonicalIssue(d.store, in.To)
	if err != nil {
		status, code := statusForError(err)
		writeError(w, status, code, err.Error(), map[string]any{"field": "to"})
		return
	}
	if from.ID == to.ID {
		writeError(w, http.StatusBadRequest, "invalid_input", "an issue cannot be linked to itself", nil)
		return
	}
	if isDryRun(r) {
		writeDryRun(w, http.StatusCreated, &model.Relation{
			FromIssue: from.Key,
			ToIssue:   to.Key,
			Type:      t,
		})
		return
	}
	if err := d.store.CreateRelation(from.ID, to.ID, t); err != nil {
		status, code := statusForError(err)
		writeError(w, status, code, err.Error(), nil)
		return
	}
	recordOp(d.store, d.logger, model.HistoryEntry{
		RepoID:      &from.RepoID,
		RepoPrefix:  repo.Prefix,
		Actor:       ActorFromContext(r.Context()),
		Op:          "relation.create",
		Kind:        "issue",
		TargetID:    &from.ID,
		TargetLabel: from.Key,
		Details:     fmt.Sprintf("%s %s", t, to.Key),
	})
	writeJSON(w, http.StatusCreated, &model.Relation{
		FromIssue: from.Key,
		ToIssue:   to.Key,
		Type:      t,
	})
}

func (d deps) handleRelationDelete(w http.ResponseWriter, r *http.Request) {
	repo, ok := resolveRepoFromPath(w, r, d.store)
	if !ok {
		return
	}
	raw, ok := readBody(r, w)
	if !ok {
		return
	}
	in, _, err := inputio.DecodeStrict[inputs.UnlinkInput](raw)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_input", err.Error(), nil)
		return
	}
	if in.A == "" || in.B == "" {
		writeError(w, http.StatusBadRequest, "invalid_input", "a and b are required", nil)
		return
	}
	a, err := resolveCanonicalIssue(d.store, in.A)
	if err != nil {
		status, code := statusForError(err)
		writeError(w, status, code, err.Error(), map[string]any{"field": "a"})
		return
	}
	if a.RepoID != repo.ID {
		writeError(w, http.StatusNotFound, "not_found", "issue a not in this repo", map[string]any{"field": "a"})
		return
	}
	b, err := resolveCanonicalIssue(d.store, in.B)
	if err != nil {
		status, code := statusForError(err)
		writeError(w, status, code, err.Error(), map[string]any{"field": "b"})
		return
	}
	if isDryRun(r) {
		rels, err := d.store.ListIssueRelations(a.ID)
		if err != nil {
			status, code := statusForError(err)
			writeError(w, status, code, err.Error(), nil)
			return
		}
		matched := 0
		if rels != nil {
			for _, r := range rels.Outgoing {
				if r.ToIssue == b.Key {
					matched++
				}
			}
			for _, r := range rels.Incoming {
				if r.FromIssue == b.Key {
					matched++
				}
			}
		}
		writeDryRun(w, http.StatusOK, &RelationDeletePreview{
			A:           a.Key,
			B:           b.Key,
			WouldRemove: matched,
		})
		return
	}
	n, err := d.store.DeleteRelation(a.ID, b.ID)
	if err != nil {
		status, code := statusForError(err)
		if errors.Is(err, store.ErrNoRelation) {
			status, code = http.StatusNotFound, "not_found"
		}
		writeError(w, status, code, err.Error(), nil)
		return
	}
	if n > 0 {
		recordOp(d.store, d.logger, model.HistoryEntry{
			RepoID:      &a.RepoID,
			RepoPrefix:  repo.Prefix,
			Actor:       ActorFromContext(r.Context()),
			Op:          "relation.delete",
			Kind:        "issue",
			TargetID:    &a.ID,
			TargetLabel: a.Key,
			Details:     fmt.Sprintf("unlinked from %s (%d row(s))", b.Key, n),
		})
	}
	// Idempotent unlink — return 200 with the row count so the CLI can print
	// a truthful "removed N relation(s)" message instead of guessing.
	writeJSON(w, http.StatusOK, map[string]int{"removed": int(n)})
}
