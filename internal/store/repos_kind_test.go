package store

import (
	"database/sql"
	"strings"
	"testing"

	"github.com/mrgeoffrich/bacio/internal/model"
)

// TestValidateRepoKindPath pins the (kind, path) truth table that the
// whole workspaces pivot rests on — the store-boundary guard every
// repos INSERT/UPDATE funnels through.
func TestValidateRepoKindPath(t *testing.T) {
	cases := []struct {
		name    string
		kind    model.RepoKind
		path    string
		wantErr bool
	}{
		{name: "git with a path is a linked repo", kind: model.RepoKindGit, path: "/tmp/checkout"},
		{name: "git without a path is a phantom", kind: model.RepoKindGit, path: ""},
		{name: "legacy empty kind reads as git", kind: "", path: "/tmp/checkout"},
		{name: "legacy empty kind, no path", kind: "", path: ""},
		{name: "workspace without a path is a workspace", kind: model.RepoKindWorkspace, path: ""},
		{name: "workspace with a path is impossible", kind: model.RepoKindWorkspace, path: "/tmp/checkout", wantErr: true},
		{name: "unknown kind is a caller bug", kind: model.RepoKind("folder"), path: "", wantErr: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := validateRepoKindPath(tc.kind, tc.path)
			if tc.wantErr && err == nil {
				t.Fatalf("validateRepoKindPath(%q, %q) = nil, want error", tc.kind, tc.path)
			}
			if !tc.wantErr && err != nil {
				t.Fatalf("validateRepoKindPath(%q, %q) = %v, want nil", tc.kind, tc.path, err)
			}
		})
	}
}

// TestCreateWorkspace covers the happy paths and every refusal of the
// workspace creator, including that it reuses AllocatePrefix rather
// than reimplementing prefix allocation.
func TestCreateWorkspace(t *testing.T) {
	cases := []struct {
		name       string
		prefix     string
		wsName     string
		wantPrefix string
		wantErr    bool
	}{
		{name: "explicit prefix is upper-cased", prefix: "ops1", wsName: "Ops", wantPrefix: "OPS1"},
		{name: "prefix is trimmed", prefix: "  WSPC  ", wsName: "Workspace", wantPrefix: "WSPC"},
		{name: "empty prefix is derived from the name", prefix: "", wsName: "Personal", wantPrefix: "PERS"},
		{name: "short name pads the derived prefix", prefix: "", wsName: "Ab", wantPrefix: "ABXX"},
		{name: "blank name is refused", prefix: "WSPC", wsName: "   ", wantErr: true},
		{name: "3-char prefix is refused", prefix: "WSP", wsName: "Ops", wantErr: true},
		{name: "non-alphanumeric prefix is refused", prefix: "WS-1", wsName: "Ops", wantErr: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := newTestStore(t)
			ws, err := s.CreateWorkspace(tc.prefix, tc.wsName)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("CreateWorkspace(%q, %q) = %+v, want error", tc.prefix, tc.wsName, ws)
				}
				return
			}
			if err != nil {
				t.Fatalf("CreateWorkspace(%q, %q): %v", tc.prefix, tc.wsName, err)
			}
			if ws.Prefix != tc.wantPrefix {
				t.Errorf("prefix = %q, want %q", ws.Prefix, tc.wantPrefix)
			}
			if ws.Kind != model.RepoKindWorkspace {
				t.Errorf("kind = %q, want %q", ws.Kind, model.RepoKindWorkspace)
			}
			if ws.Path != "" {
				t.Errorf("path = %q, want empty (a workspace has no working tree)", ws.Path)
			}
			if ws.RemoteURL != "" {
				t.Errorf("remote_url = %q, want empty", ws.RemoteURL)
			}
			if ws.UUID == "" {
				t.Error("uuid is empty — CreateWorkspace must mint one like every other creator")
			}
			if ws.Name != strings.TrimSpace(tc.wsName) {
				t.Errorf("name = %q, want %q", ws.Name, strings.TrimSpace(tc.wsName))
			}
			if ws.NextIssueNumber != 1 {
				t.Errorf("next_issue_number = %d, want 1", ws.NextIssueNumber)
			}
			if !ws.IsWorkspace() || ws.IsPhantom() || ws.HasWorkingTree() {
				t.Errorf("predicates disagree: IsWorkspace=%v IsPhantom=%v HasWorkingTree=%v, want true/false/false",
					ws.IsWorkspace(), ws.IsPhantom(), ws.HasWorkingTree())
			}
			// The row must read back the same way it was written.
			got, err := s.GetRepoByPrefix(tc.wantPrefix)
			if err != nil {
				t.Fatalf("GetRepoByPrefix(%q): %v", tc.wantPrefix, err)
			}
			if got.Kind != model.RepoKindWorkspace || got.Path != "" || got.ID != ws.ID {
				t.Errorf("round-trip mismatch: got %+v, want the created workspace %+v", got, ws)
			}
		})
	}
}

