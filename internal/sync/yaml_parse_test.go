package sync

import (
	"strings"
	"testing"
	"time"
)

// TestParseIssueYAML_RejectsUnknownFields locks in the strict-fields
// invariant: an unrecognised key fails the parse rather than being
// silently dropped. This is what protects the schema from drift —
// any future field rename needs an explicit migration rather than a
// "well, the old field disappeared" surprise.
func TestParseIssueYAML_RejectsUnknownFields(t *testing.T) {
	b := []byte(`
assignee: "geoff"
created_at: "2026-05-01T10:14:22.000Z"
description_hash: "sha256:abc"
number: 1
prs: []
relations:
  blocks: []
  duplicate_of: []
  relates_to: []
state: "todo"
tags: []
title: "An issue"
unknown_field: "boom"
updated_at: "2026-05-01T10:14:22.000Z"
uuid: "0190c2a3-7f3a-7b2c-8b21-aaaaaaaaaaaa"
`)
	_, err := ParseIssueYAML(b)
	if err == nil {
		t.Fatal("expected error on unknown field, got nil")
	}
	if !strings.Contains(err.Error(), "unknown_field") {
		t.Errorf("error doesn't mention the unknown field: %v", err)
	}
}

// TestParseIssueYAML_RejectsTypeMismatch covers the "unquoted user
// string round-trips as the wrong type" hazard. The emitter always
// quotes user strings, so a non-string YAML scalar where a string
// is expected means either hand-editing or git-mangling — refuse
// rather than coerce, even though go.yaml.in/yaml/v4 would happily
// stringify the value otherwise.
func TestParseIssueYAML_RejectsTypeMismatch(t *testing.T) {
	b := []byte(`
assignee: true
created_at: "2026-05-01T10:14:22.000Z"
description_hash: "sha256:abc"
number: 1
prs: []
relations:
  blocks: []
  duplicate_of: []
  relates_to: []
state: "todo"
tags: []
title: "An issue"
updated_at: "2026-05-01T10:14:22.000Z"
uuid: "0190c2a3-7f3a-7b2c-8b21-aaaaaaaaaaaa"
`)
	_, err := ParseIssueYAML(b)
	if err == nil {
		t.Fatal("expected error on bool-typed assignee, got nil")
	}
	if !strings.Contains(err.Error(), "assignee") {
		t.Errorf("error doesn't mention the bad field: %v", err)
	}
}

// TestParseIssueYAML_RejectsBoolInTags exercises the sequence-item
// strictness rule: tag list entries that decode to !!bool fail too.
func TestParseIssueYAML_RejectsBoolInTags(t *testing.T) {
	b := []byte(`
assignee: "geoff"
created_at: "2026-05-01T10:14:22.000Z"
description_hash: "sha256:abc"
number: 1
prs: []
relations:
  blocks: []
  duplicate_of: []
  relates_to: []
state: "todo"
tags:
  - true
title: "An issue"
updated_at: "2026-05-01T10:14:22.000Z"
uuid: "0190c2a3-7f3a-7b2c-8b21-aaaaaaaaaaaa"
`)
	_, err := ParseIssueYAML(b)
	if err == nil {
		t.Fatal("expected error on bool tag, got nil")
	}
}

// TestParseIssueYAML_HappyPath round-trips a representative
// issue.yaml shape through the parser and checks the structured
// fields land where expected.
func TestParseIssueYAML_HappyPath(t *testing.T) {
	b := []byte(`
assignee: "geoff"
created_at: "2026-05-01T10:14:22.000Z"
description_hash: "sha256:abc"
feature:
  label: "auth-rewrite"
  uuid: "0190c2a3-7f3a-7b2c-8b21-dddddddddddd"
number: 7
prs:
  - "https://github.com/x/y/pull/42"
relations:
  blocks:
    - {label: "MINI-12", uuid: "0190c2a3-7f3a-7b2c-8b21-cccccccccccc"}
  duplicate_of: []
  relates_to: []
state: "in_progress"
tags:
  - "p1"
  - "security"
title: "Add auth middleware"
updated_at: "2026-05-09T14:22:00.000Z"
uuid: "0190c2a3-7f3a-7b2c-8b21-bbbbbbbbbbbb"
`)
	got, err := ParseIssueYAML(b)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if got.UUID != "0190c2a3-7f3a-7b2c-8b21-bbbbbbbbbbbb" {
		t.Errorf("uuid: got %q", got.UUID)
	}
	if got.Number != 7 {
		t.Errorf("number: got %d", got.Number)
	}
	if got.State != "in_progress" {
		t.Errorf("state: got %q", got.State)
	}
	if got.Assignee != "geoff" {
		t.Errorf("assignee: got %q", got.Assignee)
	}
	if got.Feature == nil || got.Feature.Label != "auth-rewrite" {
		t.Errorf("feature ref missing or wrong: %+v", got.Feature)
	}
	if len(got.Tags) != 2 || got.Tags[0] != "p1" {
		t.Errorf("tags: got %+v", got.Tags)
	}
	if len(got.PRs) != 1 || got.PRs[0] != "https://github.com/x/y/pull/42" {
		t.Errorf("prs: got %+v", got.PRs)
	}
	if len(got.Relations.Blocks) != 1 || got.Relations.Blocks[0].UUID != "0190c2a3-7f3a-7b2c-8b21-cccccccccccc" {
		t.Errorf("blocks: got %+v", got.Relations.Blocks)
	}
	if !got.CreatedAt.Equal(time.Date(2026, 5, 1, 10, 14, 22, 0, time.UTC)) {
		t.Errorf("created_at: got %v", got.CreatedAt)
	}
}

// TestParseRedirectsYAML_Empty: an empty redirects.yaml is the
// common case. Treat zero-byte / whitespace-only as empty rather
// than an error.
func TestParseRedirectsYAML_Empty(t *testing.T) {
	rs, err := ParseRedirectsYAML(nil)
	if err != nil || len(rs) != 0 {
		t.Errorf("nil: got rs=%v err=%v", rs, err)
	}
	rs, err = ParseRedirectsYAML([]byte("\n\n"))
	if err != nil || len(rs) != 0 {
		t.Errorf("ws-only: got rs=%v err=%v", rs, err)
	}
}

// TestParseRedirectsYAML_HappyPath round-trips a small redirects
// file with one entry per kind.
func TestParseRedirectsYAML_HappyPath(t *testing.T) {
	b := []byte(`
- changed_at: "2026-05-09T14:30:00.000Z"
  kind: "issue"
  new: "MINI-12"
  old: "MINI-7"
  reason: "label_collision"
  uuid: "0190c2a3-7f3a-7b2c-8b21-bbbbbbbbbbbb"
- changed_at: "2026-05-10T08:15:00.000Z"
  kind: "feature"
  new: "auth-rewrite-2"
  old: "auth-rewrite"
  reason: "label_collision"
  uuid: "0190c2a3-7f3a-7b2c-8b21-dddddddddddd"
`)
	rs, err := ParseRedirectsYAML(b)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(rs) != 2 {
		t.Fatalf("len: got %d", len(rs))
	}
	if rs[0].Kind != "issue" || rs[0].Old != "MINI-7" || rs[0].New != "MINI-12" {
		t.Errorf("entry 0: got %+v", rs[0])
	}
	if rs[1].Kind != "feature" || rs[1].New != "auth-rewrite-2" {
		t.Errorf("entry 1: got %+v", rs[1])
	}
}
