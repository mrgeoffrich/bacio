# bacio.io site docs

The markdown source for the **user-facing** documentation site at
<https://bacio.io/docs/>. Synced into the [bacio-website][site] repo
at build time via `npm run sync-docs`, which clones this repo at the
ref pinned in `bacio-website/.bacio-ref` and copies everything under
this folder into `bacio-website/docs/`.

[site]: https://github.com/mrgeoffrich/bacio-website

## Editing

These pages are plain markdown with VitePress conventions:

- Frontmatter (`title`, `description`) — required for sidebar + SEO.
- `:::tip`, `:::warning`, `:::danger` containers for callouts.
- Internal links use absolute paths with no `.md` suffix
  (`/reference/cli/issue`, `/guides/sync-across-machines`).
- Sidebar order + grouping lives in `sidebar.js` alongside the pages.

When adding a new page:

1. Drop the `.md` file under the right subfolder.
2. Add an entry to `sidebar.js` so it shows up in the nav.
3. Cross-link it from related pages — at minimum, add it to the "See
   also" footer of whatever page is closest in topic.

## Marking lifecycle status

Pages and individual surfaces can be tagged `beta` (shipped but rough)
or `preview` (unreleased / behind a flag). There are three coordinated
surfaces, all opt-in:

1. **Whole-page banner** — add `status: beta` (or `preview`) to the
   page frontmatter. The website theme renders a coloured banner at
   the top of the doc, linking back to the roadmap. Use this when an
   entire command group / guide is not yet stable. Live today:
   `reference/cli/sync.md`, `reference/cli/agent.md`.

2. **Inline badge** — for a single subcommand row, flag, or section
   inside an otherwise-stable page, use VitePress' built-in `Badge`
   component:

   ```md
   | `bacio issue next --feature <slug>` <Badge type="warning" text="beta" /> | Atomically claim the next ready issue. |
   ```

   `type="warning"` is the beta colour; `type="important"` is preview.
   No frontmatter change needed.

3. **Sidebar pill** — add an entry to the `statusBadges` map at the
   top of `sidebar.js`, keyed by the same `link:` string the sidebar
   entry uses. The pill is baked into the sidebar text at config time
   (VitePress renders that field as HTML). Use whenever you also set
   frontmatter `status` — the surfaces are independent toggles but in
   practice they ship together.

When a feature graduates to stable, drop the frontmatter line, remove
the `statusBadges` entry, and delete any inline `<Badge>` calls in the
page. There's no "stable" sticker — absence of a marker is the
default.

## Relationship to the other docs/

`bacio/docs/` also contains `getting-started.md`,
`agent-cli-principles.md`, and `tui-cookbook.md`. Those are
**developer-internal** references for someone hacking on bacio itself
(linked from the project README and CLAUDE.md). The user-facing
content under `site/` is its own structured surface — overlap is
fine, drift is fine.

## Theme + build harness

The VitePress theme, the build config, the marketing page, and the
deploy setup all live in [bacio-website][site]. This folder is **just
content** plus the sidebar; everything else is over there.
