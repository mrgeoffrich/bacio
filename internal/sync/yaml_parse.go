package sync

import (
	"bytes"
	"fmt"
	"time"

	"go.yaml.in/yaml/v4"
)

// Strict YAML parser for sync-repo files. Distinct from the emitter
// (yaml_emit.go): the emitter is a hand-rolled writer tuned for
// hash-stable output, while parsing is delegated to go.yaml.in/yaml/v4
// with two non-negotiable settings:
//
//   - Decoder.KnownFields(true) — unknown fields fail loudly. Silently
//     dropping a field would let a future schema mistake (or a
//     hand-edit typo) sail through unnoticed.
//   - Strict typing on every Go struct field — the emitter always
//     quotes user strings, so a value that round-trips to the wrong
//     YAML type (e.g. `assignee: on` decoded as bool) means the file
//     was hand-edited or git mangled it. Refuse rather than coerce.
//
// All string fields that flow into a label or filename are
// NFC-normalised at parse time so the on-disk canonical form matches
// the emitter's NFC-on-write rule.
//
// The Parsed* types in this file are the wire shapes the importer
// works with. They deliberately don't share fields with model.* —
// keeping the disk schema and the DB row representation separated
// means a tweak to either side doesn't accidentally couple to the
// other.

// ParsedRepo is the on-disk shape of repos/<prefix>/repo.yaml.
//
// JSON tags mirror the YAML field names so `bacio sync inspect`'s
// `-o json` output matches the canonical on-disk schema rather than
// the Go field names. The yaml/JSON contract is that both speak the
// same vocabulary — we just use Go's idiomatic PascalCase internally.
type ParsedRepo struct {
	UUID            string    `yaml:"uuid" json:"uuid"`
	Prefix          string    `yaml:"prefix" json:"prefix"`
	Name            string    `yaml:"name" json:"name"`
	RemoteURL       string    `yaml:"remote_url" json:"remote_url"`
	NextIssueNumber int64     `yaml:"next_issue_number" json:"next_issue_number"`
	CreatedAt       time.Time `yaml:"created_at" json:"created_at"`
	UpdatedAt       time.Time `yaml:"updated_at" json:"updated_at"`
}

// ParsedFeature is the on-disk shape of feature.yaml.
type ParsedFeature struct {
	UUID            string    `yaml:"uuid" json:"uuid"`
	Slug            string    `yaml:"slug" json:"slug"`
	Title           string    `yaml:"title" json:"title"`
	DescriptionHash string    `yaml:"description_hash" json:"description_hash"`
	CreatedAt       time.Time `yaml:"created_at" json:"created_at"`
	UpdatedAt       time.Time `yaml:"updated_at" json:"updated_at"`
	// Emoji (BACI-172) round-trips the per-feature glyph used by the
	// kanban card decoration. Empty (the default) is the unset
	// signal — omitempty keeps live features without a glyph
	// byte-identical on disk to the pre-BACI-172 shape, so the sync
	// LWW gate doesn't churn on every existing feature.
	Emoji string `yaml:"emoji,omitempty" json:"emoji,omitempty"`
	// ArchivedAt round-trips the BACI-68 archive flag. Absent on disk
	// when the feature is live; present (RFC3339 UTC) when archived.
	// Pointer so omitempty actually omits the key on emit.
	ArchivedAt *time.Time `yaml:"archived_at,omitempty" json:"archived_at,omitempty"`
	// State (BACI-199) round-trips the three-state column on the
	// feature row. Pointer-of-string with omitempty so live `active`
	// features stay byte-identical on disk to the pre-BACI-199 shape
	// — the LWW gate would otherwise churn on every existing
	// feature.yaml after the schema bump. The importer treats a nil
	// pointer as "active" so a hand-written feature.yaml without the
	// key still imports cleanly.
	State *string `yaml:"state,omitempty" json:"state,omitempty"`
	// StateManual (BACI-199) round-trips the sticky-bit. Omitempty so
	// the default (false) stays off-disk — same churn-avoidance
	// rationale as State.
	StateManual bool `yaml:"state_manual,omitempty" json:"state_manual,omitempty"`
	// CollectHandoffs (BACI-333) round-trips the per-feature
	// collect-handoffs opt-out. Pointer-of-bool with omitempty so the
	// ON default (true) stays off-disk: a nil pointer on import is
	// treated as ON, and the export only writes the key when the column
	// is OFF. Unlike StateManual the default is the *true* end, so a
	// plain `bool` would emit `false` everywhere and churn every
	// feature.yaml — the pointer keeps the absent case distinct.
	CollectHandoffs *bool `yaml:"collect_handoffs,omitempty" json:"collect_handoffs,omitempty"`
}

