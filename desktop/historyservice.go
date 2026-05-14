package main

import (
	"context"
	"fmt"
	"time"

	"github.com/mrgeoffrich/bacio/internal/client"
	"github.com/mrgeoffrich/bacio/internal/model"
	"github.com/mrgeoffrich/bacio/internal/store"
)

const (
	historyDefaultPageSize = 50
	historyMaxPageSize     = 200
)

// HistoryEntryDTO is one audit-log row, shaped for the desktop history table.
type HistoryEntryDTO struct {
	ID          int64     `json:"id"`
	Actor       string    `json:"actor"`
	Op          string    `json:"op"`
	Kind        string    `json:"kind"`
	TargetLabel string    `json:"targetLabel"`
	Details     string    `json:"details"`
	CreatedAt   time.Time `json:"createdAt"`
}

// HistoryPage is one page of the audit log. Page is 0-based; HasMore reports
// whether a further page exists, so the frontend can drive Prev/Next without a
// separate total-count query.
type HistoryPage struct {
	Entries  []HistoryEntryDTO `json:"entries"`
	Page     int               `json:"page"`
	PageSize int               `json:"pageSize"`
	HasMore  bool              `json:"hasMore"`
}

// HistoryService is the Wails-bound audit-log API the desktop frontend talks
// to. It wraps a local bacio client.Client and serves the history newest-first
// in fixed-size pages. Read-only. History is per-repo, so every call needs a
// concrete repo prefix (the "all repositories" pseudo-board has no scope).
type HistoryService struct {
	client client.Client
}

func NewHistoryService(c client.Client) *HistoryService {
	return &HistoryService{client: c}
}

// resolveRepo turns a repo prefix into a *model.Repo, rejecting the empty /
// "all" pseudo-board since history is always scoped to one repo.
func (h *HistoryService) resolveRepo(ctx context.Context, repoPrefix string) (*model.Repo, error) {
	if repoPrefix == "" || repoPrefix == "all" {
		return nil, fmt.Errorf("select a repository to view its history")
	}
	return h.client.GetRepoByPrefix(ctx, repoPrefix)
}

// ListHistory returns one page of the repo's audit log, newest first. page is
// 0-based; pageSize is clamped to a sane range. It over-fetches by one row so
// HasMore can be reported without a separate count query.
func (h *HistoryService) ListHistory(repoPrefix string, page, pageSize int) (HistoryPage, error) {
	ctx := context.Background()
	if pageSize <= 0 {
		pageSize = historyDefaultPageSize
	}
	if pageSize > historyMaxPageSize {
		pageSize = historyMaxPageSize
	}
	if page < 0 {
		page = 0
	}
	repo, err := h.resolveRepo(ctx, repoPrefix)
	if err != nil {
		return HistoryPage{}, err
	}
	entries, err := h.client.ListHistory(ctx, repo, store.HistoryFilter{
		Limit:  pageSize + 1,
		Offset: page * pageSize,
	})
	if err != nil {
		return HistoryPage{}, err
	}
	hasMore := len(entries) > pageSize
	if hasMore {
		entries = entries[:pageSize]
	}
	out := make([]HistoryEntryDTO, 0, len(entries))
	for _, e := range entries {
		out = append(out, HistoryEntryDTO{
			ID:          e.ID,
			Actor:       e.Actor,
			Op:          e.Op,
			Kind:        e.Kind,
			TargetLabel: e.TargetLabel,
			Details:     e.Details,
			CreatedAt:   e.CreatedAt,
		})
	}
	return HistoryPage{
		Entries:  out,
		Page:     page,
		PageSize: pageSize,
		HasMore:  hasMore,
	}, nil
}
