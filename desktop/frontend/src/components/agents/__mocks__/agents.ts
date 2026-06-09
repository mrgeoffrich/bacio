import type {
  AgentCard as AgentCardDTO,
  ClaimDTO,
  DispatchDTO,
} from '../../../api';

// TEMP demo data — appended to real agents when ?mock=1 is in the URL.
// Covers the spectrum of card states so the grid redesign can be
// reviewed against a realistic mix. Remove before merging.
export function mockAgents(): AgentCardDTO[] {
  const now = Date.now();
  const ago = (m: number) => new Date(now - m * 60_000).toISOString();
  // The card only reads a handful of dispatch / claim fields; these
  // helpers fill the remaining required DTO fields with inert defaults
  // so the literals stay readable while satisfying the domain types.
  const dispatch = (
    d: Pick<DispatchDTO, 'id' | 'issueKey' | 'mode' | 'status'> &
      Partial<DispatchDTO>,
  ): DispatchDTO => ({
    targetAgent: '',
    payload: '',
    createdBy: 'mock',
    createdAt: ago(60),
    needsRescue: false,
    ...d,
  });
  const claim = (
    c: Pick<ClaimDTO, 'issueKey' | 'state' | 'prompt'> & Partial<ClaimDTO>,
  ): ClaimDTO => ({ claimedAt: ago(60), ...c });
  return [
    {
      sessionId: 'mock-busy-todos',
      agentName: 'brave-otter@claude.shiny',
      actor: 'claude',
      model: 'claude-opus-4-7',
      branch: 'agents-grid-redesign',
      repoPrefix: 'BACI',
      status: 'active',
      busy: true,
      busyIssue: 'BACI-204',
      hasChannel: true,
      bacioVersion: 'v0.42.0',
      bacioVersionStale: false,
      lastSeenAt: ago(0),
      claims: [
        claim({
          issueKey: 'BACI-204',
          state: 'in_pipeline',
          prompt:
            'Plan a redesign of the Agents view to fit cards on a responsive grid.',
        }),
      ],
      dispatches: [
        dispatch({ id: 901, issueKey: 'BACI-204', mode: 'plan', status: 'delivered' }),
        dispatch({ id: 898, issueKey: 'BACI-201', mode: 'review', status: 'acked' }),
        dispatch({ id: 895, issueKey: 'BACI-199', mode: 'ship', status: 'acked' }),
      ],
      todos: [
        { content: 'Read AgentsView and surrounding CSS', status: 'completed' },
        { content: 'Sketch grid layout with self-contained cards', status: 'completed' },
        { content: 'Wire ?mock=1 demo data for review', status: 'in_progress' },
        { content: 'Update CSS for status accents and progress bar', status: 'pending' },
        { content: 'Verify against multiple states (busy / waiting / questions)', status: 'pending' },
      ],
      todosDone: 2,
      todosTotal: 5,
      openQuestions: [],
    },
    {
      sessionId: 'mock-busy-question',
      agentName: 'curious-quokka@claude.shiny',
      actor: 'claude',
      model: 'claude-sonnet-4-6',
      branch: 'worktree-agent-7c41ab',
      repoPrefix: 'BACI',
      status: 'active',
      busy: true,
      busyIssue: 'BACI-187',
      hasChannel: true,
      bacioVersion: 'v0.42.0',
      bacioVersionStale: false,
      lastSeenAt: ago(2),
      claims: [
        claim({ issueKey: 'BACI-187', state: 'in_pipeline', prompt: 'Pick the auth strategy.' }),
      ],
      dispatches: [
        dispatch({ id: 884, issueKey: 'BACI-187', mode: 'design', status: 'delivered' }),
      ],
      todos: [],
      todosDone: 0,
      todosTotal: 0,
      openQuestions: [
        { id: 9991, header: 'Auth library', issueKey: 'BACI-187', askedAt: ago(2) },
      ],
    },
    {
      sessionId: 'mock-idle-clean',
      agentName: 'mellow-marten@claude.shiny',
      actor: 'claude',
      model: 'claude-opus-4-7',
      branch: 'main',
      repoPrefix: 'BACI',
      status: 'idle',
      busy: false,
      busyIssue: '',
      hasChannel: true,
      bacioVersion: 'v0.42.0',
      bacioVersionStale: false,
      lastSeenAt: ago(8),
      claims: [],
      dispatches: [
        dispatch({ id: 870, issueKey: 'BACI-198', mode: 'fix_review', status: 'acked' }),
        dispatch({ id: 866, issueKey: 'BACI-197', mode: 'implement', status: 'acked' }),
      ],
      todos: [],
      todosDone: 0,
      todosTotal: 0,
      openQuestions: [],
    },
    {
      sessionId: 'mock-rescue-needed',
      agentName: 'gentle-fox@claude.shiny',
      actor: 'claude',
      model: 'claude-opus-4-7',
      branch: 'worktree-agent-19c2d4',
      repoPrefix: 'BACI',
      status: 'active',
      busy: true,
      busyIssue: 'BACI-156',
      hasChannel: false,
      bacioVersion: 'v0.40.1',
      bacioVersionStale: true,
      lastSeenAt: ago(14),
      claims: [
        claim({ issueKey: 'BACI-156', state: 'in_pipeline', prompt: 'Implement the sync diff renderer.' }),
      ],
      dispatches: [
        dispatch({
          id: 842,
          issueKey: 'BACI-156',
          mode: 'implement',
          status: 'delivered',
          needsRescue: true,
        }),
        dispatch({ id: 838, issueKey: 'BACI-155', mode: 'design', status: 'acked' }),
      ],
      todos: [
        { content: 'Wire the new diff API', status: 'completed' },
        { content: 'Render the deletion side', status: 'in_progress' },
        { content: 'Snapshot tests', status: 'pending' },
      ],
      todosDone: 1,
      todosTotal: 3,
      openQuestions: [],
    },
    {
      sessionId: 'mock-supervisor',
      agentName: 'wise-puffin@claude.supervisor',
      actor: 'claude',
      model: 'claude-opus-4-7',
      branch: 'main',
      repoPrefix: 'BACI',
      status: 'active',
      busy: false,
      busyIssue: '',
      hasChannel: true,
      bacioVersion: 'v0.42.0',
      bacioVersionStale: false,
      lastSeenAt: ago(1),
      claims: [],
      dispatches: [
        dispatch({ id: 904, issueKey: 'BACI-205', mode: 'scope', status: 'queued' }),
        dispatch({ id: 902, issueKey: 'BACI-203', mode: 'plan', status: 'pending' }),
        dispatch({ id: 900, issueKey: 'BACI-202', mode: 'ship', status: 'acked' }),
        dispatch({ id: 896, issueKey: 'BACI-200', mode: 'review', status: 'acked' }),
        dispatch({ id: 893, issueKey: 'BACI-199', mode: 'implement', status: 'acked' }),
      ],
      todos: [],
      todosDone: 0,
      todosTotal: 0,
      openQuestions: [],
    },
  ];
}
