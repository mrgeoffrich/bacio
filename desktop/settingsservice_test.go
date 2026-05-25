package main

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/mrgeoffrich/bacio/internal/client"
)

// fakeSyncClient is a minimal client.Client for SettingsService sync
// tests — embeds the interface (so unused methods exist but panic if
// called) and implements only the touches the tests need. Same pattern
// as fakeBoardClient in boardservice_test.go.
type fakeSyncClient struct {
	client.Client
	bgEnabled    bool
	bgEnabledErr error
	bgSetErr     error
	registry     *client.SyncRegistry
	registryErr  error
	// lastSet records the most recent SetSyncBackgroundEnabled value so
	// tests can assert the round-trip.
	lastSet bool
}

func (f *fakeSyncClient) GetSyncBackgroundEnabled(context.Context) (bool, error) {
	return f.bgEnabled, f.bgEnabledErr
}

func (f *fakeSyncClient) SetSyncBackgroundEnabled(_ context.Context, value, _ bool) (bool, error) {
	f.lastSet = value
	if f.bgSetErr != nil {
		return false, f.bgSetErr
	}
	f.bgEnabled = value
	return value, nil
}

func (f *fakeSyncClient) SyncRegistry(context.Context) (*client.SyncRegistry, error) {
	if f.registryErr != nil {
		return nil, f.registryErr
	}
	return f.registry, nil
}

// TestSettingsService_SyncPreferencesRoundTrip exercises the
// Get→Set→Get round trip, asserting the DTO field reshape and that
// Set's return value is sourced from the client (not from the input).
func TestSettingsService_SyncPreferencesRoundTrip(t *testing.T) {
	fake := &fakeSyncClient{bgEnabled: false}
	s := NewSettingsService(fake)

	// Initial state — off.
	got, err := s.GetSyncPreferences()
	if err != nil {
		t.Fatalf("GetSyncPreferences: %v", err)
	}
	if got.BackgroundEnabled {
		t.Fatalf("expected off, got %+v", got)
	}

	// Flip on.
	got, err = s.SetSyncPreferences(true)
	if err != nil {
		t.Fatalf("SetSyncPreferences(true): %v", err)
	}
	if !got.BackgroundEnabled {
		t.Fatalf("expected on after Set, got %+v", got)
	}
	if !fake.lastSet {
		t.Fatalf("expected client.lastSet=true, got %v", fake.lastSet)
	}

	// Round-trip via Get.
	got, err = s.GetSyncPreferences()
	if err != nil {
		t.Fatalf("GetSyncPreferences after Set: %v", err)
	}
	if !got.BackgroundEnabled {
		t.Fatalf("expected on, got %+v", got)
	}

	// Flip off again.
	got, err = s.SetSyncPreferences(false)
	if err != nil {
		t.Fatalf("SetSyncPreferences(false): %v", err)
	}
	if got.BackgroundEnabled {
		t.Fatalf("expected off after Set, got %+v", got)
	}
}

// TestSettingsService_SyncPreferences_GetErrorPropagates surfaces a
// client-side read failure as an error from the Wails method (rather
// than silently returning zero), so the desktop's reportError path
// triggers and the operator sees the modal.
func TestSettingsService_SyncPreferences_GetErrorPropagates(t *testing.T) {
	want := errors.New("boom")
	s := NewSettingsService(&fakeSyncClient{bgEnabledErr: want})
	if _, err := s.GetSyncPreferences(); !errors.Is(err, want) {
		t.Fatalf("expected sentinel error, got %v", err)
	}
}

