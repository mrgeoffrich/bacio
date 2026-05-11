package api

import (
	"net/http"
	"strings"

	"github.com/mrgeoffrich/bacio/internal/cli/inputs"
	"github.com/mrgeoffrich/bacio/internal/inputio"
	"github.com/mrgeoffrich/bacio/internal/model"
	"github.com/mrgeoffrich/bacio/internal/store"
)

func (d deps) handleTagsAdd(w http.ResponseWriter, r *http.Request) {
	d.mutateTags(w, r, true)
}

func (d deps) handleTagsRemove(w http.ResponseWriter, r *http.Request) {
	d.mutateTags(w, r, false)
}

func (d deps) mutateTags(w http.ResponseWriter, r *http.Request, add bool) {
	repo, ok := resolveRepoFromPath(w, r, d.store)
	if !ok {
		return
	}
	raw, ok := readBody(r, w)
	if !ok {
		return
	}
	var (
		rawTags []string
	)
	if add {
		in, _, err := inputio.DecodeStrict[inputs.TagAddInput](raw)
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid_input", err.Error(), nil)
			return
		}
		rawTags = in.Tags
	} else {
		in, _, err := inputio.DecodeStrict[inputs.TagRmInput](raw)
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid_input", err.Error(), nil)
			return
		}
		rawTags = in.Tags
	}
	if len(rawTags) == 0 {
		writeError(w, http.StatusBadRequest, "invalid_input", "tags array is required", map[string]any{"field": "tags"})
		return
	}
	tags, err := store.NormalizeTags(rawTags)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_input", err.Error(), map[string]any{"field": "tags"})
		return
	}
	iss, ok := resolveIssueOnRepo(w, r, d.store, repo)
	if !ok {
		return
	}
	if isDryRun(r) {
		projected := *iss
		projected.Tags = projectTags(iss.Tags, tags, add)
		writeDryRun(w, http.StatusOK, &projected)
		return
	}
	if add {
		err = d.store.AddTagsToIssue(iss.ID, tags)
	} else {
		err = d.store.RemoveTagsFromIssue(iss.ID, tags)
	}
	if err != nil {
		status, code := statusForError(err)
		writeError(w, status, code, err.Error(), nil)
		return
	}
	updated, err := d.store.GetIssueByID(iss.ID)
	if err != nil {
		status, code := statusForError(err)
		writeError(w, status, code, err.Error(), nil)
		return
	}
	op := "tag.add"
	if !add {
		op = "tag.remove"
	}
	recordOp(d.store, d.logger, model.HistoryEntry{
		RepoID:      &iss.RepoID,
		RepoPrefix:  repo.Prefix,
		Actor:       ActorFromContext(r.Context()),
		Op:          op,
		Kind:        "issue",
		TargetID:    &iss.ID,
		TargetLabel: iss.Key,
		Details:     strings.Join(tags, ","),
	})
	writeJSON(w, http.StatusOK, updated)
}

// projectTags is kept in sync with internal/client/local_misc.go:projectTags.
// Returns the tag set that would result from add-or-remove applied to
// existing. Add preserves existing-first order; remove preserves remaining
// order.
func projectTags(existing, delta []string, add bool) []string {
	have := make(map[string]struct{}, len(existing))
	for _, t := range existing {
		have[t] = struct{}{}
	}
	if add {
		out := append([]string{}, existing...)
		for _, t := range delta {
			if _, ok := have[t]; ok {
				continue
			}
			have[t] = struct{}{}
			out = append(out, t)
		}
		return out
	}
	remove := make(map[string]struct{}, len(delta))
	for _, t := range delta {
		remove[t] = struct{}{}
	}
	out := make([]string, 0, len(existing))
	for _, t := range existing {
		if _, drop := remove[t]; drop {
			continue
		}
		out = append(out, t)
	}
	return out
}
