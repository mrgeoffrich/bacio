package main

import (
	"context"
	"time"

	"github.com/mrgeoffrich/bacio/internal/client"
	"github.com/mrgeoffrich/bacio/internal/model"
	"github.com/mrgeoffrich/bacio/internal/store"
)

// ProxyFQDNStatDTO is one per-FQDN proxy-traffic rollup, shaped for the
// desktop Monitor table (BACI-304). It mirrors model.ProxyFQDNStat field
// for field but carries camelCase JSON tags so the Wails-generated TS
// binding matches the React shape the web twin re-exports. ErrorRate is a
// fraction in [0,1]; the component formats it as a percentage. P50Ms /
// P95Ms are round-trip latency percentiles in milliseconds; FirstSeen /
// LastSeen bracket the host's activity window.
type ProxyFQDNStatDTO struct {
	Host         string    `json:"host"`
	RequestCount int64     `json:"requestCount"`
	BytesIn      int64     `json:"bytesIn"`
	BytesOut     int64     `json:"bytesOut"`
	ErrorCount   int64     `json:"errorCount"`
	ErrorRate    float64   `json:"errorRate"`
	P50Ms        int64     `json:"p50Ms"`
	P95Ms        int64     `json:"p95Ms"`
	FirstSeen    time.Time `json:"firstSeen"`
	LastSeen     time.Time `json:"lastSeen"`
}

// MonitorService is the Wails-bound proxy-capture API the desktop Monitor
// screen talks to. It wraps a local bacio client.Client and serves the
// BACI-303 per-FQDN rollup of proxy_requests. Read-only. The
// proxy_requests table is cross-cutting (no repo_id), so — unlike
// HistoryService — there is no repo prefix: the stats are global.
type MonitorService struct {
	client client.Client
}

func NewMonitorService(c client.Client) *MonitorService {
	return &MonitorService{client: c}
}

// ProxyStats returns the per-FQDN proxy-traffic rollup, busiest host
// first (the order the store already returns). sinceDays > 0 windows the
// rollup to a rolling lookback of that many days; sinceDays == 0 means
// all-time (no lower bound) — the same 0-sentinel convention the Shipped
// scope picker uses. The returned slice is always non-nil so the
// frontend can map over it unconditionally.
func (m *MonitorService) ProxyStats(sinceDays int) ([]ProxyFQDNStatDTO, error) {
	ctx := context.Background()
	var f store.ProxyStatsFilter
	if sinceDays > 0 {
		cutoff := time.Now().Add(-time.Duration(sinceDays) * 24 * time.Hour)
		f.Since = &cutoff
	}
	stats, err := m.client.ProxyStats(ctx, f)
	if err != nil {
		return nil, err
	}
	out := make([]ProxyFQDNStatDTO, 0, len(stats))
	for _, s := range stats {
		out = append(out, ProxyFQDNStatDTO{
			Host:         s.Host,
			RequestCount: s.RequestCount,
			BytesIn:      s.BytesIn,
			BytesOut:     s.BytesOut,
			ErrorCount:   s.ErrorCount,
			ErrorRate:    s.ErrorRate,
			P50Ms:        s.P50MS,
			P95Ms:        s.P95MS,
			FirstSeen:    s.FirstSeen,
			LastSeen:     s.LastSeen,
		})
	}
	return out, nil
}

// ProxyCaptureRowDTO is one row of the BACI-308 capture drill-down list,
// shaped for the desktop Monitor sheet. It carries the proxy_requests fields
// the capture list renders plus the IssueKey / Mode enrichment, with camelCase
// JSON tags so the Wails binding matches the web twin's TS-only shape (the
// same cross-transport split as ProxyFQDNStatDTO). IssueKey / Mode are empty
// when the capture has no dispatch. IsAnthropic gates the "parsed transcript"
// affordance; HasRaw whether the raw .http file is available.
type ProxyCaptureRowDTO struct {
	ID          int64     `json:"id"`
	Method      string    `json:"method"`
	Host        string    `json:"host"`
	Path        string    `json:"path"`
	Status      int       `json:"status"`
	BytesIn     int64     `json:"bytesIn"`
	BytesOut    int64     `json:"bytesOut"`
	DurationMs  int64     `json:"durationMs"`
	IsStream    bool      `json:"isStream"`
	IsAnthropic bool      `json:"isAnthropic"`
	HasRaw      bool      `json:"hasRaw"`
	DispatchID  *int64    `json:"dispatchId,omitempty"`
	IssueKey    string    `json:"issueKey,omitempty"`
	Mode        string    `json:"mode,omitempty"`
	StartedAt   time.Time `json:"startedAt"`
}

