// Issue-domain Wails calls (BACI-359): issue reads + edits, comments, PRs.
import { BoardService } from '../../bindings/github.com/mrgeoffrich/bacio/desktop';
import type { IssueDetail, IssueBriefDTO, PRDTO, BoardCard } from './contract';
import { normalize } from './normalize';

export async function getIssue(repoPrefix: string, key: string): Promise<IssueDetail> {
  try {
    return await BoardService.GetIssue(repoPrefix, key);
  } catch (err) {
    throw normalize(err);
  }
}

// getIssueBrief returns the workspace-shaped payload — issue meta +
// feature + relations + inlined linked-doc bodies + comments +
// claimants + the derived `taken` flag + the structured WaitingState
// (BACI-145). Backs the top-level IssueWorkspace view.
export async function getIssueBrief(repoPrefix: string, key: string): Promise<IssueBriefDTO> {
  try {
    return await BoardService.GetIssueBrief(repoPrefix, key);
  } catch (err) {
    throw normalize(err);
  }
}

// attachPullRequest attaches a pull-request URL to an issue and returns
// the resolved PRDTO. Used by the IssueWorkspace's PR-attach form;
// validation errors (bad scheme / missing host) surface verbatim.
export async function attachPullRequest(
  repoPrefix: string,
  key: string,
  url: string,
): Promise<PRDTO> {
  try {
    return await BoardService.AttachPullRequest(repoPrefix, key, url);
  } catch (err) {
    throw normalize(err);
  }
}

// addIssue (BACI-166) creates a new issue and returns the freshly-shaped
// BoardCard. Backs the "+ from prompt" composer in the Topbar: the
// composer calls addIssue then chains dispatchIssue(_, _, 'scope') to
// queue the scope worker on the new issue. Mirrors dispatchIssue's
// shape line-for-line; validation (empty title, control chars, etc.)
// lives at the store boundary inside BoardService.AddIssue.
// autoRun (BACI-374) arms the new card to run the full
// Scope → Plan → Implement → Ship chain immediately; the returned card
// then comes back in the Pipeline column. Defaults to false here so a
// caller that doesn't opt in gets today's inert Backlog card — the
// composer is the surface that defaults it on.
export async function addIssue(
  repoPrefix: string,
  title: string,
  description: string,
  featureSlug = '',
  autoRun = false,
): Promise<BoardCard> {
  try {
    return await BoardService.AddIssue(repoPrefix, title, description, featureSlug, autoRun);
  } catch (err) {
    throw normalize(err);
  }
}

// setIssueState changes an issue's state — backs the board's drag-to-move,
// persisting the column change so it survives the next refresh poll.
export async function setIssueState(
  repoPrefix: string,
  key: string,
  state: string,
): Promise<BoardCard> {
  try {
    return await BoardService.SetIssueState(repoPrefix, key, state);
  } catch (err) {
    throw normalize(err);
  }
}

// updateIssueDescription replaces an issue's description and returns the
// refreshed issue-drawer payload.
export async function updateIssueDescription(
  repoPrefix: string,
  key: string,
  description: string,
): Promise<IssueDetail> {
  try {
    return await BoardService.UpdateIssueDescription(repoPrefix, key, description);
  } catch (err) {
    throw normalize(err);
  }
}

// updateIssueTitle replaces an issue's title and returns the refreshed
// issue-drawer payload.
export async function updateIssueTitle(
  repoPrefix: string,
  key: string,
  title: string,
): Promise<IssueDetail> {
  try {
    return await BoardService.UpdateIssueTitle(repoPrefix, key, title);
  } catch (err) {
    throw normalize(err);
  }
}

// updateIssueCustomerImpact (BACI-349) replaces an issue's one-line
// customer impact and returns the refreshed issue-drawer payload. An
// empty value is legitimate — it clears the field — so it's always sent.
export async function updateIssueCustomerImpact(
  repoPrefix: string,
  key: string,
  customerImpact: string,
): Promise<IssueDetail> {
  try {
    return await BoardService.UpdateIssueCustomerImpact(repoPrefix, key, customerImpact);
  } catch (err) {
    throw normalize(err);
  }
}

// addComment appends a comment to an issue and returns the refreshed
// issue-drawer payload. An empty author falls back to the OS username.
// opts.eval (BACI-131) flags the row as a quality-review note posted
// from the kanban quick-eval composer — the server pins the in-flight
// (agent_session_id, dispatch_id, mode) snapshot onto the comment.
// opts.transcriptEventRef (BACI-141) is the optional per-event anchor
// the transcript viewer's per-event composer sets — empty leaves the
// note rendered pinned to the dispatch prompt card.
export async function addComment(
  repoPrefix: string,
  key: string,
  author: string,
  body: string,
  opts?: { eval?: boolean; transcriptEventRef?: string },
): Promise<IssueDetail> {
  try {
    return await BoardService.AddComment(
      repoPrefix,
      key,
      author,
      body,
      !!opts?.eval,
      opts?.transcriptEventRef ?? '',
    );
    // ^ Wails binding parameter is `isEval` (renamed from `eval` so the
    //   generated TS doesn't clash with the JavaScript reserved word).
  } catch (err) {
    throw normalize(err);
  }
}

// deleteComment removes a comment from an issue and returns the
// refreshed issue-drawer payload. The comment is addressed by its uuid.
export async function deleteComment(
  repoPrefix: string,
  key: string,
  commentUUID: string,
): Promise<IssueDetail> {
  try {
    return await BoardService.DeleteComment(repoPrefix, key, commentUUID);
  } catch (err) {
    throw normalize(err);
  }
}