// TestSettingsService_GetSyncRegistry pins the DTO reshape: snake-case
// client.SyncRegistry → camelCase SyncRegistryDTO, with timestamps
// formatted RFC3339 and the inner project / nested status fields
// preserved verbatim.
func TestSettingsService_GetSyncRegistry(t *testing.T) {
	cloned := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	last := time.Date(2026, 5, 6, 7, 8, 9, 0, time.UTC)
	errMsg := "git push failed"
	reg := &client.SyncRegistry{
		SyncRepos: []client.SyncRepoEntry{
			{
				RemoteURL:  "git@example.com:bacio-sync.git",
				Label:      "bacio-sync",
				LocalPath:  "/Users/x/.bacio/sync/bacio-sync",
				ClonedAt:   cloned,
				LastSyncAt: &last,
				LastError:  &errMsg,
				InProgress: true,
				Projects: []client.MemberProject{
					{Prefix: "BACI", Name: "bacio", UUID: "uuid-1", Status: "linked"},
					{Prefix: "OTHR", Name: "other", UUID: "uuid-2", Status: "phantom"},
					{Prefix: "GONE", Name: "", UUID: "", Status: "absent"},
				},
			},
		},
		UnsyncedProjects: []client.UnsyncedProject{
			{Prefix: "MISC", Name: "misc", UUID: "uuid-3", Path: "/Users/x/repos/misc"},
		},
	}
	s := NewSettingsService(&fakeSyncClient{registry: reg})

	got, err := s.GetSyncRegistry()
	if err != nil {
		t.Fatalf("GetSyncRegistry: %v", err)
	}

	if len(got.SyncRepos) != 1 {
		t.Fatalf("expected 1 sync repo, got %d", len(got.SyncRepos))
	}
	entry := got.SyncRepos[0]
	if entry.RemoteURL != "git@example.com:bacio-sync.git" {
		t.Errorf("RemoteURL=%q", entry.RemoteURL)
	}
	if entry.Label != "bacio-sync" {
		t.Errorf("Label=%q", entry.Label)
	}
	if entry.LocalPath != "/Users/x/.bacio/sync/bacio-sync" {
		t.Errorf("LocalPath=%q", entry.LocalPath)
	}
	if entry.ClonedAt != "2026-01-02T03:04:05Z" {
		t.Errorf("ClonedAt=%q", entry.ClonedAt)
	}
	if entry.LastSyncAt != "2026-05-06T07:08:09Z" {
		t.Errorf("LastSyncAt=%q", entry.LastSyncAt)
	}
	if entry.LastError != errMsg {
		t.Errorf("LastError=%q", entry.LastError)
	}
	if !entry.InProgress {
		t.Errorf("InProgress=false")
	}
	if len(entry.Projects) != 3 {
		t.Fatalf("expected 3 projects, got %d", len(entry.Projects))
	}
	if entry.Projects[0].Status != "linked" || entry.Projects[1].Status != "phantom" || entry.Projects[2].Status != "absent" {
		t.Errorf("project statuses: %+v", entry.Projects)
	}
	if entry.Projects[0].UUID != "uuid-1" {
		t.Errorf("Projects[0].UUID=%q", entry.Projects[0].UUID)
	}

	if len(got.UnsyncedProjects) != 1 {
		t.Fatalf("expected 1 unsynced project, got %d", len(got.UnsyncedProjects))
	}
	un := got.UnsyncedProjects[0]
	if un.Prefix != "MISC" || un.Name != "misc" || un.UUID != "uuid-3" || un.Path != "/Users/x/repos/misc" {
		t.Errorf("unsynced project: %+v", un)
	}
}

// TestSettingsService_GetSyncRegistry_NilOptionals verifies the empty
// path: a registry entry with no last-sync timestamp and no last error
// flattens to empty strings (omitempty-friendly on the wire). Mirrors
// the localClient's tolerance for never-synced rows.
func TestSettingsService_GetSyncRegistry_NilOptionals(t *testing.T) {
	cloned := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	reg := &client.SyncRegistry{
		SyncRepos: []client.SyncRepoEntry{
			{
				RemoteURL:  "git@example.com:bacio-sync.git",
				Label:      "bacio-sync",
				LocalPath:  "/Users/x/.bacio/sync/bacio-sync",
				ClonedAt:   cloned,
				LastSyncAt: nil,
				LastError:  nil,
				InProgress: false,
				Projects:   []client.MemberProject{},
			},
		},
		UnsyncedProjects: []client.UnsyncedProject{},
	}
	s := NewSettingsService(&fakeSyncClient{registry: reg})

	got, err := s.GetSyncRegistry()
	if err != nil {
		t.Fatalf("GetSyncRegistry: %v", err)
	}
	if len(got.SyncRepos) != 1 {
		t.Fatalf("expected 1 sync repo, got %d", len(got.SyncRepos))
	}
	entry := got.SyncRepos[0]
	if entry.LastSyncAt != "" {
		t.Errorf("expected empty LastSyncAt, got %q", entry.LastSyncAt)
	}
	if entry.LastError != "" {
		t.Errorf("expected empty LastError, got %q", entry.LastError)
	}
	if len(entry.Projects) != 0 {
		t.Errorf("expected empty Projects, got %+v", entry.Projects)
	}
	if len(got.UnsyncedProjects) != 0 {
		t.Errorf("expected empty UnsyncedProjects, got %+v", got.UnsyncedProjects)
	}
}

// TestSettingsService_GetSyncRegistry_ErrorPropagates surfaces a
// client-side read failure to the Wails caller.
func TestSettingsService_GetSyncRegistry_ErrorPropagates(t *testing.T) {
	want := errors.New("nope")
	s := NewSettingsService(&fakeSyncClient{registryErr: want})
	if _, err := s.GetSyncRegistry(); !errors.Is(err, want) {
		t.Fatalf("expected sentinel error, got %v", err)
	}
}