// TestCreateWorkspacePrefixNamespace pins that workspaces share the one
// prefix namespace with git repos: a clash is refused, and the derive
// path varies the prefix through AllocatePrefix instead of failing.
func TestCreateWorkspacePrefixNamespace(t *testing.T) {
	s := newTestStore(t)
	if _, err := s.CreateRepo("COLL", "collide", t.TempDir(), ""); err != nil {
		t.Fatalf("create git repo: %v", err)
	}
	if ws, err := s.CreateWorkspace("COLL", "Collide"); err == nil {
		t.Fatalf("CreateWorkspace with a taken prefix = %+v, want UNIQUE violation", ws)
	}
	// Derived allocation walks off the clash rather than erroring —
	// proof it goes through AllocatePrefix, not a private copy.
	ws, err := s.CreateWorkspace("", "Collide")
	if err != nil {
		t.Fatalf("CreateWorkspace with derived prefix: %v", err)
	}
	if ws.Prefix == "COLL" || len(ws.Prefix) != 4 {
		t.Errorf("derived prefix = %q, want a 4-char non-clashing prefix", ws.Prefix)
	}
}

// TestRepoKindRoundTrip walks every read path in this file and asserts
// each one materialises the kind discriminator. Before the pivot wired
// `kind` into repoCols/scanRepo, all of these returned "" and
// `bacio repo list -o json` emitted `"kind": ""`.
func TestRepoKindRoundTrip(t *testing.T) {
	s := newTestStore(t)
	repoPath := t.TempDir()
	git, err := s.CreateRepo("GITR", "git-repo", repoPath, "git@example.com:acme/git-repo.git")
	if err != nil {
		t.Fatalf("create git repo: %v", err)
	}
	phantom, err := s.CreatePhantomRepo("phantom-uuid", "PHAN", "phantom", "git@example.com:acme/phantom.git")
	if err != nil {
		t.Fatalf("create phantom: %v", err)
	}
	ws, err := s.CreateWorkspace("WSPC", "Workspace")
	if err != nil {
		t.Fatalf("create workspace: %v", err)
	}

	cases := []struct {
		name     string
		get      func() (*model.Repo, error)
		wantKind model.RepoKind
	}{
		{"GetRepoByID/git", func() (*model.Repo, error) { return s.GetRepoByID(git.ID) }, model.RepoKindGit},
		{"GetRepoByID/phantom", func() (*model.Repo, error) { return s.GetRepoByID(phantom.ID) }, model.RepoKindGit},
		{"GetRepoByID/workspace", func() (*model.Repo, error) { return s.GetRepoByID(ws.ID) }, model.RepoKindWorkspace},
		{"GetRepoByPrefix/git", func() (*model.Repo, error) { return s.GetRepoByPrefix("GITR") }, model.RepoKindGit},
		{"GetRepoByPrefix/workspace", func() (*model.Repo, error) { return s.GetRepoByPrefix("WSPC") }, model.RepoKindWorkspace},
		{"GetRepoByUUID/phantom", func() (*model.Repo, error) { return s.GetRepoByUUID("phantom-uuid") }, model.RepoKindGit},
		{"GetRepoByUUID/workspace", func() (*model.Repo, error) { return s.GetRepoByUUID(ws.UUID) }, model.RepoKindWorkspace},
		{"GetRepoByPath/git", func() (*model.Repo, error) { return s.GetRepoByPath(repoPath) }, model.RepoKindGit},
	}
	// An empty path must never resolve: it is shared by the phantom and
	// the workspace above, so a match would be arbitrary.
	if got, err := s.GetRepoByPath(""); err != ErrNotFound {
		t.Errorf("GetRepoByPath(\"\") = (%+v, %v), want ErrNotFound", got, err)
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := tc.get()
			if err != nil {
				t.Fatalf("read: %v", err)
			}
			if got.Kind != tc.wantKind {
				t.Errorf("kind = %q, want %q", got.Kind, tc.wantKind)
			}
		})
	}

	// The predicate table from model.Repo, exercised on scanned rows.
	for _, tc := range []struct {
		repo                                     *model.Repo
		workspace, phantom, hasTree, wantKindGit bool
	}{
		{repo: git, hasTree: true, wantKindGit: true},
		{repo: phantom, phantom: true, wantKindGit: true},
		{repo: ws, workspace: true},
	} {
		r, err := s.GetRepoByID(tc.repo.ID)
		if err != nil {
			t.Fatalf("re-read %s: %v", tc.repo.Prefix, err)
		}
		if r.IsWorkspace() != tc.workspace || r.IsPhantom() != tc.phantom || r.HasWorkingTree() != tc.hasTree {
			t.Errorf("%s predicates: IsWorkspace=%v IsPhantom=%v HasWorkingTree=%v, want %v/%v/%v",
				r.Prefix, r.IsWorkspace(), r.IsPhantom(), r.HasWorkingTree(), tc.workspace, tc.phantom, tc.hasTree)
		}
	}

	// ListRepos carries the discriminator too — it is the source for
	// `bacio repo list`, the REST /repos shape and the desktop boards.
	all, err := s.ListRepos()
	if err != nil {
		t.Fatalf("ListRepos: %v", err)
	}
	if len(all) != 3 {
		t.Fatalf("ListRepos returned %d repos, want 3", len(all))
	}
	for _, r := range all {
		if r.Kind == "" {
			t.Errorf("%s: ListRepos emitted an empty kind — repoCols/scanRepo are out of step", r.Prefix)
		}
		want := model.RepoKindGit
		if r.Prefix == "WSPC" {
			want = model.RepoKindWorkspace
		}
		if r.Kind != want {
			t.Errorf("%s: kind = %q, want %q", r.Prefix, r.Kind, want)
		}
	}
}

