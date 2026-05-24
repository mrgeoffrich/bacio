package client

import (
	"context"
	"fmt"
	"strings"

	"github.com/mrgeoffrich/bacio/internal/cli/inputs"
	"github.com/mrgeoffrich/bacio/internal/model"
	"github.com/mrgeoffrich/bacio/internal/store"
)

// ----- Comments -----

func (c *localClient) ListComments(ctx context.Context, repo *model.Repo, key string) ([]*model.Comment, error) {
	iss, err := c.GetIssueByKey(ctx, repo, key)
	if err != nil {
		return nil, err
	}
	cs, err := c.store.ListComments(iss.ID)
	if err != nil {
		return nil, err
	}
	if cs == nil {
		cs = []*model.Comment{}
	}
	return cs, nil
}

func (c *localClient) AddComment(ctx context.Context, repo *model.Repo, in inputs.CommentAddInput, dryRun bool) (*model.Comment, error) {
	if in.IssueKey == "" || in.Author == "" || in.Body == "" {
		return nil, fmt.Errorf("issue_key, author, and body are required")
	}
	iss, err := c.GetIssueByKey(ctx, repo, in.IssueKey)
	if err != nil {
		return nil, err
	}
	// BACI-131: when in.Eval is true, the store snapshots the in-flight
	// (session, dispatch, mode) triple so the row carries the exact run
	// context that was live the moment the user clicked the kanban
	// quick-eval button. Non-eval calls skip the lookup entirely.
	var evalCtx store.EvalContext
	if in.Eval {
		ec, err := c.store.ResolveEvalContext(iss.ID)
		if err != nil {
			return nil, err
		}
		evalCtx = ec
	}
	if dryRun {
		return &model.Comment{
			IssueID:        iss.ID,
			Author:         in.Author,
			Body:           in.Body,
			Eval:           in.Eval,
			AgentSessionID: evalCtx.AgentSessionID,
			DispatchID:     evalCtx.DispatchID,
			Mode:           evalCtx.Mode,
		}, nil
	}
	cm, err := c.store.CreateComment(store.CreateCommentIn{
		IssueID:        iss.ID,
		Author:         in.Author,
		Body:           in.Body,
		Eval:           in.Eval,
		AgentSessionID: evalCtx.AgentSessionID,
		DispatchID:     evalCtx.DispatchID,
		Mode:           evalCtx.Mode,
	})
	if err != nil {
		return nil, err
	}
	details := "by " + in.Author
	if in.Eval {
		// Distinguishes eval rows in `bacio history -o json` without
		// the reviewer having to re-query the comment row.
		details += " (eval)"
	}
	c.recordOp(model.HistoryEntry{
		RepoID: &iss.RepoID, RepoPrefix: repo.Prefix,
		Op: "comment.add", Kind: "issue",
		TargetID: &iss.ID, TargetLabel: iss.Key,
		Details: details,
	})
	return cm, nil
}

func (c *localClient) DeleteComment(ctx context.Context, repo *model.Repo, in inputs.CommentRmInput, dryRun bool) (*CommentDeletePreview, int64, error) {
	if in.IssueKey == "" || in.CommentUUID == "" {
		return nil, 0, fmt.Errorf("issue_key and comment_uuid are required")
	}
	iss, err := c.GetIssueByKey(ctx, repo, in.IssueKey)
	if err != nil {
		return nil, 0, err
	}
	cm, err := c.store.GetCommentByUUID(in.CommentUUID)
	if err != nil {
		return nil, 0, err
	}
	if cm.IssueID != iss.ID {
		return nil, 0, fmt.Errorf("comment %q does not belong to %s", in.CommentUUID, iss.Key)
	}
	if dryRun {
		return &CommentDeletePreview{IssueKey: iss.Key, CommentUUID: cm.UUID, WouldRemove: 1}, 0, nil
	}
	if err := c.store.DeleteCommentByUUID(cm.UUID); err != nil {
		return nil, 0, err
	}
	c.recordOp(model.HistoryEntry{
		RepoID: &iss.RepoID, RepoPrefix: repo.Prefix,
		Op: "comment.delete", Kind: "issue",
		TargetID: &iss.ID, TargetLabel: iss.Key,
		Details: "by " + cm.Author,
	})
	return nil, 1, nil
}

// ----- Relations -----

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

func (c *localClient) LinkRelation(ctx context.Context, repo *model.Repo, in inputs.LinkInput, dryRun bool) (*model.Relation, error) {
	t, err := parseRelType(in.Type)
	if err != nil {
		return nil, err
	}
	from, err := c.GetIssueByKey(ctx, repo, in.From)
	if err != nil {
		return nil, err
	}
	to, err := c.GetIssueByKey(ctx, repo, in.To)
	if err != nil {
		return nil, err
	}
	if from.ID == to.ID {
		return nil, fmt.Errorf("an issue cannot be linked to itself")
	}
	if dryRun {
		return &model.Relation{
			FromIssue: from.Key,
			ToIssue:   to.Key,
			Type:      t,
		}, nil
	}
	if err := c.store.CreateRelation(from.ID, to.ID, t); err != nil {
		return nil, err
	}
	c.recordOp(model.HistoryEntry{
		RepoID: &from.RepoID, RepoPrefix: repo.Prefix,
		Op: "relation.create", Kind: "issue",
		TargetID: &from.ID, TargetLabel: from.Key,
		Details: fmt.Sprintf("%s %s", t, to.Key),
	})
	return &model.Relation{
		FromIssue: from.Key,
		ToIssue:   to.Key,
		Type:      t,
	}, nil
}