// ListCaptures returns the BACI-308 filtered capture list the Monitor sheet
// walks an FQDN row down into — newest-first, capped, each row enriched with
// its dispatch's issue-key + mode. host scopes to one upstream; dispatchID > 0
// scopes to one job; anthropicOnly keeps only the parseable message-API
// captures; sinceDays > 0 windows to a rolling lookback (0 = all-time, the same
// sentinel ProxyStats uses). The returned slice is always non-nil.
func (m *MonitorService) ListCaptures(host string, dispatchID int64, anthropicOnly bool, sinceDays int) ([]ProxyCaptureRowDTO, error) {
	ctx := context.Background()
	f := store.ProxyRequestFilter{Host: host}
	if dispatchID > 0 {
		f.DispatchID = &dispatchID
	}
	if anthropicOnly {
		t := true
		f.IsAnthropic = &t
	}
	if sinceDays > 0 {
		cutoff := time.Now().Add(-time.Duration(sinceDays) * 24 * time.Hour)
		f.Since = &cutoff
	}
	rows, err := m.client.ListProxyCaptures(ctx, f)
	if err != nil {
		return nil, err
	}
	out := make([]ProxyCaptureRowDTO, 0, len(rows))
	for _, r := range rows {
		out = append(out, ProxyCaptureRowDTO{
			ID:          r.ID,
			Method:      r.Method,
			Host:        r.Host,
			Path:        r.Path,
			Status:      r.Status,
			BytesIn:     r.BytesIn,
			BytesOut:    r.BytesOut,
			DurationMs:  r.DurationMS,
			IsStream:    r.IsStream,
			IsAnthropic: r.IsAnthropic,
			HasRaw:      r.RawLogPath != "",
			DispatchID:  r.DispatchID,
			IssueKey:    r.IssueKey,
			Mode:        r.Mode,
			StartedAt:   r.StartedAt,
		})
	}
	return out, nil
}

// CaptureRaw returns the raw, auth-redacted .http capture text for one
// proxy_requests id — the escape hatch the sheet falls back to for a capture
// that isn't a parseable Anthropic turn. Errors (surfaced to the frontend)
// when the row has no raw file or it's been pruned.
func (m *MonitorService) CaptureRaw(id int64) (string, error) {
	raw, err := m.client.ProxyCaptureRaw(context.Background(), id)
	if err != nil {
		return "", err
	}
	return string(raw), nil
}

// Capture returns the BACI-306 parsed detail of one captured Anthropic SSE
// turn (proxy_messages row), keyed on the proxy_requests id. Errors when the
// capture wasn't parseable. The snake_case model.ProxyMessage shape is aligned
// to the React transcript types, so the binding returns it directly.
func (m *MonitorService) Capture(id int64) (*model.ProxyMessage, error) {
	return m.client.AnthropicCapture(context.Background(), id)
}

// JobTranscript returns the BACI-306 assembled per-job transcript for a
// dispatch — the ordered primary-thread messages, summed usage, and auxiliary
// turns. Errors when the dispatch has no parsed captures. The snake_case
// model.AnthropicTranscript shape is aligned to the React transcript types, so
// the adapter consumes it directly.
func (m *MonitorService) JobTranscript(dispatchID int64) (*model.AnthropicTranscript, error) {
	return m.client.JobTranscript(context.Background(), dispatchID)
}
