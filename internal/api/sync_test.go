package api_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/mrgeoffrich/bacio/internal/api"
	"github.com/mrgeoffrich/bacio/internal/store"
	bsync "github.com/mrgeoffrich/bacio/internal/sync"
)

// syncStatus mirrors api.SyncStatusOut for decoding test responses.
type syncStatus struct {
	Prefix            string  `json:"prefix"`
	Configured        bool    `json:"configured"`
	MirroredBy        string  `json:"mirrored_by"`
	BackgroundEnabled bool    `json:"background_enabled"`
	InProgress        bool    `json:"in_progress"`
	LastError         *string `json:"last_error"`
	Remote            string  `json:"remote"`
}

// TestSyncStatusListEmptyConfig: GET /sync over a DB whose one repo has
// no .bacio/config.yaml reports configured:false but still echoes the
// global background_enabled toggle (default true).
func TestSyncStatusList(t *testing.T) {
	ts, s := newTestAPI(t, api.Options{})
	repo := seedRepo(t, s)

	resp, body := apiGet(t, ts.URL+"/sync")
	if resp.StatusCode != 200 {
		t.Fatalf("status: %d body: %s", resp.StatusCode, body)
	}
	var out []syncStatus
	if err := json.Unmarshal(body, &out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(out) != 1 {
		t.Fatalf("expected 1 status, got %d", len(out))
	}
	st := out[0]
	if st.Prefix != repo.Prefix {
		t.Fatalf("prefix = %q, want %q", st.Prefix, repo.Prefix)
	}
	if st.Configured {
		t.Fatal("a repo with no .bacio/config.yaml should report configured:false")
	}
	if !st.BackgroundEnabled {
		t.Fatal("background_enabled should default to true (opt-out)")
	}
}

// TestSyncStatusGetPerRepo: GET /repos/{prefix}/sync returns the same
// per-repo shape.
func TestSyncStatusGetPerRepo(t *testing.T) {
	ts, s := newTestAPI(t, api.Options{})
	repo := seedRepo(t, s)

	resp, body := apiGet(t, ts.URL+"/repos/"+repo.Prefix+"/sync")
	if resp.StatusCode != 200 {
		t.Fatalf("status: %d body: %s", resp.StatusCode, body)
	}
	var st syncStatus
	if err := json.Unmarshal(body, &st); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if st.Prefix != repo.Prefix || st.Configured {
		t.Fatalf("got %+v, want prefix=%s configured=false", st, repo.Prefix)
	}
}

// TestSyncStatusGetUnknownRepo: an unknown prefix 404s.
func TestSyncStatusGetUnknownRepo(t *testing.T) {
	ts, _ := newTestAPI(t, api.Options{})
	resp, _ := apiGet(t, ts.URL+"/repos/ZZZZ/sync")
	if resp.StatusCode != 404 {
		t.Fatalf("status = %d, want 404", resp.StatusCode)
	}
}

// TestSyncPreferencesRoundTrip: PUT then GET /settings/sync-preferences,
// confirming the store reflects the write and an audit row is recorded.
func TestSyncPreferencesRoundTrip(t *testing.T) {
	ts, s := newTestAPI(t, api.Options{})
	// Default is true; flip to false.
	resp, body := apiReq(t, "PUT", ts.URL+"/settings/sync-preferences",
		map[string]any{"background_enabled": false},
		map[string]string{"X-Actor": "agent-alice"})
	if resp.StatusCode != 200 {
		t.Fatalf("PUT status: %d body: %s", resp.StatusCode, body)
	}
	var out struct {
		BackgroundEnabled bool `json:"background_enabled"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if out.BackgroundEnabled {
		t.Fatal("PUT response did not echo false")
	}
	resp2, body2 := apiGet(t, ts.URL+"/settings/sync-preferences")
	if resp2.StatusCode != 200 {
		t.Fatalf("GET status: %d body: %s", resp2.StatusCode, body2)
	}
	_ = json.Unmarshal(body2, &out)
	if out.BackgroundEnabled {
		t.Fatal("GET after PUT: background_enabled is true, want false")
	}
	stored, err := s.GetSyncBackgroundEnabled()
	if err != nil || stored {
		t.Fatalf("store reads %v err=%v, want false", stored, err)
	}
	assertHistoryOps(t, s, []string{"sync_pref.update"})
}

// TestSyncPreferencesDryRun: ?dry_run=true projects the value without
// writing.
func TestSyncPreferencesDryRun(t *testing.T) {
	ts, s := newTestAPI(t, api.Options{})
	resp, _ := apiReq(t, "PUT", ts.URL+"/settings/sync-preferences?dry_run=true",
		map[string]any{"background_enabled": false}, nil)
	if resp.StatusCode != 200 {
		t.Fatalf("dry-run status = %d", resp.StatusCode)
	}
	// The store must still read the default (true) — no write happened.
	if v, _ := s.GetSyncBackgroundEnabled(); !v {
		t.Fatal("dry-run must not write the toggle")
	}
}

// TestSyncPreferencesUnknownField: an unknown field is rejected.
func TestSyncPreferencesUnknownField(t *testing.T) {
	ts, _ := newTestAPI(t, api.Options{})
	resp, _ := apiReq(t, "PUT", ts.URL+"/settings/sync-preferences",
		map[string]any{"bogus": true}, nil)
	if resp.StatusCode != 400 {
		t.Fatalf("status = %d, want 400 for unknown field", resp.StatusCode)
	}
}

// ---------- GET /sync/repos (BACI-107) ----------

// syncRegistry mirrors api.SyncRegistryOut for decoding test responses.
// Slim copies — only the fields the test actually inspects — so the
// test doesn't break on additive wire changes.
type syncRegistry struct {
	SyncRepos        []syncRepoEntry        `json:"sync_repos"`
	UnsyncedProjects []syncUnsyncedProjects `json:"unsynced_projects"`
}

type syncRepoEntry struct {
	RemoteURL  string              `json:"remote_url"`
	Label      string              `json:"label"`
	LocalPath  string              `json:"local_path"`
	LastSyncAt *time.Time          `json:"last_sync_at,omitempty"`
	LastError  *string             `json:"last_error,omitempty"`
	InProgress bool                `json:"in_progress"`
	Projects   []syncMemberProject `json:"projects"`
}

type syncMemberProject struct {
	Prefix string `json:"prefix"`
	Name   string `json:"name"`
	UUID   string `json:"uuid,omitempty"`
	Status string `json:"status"`
}

type syncUnsyncedProjects struct {
	Prefix string `json:"prefix"`
	Name   string `json:"name"`
	UUID   string `json:"uuid"`
	Path   string `json:"path"`
}

// writeProjectSyncConfig drops a .bacio/config.yaml at projectPath with
// the given remote — the minimal setup an "I belong to that sync repo"
// project repo carries on disk. Uses bsync.WriteProjectConfig so the
// emitted format is byte-stable with what `bacio sync init` writes.
func writeProjectSyncConfig(t *testing.T, projectPath, remote string) {
	t.Helper()
	if err := bsync.WriteProjectConfig(projectPath, bsync.ProjectConfig{
		Sync: bsync.ProjectSync{Remote: remote},
	}); err != nil {
		t.Fatalf("WriteProjectConfig: %v", err)
	}
}

// mkSyncCloneLayout writes a sync-repo-style `<root>/repos/<prefix>/`
// directory for each prefix. Mirrors the helper in
// internal/sync/membership_test.go but local to this package.
func mkSyncCloneLayout(t *testing.T, prefixes ...string) string {
	t.Helper()
	root := t.TempDir()
	for _, p := range prefixes {
		if err := os.MkdirAll(filepath.Join(root, "repos", p), 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", p, err)
		}
	}
	return root
}

// TestSyncRegistryListEmpty: no sync_remotes rows, one repo with no
// .bacio/config.yaml — empty sync_repos, the lone repo turns up in
// unsynced_projects.
func TestSyncRegistryListEmpty(t *testing.T) {
	ts, s := newTestAPI(t, api.Options{})
	repo := seedRepo(t, s)

	resp, body := apiGet(t, ts.URL+"/sync/repos")
	if resp.StatusCode != 200 {
		t.Fatalf("status: %d body: %s", resp.StatusCode, body)
	}
	var out syncRegistry
	if err := json.Unmarshal(body, &out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(out.SyncRepos) != 0 {
		t.Fatalf("sync_repos: want 0, got %d", len(out.SyncRepos))
	}
	if len(out.UnsyncedProjects) != 1 {
		t.Fatalf("unsynced_projects: want 1, got %d", len(out.UnsyncedProjects))
	}
	up := out.UnsyncedProjects[0]
	if up.Prefix != repo.Prefix || up.Name != repo.Name || up.UUID != repo.UUID || up.Path != repo.Path {
		t.Fatalf("unsynced[0] = %+v, want prefix/name/uuid/path of %+v", up, repo)
	}
}

// TestSyncRegistryListWithRemote: seed a sync_remotes row pointing at
// a temp directory containing repos/MINI (linked locally) and
// repos/PHAN (a phantom row in the DB). Asserts the registry surfaces
// both, the label is derived from the URL, and unsynced_projects
// excludes both prefixes.
func TestSyncRegistryListWithRemote(t *testing.T) {
	ts, s := newTestAPI(t, api.Options{})

	// MINI is the default seedRepo prefix — linked locally with a
	// real path. PHAN is a phantom (path == "").
	repo := seedRepo(t, s)
	phantom, err := s.CreatePhantomRepo(uuidFor("phantom-PHAN"), "PHAN", "phantom-name", "")
	if err != nil {
		t.Fatalf("CreatePhantomRepo: %v", err)
	}

	// Lay out the sync clone with both prefixes.
	clone := mkSyncCloneLayout(t, repo.Prefix, phantom.Prefix)
	remote := "git@example.com:bacio/team-sync.git"
	if err := s.UpsertSyncRemote(remote, clone); err != nil {
		t.Fatalf("UpsertSyncRemote: %v", err)
	}

	resp, body := apiGet(t, ts.URL+"/sync/repos")
	if resp.StatusCode != 200 {
		t.Fatalf("status: %d body: %s", resp.StatusCode, body)
	}
	var out syncRegistry
	if err := json.Unmarshal(body, &out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(out.SyncRepos) != 1 {
		t.Fatalf("sync_repos: want 1, got %d", len(out.SyncRepos))
	}
	entry := out.SyncRepos[0]
	if entry.RemoteURL != remote {
		t.Fatalf("remote_url = %q, want %q", entry.RemoteURL, remote)
	}
	// DeriveSyncLabel strips the trailing path segment + .git — for
	// the URL above that's "team-sync".
	if entry.Label != "team-sync" {
		t.Fatalf("label = %q, want %q", entry.Label, "team-sync")
	}
	if entry.LocalPath != clone {
		t.Fatalf("local_path = %q, want %q", entry.LocalPath, clone)
	}
	if len(entry.Projects) != 2 {
		t.Fatalf("projects: want 2, got %d (%+v)", len(entry.Projects), entry.Projects)
	}
	gotByPrefix := map[string]syncMemberProject{}
	for _, p := range entry.Projects {
		gotByPrefix[p.Prefix] = p
	}
	if mini, ok := gotByPrefix[repo.Prefix]; !ok || mini.Status != "linked" {
		t.Fatalf("MINI member = %+v, want status=linked", mini)
	}
	if phan, ok := gotByPrefix[phantom.Prefix]; !ok || phan.Status != "phantom" {
		t.Fatalf("PHAN member = %+v, want status=phantom", phan)
	}
	// MINI's repo row has a name + uuid; assert they're echoed.
	if mini := gotByPrefix[repo.Prefix]; mini.Name != repo.Name || mini.UUID != repo.UUID {
		t.Fatalf("MINI member: name=%q uuid=%q, want %q / %q", mini.Name, mini.UUID, repo.Name, repo.UUID)
	}
	// Both prefixes are surfaced under the sync repo, so neither
	// should land in unsynced_projects.
	for _, up := range out.UnsyncedProjects {
		if up.Prefix == repo.Prefix || up.Prefix == phantom.Prefix {
			t.Fatalf("unsynced contains surfaced prefix %q: %+v", up.Prefix, out.UnsyncedProjects)
		}
	}
}

// TestSyncRegistryListAbsentMember: the sync repo carries a prefix the
// local DB has never seen — emitted as status=absent with empty
// uuid/name.
func TestSyncRegistryListAbsentMember(t *testing.T) {
	ts, s := newTestAPI(t, api.Options{})
	// seedRepo here keeps unsynced_projects non-empty without
	// affecting the absent-member assertion.
	_ = seedRepo(t, s)

	clone := mkSyncCloneLayout(t, "UNKN")
	remote := "https://example.com/team/registry.git"
	if err := s.UpsertSyncRemote(remote, clone); err != nil {
		t.Fatalf("UpsertSyncRemote: %v", err)
	}

	resp, body := apiGet(t, ts.URL+"/sync/repos")
	if resp.StatusCode != 200 {
		t.Fatalf("status: %d body: %s", resp.StatusCode, body)
	}
	var out syncRegistry
	if err := json.Unmarshal(body, &out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(out.SyncRepos) != 1 || len(out.SyncRepos[0].Projects) != 1 {
		t.Fatalf("expected one sync repo with one project, got %+v", out.SyncRepos)
	}
	p := out.SyncRepos[0].Projects[0]
	if p.Prefix != "UNKN" || p.Status != "absent" || p.Name != "" || p.UUID != "" {
		t.Fatalf("absent member = %+v, want {UNKN absent \"\" \"\"}", p)
	}
}

// TestSyncRegistryListMissingClone: sync_remotes.local_path doesn't
// exist on disk — the entry still appears but with an empty Projects
// slice and no 5xx.
func TestSyncRegistryListMissingClone(t *testing.T) {
	ts, s := newTestAPI(t, api.Options{})
	remote := "git@example.com:bacio/missing.git"
	// Local path that definitely doesn't exist.
	if err := s.UpsertSyncRemote(remote, filepath.Join(t.TempDir(), "nope")); err != nil {
		t.Fatalf("UpsertSyncRemote: %v", err)
	}

	resp, body := apiGet(t, ts.URL+"/sync/repos")
	if resp.StatusCode != 200 {
		t.Fatalf("status: %d body: %s", resp.StatusCode, body)
	}
	var out syncRegistry
	if err := json.Unmarshal(body, &out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(out.SyncRepos) != 1 {
		t.Fatalf("sync_repos: want 1, got %d", len(out.SyncRepos))
	}
	entry := out.SyncRepos[0]
	if entry.RemoteURL != remote {
		t.Fatalf("remote_url = %q, want %q", entry.RemoteURL, remote)
	}
	if entry.Projects == nil {
		t.Fatalf("projects: want non-nil empty slice, got nil (would marshal as null)")
	}
	if len(entry.Projects) != 0 {
		t.Fatalf("projects: want [], got %+v", entry.Projects)
	}
}

// TestSyncRegistryListConfiguredProjectNotUnsynced: a repo with a
// valid .bacio/config.yaml pointing at the registry's URL must NOT
// appear in unsynced_projects, even when the sync clone isn't laid
// out on disk (i.e. the registry doesn't see the prefix yet but the
// config does point at the registered remote).
func TestSyncRegistryListConfiguredProjectNotUnsynced(t *testing.T) {
	ts, s := newTestAPI(t, api.Options{})
	repo := seedRepo(t, s)

	remote := "https://example.com/team/configured.git"
	clone := t.TempDir() // empty clone — no repos/ subdir
	if err := s.UpsertSyncRemote(remote, clone); err != nil {
		t.Fatalf("UpsertSyncRemote: %v", err)
	}
	writeProjectSyncConfig(t, repo.Path, remote)

	resp, body := apiGet(t, ts.URL+"/sync/repos")
	if resp.StatusCode != 200 {
		t.Fatalf("status: %d body: %s", resp.StatusCode, body)
	}
	var out syncRegistry
	if err := json.Unmarshal(body, &out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	for _, up := range out.UnsyncedProjects {
		if up.Prefix == repo.Prefix {
			t.Fatalf("configured repo appears under unsynced_projects: %+v", up)
		}
	}
}

// TestSyncStatusReportsMirroredWithoutOwnConfig pins BACI-376: the
// export is whole-DB, so a repo with no .bacio/config.yaml of its own
// is still mirrored into any sync repo on this machine that carries its
// prefix. The status must say so — reporting only configured:false is
// what made the Pipeline badge disagree with the Sync settings screen,
// which lists the same repo as a linked member of that sync repo.
func TestSyncStatusReportsMirroredWithoutOwnConfig(t *testing.T) {
	ts, s := newTestAPI(t, api.Options{})
	repo := seedRepo(t, s) // no .bacio/config.yaml written

	clone := mkSyncCloneLayout(t, repo.Prefix)
	if err := s.UpsertSyncRemote("git@example.com:bacio/team-sync.git", clone); err != nil {
		t.Fatalf("UpsertSyncRemote: %v", err)
	}

	resp, body := apiGet(t, ts.URL+"/repos/"+repo.Prefix+"/sync")
	if resp.StatusCode != 200 {
		t.Fatalf("status: %d body: %s", resp.StatusCode, body)
	}
	var st syncStatus
	if err := json.Unmarshal(body, &st); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if st.Configured {
		t.Fatal("configured = true, want false (the repo has no sync remote of its own)")
	}
	if st.MirroredBy != "team-sync" {
		t.Fatalf("mirrored_by = %q, want team-sync", st.MirroredBy)
	}
}

// TestSyncStatusUnmirroredRepoStaysEmpty: with no sync remotes at all,
// mirrored_by stays empty — the badge's "not set up" state has to remain
// reachable.
func TestSyncStatusUnmirroredRepoStaysEmpty(t *testing.T) {
	ts, s := newTestAPI(t, api.Options{})
	repo := seedRepo(t, s)

	resp, body := apiGet(t, ts.URL+"/repos/"+repo.Prefix+"/sync")
	if resp.StatusCode != 200 {
		t.Fatalf("status: %d body: %s", resp.StatusCode, body)
	}
	var st syncStatus
	if err := json.Unmarshal(body, &st); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if st.MirroredBy != "" {
		t.Fatalf("mirrored_by = %q, want empty", st.MirroredBy)
	}
}

// Compile-time sanity: ErrNotFound stays the sentinel both packages
// use so the test file's imports aren't elided.
var _ = store.ErrNotFound