// TestRepoKindLegacyRowsReadAsGit covers the two ways a row can carry
// no explicit kind: a DB migrated from the pre-pivot schema (the column
// DEFAULT backfills 'git') and a row whose kind was written as the
// empty string. Both must scan as RepoKindGit — a blank discriminator
// would leak straight to `bacio repo list -o json` and make every
// legacy phantom look like a non-phantom to the predicates.
func TestRepoKindLegacyRowsReadAsGit(t *testing.T) {
	s, err := Open(newPrePivotFixtureDB(t))
	if err != nil {
		t.Fatalf("open pre-pivot DB: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })

	migrated, err := s.GetRepoByPrefix("OLD")
	if err != nil {
		t.Fatalf("read migrated repo: %v", err)
	}
	if migrated.Kind != model.RepoKindGit {
		t.Errorf("migrated repo kind = %q, want %q", migrated.Kind, model.RepoKindGit)
	}
	if migrated.IsWorkspace() || !migrated.HasWorkingTree() {
		t.Errorf("migrated repo predicates: IsWorkspace=%v HasWorkingTree=%v, want false/true",
			migrated.IsWorkspace(), migrated.HasWorkingTree())
	}

	// A row written with an explicit '' (no DEFAULT to lean on) is
	// normalised on the way out rather than surfacing blank.
	if _, err := s.DB.Exec(`UPDATE repos SET kind = '' WHERE prefix = 'OLD'`); err != nil {
		t.Fatalf("blank the kind column: %v", err)
	}
	blank, err := s.GetRepoByPrefix("OLD")
	if err != nil {
		t.Fatalf("re-read repo: %v", err)
	}
	if blank.Kind != model.RepoKindGit {
		t.Errorf("blank kind scanned as %q, want %q", blank.Kind, model.RepoKindGit)
	}
	// And it still lists as a git repo, not a workspace.
	gits, err := s.ListReposByKind(model.RepoKindGit)
	if err != nil {
		t.Fatalf("ListReposByKind(git): %v", err)
	}
	if len(gits) != 1 || gits[0].Prefix != "OLD" {
		t.Errorf("ListReposByKind(git) = %+v, want the one legacy row", gits)
	}
}

// TestWorkspacePathInvariant pins the store-boundary refusals: nothing
// in this file may produce a workspace row with a path.
func TestWorkspacePathInvariant(t *testing.T) {
	t.Run("insertRepo refuses a pathed workspace", func(t *testing.T) {
		s := newTestStore(t)
		if r, err := s.insertRepo("ws-uuid", "WSPC", "Workspace", model.RepoKindWorkspace, "/tmp/somewhere", ""); err == nil {
			t.Fatalf("insertRepo(workspace, path) = %+v, want refusal", r)
		}
		var n int
		if err := s.DB.QueryRow(`SELECT COUNT(*) FROM repos`).Scan(&n); err != nil {
			t.Fatalf("count repos: %v", err)
		}
		if n != 0 {
			t.Errorf("%d repo rows written despite the refusal, want 0", n)
		}
	})

	t.Run("insertRepo refuses an unknown kind", func(t *testing.T) {
		s := newTestStore(t)
		if r, err := s.insertRepo("x-uuid", "XXXX", "Mystery", model.RepoKind("folder"), "", ""); err == nil {
			t.Fatalf("insertRepo(unknown kind) = %+v, want refusal", r)
		}
	})

	t.Run("UpgradePhantomRepo refuses a workspace", func(t *testing.T) {
		s := newTestStore(t)
		ws, err := s.CreateWorkspace("WSPC", "Workspace")
		if err != nil {
			t.Fatalf("create workspace: %v", err)
		}
		err = s.UpgradePhantomRepo(ws.UUID, t.TempDir())
		if err == nil {
			t.Fatal("UpgradePhantomRepo on a workspace succeeded, want refusal (a workspace is pathless, not a phantom)")
		}
		if !strings.Contains(err.Error(), "workspace") {
			t.Errorf("refusal = %q, want a workspace-specific message rather than the git 'not phantom' one", err)
		}
		after, err := s.GetRepoByID(ws.ID)
		if err != nil {
			t.Fatalf("re-read workspace: %v", err)
		}
		if after.Path != "" || after.Kind != model.RepoKindWorkspace {
			t.Errorf("workspace mutated by the refused upgrade: kind=%q path=%q", after.Kind, after.Path)
		}
	})

	t.Run("UpgradePhantomRepo still upgrades a git phantom", func(t *testing.T) {
		s := newTestStore(t)
		if _, err := s.CreatePhantomRepo("phantom-uuid", "PHAN", "phantom", ""); err != nil {
			t.Fatalf("create phantom: %v", err)
		}
		dir := t.TempDir()
		if err := s.UpgradePhantomRepo("phantom-uuid", dir); err != nil {
			t.Fatalf("UpgradePhantomRepo: %v", err)
		}
		got, err := s.GetRepoByUUID("phantom-uuid")
		if err != nil {
			t.Fatalf("re-read: %v", err)
		}
		if got.Path != dir || got.Kind != model.RepoKindGit {
			t.Errorf("upgraded repo: kind=%q path=%q, want git + %q", got.Kind, got.Path, dir)
		}
	})

	t.Run("UpgradePhantomRepo still refuses a linked git repo", func(t *testing.T) {
		s := newTestStore(t)
		repo, err := s.CreateRepo("GITR", "git-repo", t.TempDir(), "")
		if err != nil {
			t.Fatalf("create repo: %v", err)
		}
		err = s.UpgradePhantomRepo(repo.UUID, t.TempDir())
		if err == nil || !strings.Contains(err.Error(), "not phantom") {
			t.Errorf("UpgradePhantomRepo on a linked repo = %v, want the 'not phantom' refusal", err)
		}
	})
}

// TestListReposByKind pins the sibling-verb filter (chosen over
// changing ListRepos's signature, which two interfaces declare).
func TestListReposByKind(t *testing.T) {
	s := newTestStore(t)
	if _, err := s.CreateRepo("GITR", "git-repo", t.TempDir(), ""); err != nil {
		t.Fatalf("create git repo: %v", err)
	}
	if _, err := s.CreatePhantomRepo("phantom-uuid", "PHAN", "phantom", ""); err != nil {
		t.Fatalf("create phantom: %v", err)
	}
	if _, err := s.CreateWorkspace("AWSP", "A workspace"); err != nil {
		t.Fatalf("create workspace A: %v", err)
	}
	if _, err := s.CreateWorkspace("ZWSP", "Z workspace"); err != nil {
		t.Fatalf("create workspace Z: %v", err)
	}

	cases := []struct {
		name        string
		kind        model.RepoKind
		wantPrefix  []string
		wantErr     bool
		wantOrdered bool
	}{
		{name: "git excludes workspaces (phantoms included)", kind: model.RepoKindGit, wantPrefix: []string{"GITR", "PHAN"}, wantOrdered: true},
		{name: "empty kind means git", kind: "", wantPrefix: []string{"GITR", "PHAN"}, wantOrdered: true},
		{name: "workspaces only", kind: model.RepoKindWorkspace, wantPrefix: []string{"AWSP", "ZWSP"}, wantOrdered: true},
		{name: "unknown kind errors", kind: model.RepoKind("folder"), wantErr: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := s.ListReposByKind(tc.kind)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("ListReposByKind(%q) = %+v, want error", tc.kind, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("ListReposByKind(%q): %v", tc.kind, err)
			}
			var prefixes []string
			for _, r := range got {
				prefixes = append(prefixes, r.Prefix)
			}
			if strings.Join(prefixes, ",") != strings.Join(tc.wantPrefix, ",") {
				t.Errorf("prefixes = %v, want %v (ordered by prefix)", prefixes, tc.wantPrefix)
			}
		})
	}

	// The two filters must partition ListRepos exactly.
	all, err := s.ListRepos()
	if err != nil {
		t.Fatalf("ListRepos: %v", err)
	}
	gits, err := s.ListReposByKind(model.RepoKindGit)
	if err != nil {
		t.Fatalf("ListReposByKind(git): %v", err)
	}
	workspaces, err := s.ListReposByKind(model.RepoKindWorkspace)
	if err != nil {
		t.Fatalf("ListReposByKind(workspace): %v", err)
	}
	if len(gits)+len(workspaces) != len(all) {
		t.Errorf("partition mismatch: %d git + %d workspace != %d total", len(gits), len(workspaces), len(all))
	}
}

// TestRepoCascadeCountsCoverEveryRepoScopedTable is the self-maintaining
// half of the honesty guarantee: it asks SQLite which tables actually
// carry an FK to repos(id) and fails if one of them has no field in
// RepoCascadeCounts. Add a repo-scoped table without a counter and this
// test tells you, rather than `bacio repo rm` quietly under-reporting
// what it is about to destroy.
func TestRepoCascadeCountsCoverEveryRepoScopedTable(t *testing.T) {
	s := newTestStore(t)

	// Counted by RepoCascadeCountsForID via a direct repo_id predicate.
	counted := map[string]bool{
		"issues":           true,
		"features":         true,
		"documents":        true,
		"doc_folders":      true,
		"kanban_columns":   true,
		"tui_settings":     true,
		"repo_settings":    true,
		"agent_sessions":   true,
		"agent_dispatches": true,
		"agent_channels":   true,
		"user_messages":    true,
		"notifications":    true,
	}

	rows, err := s.DB.Query(`SELECT name FROM sqlite_master WHERE type = 'table' AND name NOT LIKE 'sqlite_%' ORDER BY name`)
	if err != nil {
		t.Fatalf("list tables: %v", err)
	}
	var tables []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			rows.Close()
			t.Fatalf("scan table name: %v", err)
		}
		tables = append(tables, name)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate tables: %v", err)
	}

	found := map[string]bool{}
	for _, table := range tables {
		fks, err := s.DB.Query(`SELECT "table", "on_delete" FROM pragma_foreign_key_list(?)`, table)
		if err != nil {
			t.Fatalf("foreign_key_list(%s): %v", table, err)
		}
		for fks.Next() {
			var parent, onDelete string
			if err := fks.Scan(&parent, &onDelete); err != nil {
				fks.Close()
				t.Fatalf("scan fk for %s: %v", table, err)
			}
			if parent != "repos" {
				continue
			}
			found[table] = true
			if !counted[table] {
				t.Errorf("table %q references repos(id) ON DELETE %s but has no field in RepoCascadeCounts — `bacio repo rm` would under-report the blast radius", table, onDelete)
			}
		}
		fks.Close()
		if err := fks.Err(); err != nil {
			t.Fatalf("iterate fks for %s: %v", table, err)
		}
	}

	// The other direction, which also keeps the loop above from
	// passing vacuously if the pragma ever stops returning rows: every
	// table we claim to count must really be repo-scoped.
	for table := range counted {
		if !found[table] {
			t.Errorf("RepoCascadeCounts counts %q, but no FK from %q to repos(id) was found in the live schema", table, table)
		}
	}
}

