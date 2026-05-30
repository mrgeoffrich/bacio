package main

import (
	"context"
	"time"

	"github.com/mrgeoffrich/bacio/internal/client"
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
