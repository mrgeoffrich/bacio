// Package schema centralises the JSON-input schema registry used by both
// `bacio schema` and `GET /schema*`. Keeping the registry here avoids
// the HTTP layer reaching into internal/cli (which would drag CLI flag
// globals along with it).
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
	{"issue.edit", "Update an issue's title, description, feature, or base branch.", typeOf[inputs.IssueEditInput](), inputs.ExampleIssueEdit},
	{"issue.state", "Set an issue's state.", typeOf[inputs.IssueStateInput](), inputs.ExampleIssueState},
	{"issue.assign", "Assign an issue to a person or agent.", typeOf[inputs.IssueAssignInput](), inputs.ExampleIssueAssign},
	{"issue.unassign", "Clear an issue's assignee.", typeOf[inputs.IssueUnassignInput](), inputs.ExampleIssueUnassign},
	{"issue.next", "Atomically claim the next ready issue in a feature.", typeOf[inputs.IssueNextInput](), inputs.ExampleIssueNext},
	{"issue.rm", "Delete an issue (and its comments).", typeOf[inputs.IssueRmInput](), inputs.ExampleIssueRm},
	{"issue.reorder", "Move a card within its Backlog/Shipping ordering band (Pipeline). Position is 1-based; 1 is the top of the column (next to go).", typeOf[inputs.IssueReorderInput](), inputs.ExampleIssueReorder},
	{"issue.process.set", "Assign a process (job chain) to an in_pipeline card (Pipeline). Pass either a preset slug via process (e.g. plan-implement-ship) OR an explicit ordered stage list via stages (e.g. [\"design\",\"plan_large\",\"implement\",\"ship\"]) — exactly one, they are mutually exclusive.", typeOf[inputs.IssueProcessInput](), inputs.ExampleIssueProcess},
	{"issue.process.edit", "Edit the PENDING TAIL of an in_pipeline card's job chain (Pipeline, BACI-294). stages is the re-ordered ordered list of pending job modes that replaces the card's pending jobs; the completed/running/cancelled jobs are kept verbatim as a locked prefix and the tail is re-sequenced after them. Ship may appear only as the final stage of the whole chain; duplicate modes are allowed (e.g. implement → review → implement).", typeOf[inputs.IssueProcessEditInput](), inputs.ExampleIssueProcessEdit},
	{"issue.ship", "Hand off an in_pipeline card to the Shipping column (to_be_shipped). Dispatches no agent — the ship agent fires from Shipping / auto-ship.", typeOf[inputs.IssueShipInput](), inputs.ExampleIssueShip},
	{"issue.auto-ship", "Toggle the per-repo Shipping-column auto-ship (Pipeline). When on, the controller dispatches a ship agent against the top to_be_shipped card.", typeOf[inputs.RepoAutoShipInput](), inputs.ExampleRepoAutoShip},

	{"feature.add", "Create a feature in the current repo.", typeOf[inputs.FeatureAddInput](), inputs.ExampleFeatureAdd},
	{"feature.edit", "Update a feature's title or description.", typeOf[inputs.FeatureEditInput](), inputs.ExampleFeatureEdit},
	{"feature.rm", "Delete a feature (issues are kept, unlinked).", typeOf[inputs.FeatureRmInput](), inputs.ExampleFeatureRm},
	{"feature.comment.add", "Add a comment to a feature — the BACI-124 chronological-handoff scratchpad implement-mode workers post to on close-out.", typeOf[inputs.FeatureCommentAddInput](), inputs.ExampleFeatureCommentAdd},
	{"feature.comment.rm", "Delete a comment from a feature (BACI-124). Addressed by the comment's immutable uuid.", typeOf[inputs.FeatureCommentRmInput](), inputs.ExampleFeatureCommentRm},

	{"comment.add", "Add a comment to an issue. Set `eval: true` to mark it as a quality-review note (BACI-131) — the server pins the in-flight (agent_session_id, dispatch_id, mode) snapshot onto the row at write time.", typeOf[inputs.CommentAddInput](), inputs.ExampleCommentAdd},
	{"comment.rm", "Delete a comment from an issue.", typeOf[inputs.CommentRmInput](), inputs.ExampleCommentRm},

	{"link", "Create a relation (blocks, relates-to, duplicate-of) between two issues.", typeOf[inputs.LinkInput](), inputs.ExampleLink},
	{"unlink", "Remove all relations between two issues.", typeOf[inputs.UnlinkInput](), inputs.ExampleUnlink},

	{"pr.create", "Open a GitHub PR labelled bacio:<KEY> after pre-flighting for an existing labelled PR (refuses on OPEN/MERGED unless force=true, warns on CLOSED-only). On success the URL is funnelled through `bacio pr attach` so the local DB stays in sync. Shells out to the `gh` CLI — local-only; rejected under --remote.", typeOf[inputs.PRCreateInput](), inputs.ExamplePRCreate},
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
	{"repo.link", "Link a phantom repo (a sync_clone-imported row with no local path) to a local working tree (BACI-112). Resolves the owning sync repo by walking the sync_remotes registry, runs UpgradePhantomRepo, then writes .bacio/config.yaml with the sync remote URL. Idempotent: re-linking to the same path is a no-op.", typeOf[inputs.RepoLinkInput](), inputs.ExampleRepoLink},

	{"agent.register", "Register (or refresh) an AI-agent session against the current repo.", typeOf[inputs.AgentRegisterInput](), inputs.ExampleAgentRegister},
	{"agent.heartbeat", "Bump last_seen_at on an existing agent session (optional — register / claim / release already bump it).", typeOf[inputs.AgentHeartbeatInput](), inputs.ExampleAgentHeartbeat},
	{"agent.end", "End an agent session and auto-release every open claim it holds (unassigning any issue left with no open claims). Each cascaded release leaves its issue's state alone unless `state_on_orphan` is set (BACI-300 retired the in_progress default — a claim is a focus marker, not a state move).", typeOf[inputs.AgentEndInput](), inputs.ExampleAgentEnd},
	{"agent.claim", "Focus an agent on an issue — records the claim and stamps the issue's assignee with the claiming identity. State-neutral since BACI-300 (a claim no longer moves the issue's state). No-op on a re-claim by the same session.", typeOf[inputs.AgentClaimInput](), inputs.ExampleAgentClaim},
	{"agent.release", "Release an agent's claim on an issue — clears the assignee once the issue has no open claims left. `final_state` is optional: omit it and the issue's state is left untouched (BACI-300); pass a valid issue state to move it atomically.", typeOf[inputs.AgentReleaseInput](), inputs.ExampleAgentRelease},
	{"agent.ack", "Acknowledge a dispatch and record an optional reply note.", typeOf[inputs.AgentAckInput](), inputs.ExampleAgentAck},
	{"agent.cancel", "Cancel a queued or pending dispatch — flips the row to status='cancelled' so it stops appearing as 'waiting' on the targeted issue's kanban card (BACI-255). Delivered dispatches are rejected (BACI-130 — the worker has the Task in hand; interrupt the agent itself instead).", typeOf[inputs.AgentCancelInput](), inputs.ExampleAgentCancel},
	{"issue.dispatch", "State-gated auto-pick dispatch — re-check the stage's state-gate against the issue's current state, then pick the most-recently-active free agent automatically.", typeOf[inputs.IssueDispatchInput](), inputs.ExampleIssueDispatch},

	{"agent.questions.list", "List ask_user_question rows for a session (defaults to open state).", typeOf[inputs.AgentQuestionsListInput](), inputs.ExampleAgentQuestionsList},
	{"agent.questions.show", "Show one ask_user_question row by id.", typeOf[inputs.AgentQuestionsShowInput](), inputs.ExampleAgentQuestionsShow},
	{"agent.questions.answer", "Answer an open ask_user_question — agent receives the answer as the tool result.", typeOf[inputs.AgentQuestionsAnswerInput](), inputs.ExampleAgentQuestionsAnswer},
	{"agent.questions.cancel", "Cancel (dismiss) an open ask_user_question — agent receives a tool error.", typeOf[inputs.AgentQuestionsCancelInput](), inputs.ExampleAgentQuestionsCancel},

	{"settings.template.set", "Set a dispatch prompt template's body.", typeOf[inputs.SettingsTemplateSetInput](), inputs.ExampleSettingsTemplateSet},
	{"settings.template.reset", "Reset a built-in dispatch prompt template's body to its embedded default.", typeOf[inputs.SettingsTemplateResetInput](), inputs.ExampleSettingsTemplateReset},
	{"settings.template.add", "Create a new dispatch prompt template (slug, name, body, action label, concurrency limit).", typeOf[inputs.SettingsTemplateAddInput](), inputs.ExampleSettingsTemplateAdd},
	{"settings.template.rename", "Rename a dispatch prompt template — slug change cascades to agent_dispatches.mode.", typeOf[inputs.SettingsTemplateRenameInput](), inputs.ExampleSettingsTemplateRename},
	{"settings.template.rm", "Delete a dispatch prompt template (historical dispatch rows keep the slug verbatim).", typeOf[inputs.SettingsTemplateRmInput](), inputs.ExampleSettingsTemplateRm},
	{"settings.template.restore-defaults", "Re-seed any missing built-in dispatch prompt templates from the embedded defaults (idempotent).", typeOf[inputs.SettingsTemplateRestoreDefaultsInput](), inputs.ExampleSettingsTemplateRestoreDefaults},
	{"settings.template.set-concurrency", "Set a dispatch prompt template's concurrency_limit — the per-(repo, slug) cap the BACI-51 matcher enforces on in-flight (pending+delivered) dispatches. 0 = unlimited.", typeOf[inputs.SettingsTemplateSetConcurrencyInput](), inputs.ExampleSettingsTemplateSetConcurrency},
	{"settings.template.set-action-label", "Set a dispatch prompt template's action_label — the imperative override rendered on the dispatch action menus (BACI-67). Empty = derive from name.", typeOf[inputs.SettingsTemplateSetActionLabelInput](), inputs.ExampleSettingsTemplateSetActionLabel},

	{"worktree.init", "Initialise a per-worktree bacio environment manifest (BACI-63). Writes environment-config.yaml at the worktree root, registers the slug + port + db_path in ~/.bacio/worktrees.yaml, and appends environment-config.yaml to .gitignore (idempotent). Slug defaults to the worktree basename; port is auto-allocated. By default the manifest pins the shared ~/.bacio/db.sqlite (port isolation only, so issue calls still reach the ticket) — set isolate_db=true to opt into a per-worktree DB at .bacio/db.sqlite, or db_path to pin an explicit path.", typeOf[inputs.WorktreeInitInput](), inputs.ExampleWorktreeInit},
	{"worktree.rm", "Remove a worktree's environment manifest and its row from ~/.bacio/worktrees.yaml. Confirm must equal the manifest's slug. With purge_db=true, the worktree's SQLite DB is deleted too. By default any bacio process listening on the manifest's API port is terminated (SIGTERM then SIGKILL after a grace) so a torn-down worktree can't orphan a process holding the shared ui_leader lease — set keep_processes=true to skip that. A dry-run lists the PIDs it would signal without touching anything.", typeOf[inputs.WorktreeRmInput](), inputs.ExampleWorktreeRm},

	{"issue.archive", "Archive an issue — stamps archived_at and hides the row from default lists (BACI-68). The row, its comments, relations, PRs, tags and audit history are retained. Sticky: reopening an archived issue does NOT auto-unarchive it.", typeOf[inputs.IssueArchiveInput](), inputs.ExampleIssueArchive},
	{"issue.unarchive", "Unarchive an issue — clears archived_at so the row shows up in default lists again (BACI-68).", typeOf[inputs.IssueUnarchiveInput](), inputs.ExampleIssueUnarchive},
	{"feature.archive", "Archive a feature (BACI-68). Same semantics as issue.archive on the parent record.", typeOf[inputs.FeatureArchiveInput](), inputs.ExampleFeatureArchive},
	{"feature.unarchive", "Unarchive a feature (BACI-68).", typeOf[inputs.FeatureUnarchiveInput](), inputs.ExampleFeatureUnarchive},
	{"feature.state", "Set a feature's state (active|done|cancelled) — BACI-199. Writes only the state column; the auto-close pin (`state_manual`) is decoupled (BACI-250) — use `bacio feature auto-close` to flip it. Archive (archived_at) stays orthogonal — a `done` or `cancelled` feature is still visible by default until explicitly archived.", typeOf[inputs.FeatureStateInput](), inputs.ExampleFeatureState},
	{"feature.auto-close", "Toggle a feature's auto-close behaviour (BACI-250). Auto-close ON (the default) clears `state_manual`, so the BACI-199 archive-sweep's auto-completion pass may promote this feature to `done`/`cancelled` once every child issue is terminal. Auto-close OFF sets `state_manual=1`, pinning long-lived catch-all features like `bugs` and `maintenance` so they stay `active` indefinitely. Writes only the sticky bit — does not touch the state column.", typeOf[inputs.FeatureAutoCloseInput](), inputs.ExampleFeatureAutoClose},
	{"doc.archive", "Archive a document (BACI-68). The doc and its links remain; default lists hide it.", typeOf[inputs.DocArchiveInput](), inputs.ExampleDocArchive},
	{"doc.unarchive", "Unarchive a document (BACI-68).", typeOf[inputs.DocUnarchiveInput](), inputs.ExampleDocUnarchive},
	{"archive.sweep", "Manually trigger the BACI-68 archive sweep on demand. Same three SQL passes the leader-elected Controller runs hourly: archive issues whose terminal_at is older than the configured retention period (BACI-162; default 7 days, editable via `bacio settings archive`), then features whose every child issue is archived, then docs whose every linked parent is archived. Idempotent. Returns {issues_archived, features_archived, documents_archived}.", typeOf[inputs.ArchiveSweepInput](), inputs.ExampleArchiveSweep},
	{"settings.show-archived", "Toggle the BACI-68 display.show_archived global setting. When on, lists / boards / docs / features views include archived rows by default (the per-call --include-archived flag still works either way).", typeOf[inputs.SettingsShowArchivedInput](), inputs.ExampleSettingsShowArchived},
	{"settings.sync-background", "Toggle the BACI-89 sync.background_enabled global setting. When on (the default — background sync is opt-OUT), the leader-elected controller continually mirrors every sync-enabled repo on a timer. Set to false for manual-only `bacio sync`.", typeOf[inputs.SettingsSyncBackgroundInput](), inputs.ExampleSettingsSyncBackground},
	{"settings.shipped-sfx", "Toggle the BACI-240 ui.shipped_sfx global setting. When on, the Pipeline Shipping-column Shipped pill plays a short ka-ching SFX on every genuine ship (silently no-ops under the browser autoplay policy until a user gesture lands). Default ON (BACI-295); `prefers-reduced-motion` no longer mutes it — that preference governs animation, not audio.", typeOf[inputs.SettingsShippedSfxInput](), inputs.ExampleSettingsShippedSfx},
	{"settings.archive", "Configure the BACI-162 auto-archive behaviour. `auto_enabled` gates the hourly issue auto-archive pass (defaults to true — auto-archive is opt-OUT); `retention_days` is the number of days a terminal-state issue's `terminal_at` must sit before the next sweep archives it (1..3650; default 7). Both fields are required and written atomically. The feature + document cascade passes always run regardless of `auto_enabled` — a manually-archived issue still cascades to its parent feature and linked docs.", typeOf[inputs.SettingsArchiveInput](), inputs.ExampleSettingsArchive},
	{"settings.default-feature", "Set or clear the per-repo `default_feature` setting (BACI-235). When set, issues created without an explicit `feature_slug` auto-apply to this feature (across the CLI flag path, --json path, REST/UI, TUI new-issue flow). Empty `slug` clears the setting (featureless creates again — the legacy default). The verb is per-repo despite living under the otherwise-global `settings` group; the FK on `default_feature_id` is ON DELETE SET NULL so deleting the referenced feature auto-clears the setting.", typeOf[inputs.SettingsDefaultFeatureInput](), inputs.ExampleSettingsDefaultFeature},

	{"sync.setup", "Set a project repo up for sync over HTTP (BACI-110). Three modes: `init` runs the equivalent of `bacio sync init` (write project config, create/attach sync repo at local_path, export+commit+push); `clone` runs `bacio sync clone` (git-clone the sync repo from remote, import, write config) and honours allow_renumber for the BACI-4 collision preview gate; `attach` joins an existing sync_remotes registry entry (resolves local_path from the registry, runs the additive import + writes the project config — no git clone). HTTP-only — the CLI already covers init/clone with `bacio sync init` / `bacio sync clone`. Renumber collisions return 409 + the engine's preview; re-POST with allow_renumber=true to confirm.", typeOf[inputs.SyncSetupInput](), inputs.ExampleSyncSetupClone},
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
