// Package bacio exposes embedded asset bytes for the bacio binary.
// It lives at the module root because //go:embed cannot traverse parent
// directories — by being a sibling of the .claude/ and examples/ trees
// it can reach the canonical SKILL.md and the sample-skills tree
// without copying.
package bacio

import "embed"

//go:embed .claude/skills/bacio/SKILL.md
var SkillMarkdown []byte

// SampleSkillsFS holds the bundled flow-level sample skills (file-issue,
// triage, stand-up, plan-feature) that `bacio install-sample-skills` writes
// into a downstream repo's .claude/skills/. The FS is rooted at the
// module root, so each skill lives at examples/skills/<name>/SKILL.md.
//
//go:embed examples/skills
var SampleSkillsFS embed.FS
