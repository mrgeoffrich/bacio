// Package schema centralises the JSON-input schema registry used by both
// `bacio schema` and `GET /schema*`. Keeping the registry here avoids the
// HTTP layer reaching into internal/cli (which would also pull in CLI
// globals like opts.user).
package schema

import (
	"fmt"
	"reflect"
	"strings"

	"github.com/invopop/jsonschema"

	"github.com/mrgeoffrich/bacio/internal/cli/inputs"
)

// Entry registers one mutating command's JSON-input schema. The dotted
// name (e.g. "issue.add") matches the cobra command path with spaces
// replaced by dots — the same convention used for history op names
// like "issue.create".
type Entry struct {
	Name      string
	Short     string
	InputType reflect.Type
	Example   any
}

func typeOf[T any]() reflect.Type { return reflect.TypeOf((*T)(nil)).Elem() }

// Registry is the single source of truth for what `bacio schema` knows
// about. Adding a mutating command means adding one row here; the runtime
// schema and the cobra command stay aligned because both consume the same
// inputs.*Input struct.
var Registry = []Entry{
	{"issue.add", "Create an issue in the current repo.", typeOf[inputs.IssueAddInput](), inputs.ExampleIssueAdd},
	{"issue.edit", "Update an issue's title, description, or feature.", typeOf[inputs.IssueEditInput](), inputs.ExampleIssueEdit},
	{"issue.state", "Set an issue's state.", typeOf[inputs.IssueStateInput](), inputs.ExampleIssueState},
	{"issue.assign", "Assign an issue to a person or agent.", typeOf[inputs.IssueAssignInput](), inputs.ExampleIssueAssign},
	{"issue.unassign", "Clear an issue's assignee.", typeOf[inputs.IssueUnassignInput](), inputs.ExampleIssueUnassign},
	{"issue.next", "Atomically claim the next ready issue in a feature.", typeOf[inputs.IssueNextInput](), inputs.ExampleIssueNext},
	{"issue.rm", "Delete an issue (and its comments).", typeOf[inputs.IssueRmInput](), inputs.ExampleIssueRm},

	{"feature.add", "Create a feature in the current repo.", typeOf[inputs.FeatureAddInput](), inputs.ExampleFeatureAdd},
	{"feature.edit", "Update a feature's title or description.", typeOf[inputs.FeatureEditInput](), inputs.ExampleFeatureEdit},
	{"feature.rm", "Delete a feature (issues are kept, unlinked).", typeOf[inputs.FeatureRmInput](), inputs.ExampleFeatureRm},

	{"comment.add", "Add a comment to an issue.", typeOf[inputs.CommentAddInput](), inputs.ExampleCommentAdd},

	{"link", "Create a relation (blocks, relates-to, duplicate-of) between two issues.", typeOf[inputs.LinkInput](), inputs.ExampleLink},
	{"unlink", "Remove all relations between two issues.", typeOf[inputs.UnlinkInput](), inputs.ExampleUnlink},

	{"pr.attach", "Attach a pull-request URL to an issue.", typeOf[inputs.PRAttachInput](), inputs.ExamplePRAttach},
	{"pr.detach", "Detach a pull-request URL from an issue.", typeOf[inputs.PRDetachInput](), inputs.ExamplePRDetach},

	{"tag.add", "Add tags to an issue (idempotent).", typeOf[inputs.TagAddInput](), inputs.ExampleTagAdd},
	{"tag.rm", "Remove tags from an issue.", typeOf[inputs.TagRmInput](), inputs.ExampleTagRm},

	{"doc.add", "Create a document in the current repo.", typeOf[inputs.DocAddInput](), inputs.ExampleDocAdd},
	{"doc.upsert", "Create or update a document (same shape as doc.add).", typeOf[inputs.DocAddInput](), inputs.ExampleDocAdd},
	{"doc.edit", "Edit a document's type and/or content.", typeOf[inputs.DocEditInput](), inputs.ExampleDocEdit},
	{"doc.rename", "Rename a document, preserving its links.", typeOf[inputs.DocRenameInput](), inputs.ExampleDocRename},
	{"doc.rm", "Delete a document and its links.", typeOf[inputs.DocRmInput](), inputs.ExampleDocRm},
	{"doc.link", "Link a document to an issue or feature.", typeOf[inputs.DocLinkInput](), inputs.ExampleDocLink},
	{"doc.unlink", "Remove a document's link to an issue or feature.", typeOf[inputs.DocUnlinkInput](), inputs.ExampleDocUnlink},
	{"doc.export", "Write a document's content to disk.", typeOf[inputs.DocExportInput](), inputs.ExampleDocExport},

	{"repo.rm", repoRmDescription, typeOf[inputs.RepoRmInput](), inputs.ExampleRepoRm},
}

// repoRmDescription is the LLM-targeted warning text published via
// `bacio schema show repo.rm` and `GET /schema/repo.rm`. Kept as a const
// so the wording is the same for every consumer of the schema.
const repoRmDescription = "DESTRUCTIVE & IRREVERSIBLE. Deletes a repo and ALL of its issues, " +
	"comments, features, documents, document links, issue relations, PR " +
	"attachments, TUI settings, and history rows. There is no undo. " +
	"Requires `confirm` to exactly match the target repo's prefix " +
	"(case-insensitive). Without it the CLI returns the impact preview " +
	"and errors out — agents driving bacio MUST stop and ask the user to " +
	"approve before re-running with `confirm` set. Always run with " +
	"`--dry-run` first to inspect the cascade."

// Find looks up a registry entry by its dotted name.
func Find(name string) (Entry, bool) {
	name = strings.TrimSpace(name)
	for _, e := range Registry {
		if e.Name == name {
			return e, true
		}
	}
	return Entry{}, false
}

// Build reflects a registry entry's input type into a JSON Schema and
// attaches the entry's metadata (title, description, examples).
func Build(e Entry) *jsonschema.Schema {
	r := &jsonschema.Reflector{
		Anonymous:      true,
		ExpandedStruct: true,
		DoNotReference: true,
	}
	s := r.ReflectFromType(e.InputType)
	s.Version = "https://json-schema.org/draft/2020-12/schema"
	s.ID = jsonschema.ID(fmt.Sprintf("bacio://schema/%s", e.Name))
	s.Title = e.InputType.Name()
	s.Description = e.Short
	if e.Example != nil {
		s.Examples = []any{e.Example}
	}
	return s
}

// All returns every entry's schema keyed by name. Map iteration is
// indeterminate; callers that need ordering should use Names().
func All() map[string]*jsonschema.Schema {
	out := make(map[string]*jsonschema.Schema, len(Registry))
	for _, e := range Registry {
		out[e.Name] = Build(e)
	}
	return out
}

// Names returns every entry name in registry declaration order.
func Names() []string {
	out := make([]string, 0, len(Registry))
	for _, e := range Registry {
		out = append(out, e.Name)
	}
	return out
}
