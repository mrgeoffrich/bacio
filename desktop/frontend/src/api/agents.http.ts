// Agents-domain HTTP transport (BACI-359). Fetch wrappers + reshapers over
// the `bacio api` REST surface; the public ./api surface is the same as the
// Wails seam's. See ./client.http for the shared plumbing.
import { call } from './client.http';
import { reshapeDispatch } from './wire/dispatch';
import type { ApiDispatch, ApiUserMessage } from './wire/dispatch';
import type { DispatchDTO, SessionQuestionRow, AgentCard } from './contract';

export async function listAgents(repoPrefix: string): Promise<AgentCard[]> {
  // BACI-50: bacio api now ships the composite endpoint that the
  // desktop's BoardService.ListAgents used to build locally. The
  // assembled card shape uses camelCase JSON tags matching the
  // existing AgentCard TS type, so no per-field reshape is needed —
  // the API response IS the AgentCard.
  const path = (!repoPrefix || repoPrefix === 'all')
    ? '/agents/cards'
    : `/repos/${repoPrefix}/agents/cards`;
  const cards = await call<AgentCard[]>(path);
  // Normalise nullable arrays so consumers can iterate without
  // defensive checks — matches what the Wails binding returns.
  return (cards ?? []).map(c => ({
    ...c,
    claims: c.claims ?? [],
    dispatches: c.dispatches ?? [],
    todos: c.todos ?? [],
    openQuestions: c.openQuestions ?? [],
  }));
}

export async function getSessionQuestion(id: number): Promise<SessionQuestionRow> {
  return await call<SessionQuestionRow>(`/agents/questions/${id}`);
}

export async function answerSessionQuestion(
  id: number,
  answers: Record<string, unknown>,
): Promise<SessionQuestionRow> {
  return await call<SessionQuestionRow>(`/agents/questions/${id}/answer`, {
    method: 'POST',
    body: { answers },
  });
}

export async function cancelSessionQuestion(id: number): Promise<SessionQuestionRow> {
  return await call<SessionQuestionRow>(`/agents/questions/${id}/cancel`, {
    method: 'POST',
  });
}

// dispatchIssue queues a state-gated auto-pick dispatch (BACI-40). The
// server re-checks the stage's state-gate against the issue's current
// state and picks the most-recently-active free agent — the caller
// names neither an agent nor a note. Errors from the server (no free
// agent / state-gate mismatch) come back as the error envelope and
// surface through reportError() up in App.tsx.
export async function dispatchIssue(
  repoPrefix: string,
  issueKey: string,
  mode: string,
): Promise<DispatchDTO> {
  if (!repoPrefix || repoPrefix === 'all') {
    const i = issueKey.lastIndexOf('-');
    if (i <= 0) throw new Error(`invalid issue key: ${issueKey}`);
    repoPrefix = issueKey.slice(0, i);
  }
  const raw = await call<ApiDispatch>(
    `/repos/${repoPrefix}/issues/${issueKey}/dispatch`,
    { method: 'POST', body: { mode } },
  );
  return reshapeDispatch(raw);
}

// cancelWaitingDispatch (BACI-51) is the spinner-as-cancel-button
// handler in web mode: two round-trips — GET the active dispatch on
// the issue, POST cancel against its id. A 404 from the GET means the
// dispatch cleared between the click and the call landing (e.g. the
// matcher bound it) — that's a no-op success, not an error.
//
// BACI-130: the POST can also race a delivery — the matcher binds a
// queued row to a worker and immediately delivers, between the
// spinner-button render and the click landing. The server now
// rejects cancel-after-delivery with a 409 ("dispatch N has been
// delivered; cannot cancel"). Swallow that the same way as the 404
// — the UI's spinner-cancel button is best-effort, not a "did you
// really want to do this" confirm.
export async function cancelWaitingDispatch(
  repoPrefix: string,
  issueKey: string,
): Promise<void> {
  if (!repoPrefix || repoPrefix === 'all') {
    const i = issueKey.lastIndexOf('-');
    if (i <= 0) throw new Error(`invalid issue key: ${issueKey}`);
    repoPrefix = issueKey.slice(0, i);
  }
  let dsp: ApiDispatch;
  try {
    dsp = await call<ApiDispatch>(
      `/repos/${repoPrefix}/issues/${issueKey}/waiting-dispatch`,
      { method: 'GET' },
    );
  } catch (err: unknown) {
    // call() throws an Error whose message is the server envelope's
    // `error` field — the 404 body says "no waiting dispatch for <key>".
    // Match on that prefix to treat the race-cleared case as a no-op
    // (the matcher bound or another UI cancelled between click + call).
    const msg = err instanceof Error ? err.message : String(err);
    if (msg.startsWith('no waiting dispatch')) return;
    throw err;
  }
  try {
    await call<unknown>(`/agents/dispatches/${dsp.id}/cancel`, { method: 'POST' });
  } catch (err: unknown) {
    // BACI-130: a 409 "delivered, cannot cancel" race is a no-op
    // success — the worker took the Task between render and click.
    const msg = err instanceof Error ? err.message : String(err);
    if (msg.includes('has been delivered')) return;
    throw err;
  }
}

// rescueDispatch (BACI-190) posts a `from="bacio-rescue"` channel event
// to an idle supervisor session asking it to handle a dead worker's
// stranded worktree INLINE. Backend re-checks eligibility (status,
// creator, dead session, idle supervisor) and returns 409 when the
// rescue can't be queued — surfaced via the usual error envelope.
export async function rescueDispatch(dispatchID: number): Promise<DispatchDTO> {
  const raw = await call<ApiDispatch>(
    `/agents/dispatches/${dispatchID}/rescue`,
    { method: 'POST' },
  );
  return reshapeDispatch(raw);
}

// sendSessionMessage (BACI-286) POSTs a user→agent steer message at a
// busy session. The channel serving that session pushes it as a
// `<channel kind="message">` tag at the worker's next turn boundary —
// NOT a dispatch. Unknown session → 404, empty/over-cap body → 400, both
// surfaced via the usual error envelope.
export async function sendSessionMessage(
  sessionID: string,
  body: string,
): Promise<ApiUserMessage> {
  return await call<ApiUserMessage>(
    `/agents/sessions/${encodeURIComponent(sessionID)}/messages`,
    { method: 'POST', body: { body } },
  );
}
