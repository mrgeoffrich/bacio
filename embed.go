// Package bacio exposes embedded asset bytes for the bacio binary.
// It lives at the module root because //go:embed cannot traverse parent
// directories — being at the root it can reach prompts/ and the
// webui/ bundle.
package bacio

import "embed"

// SkillMarkdown is the bacio Claude Code skill shipped into a target repo
// by `bacio install-skill`. It is sourced from prompts/SKILL.md, deliberately
// kept separate from .claude/skills/bacio/SKILL.md — the latter is the
// skill loaded for bacio's own development sessions, and the two can now
// diverge (e.g. trim the installed copy) without affecting each other.
//
//go:embed prompts/SKILL.md
var SkillMarkdown []byte

// PromptsFS holds the editable agent prompt sources at prompts/agents/.
// Each `.md` file is a worker brief (one per built-in template slug, plus
// the reserved `_dispatch_preamble.md`) or a shared block referenced via
// the `{{> name}}` include directive (`_preamble.md`, `_postamble.md`).
// internal/model loads from here at package init and at render time.
//
//go:embed prompts/agents/*.md
var PromptsFS embed.FS

// WebUIFS holds the browser-served React bundle produced by
// `npm --prefix desktop/frontend run build:web` and copied into the
// repo-root `webui/` directory by build.sh / Taskfile.yml. `bacio api`
// serves it at /ui/ when populated. Lives here for the same reason as
// the other embeds — //go:embed can't traverse upward from internal/.
//
// `all:webui` opts in to embedding dotfiles too so a `.gitkeep`
// placeholder in a clean checkout is enough to satisfy the embed
// directive (which errors at compile time if no files match).
//
//go:embed all:webui
var WebUIFS embed.FS
