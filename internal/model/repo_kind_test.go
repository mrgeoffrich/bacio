package model

import "testing"

// TestRepoPredicates pins the (Kind, Path) truth table documented above
// the predicates. `Path == ""` used to mean "phantom" and nothing else;
// once workspaces exist it means "phantom OR workspace", and the two
// must never be confused — a workspace mistaken for a phantom is
// silently skipped by background sync, refused by sync setup, and
// listed as absent.
//
// The empty-Kind row is deliberate: a Repo whose Kind was never
// populated (a struct built in Go, or a row read by a path that has not
// yet added `kind` to its column list) must behave exactly like a git
// repo, matching the DB column's DEFAULT 'git'.
func TestRepoPredicates(t *testing.T) {
	for _, tc := range []struct {
		name                                        string
		repo                                        Repo
		wantWorkspace, wantPhantom, wantWorkingTree bool
	}{
		{
			name: "git repo with a checkout",
			repo: Repo{Kind: RepoKindGit, Path: "/abs/repo"},
			// not a workspace, not a phantom, has a tree
			wantWorkspace: false, wantPhantom: false, wantWorkingTree: true,
		},
		{
			name:          "phantom git repo",
			repo:          Repo{Kind: RepoKindGit, Path: ""},
			wantWorkspace: false, wantPhantom: true, wantWorkingTree: false,
		},
		{
			name:          "workspace",
			repo:          Repo{Kind: RepoKindWorkspace, Path: ""},
			wantWorkspace: true, wantPhantom: false, wantWorkingTree: false,
		},
		{
			name:          "legacy row with an unset kind and a checkout",
			repo:          Repo{Kind: "", Path: "/abs/repo"},
			wantWorkspace: false, wantPhantom: false, wantWorkingTree: true,
		},
		{
			name:          "legacy row with an unset kind and no checkout",
			repo:          Repo{Kind: "", Path: ""},
			wantWorkspace: false, wantPhantom: true, wantWorkingTree: false,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := tc.repo
			if got := r.IsWorkspace(); got != tc.wantWorkspace {
				t.Errorf("IsWorkspace() = %v, want %v", got, tc.wantWorkspace)
			}
			if got := r.IsPhantom(); got != tc.wantPhantom {
				t.Errorf("IsPhantom() = %v, want %v", got, tc.wantPhantom)
			}
			if got := r.HasWorkingTree(); got != tc.wantWorkingTree {
				t.Errorf("HasWorkingTree() = %v, want %v", got, tc.wantWorkingTree)
			}
			// A workspace and a phantom are mutually exclusive, and
			// neither ever has a working tree — the invariant every
			// `Path == ""` call site is being rewritten against.
			if r.IsWorkspace() && r.IsPhantom() {
				t.Error("a repo reported as BOTH a workspace and a phantom")
			}
			if r.HasWorkingTree() && (r.IsWorkspace() || r.IsPhantom()) {
				t.Error("a repo with a working tree reported as a workspace or a phantom")
			}
		})
	}
}
