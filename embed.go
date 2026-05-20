// Package bacio exposes embedded asset bytes for the bacio binary.
// It lives at the module root because //go:embed cannot traverse parent
// directories — by being a sibling of the .claude/ tree it can reach
// the canonical SKILL.md without copying.
package bacio

import "embed"

//go:embed .claude/skills/bacio/SKILL.md
var SkillMarkdown []byte

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