// ParsedRef is a {label, uuid} cross-reference. Both fields are
// always present in emitted YAML; the importer treats uuid as
// canonical and label as a stale-tolerant hint.
type ParsedRef struct {
	Label string `yaml:"label" json:"label"`
	UUID  string `yaml:"uuid" json:"uuid"`
}

// ParsedRelations is the `relations: {blocks, relates_to,
// duplicate_of}` map inside issue.yaml. Each bucket is always
// emitted (with `[]` when empty), so missing keys here means
// hand-editing or schema drift.
type ParsedRelations struct {
	Blocks      []ParsedRef `yaml:"blocks" json:"blocks"`
	RelatesTo   []ParsedRef `yaml:"relates_to" json:"relates_to"`
	DuplicateOf []ParsedRef `yaml:"duplicate_of" json:"duplicate_of"`
}

// ParsedIssue is the on-disk shape of issue.yaml.
type ParsedIssue struct {
	UUID     string `yaml:"uuid" json:"uuid"`
	Number   int64  `yaml:"number" json:"number"`
	Title    string `yaml:"title" json:"title"`
	State    string `yaml:"state" json:"state"`
	Assignee string `yaml:"assignee" json:"assignee"`
	// CustomerImpact (BACI-349) round-trips the optional one-line customer
	// impact — authored content like Title/Assignee, so it syncs. omitempty
	// keeps an empty field off-disk so pre-BACI-349 issue files don't churn.
	CustomerImpact  string          `yaml:"customer_impact,omitempty" json:"customer_impact,omitempty"`
	Tags            []string        `yaml:"tags" json:"tags"`
	PRs             []string        `yaml:"prs" json:"prs"`
	Feature         *ParsedRef      `yaml:"feature,omitempty" json:"feature,omitempty"`
	Relations       ParsedRelations `yaml:"relations" json:"relations"`
	DescriptionHash string          `yaml:"description_hash" json:"description_hash"`
	CreatedAt       time.Time       `yaml:"created_at" json:"created_at"`
	UpdatedAt       time.Time       `yaml:"updated_at" json:"updated_at"`
	// ArchivedAt round-trips the BACI-68 archive flag. See ParsedFeature.
	ArchivedAt *time.Time `yaml:"archived_at,omitempty" json:"archived_at,omitempty"`
}

// ParsedComment is the on-disk shape of a comment .yaml file.
//
// BACI-131 added `eval`, `agent_session_id`, and `mode` as optional
// frontmatter keys carrying the in-flight context captured when an
// eval comment was posted. They are emitted only when `eval` is true
// (and only when their values are non-empty), so a normal comment's
// on-disk file stays byte-identical to today — no diff churn on every
// existing comment.
//
// `dispatch_id` is deliberately NOT round-tripped: it is a local-DB
// integer FK to agent_dispatches.id, which can't survive a sync to a
// sibling machine (the agent_* tables are never synced, so the FK
// targets nothing on the other side). The (agent_session_id, mode)
// pair is the durable cross-machine snapshot; dispatch_id stays
// local-only and is rebuilt-by-implication if a reviewer needs it.
type ParsedComment struct {
	UUID           string    `yaml:"uuid" json:"uuid"`
	Author         string    `yaml:"author" json:"author"`
	BodyHash       string    `yaml:"body_hash" json:"body_hash"`
	CreatedAt      time.Time `yaml:"created_at" json:"created_at"`
	Eval           bool      `yaml:"eval,omitempty" json:"eval,omitempty"`
	AgentSessionID string    `yaml:"agent_session_id,omitempty" json:"agent_session_id,omitempty"`
	Mode           string    `yaml:"mode,omitempty" json:"mode,omitempty"`
	// Kind (BACI-333) round-trips the feature-comment note/handoff
	// discriminator. Omitempty so the 'note' default stays off-disk:
	// only feature comments ever set it (the export emits it only when
	// not 'note'), and issue comments leave it empty so their YAML is
	// byte-identical to today. An empty value on import means 'note'.
	Kind string `yaml:"kind,omitempty" json:"kind,omitempty"`
}

// ParsedDocLink is one entry in doc.yaml's `links:` sequence.
type ParsedDocLink struct {
	Kind        string `yaml:"kind" json:"kind"` // "issue" | "feature"
	TargetLabel string `yaml:"target_label" json:"target_label"`
	TargetUUID  string `yaml:"target_uuid" json:"target_uuid"`
}

