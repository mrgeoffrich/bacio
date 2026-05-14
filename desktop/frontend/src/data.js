// Fake board data for the UI kit
export const boards = [
  { id: 'b1', name: 'Bacio v2',       count: 14, active: true },
  { id: 'b2', name: 'Marketing site', count: 6 },
  { id: 'b3', name: 'Docs',           count: 3 },
];

export const columns = [
  { id: 'todo',    name: 'Todo',    status: 'todo' },
  { id: 'doing',   name: 'Doing',   status: 'doing' },
  { id: 'review',  name: 'Review',  status: 'review' },
  { id: 'done',    name: 'Done',    status: 'done' },
  { id: 'blocked', name: 'Blocked', status: 'blocked' },
];

export const cards = [
  { id: 'BAC-204', column: 'doing',   title: 'Drag-and-drop on touch devices feels jittery', tags: ['frontend'],       assignees: ['claude', 'JR'], pr: '#4821', comments: 4, claude: true },
  { id: 'BAC-198', column: 'review',  title: 'Persist column widths across reloads',         tags: ['frontend'],       assignees: ['SK'],           pr: '#4815', comments: 2 },
  { id: 'BAC-187', column: 'blocked', title: 'Auth flow returns 502 on cold start',          tags: ['infra', 'api'],   assignees: ['AT'],           note: 'blocked on infra', comments: 6 },
  { id: 'BAC-211', column: 'todo',    title: 'Empty state copy needs a pass',                tags: ['copy'],           assignees: [] },
  { id: 'BAC-210', column: 'todo',    title: 'Add keyboard shortcuts hint to toolbar',       tags: ['frontend'],       assignees: ['JR'] },
  { id: 'BAC-209', column: 'todo',    title: 'Webhook signature mismatch on retry',          tags: ['api'],            assignees: [],               priority: true },
  { id: 'BAC-202', column: 'doing',   title: 'Refactor column drag controller',              tags: ['frontend'],       assignees: ['claude'],       claude: true, comments: 1 },
  { id: 'BAC-201', column: 'doing',   title: 'Move agent run logs into card timeline',       tags: ['frontend', 'api'], assignees: ['claude', 'SK'], claude: true },
  { id: 'BAC-195', column: 'review',  title: 'Tighten spacing on the issue drawer',          tags: ['frontend'],       assignees: ['MK'],           pr: '#4817' },
  { id: 'BAC-188', column: 'done',    title: 'Wire up status pills to filter chips',         tags: ['frontend'],       assignees: ['JR'] },
  { id: 'BAC-186', column: 'done',    title: 'Ship board export to JSON',                    tags: ['api'],            assignees: ['SK'] },
  { id: 'BAC-185', column: 'done',    title: 'Add command palette',                          tags: ['frontend'],       assignees: ['claude'],       claude: true },
  { id: 'BAC-180', column: 'blocked', title: 'Handle GitHub rate-limit gracefully',          tags: ['api'],            assignees: [],               note: 'waiting on github support' },
  { id: 'BAC-176', column: 'review',  title: 'Tag colors are not WCAG AA in dark mode',      tags: ['a11y', 'frontend'], assignees: ['MK'],         pr: '#4812' },
];

export function columnStatus(colId) {
  const col = columns.find(c => c.id === colId);
  return col ? col.status : 'todo';
}