// BlockersFor is a thin pass-through to the store (BACI-114). Used by
// boardcards.Assemble for the per-card "blocked by open issue" marker —
// one bulk read per board poll, no audit row (it's a pure read).
func (c *localClient) BlockersFor(ctx context.Context, ids []int64) (map[int64][]store.IssueBlocker, error) {
	return c.store.BlockersFor(ids)
}

func (c *localClient) UnlinkRelation(ctx context.Context, repo *model.Repo, in inputs.UnlinkInput, dryRun bool) (*RelationDeletePreview, int64, error) {
	a, err := c.GetIssueByKey(ctx, repo, in.A)
	if err != nil {
		return nil, 0, err
	}
	b, err := c.GetIssueByKey(ctx, repo, in.B)
	if err != nil {
		return nil, 0, err
	}
	if dryRun {
		rels, err := c.store.ListIssueRelations(a.ID)
		if err != nil {
			return nil, 0, err
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
		return &RelationDeletePreview{
			A:           a.Key,
			B:           b.Key,
			WouldRemove: matched,
		}, 0, nil
	}
	n, err := c.store.DeleteRelation(a.ID, b.ID)
	if err != nil {
		return nil, 0, err
	}
	if n > 0 {
		c.recordOp(model.HistoryEntry{
			RepoID: &a.RepoID, RepoPrefix: repo.Prefix,
			Op: "relation.delete", Kind: "issue",
			TargetID: &a.ID, TargetLabel: a.Key,
			Details: fmt.Sprintf("unlinked from %s (%d row(s))", b.Key, n),
		})
	}
	return nil, n, nil
}

// ----- Pull requests -----

func (c *localClient) ListPRs(ctx context.Context, repo *model.Repo, key string) ([]*model.PullRequest, error) {
	iss, err := c.GetIssueByKey(ctx, repo, key)
	if err != nil {
		return nil, err
	}
	prs, err := c.store.ListPRs(iss.ID)
	if err != nil {
		return nil, err
	}
	if prs == nil {
		prs = []*model.PullRequest{}
	}
	return prs, nil
}

func (c *localClient) AttachPR(ctx context.Context, repo *model.Repo, key, url string, dryRun bool) (*model.PullRequest, error) {
	pr, err := store.ValidatePRURLStrict(url)
	if err != nil {
		return nil, err
	}
	iss, err := c.GetIssueByKey(ctx, repo, key)
	if err != nil {
		return nil, err
	}
	if dryRun {
		return &model.PullRequest{IssueID: iss.ID, URL: pr}, nil
	}
	created, err := c.store.AttachPR(iss.ID, pr)
	if err != nil {
		return nil, err
	}
	c.recordOp(model.HistoryEntry{
		RepoID: &iss.RepoID, RepoPrefix: repo.Prefix,
		Op: "pr.attach", Kind: "issue",
		TargetID: &iss.ID, TargetLabel: iss.Key,
		Details: pr,
	})
	return created, nil
}

func (c *localClient) DetachPR(ctx context.Context, repo *model.Repo, key, url string, dryRun bool) (*PRDetachPreview, int64, error) {
	clean := strings.TrimSpace(url)
	iss, err := c.GetIssueByKey(ctx, repo, key)
	if err != nil {
		return nil, 0, err
	}
	if dryRun {
		prs, err := c.store.ListPRs(iss.ID)
		if err != nil {
			return nil, 0, err
		}
		matched := false
		for _, p := range prs {
			if p.URL == clean {
				matched = true
				break
			}
		}
		if !matched {
			return nil, 0, fmt.Errorf("no PR matching %q on %s", url, iss.Key)
		}
		return &PRDetachPreview{IssueKey: iss.Key, URL: clean, WouldRemove: 1}, 0, nil
	}
	n, err := c.store.DetachPR(iss.ID, clean)
	if err != nil {
		return nil, 0, err
	}
	if n == 0 {
		return nil, 0, fmt.Errorf("no PR matching %q on %s", url, iss.Key)
	}
	c.recordOp(model.HistoryEntry{
		RepoID: &iss.RepoID, RepoPrefix: repo.Prefix,
		Op: "pr.detach", Kind: "issue",
		TargetID: &iss.ID, TargetLabel: iss.Key,
		Details: clean,
	})
	return nil, n, nil
}

// ----- Tags -----

func (c *localClient) AddTags(ctx context.Context, repo *model.Repo, key string, tags []string, dryRun bool) (*model.Issue, error) {
	return c.mutateTags(ctx, repo, key, tags, true, dryRun)
}

func (c *localClient) RemoveTags(ctx context.Context, repo *model.Repo, key string, tags []string, dryRun bool) (*model.Issue, error) {
	return c.mutateTags(ctx, repo, key, tags, false, dryRun)
}

func (c *localClient) mutateTags(ctx context.Context, repo *model.Repo, key string, raw []string, add, dryRun bool) (*model.Issue, error) {
	tags, err := store.NormalizeTags(raw)
	if err != nil {
		return nil, err
	}
	iss, err := c.GetIssueByKey(ctx, repo, key)
	if err != nil {
		return nil, err
	}
	if dryRun {
		projected := *iss
		projected.Tags = projectTags(iss.Tags, tags, add)
		return &projected, nil
	}
	op := "tag.add"
	if add {
		err = c.store.AddTagsToIssue(iss.ID, tags)
	} else {
		err = c.store.RemoveTagsFromIssue(iss.ID, tags)
		op = "tag.remove"
	}
	if err != nil {
		return nil, err
	}
	updated, err := c.store.GetIssueByID(iss.ID)
	if err != nil {
		return nil, err
	}
	c.recordOp(model.HistoryEntry{
		RepoID: &iss.RepoID, RepoPrefix: repo.Prefix,
		Op: op, Kind: "issue",
		TargetID: &iss.ID, TargetLabel: iss.Key,
		Details: strings.Join(tags, ","),
	})
	return updated, nil
}

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
