package store

import (
	"strings"
	"testing"
)

// TestValidateFolderName and TestValidateKanbanColumnName lock in the
// store-boundary rules for the two container names introduced by the
// doc-folders / Kanban pivot. Both are modelled on
// ValidateDocFilenameStrict — the '/' and '\\' rejections in particular
// are load-bearing: a name is one segment of a derived display path and
// of a sync record path, and a separator inside one would let a single
// folder or lane forge a nested record folder.
func TestValidateFolderName(t *testing.T) {
	for _, tc := range []struct {
		name    string
		in      string
		wantErr string // substring; "" means the input must be accepted
	}{
		{"simple", "Design", ""},
		{"spaces inside", "API Reference", ""},
		{"unicode", "Grüße 🎉", ""},
		{"dot prefix", ".hidden", ""},
		{"empty", "", "required"},
		{"leading space", " Design", "leading or trailing whitespace"},
		{"trailing space", "Design ", "leading or trailing whitespace"},
		{"dot", ".", "is not allowed"},
		{"dotdot", "..", "is not allowed"},
		{"forward slash", "Design/API", "must not contain"},
		{"back slash", `Design\API`, "must not contain"},
		{"newline", "Design\nAPI", "disallowed control character"},
		{"nul", "Design\x00", "disallowed control character"},
		{"del", "Design\x7f", "disallowed control character"},
		{"too long", strings.Repeat("a", maxFolderNameLen+1), "too long"},
		{"at cap", strings.Repeat("a", maxFolderNameLen), ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ValidateFolderName(tc.in)
			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("ValidateFolderName(%q) = error %v, want accepted", tc.in, err)
				}
				if got != tc.in {
					t.Errorf("ValidateFolderName(%q) = %q, want the input unchanged", tc.in, got)
				}
				return
			}
			if err == nil {
				t.Fatalf("ValidateFolderName(%q) accepted, want error containing %q", tc.in, tc.wantErr)
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Errorf("ValidateFolderName(%q) error = %q, want it to contain %q", tc.in, err, tc.wantErr)
			}
			if !strings.Contains(err.Error(), "folder name") {
				t.Errorf("ValidateFolderName(%q) error = %q, want it to name the field 'folder name'", tc.in, err)
			}
		})
	}
}

func TestValidateKanbanColumnName(t *testing.T) {
	for _, tc := range []struct {
		name    string
		in      string
		wantErr string
	}{
		{"simple", "Doing", ""},
		{"spaces inside", "Waiting on Bob", ""},
		{"empty", "", "required"},
		{"trailing space", "Doing ", "leading or trailing whitespace"},
		{"dotdot", "..", "is not allowed"},
		{"forward slash", "Doing/Blocked", "must not contain"},
		{"tab", "Doing\tnow", "disallowed control character"},
		{"too long", strings.Repeat("a", maxKanbanColumnNameLen+1), "too long"},
		{"at cap", strings.Repeat("a", maxKanbanColumnNameLen), ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ValidateKanbanColumnName(tc.in)
			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("ValidateKanbanColumnName(%q) = error %v, want accepted", tc.in, err)
				}
				if got != tc.in {
					t.Errorf("ValidateKanbanColumnName(%q) = %q, want the input unchanged", tc.in, got)
				}
				return
			}
			if err == nil {
				t.Fatalf("ValidateKanbanColumnName(%q) accepted, want error containing %q", tc.in, tc.wantErr)
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Errorf("ValidateKanbanColumnName(%q) error = %q, want it to contain %q", tc.in, err, tc.wantErr)
			}
			if !strings.Contains(err.Error(), "column name") {
				t.Errorf("ValidateKanbanColumnName(%q) error = %q, want it to name the field 'column name'", tc.in, err)
			}
		})
	}
}