// ParsedDocument is the on-disk shape of doc.yaml.
type ParsedDocument struct {
	UUID        string          `yaml:"uuid" json:"uuid"`
	Filename    string          `yaml:"filename" json:"filename"`
	Type        string          `yaml:"type" json:"type"`
	SourcePath  string          `yaml:"source_path" json:"source_path"`
	Links       []ParsedDocLink `yaml:"links" json:"links"`
	ContentHash string          `yaml:"content_hash" json:"content_hash"`
	CreatedAt   time.Time       `yaml:"created_at" json:"created_at"`
	UpdatedAt   time.Time       `yaml:"updated_at" json:"updated_at"`
	// ArchivedAt round-trips the BACI-68 archive flag. See ParsedFeature.
	ArchivedAt *time.Time `yaml:"archived_at,omitempty" json:"archived_at,omitempty"`
}

// ParsedWorkspace is the on-disk shape of repos/<prefix>/workspace.yaml
// — the sentinel whose mere PRESENCE marks a synced prefix as a
// workspace rather than a git repo.
//
// `kind` lives here rather than in repo.yaml on purpose: repo.yaml is
// parsed by every bacio ever shipped with KnownFields(true), so a new
// key there would hard-fail an older binary's whole sync run and be
// stripped on its next export. See the A0 rule in paths.go.
//
// The file carries the *repo's* uuid (not a uuid of its own) so a
// reader can cross-check the sentinel against the sibling repo.yaml,
// and the repo's timestamps so the file is byte-stable across exports.
type ParsedWorkspace struct {
	UUID      string    `yaml:"uuid" json:"uuid"`
	Kind      string    `yaml:"kind" json:"kind"`
	CreatedAt time.Time `yaml:"created_at" json:"created_at"`
	UpdatedAt time.Time `yaml:"updated_at" json:"updated_at"`
}

// ParsedDocFolder is the on-disk shape of
// repos/<prefix>/folders/<uuid>/folder.yaml.
//
// Membership lives HERE, on the container, never on the member:
// `documents` is the ordered sequence of document uuids in this folder,
// and the order within the sequence IS the tree order. That is the
// load-bearing choice of the whole design — it keeps doc.yaml
// byte-identical to what an older binary writes, so nothing breaks in
// either direction.
//
// ParentUUID is the empty string for a root folder. It is always
// emitted (rather than omitted) so the schema is uniform.
type ParsedDocFolder struct {
	UUID       string    `yaml:"uuid" json:"uuid"`
	Name       string    `yaml:"name" json:"name"`
	ParentUUID string    `yaml:"parent_uuid" json:"parent_uuid"`
	Position   int       `yaml:"position" json:"position"`
	Documents  []string  `yaml:"documents" json:"documents"`
	CreatedAt  time.Time `yaml:"created_at" json:"created_at"`
	UpdatedAt  time.Time `yaml:"updated_at" json:"updated_at"`
}

// ParsedKanbanColumn is the on-disk shape of
// repos/<prefix>/kanban/<uuid>/column.yaml. Same container-side
// membership rule as ParsedDocFolder: `issues` is the ordered sequence
// of issue uuids in this lane, and the order IS the top-to-bottom card
// order. issue.yaml stays byte-identical to today.
type ParsedKanbanColumn struct {
	UUID      string    `yaml:"uuid" json:"uuid"`
	Name      string    `yaml:"name" json:"name"`
	Position  int       `yaml:"position" json:"position"`
	Issues    []string  `yaml:"issues" json:"issues"`
	CreatedAt time.Time `yaml:"created_at" json:"created_at"`
	UpdatedAt time.Time `yaml:"updated_at" json:"updated_at"`
}

// ParsedRedirect is one entry in redirects.yaml. The file is a
// top-level YAML sequence of these.
type ParsedRedirect struct {
	Kind      string    `yaml:"kind" json:"kind"` // "issue" | "feature" | "document"
	Old       string    `yaml:"old" json:"old"`
	New       string    `yaml:"new" json:"new"`
	UUID      string    `yaml:"uuid" json:"uuid"`
	ChangedAt time.Time `yaml:"changed_at" json:"changed_at"`
	Reason    string    `yaml:"reason" json:"reason"`
}

// ParseRepoYAML decodes repo.yaml bytes into a ParsedRepo with strict
// typing. Returns an error on unknown fields, type mismatches, or
// invalid YAML. NFC-normalises string fields used as labels.
func ParseRepoYAML(b []byte) (*ParsedRepo, error) {
	var r ParsedRepo
	if err := strictDecode(b, &r); err != nil {
		return nil, fmt.Errorf("parse repo.yaml: %w", err)
	}
	r.Prefix = NormalizeNFC(r.Prefix)
	r.Name = NormalizeNFC(r.Name)
	return &r, nil
}

