// Sidebar for the bacio.io docs site. Imported by bacio-website's
// VitePress config after `npm run sync-docs` copies this file (along
// with every .md page) into bacio-website/docs/sidebar.js.
//
// Keeping the sidebar here means a PR that adds a new page in
// bacio/docs/site/ can update the sidebar in the same commit — the
// website side just re-syncs and picks up both.

// Lifecycle markers shown as pills in the sidebar. Keyed by link path
// (the same string used in the `link:` field below). Two values today:
// `beta` and `preview` (= unreleased). The same page must also declare
// `status: beta|preview` in its frontmatter for the in-page banner —
// this map only governs the sidebar pill. Styling lives in
// bacio-website/docs/.vitepress/theme/style.css.
const statusBadges = {
  '/reference/cli/sync': 'beta',
  '/reference/cli/agent': 'beta',
};

// VitePress renders sidebar `text` via `v-html`, so we bake the badge
// markup straight in. Keeping the wrap here means the raw sidebar list
// below stays readable.
function withBadge(item) {
  const next = { ...item };
  if (next.items) next.items = next.items.map(withBadge);
  const status = next.link && statusBadges[next.link];
  if (status) {
    next.text = `${next.text}<span class="bacio-sidebar-badge bacio-sidebar-badge--${status}" aria-label="${status}">${status}</span>`;
  }
  return next;
}

const rawSidebar = [
  {
    text: 'Getting started',
    items: [
      { text: 'Quickstart', link: '/' },
      { text: 'Install', link: '/getting-started/install' },
      { text: 'Your first board', link: '/getting-started/first-board' },
      { text: 'Your first week', link: '/getting-started/your-first-week' },
    ],
  },
  {
    text: 'Guides',
    items: [
      { text: 'Work with Claude Code', link: '/guides/work-with-claude-code' },
      { text: 'Work with Codex', link: '/guides/work-with-codex' },
      { text: 'Drive it without an agent', link: '/guides/drive-it-without-an-agent' },
      { text: 'Sync across machines', link: '/guides/sync-across-machines' },
      { text: 'Browse in your editor', link: '/guides/browse-in-your-editor' },
      { text: 'Run the API server', link: '/guides/run-the-api-server' },
      { text: 'Find things fast', link: '/guides/filter-and-search' },
    ],
  },
  {
    text: 'CLI reference',
    collapsed: false,
    items: [
      { text: 'Overview', link: '/reference/cli/' },
      { text: 'bacio init', link: '/reference/cli/init' },
      { text: 'bacio repo', link: '/reference/cli/repo' },
      { text: 'bacio feature', link: '/reference/cli/feature' },
      { text: 'bacio issue', link: '/reference/cli/issue' },
      { text: 'bacio comment', link: '/reference/cli/comment' },
      { text: 'bacio link / unlink', link: '/reference/cli/link' },
      { text: 'bacio pr', link: '/reference/cli/pr' },
      { text: 'bacio tag', link: '/reference/cli/tag' },
      { text: 'bacio doc', link: '/reference/cli/doc' },
      { text: 'bacio status', link: '/reference/cli/status' },
      { text: 'bacio history', link: '/reference/cli/history' },
      { text: 'bacio agent', link: '/reference/cli/agent' },
      { text: 'bacio schema', link: '/reference/cli/schema' },
      { text: 'bacio sync', link: '/reference/cli/sync' },
      { text: 'bacio api', link: '/reference/cli/api' },
      { text: 'bacio web', link: '/reference/cli/web' },
      { text: 'bacio install-skill', link: '/reference/cli/install-skill' },
      { text: 'bacio install-sample-skills', link: '/reference/cli/install-sample-skills' },
      { text: 'bacio tui', link: '/reference/cli/tui' },
    ],
  },
  {
    text: 'TUI reference',
    items: [
      { text: 'Board tab', link: '/reference/tui/board' },
      { text: 'Features tab', link: '/reference/tui/features' },
      { text: 'Docs tab', link: '/reference/tui/docs' },
      { text: 'History tab', link: '/reference/tui/history' },
      { text: 'Keybindings', link: '/reference/tui/keybindings' },
    ],
  },
  {
    text: 'Other reference',
    items: [
      { text: 'Configuration', link: '/reference/config' },
      { text: 'JSON payloads', link: '/reference/json-payloads' },
      { text: 'Exit codes', link: '/reference/exit-codes' },
      { text: 'Troubleshooting', link: '/reference/troubleshooting' },
    ],
  },
  {
    text: 'Concepts',
    items: [
      { text: 'Why bacio', link: '/concepts/why-bacio' },
      { text: 'Data model', link: '/concepts/data-model' },
      { text: 'Local-first and audit log', link: '/concepts/local-first-and-audit' },
      { text: 'How agents drive bacio', link: '/concepts/how-agents-drive-bacio' },
    ],
  },
  {
    text: 'More',
    items: [
      { text: 'FAQ', link: '/faq' },
      { text: 'Changelog', link: '/changelog' },
    ],
  },
];

export const sidebar = rawSidebar.map(withBadge);