// TestRepoCascadeCountsNewTables seeds one row in each table the
// pre-pivot preview silently ignored and asserts the counts report
// them — and that they are scoped to the target repo, not global.
//
// Rows are inserted with raw SQL on purpose: this test is about the
// counting, not about whichever store API happens to own each table,
// and it must not go stale when those APIs change.
func TestRepoCascadeCountsNewTables(t *testing.T) {
	s := newTestStore(t)
	target, err := s.CreateRepo("KILL", "kill-me", t.TempDir(), "")
	if err != nil {
		t.Fatalf("create target repo: %v", err)
	}
	keep, err := s.CreateRepo("KEEP", "keep-me", t.TempDir(), "")
	if err != nil {
		t.Fatalf("create keep repo: %v", err)
	}

	seed := func(repoID int64, tag string) {
		t.Helper()
		exec := func(query string, args ...any) sql.Result {
			t.Helper()
			res, err := s.DB.Exec(query, args...)
			if err != nil {
				t.Fatalf("seed %s (%s): %v", query, tag, err)
			}
			return res
		}
		exec(`INSERT INTO doc_folders (uuid, repo_id, name) VALUES (?, ?, 'Design')`, "folder-"+tag, repoID)
		exec(`INSERT INTO kanban_columns (uuid, repo_id, name, position) VALUES (?, ?, 'Doing', 0)`, "column-"+tag, repoID)
		exec(`INSERT INTO repo_settings (repo_id) VALUES (?)`, repoID)
		sess := exec(`INSERT INTO agent_sessions (session_id, repo_id, actor) VALUES (?, ?, 'tester')`, "session-"+tag, repoID)
		sessPK, err := sess.LastInsertId()
		if err != nil {
			t.Fatalf("session id (%s): %v", tag, err)
		}
		exec(`INSERT INTO agent_dispatches (repo_id, created_by) VALUES (?, 'tester')`, repoID)
		exec(`INSERT INTO agent_channels (repo_id, host, claude_pid) VALUES (?, ?, 4242)`, repoID, "host-"+tag)
		exec(`INSERT INTO user_messages (session_pk, repo_id, body, created_by) VALUES (?, ?, 'ping', 'tester')`, sessPK, repoID)
		exec(`INSERT INTO notifications (repo_id, body, source_agent) VALUES (?, 'done', 'tester')`, repoID)
	}
	seed(target.ID, "target")
	seed(keep.ID, "keep")

	counts, err := s.RepoCascadeCountsForID(target.ID)
	if err != nil {
		t.Fatalf("cascade counts: %v", err)
	}
	want := RepoCascadeCounts{
		DocFolders: 1, KanbanColumns: 1, RepoSettings: 1,
		AgentSessions: 1, AgentDispatches: 1, AgentChannels: 1,
		UserMessages: 1, Notifications: 1,
	}
	if counts != want {
		t.Fatalf("cascade counts:\n got %+v\nwant %+v", counts, want)
	}

	// The cascade really does take them: delete and re-count.
	if err := s.DeleteRepo(target.ID); err != nil {
		t.Fatalf("delete repo: %v", err)
	}
	after, err := s.RepoCascadeCountsForID(target.ID)
	if err != nil {
		t.Fatalf("cascade counts after delete: %v", err)
	}
	if after != (RepoCascadeCounts{}) {
		t.Errorf("rows survived the cascade: %+v", after)
	}
	// The neighbouring repo's rows are untouched.
	keepCounts, err := s.RepoCascadeCountsForID(keep.ID)
	if err != nil {
		t.Fatalf("keep cascade counts: %v", err)
	}
	if keepCounts != want {
		t.Errorf("keep repo counts:\n got %+v\nwant %+v", keepCounts, want)
	}
}