// ParseFeatureYAML decodes feature.yaml bytes.
func ParseFeatureYAML(b []byte) (*ParsedFeature, error) {
	var f ParsedFeature
	if err := strictDecode(b, &f); err != nil {
		return nil, fmt.Errorf("parse feature.yaml: %w", err)
	}
	f.Slug = NormalizeNFC(f.Slug)
	f.Title = NormalizeNFC(f.Title)
	return &f, nil
}

// ParseIssueYAML decodes issue.yaml bytes. Slugs/labels in
// cross-references are NFC-normalised; uuids are passed through
// unchanged (they're already 8-4-4-4-12 hex).
func ParseIssueYAML(b []byte) (*ParsedIssue, error) {
	var i ParsedIssue
	if err := strictDecode(b, &i); err != nil {
		return nil, fmt.Errorf("parse issue.yaml: %w", err)
	}
	i.Title = NormalizeNFC(i.Title)
	i.Assignee = NormalizeNFC(i.Assignee)
	i.CustomerImpact = NormalizeNFC(i.CustomerImpact)
	for k := range i.Tags {
		i.Tags[k] = NormalizeNFC(i.Tags[k])
	}
	if i.Feature != nil {
		i.Feature.Label = NormalizeNFC(i.Feature.Label)
	}
	for k := range i.Relations.Blocks {
		i.Relations.Blocks[k].Label = NormalizeNFC(i.Relations.Blocks[k].Label)
	}
	for k := range i.Relations.RelatesTo {
		i.Relations.RelatesTo[k].Label = NormalizeNFC(i.Relations.RelatesTo[k].Label)
	}
	for k := range i.Relations.DuplicateOf {
		i.Relations.DuplicateOf[k].Label = NormalizeNFC(i.Relations.DuplicateOf[k].Label)
	}
	return &i, nil
}

// ParseCommentYAML decodes a comment .yaml file's bytes.
func ParseCommentYAML(b []byte) (*ParsedComment, error) {
	var c ParsedComment
	if err := strictDecode(b, &c); err != nil {
		return nil, fmt.Errorf("parse comment.yaml: %w", err)
	}
	c.Author = NormalizeNFC(c.Author)
	return &c, nil
}

// ParseDocumentYAML decodes doc.yaml bytes.
func ParseDocumentYAML(b []byte) (*ParsedDocument, error) {
	var d ParsedDocument
	if err := strictDecode(b, &d); err != nil {
		return nil, fmt.Errorf("parse doc.yaml: %w", err)
	}
	d.Filename = NormalizeNFC(d.Filename)
	for k := range d.Links {
		d.Links[k].TargetLabel = NormalizeNFC(d.Links[k].TargetLabel)
	}
	return &d, nil
}

// ParseWorkspaceYAML decodes a workspace.yaml sentinel.
func ParseWorkspaceYAML(b []byte) (*ParsedWorkspace, error) {
	var w ParsedWorkspace
	if err := strictDecode(b, &w); err != nil {
		return nil, fmt.Errorf("parse workspace.yaml: %w", err)
	}
	return &w, nil
}

// ParseDocFolderYAML decodes a folder.yaml record.
func ParseDocFolderYAML(b []byte) (*ParsedDocFolder, error) {
	var f ParsedDocFolder
	if err := strictDecode(b, &f); err != nil {
		return nil, fmt.Errorf("parse folder.yaml: %w", err)
	}
	f.Name = NormalizeNFC(f.Name)
	return &f, nil
}

// ParseKanbanColumnYAML decodes a column.yaml record.
func ParseKanbanColumnYAML(b []byte) (*ParsedKanbanColumn, error) {
	var c ParsedKanbanColumn
	if err := strictDecode(b, &c); err != nil {
		return nil, fmt.Errorf("parse column.yaml: %w", err)
	}
	c.Name = NormalizeNFC(c.Name)
	return &c, nil
}

// ParseRedirectsYAML decodes redirects.yaml bytes. The file is a
// top-level YAML sequence; an empty/missing file is the caller's
// responsibility to skip — this function expects valid bytes.
func ParseRedirectsYAML(b []byte) ([]ParsedRedirect, error) {
	// Empty file is a valid "no redirects" state. Treat ws-only the
	// same — go.yaml.in returns an EOF-style error otherwise.
	if len(bytes.TrimSpace(b)) == 0 {
		return nil, nil
	}
	var rs []ParsedRedirect
	if err := strictDecode(b, &rs); err != nil {
		return nil, fmt.Errorf("parse redirects.yaml: %w", err)
	}
	for k := range rs {
		rs[k].Old = NormalizeNFC(rs[k].Old)
		rs[k].New = NormalizeNFC(rs[k].New)
	}
	return rs, nil
}

