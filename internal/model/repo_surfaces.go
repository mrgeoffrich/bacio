package model

// RepoSurfaces is one space's resolved nav-surface gates — which of the
// top-nav tabs that space exposes. Purely presentational: no Go
// subsystem branches on these, they gate React buttons and the route
// redirect that keeps a deep-link off a switched-off surface.
//
// NOT the same concept as internal/agentmode, despite ShowAgentSurfaces
// carrying the UI label "Agent Mode". That package owns the
// process-level BACIO_AGENT_MODE env var, which gates hook subcommands,
// the channel dispatch poller and the destructive `rm` verbs for a
// whole bacio invocation. This is a persisted per-space display
// preference. The identifiers are deliberately different so a grep for
// one never lands on the other.
type RepoSurfaces struct {
	// ShowAgentSurfaces gates the Agentic Pipeline, Agents and Monitor
	// tabs as a group. UI label: "Agent Mode".
	ShowAgentSurfaces bool `json:"show_agent_surfaces"`
	// ShowKanban gates the Kanban tab. UI label: "Show Kanban Board".
	ShowKanban bool `json:"show_kanban"`
}

// ResolveRepoSurfaces is THE default-resolution function for the two
// gates. Every reader goes through it; nobody reads the raw nullable
// columns and decides for itself.
//
// The columns are nullable rather than NOT NULL DEFAULT because neither
// default can be expressed in DDL. repo_settings rows are created
// lazily by whichever setting is written first, so GetRepoSettings maps
// sql.ErrNoRows to a zero-valued struct and a column DEFAULT never
// applies to the many repos that have no row at all. And the default
// depends on repos.kind, which repo_settings cannot see. So: nil means
// "never written", and the kind decides.
//
// The defaults encode what each space kind is for. A git repo has a
// working tree, so agents can be dispatched into it and the Agentic
// Pipeline is its home board. A manual workspace has no working tree —
// a dispatched agent would have nowhere to work — so the Kanban is its
// board and the agent surfaces start hidden. Either default can be
// overridden per space; a workspace with Agent Mode switched on does
// show all three agent tabs.
//
// A legacy empty kind reads as git, matching Repo.IsPhantom's reasoning
// and the repos.kind column's own DEFAULT 'git'.
func ResolveRepoSurfaces(kind RepoKind, agentSet, kanbanSet *bool) RepoSurfaces {
	isWorkspace := kind == RepoKindWorkspace
	out := RepoSurfaces{
		ShowAgentSurfaces: !isWorkspace,
		ShowKanban:        isWorkspace,
	}
	if agentSet != nil {
		out.ShowAgentSurfaces = *agentSet
	}
	if kanbanSet != nil {
		out.ShowKanban = *kanbanSet
	}
	return out
}
