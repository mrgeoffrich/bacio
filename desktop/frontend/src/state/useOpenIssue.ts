import { useCallback, useEffect, useState } from 'react';
import { useParams } from 'react-router';
import * as api from '../api';
import type { IssueBriefDTO } from '../api';
import { reportError } from '../errors';
import { useInterval, POLL_INTERVAL_MS } from '../lib/hooks/useInterval';
import { useActiveRepo } from './RepoProvider';
import { useCards } from './CardsProvider';

export type OpenIssue = {
  openIssueKey: string;
  brief: IssueBriefDTO | null;
  // Propagated up from the workspace's InlineDescriptionEditor so the brief
  // poll merge can preserve the in-progress textarea.
  setDescEditing: (editing: boolean) => void;
  saveTitle: (title: string) => Promise<void>;
  saveDescription: (description: string) => Promise<void>;
  saveCustomerImpact: (customerImpact: string) => Promise<void>;
  addComment: (
    author: string,
    body: string,
    opts?: { eval?: boolean; transcriptEventRef?: string },
  ) => Promise<void>;
  deleteComment: (commentUUID: string) => Promise<void>;
  attachPR: (url: string) => Promise<void>;
};

// useOpenIssue (BACI-361) is the route-scoped brief hook IssueWorkspace owns.
// App used to carry the open issue's brief at the App level + a descEditing
// flag ping-ponged through the workspace; now the hook lives *inside* the
// /:prefix/issues/:key route element, so the eager load + 10s poll + the
// descEditing guard all tear down automatically on route unmount (close /
// repo-change) — no App↔workspace prop plumbing. The issue key comes from
// useParams(), the active repo from useActiveRepo(), and the cards refresh
// from useCards() (the workspace writes refresh both the brief and the cards).
export function useOpenIssue(): OpenIssue {
  const { activeBoard } = useActiveRepo();
  const { refreshCards } = useCards();
  const { key } = useParams();
  const openIssueKey = key ?? '';

  const [brief, setBrief] = useState<IssueBriefDTO | null>(null);
  const [descEditing, setDescEditing] = useState(false);

  // refreshBrief reloads the workspace payload. Pass { silent: true } on the
  // poll path so a transient failure logs instead of pushing through the
  // modal. The descEditing guard preserves the user's in-progress description
  // textarea: when set, the new brief is taken but its description is replaced
  // by the previous one, so a poll landing mid-edit doesn't stomp the buffer.
  const refreshBrief = useCallback((opts: { silent?: boolean } = {}) => {
    if (!activeBoard || !openIssueKey) return;
    api.getIssueBrief(activeBoard, openIssueKey)
      .then(next => {
        setBrief(prev => {
          if (descEditing && prev) {
            return {
              ...next,
              issue: { ...next.issue, description: prev.issue.description },
            };
          }
          return next;
        });
      })
      .catch(err => {
        if (opts.silent) console.warn('brief refresh failed:', err);
        else reportError(err, { headline: "Couldn't refresh issue" });
      });
  }, [activeBoard, openIssueKey, descEditing]);

  // Eager load when the open issue changes (mount, or one issue → another via
  // prev/next). Clear the stale brief so the workspace skeleton renders
  // instead of the last issue's data flashing.
  useEffect(() => {
    if (!activeBoard || !openIssueKey) return;
    setBrief(null);
    refreshBrief();
    // refreshBrief intentionally omitted: this effect should re-run only when
    // the open issue changes, not when the callback identity does.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [activeBoard, openIssueKey]);

  // Poll the brief every 10s while the workspace route is mounted. The hook is
  // route-scoped, so the timer tears down on close / repo change / unmount —
  // no activeView gate needed (the route only mounts on the workspace).
  useInterval(() => refreshBrief({ silent: true }), POLL_INTERVAL_MS, !!activeBoard && !!openIssueKey);

  // Workspace write callbacks — each wraps the api.* call and refreshes the
  // brief so the inline view re-renders with the persisted state. Failures
  // surface through reportError; the workspace components catch their own
  // setBusy flags.
  const saveDescription = useCallback(async (description: string) => {
    if (!openIssueKey) return;
    try {
      await api.updateIssueDescription(activeBoard, openIssueKey, description);
      refreshBrief();
      // The Board card carries no description column, but the card list still
      // benefits from a refresh so any concurrent state changes land.
      refreshCards({ silent: true });
    } catch (err) {
      reportError(err, { headline: "Couldn't save description" });
      throw err;
    }
  }, [activeBoard, openIssueKey, refreshBrief, refreshCards]);

  // BACI-292: title editing from the workspace header. The card list carries
  // the title, so the cards refresh re-renders the kanban card immediately.
  const saveTitle = useCallback(async (title: string) => {
    if (!openIssueKey) return;
    try {
      await api.updateIssueTitle(activeBoard, openIssueKey, title);
      refreshBrief();
      refreshCards({ silent: true });
    } catch (err) {
      reportError(err, { headline: "Couldn't save title" });
      throw err;
    }
  }, [activeBoard, openIssueKey, refreshBrief, refreshCards]);

  // BACI-349: customer-impact editing from the workspace header. An empty
  // value is a legitimate save (clears the field).
  const saveCustomerImpact = useCallback(async (customerImpact: string) => {
    if (!openIssueKey) return;
    try {
      await api.updateIssueCustomerImpact(activeBoard, openIssueKey, customerImpact);
      refreshBrief();
      refreshCards({ silent: true });
    } catch (err) {
      reportError(err, { headline: "Couldn't save customer impact" });
      throw err;
    }
  }, [activeBoard, openIssueKey, refreshBrief, refreshCards]);

  // BACI-141: opts carries the eval flag and the transcript_event_ref the
  // per-event composer fills in. The CommentComposer path passes (author,
  // body) and lands a plain row.
  const addComment = useCallback(async (
    author: string,
    body: string,
    opts?: { eval?: boolean; transcriptEventRef?: string },
  ) => {
    if (!openIssueKey) return;
    try {
      await api.addComment(activeBoard, openIssueKey, author, body, opts);
      refreshBrief();
    } catch (err) {
      reportError(err, { headline: "Couldn't add comment" });
      throw err;
    }
  }, [activeBoard, openIssueKey, refreshBrief]);

  const deleteComment = useCallback(async (commentUUID: string) => {
    if (!openIssueKey || !commentUUID) return;
    try {
      await api.deleteComment(activeBoard, openIssueKey, commentUUID);
      refreshBrief();
    } catch (err) {
      reportError(err, { headline: "Couldn't delete comment" });
      throw err;
    }
  }, [activeBoard, openIssueKey, refreshBrief]);

  const attachPR = useCallback(async (url: string) => {
    if (!openIssueKey) return;
    await api.attachPullRequest(activeBoard, openIssueKey, url);
    refreshBrief();
  }, [activeBoard, openIssueKey, refreshBrief]);

  return {
    openIssueKey,
    brief,
    setDescEditing,
    saveTitle,
    saveDescription,
    saveCustomerImpact,
    addComment,
    deleteComment,
    attachPR,
  };
}