// strictDecode runs the v4 decoder with KnownFields(true) so unknown
// fields produce errors. It also walks the parsed node tree first to
// enforce strict scalar typing on the string fields the design doc
// calls out — go.yaml.in/yaml/v4 will happily coerce `assignee: true`
// (a YAML 1.2 boolean) into a Go string, but the design rule is
// "refuse rather than coerce" because every emitted user string is
// always quoted, so an unquoted scalar in the wrong shape means
// either a hand-edit or git mangling we'd rather catch loudly.
//
// Strictness applies to scalar fields that hold user-supplied free
// text; structural fields (number, created_at, prs[], …) are typed
// by the Go struct and rely on the decoder's normal type checking.
// stringFields lists the scalar keys whose values must carry the
// !!str YAML tag.
func strictDecode(b []byte, out any) error {
	// Round 1: validate scalar tags on the raw node tree.
	var root yaml.Node
	if err := yaml.NewDecoder(bytes.NewReader(b)).Decode(&root); err != nil {
		return err
	}
	if err := assertStringScalarTags(&root); err != nil {
		return err
	}
	// Round 2: typed decode with KnownFields(true) for unknown-field
	// rejection. We re-parse rather than reuse the node above so the
	// existing yaml.Decode/.Decode wiring keeps doing the heavy
	// lifting (defaults, time.Time parsing, etc.).
	dec := yaml.NewDecoder(bytes.NewReader(b))
	dec.KnownFields(true)
	if err := dec.Decode(out); err != nil {
		return err
	}
	return nil
}

// stringFields is the allowlist of map keys whose scalar values must
// be YAML strings (tag !!str). Any unquoted bool / int / null sitting
// where the schema expects a string fails the parse with a clear
// error. The set is deliberately small — the design doc only calls
// out user-text fields like assignee, title, name as the threat
// surface, not numeric fields like number where the type is
// unambiguous.
var stringFields = map[string]struct{}{
	"assignee":         {},
	"author":           {},
	"customer_impact":  {},
	"name":             {},
	"parent_uuid":      {},
	"prefix":           {},
	"reason":           {},
	"remote_url":       {},
	"slug":             {},
	"source_path":      {},
	"state":            {},
	"target_label":     {},
	"target_uuid":      {},
	"title":            {},
	"type":             {},
	"uuid":             {},
	"kind":             {},
	"label":            {},
	"old":              {},
	"new":              {},
	"filename":         {},
	"description_hash": {},
	"content_hash":     {},
	"body_hash":        {},
}

// assertStringScalarTags walks the YAML node tree and returns an
// error if any value under a key in stringFields carries a tag
// other than !!str. Recursive across maps and sequences so
// `relations.blocks[*].label` is checked too.
//
// We special-case sequences-of-strings (tags[], prs[]) by descending
// into them when the parent key is one of those — every scalar item
// must be !!str.
func assertStringScalarTags(n *yaml.Node) error {
	switch n.Kind {
	case yaml.DocumentNode:
		for _, c := range n.Content {
			if err := assertStringScalarTags(c); err != nil {
				return err
			}
		}
	case yaml.MappingNode:
		for i := 0; i+1 < len(n.Content); i += 2 {
			key := n.Content[i].Value
			val := n.Content[i+1]
			if _, isStringKey := stringFields[key]; isStringKey && val.Kind == yaml.ScalarNode {
				if val.Tag != "" && val.Tag != "!!str" {
					return fmt.Errorf("strict typing: field %q expects a string but got YAML tag %s (line %d, value %q) — quote the value to disambiguate",
						key, val.Tag, val.Line, val.Value)
				}
			}
			if (key == "tags" || key == "prs") && val.Kind == yaml.SequenceNode {
				for _, item := range val.Content {
					if item.Kind == yaml.ScalarNode && item.Tag != "" && item.Tag != "!!str" {
						return fmt.Errorf("strict typing: %q items must be strings but got YAML tag %s (line %d, value %q)",
							key, item.Tag, item.Line, item.Value)
					}
				}
			}
			if err := assertStringScalarTags(val); err != nil {
				return err
			}
		}
	case yaml.SequenceNode:
		for _, c := range n.Content {
			if err := assertStringScalarTags(c); err != nil {
				return err
			}
		}
	}
	return nil
}
