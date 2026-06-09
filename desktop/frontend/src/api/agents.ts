// Agents-domain Wails calls (BACI-359): agent cards, ask_user_question,
// dispatch enqueue / cancel / rescue, and the BACI-286 steer message. The
// question / message wrappers return the Wails-bound shapes verbatim.
import { BoardService } from '../../bindings/github.com/mrgeoffrich/bacio/desktop';
import type { AgentCard, DispatchDTO } from './contract';
import { normalize } from './normalize';

export async function listAgents(repoPrefix: string): Promise<AgentCard[]> {
  try {
    return await BoardService.ListAgents(repoPrefix);
  } catch (err) {
    throw normalize(err);
  }
}

export async function getSessionQuestion(id: number) {
  try {
    return await BoardService.GetSessionQuestion(id);
  } catch (err) {
    throw normalize(err);
  }
}

export async function answerSessionQuestion(id: number, answers: Record<string, unknown>) {
  try {
    return await BoardService.AnswerSessionQuestion(id, answers);
  } catch (err) {
    throw normalize(err);
  }
}

export async function cancelSessionQuestion(id: number) {
  try {
    return await BoardService.CancelSessionQuestion(id);
  } catch (err) {
    throw normalize(err);
  }
}

// dispatchIssue queues a dispatch against an issue for a job stage. The
// agent is auto-picked by the backend (the most-recently-active free
// agent) — the caller never names one. Post-BACI-51 this is the enqueue
// path: the dispatch is always queued; the matcher binds an agent later.
export async function dispatchIssue(
  repoPrefix: string,
  issueKey: string,
  mode: string,
): Promise<DispatchDTO> {
  try {
    return await BoardService.DispatchIssue(repoPrefix, issueKey, mode);
  } catch (err) {
    throw normalize(err);
  }
}

// cancelWaitingDispatch (BACI-51) withdraws an issue's queued / pending /
// delivered dispatch — the spinner-as-cancel-button click handler. The
// backend resolves the dispatch id from the issue + cancels in one call
// so we don't round-trip the id through card DTOs.
export async function cancelWaitingDispatch(
  repoPrefix: string,
  issueKey: string,
): Promise<void> {
  try {
    await BoardService.CancelWaitingDispatch(repoPrefix, issueKey);
  } catch (err) {
    throw normalize(err);
  }
}

// rescueDispatch (BACI-190) posts a `from="bacio-rescue"` channel event
// to an idle supervisor session asking it to handle a dead worker's
// stranded worktree INLINE. The AgentsView dispatch row renders a
// "Rescue" button on dispatches whose target session has ended without
// the dispatch being acked (the NeedsRescue flag on DispatchDTO).
// Eligibility re-checks live on the backend so a stale UI click can't
// queue an invalid rescue; errors surface as Error.message and bubble
// through reportError() in App.tsx.
export async function rescueDispatch(dispatchID: number): Promise<DispatchDTO> {
  try {
    return await BoardService.RescueDispatch(dispatchID);
  } catch (err) {
    throw normalize(err);
  }
}

// sendSessionMessage (BACI-286) pushes a user→agent steer message at a
// busy session. The channel serving that session injects it as a
// `<channel kind="message">` tag at the worker's next turn boundary —
// NOT a dispatch. Backs the "message" button on the Pipeline running-job
// card (targets card.runningSessionId) and the Agents-page session card
// (targets a.sessionId). Errors (unknown session, empty/over-cap body)
// surface as Error.message for a toast.
export async function sendSessionMessage(sessionID: string, body: string) {
  try {
    return await BoardService.SendSessionMessage(sessionID, body);
  } catch (err) {
    throw normalize(err);
  }
}
