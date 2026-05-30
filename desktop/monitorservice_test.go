package main

import (
	"context"
	"testing"
	"time"

	"github.com/mrgeoffrich/bacio/internal/client"
	"github.com/mrgeoffrich/bacio/internal/model"
	"github.com/mrgeoffrich/bacio/internal/store"
)

// fakeMonitorClient is a minimal client.Client for MonitorService tests —
// it embeds the interface (so unused methods exist but panic if called)
// and implements only ProxyStats. It records the filter it was handed so
// the test can assert the sinceDays → ProxyStatsFilter mapping, and
// returns canned rows so the DTO reshape is exercised. Mirrors the
// fakeFeatureClient pattern in featureservice_test.go.
type fakeMonitorClient struct {
	client.Client
	lastFilter store.ProxyStatsFilter
	stats      []*model.ProxyFQDNStat
}

func (f *fakeMonitorClient) ProxyStats(_ context.Context, filter store.ProxyStatsFilter) ([]*model.ProxyFQDNStat, error) {
	f.lastFilter = filter
	return f.stats, nil
}

// TestMonitorServiceProxyStatsAllTime pins that sinceDays==0 leaves the
// filter window open (all-time) and that the busiest-first order plus the
// per-field reshape from model.ProxyFQDNStat to the camelCase DTO survive.
func TestMonitorServiceProxyStatsAllTime(t *testing.T) {
	first := time.Now().Add(-48 * time.Hour)
	last := time.Now().Add(-time.Hour)
	fake := &fakeMonitorClient{
		stats: []*model.ProxyFQDNStat{
			{
				Host: "api.anthropic.com", RequestCount: 3, BytesIn: 300, BytesOut: 600,
				ErrorCount: 1, ErrorRate: 1.0 / 3.0, P50MS: 200, P95MS: 300,
				FirstSeen: first, LastSeen: last,
			},
			{
				Host: "example.com", RequestCount: 1, BytesIn: 100, BytesOut: 200,
				ErrorCount: 0, ErrorRate: 0, P50MS: 50, P95MS: 50,
				FirstSeen: first, LastSeen: last,
			},
		},
	}
	svc := NewMonitorService(fake)
	got, err := svc.ProxyStats(0)
	if err != nil {
		t.Fatalf("ProxyStats: %v", err)
	}
	if fake.lastFilter.Since != nil {
		t.Fatalf("sinceDays=0 should leave Since nil (all-time), got %v", *fake.lastFilter.Since)
	}
	if len(got) != 2 {
		t.Fatalf("len = %d, want 2 — %+v", len(got), got)
	}
	// Busiest-first ordering (the store's order) is preserved verbatim.
	if got[0].Host != "api.anthropic.com" || got[1].Host != "example.com" {
		t.Fatalf("order wrong: %q, %q", got[0].Host, got[1].Host)
	}
	// The reshape carried every field across the snake→camel boundary.
	top := got[0]
	if top.RequestCount != 3 || top.BytesIn != 300 || top.BytesOut != 600 ||
		top.ErrorCount != 1 || top.P50Ms != 200 || top.P95Ms != 300 {
		t.Fatalf("reshape lost a field: %+v", top)
	}
	if !top.FirstSeen.Equal(first) || !top.LastSeen.Equal(last) {
		t.Fatalf("timestamps not carried: first=%v last=%v", top.FirstSeen, top.LastSeen)
	}
}

// TestMonitorServiceProxyStatsWindow pins that a positive sinceDays maps
// to a rolling lookback cutoff on the filter — sinceDays days back from
// now, within a small tolerance for the wall-clock read.
func TestMonitorServiceProxyStatsWindow(t *testing.T) {
	fake := &fakeMonitorClient{}
	svc := NewMonitorService(fake)
	before := time.Now()
	if _, err := svc.ProxyStats(7); err != nil {
		t.Fatalf("ProxyStats: %v", err)
	}
	after := time.Now()
	if fake.lastFilter.Since == nil {
		t.Fatalf("sinceDays=7 should set a Since cutoff")
	}
	cutoff := *fake.lastFilter.Since
	wantMin := before.Add(-7 * 24 * time.Hour)
	wantMax := after.Add(-7 * 24 * time.Hour)
	if cutoff.Before(wantMin) || cutoff.After(wantMax) {
		t.Fatalf("cutoff %v not within 7-day lookback window [%v, %v]", cutoff, wantMin, wantMax)
	}
}

// TestMonitorServiceProxyStatsEmpty pins that an empty rollup reshapes to
// a non-nil empty slice so the frontend never sees a null.
func TestMonitorServiceProxyStatsEmpty(t *testing.T) {
	fake := &fakeMonitorClient{stats: nil}
	svc := NewMonitorService(fake)
	got, err := svc.ProxyStats(0)
	if err != nil {
		t.Fatalf("ProxyStats: %v", err)
	}
	if got == nil {
		t.Fatalf("ProxyStats returned nil slice, want non-nil empty")
	}
	if len(got) != 0 {
		t.Fatalf("len = %d, want 0", len(got))
	}
}
