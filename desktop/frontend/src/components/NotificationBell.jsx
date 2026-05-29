import React, { useEffect, useRef, useState } from 'react';
import { reportError } from '../errors';
import * as api from '../api';
import { formatWhen } from '../lib/formatWhen';
import Icon from './Icon.jsx';
import Tooltip from './Tooltip.jsx';

// NotificationBell (BACI-287) is the topbar's global agent→user
// notification bell — a bell button with an unread-count badge plus an
// anchored dropdown listing notifications. Hand-rolled with the same
// ref + mousedown + Escape recipe as ShippedPopover / RepoPicker rather
// than pulling a popover dep.
//
// The bell is deliberately cross-repo: it lists notifications from every
// repo (each row labelled with its repo prefix), mirroring the /history
// all-repos view. Opening the bell does NOT auto-clear the badge — read
// state only changes on an explicit per-row "mark read" or the "mark all
// read" footer (BACI-287 resolved design).
//
// Props:
//   unreadCount — the badge number; App.jsx polls /notifications/count on
//                 the same 10s cadence as the other live readouts so the
//                 badge stays in lockstep without the dropdown being open.
//   onCountChange — App-level setter so a mark-read / mark-all updates the
//                 badge immediately rather than waiting for the next poll.
//   onOpenIssue — invoked with (repoPrefix, key) when an issue-linked row
//                 is clicked; the bell closes itself afterwards. Cross-repo
//                 — the row carries its own prefix, which may differ from
//                 the active board.
export default function NotificationBell({ unreadCount, onCountChange, onOpenIssue }) {
  const [open, setOpen] = useState(false);
  // status: 'idle' | 'loading' | 'ready' | 'error'
  const [status, setStatus] = useState('idle');
  const [rows, setRows] = useState([]);
  const [error, setError] = useState('');
  // showAll toggles the unread-default / show-everything filter.
  const [showAll, setShowAll] = useState(false);

  const rootRef = useRef(null);

  // Outside-click + Escape — same recipe as ShippedPopover.
  useEffect(() => {
    if (!open) return;
    const onDown = (e) => {
      if (rootRef.current && !rootRef.current.contains(e.target)) setOpen(false);
    };
    const onKey = (e) => {
      if (e.key === 'Escape') setOpen(false);
    };
    document.addEventListener('mousedown', onDown);
    document.addEventListener('keydown', onKey);
    return () => {
      document.removeEventListener('mousedown', onDown);
      document.removeEventListener('keydown', onKey);
    };
  }, [open]);

  // fetchRows pulls the list under the current filter. Invoked on open and
  // after the show-all toggle / mark actions so the dropdown stays fresh.
  const fetchRows = () => {
    let cancelled = false;
    setStatus('loading');
    setError('');
    api.listNotifications(!showAll)
      .then((next) => {
        if (cancelled) return;
        setRows(next ?? []);
        setStatus('ready');
      })
      .catch((err) => {
        if (cancelled) return;
        reportError(err, { headline: "Couldn't load notifications" });
        setError(err?.message || String(err));
        setStatus('error');
      });
    return () => { cancelled = true; };
  };

  // First-open + show-all toggle fetch. Re-runs whenever the filter flips
  // while open so toggling "show all" repaints immediately.
  useEffect(() => {
    if (!open) return;
    return fetchRows();
    // fetchRows reads showAll; re-run on open + showAll change.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [open, showAll]);

  // Refresh the cross-repo unread count after a mutation so the badge
  // tracks the dropdown without waiting for the next poll.
  const refreshCount = () => {
    api.countUnreadNotifications()
      .then((n) => { if (typeof onCountChange === 'function') onCountChange(n); })
      .catch(() => { /* badge is best-effort */ });
  };

  const markRead = (id) => {
    api.markNotificationRead(id)
      .then(() => {
        // Reflect locally: in unread view the row drops out; in show-all
        // it stays but loses its unread styling. Simplest correct refetch.
        fetchRows();
        refreshCount();
      })
      .catch((err) => reportError(err, { headline: "Couldn't mark notification read" }));
  };

  const markAll = () => {
    api.markAllNotificationsRead()
      .then(() => {
        fetchRows();
        refreshCount();
      })
      .catch((err) => reportError(err, { headline: "Couldn't mark notifications read" }));
  };

  const pickRow = (row) => {
    // An issue-linked row deep-links to its ticket (cross-repo — use the
    // row's own prefix). Marking it read is the same explicit affordance as
    // the per-row button; opening the issue does not auto-clear it.
    if (row.issue_key && row.repo_prefix && typeof onOpenIssue === 'function') {
      onOpenIssue(row.repo_prefix, row.issue_key);
      setOpen(false);
    }
  };

  const safeCount = Number.isFinite(unreadCount) && unreadCount > 0 ? unreadCount : 0;
  const tooltip = safeCount > 0
    ? `${safeCount} unread ${safeCount === 1 ? 'notification' : 'notifications'}`
    : 'Notifications';

  return (
    <div className="mk-notif-root" ref={rootRef}>
      <Tooltip label={tooltip}>
        <button
          type="button"
          className="mk-icbtn mk-notif-btn"
          aria-label={tooltip}
          aria-haspopup="dialog"
          aria-expanded={open}
          onClick={() => setOpen(o => !o)}
        >
          <Icon name="bell" />
          {safeCount > 0 && (
            <span className="mk-notif-badge" aria-hidden="true">
              {safeCount > 99 ? '99+' : safeCount}
            </span>
          )}
        </button>
      </Tooltip>
      {open && (
        <div className="mk-notif-popover" role="dialog" aria-label="Notifications">
          <div className="mk-notif-head">
            <span className="mk-notif-title">Notifications</span>
            <div className="mk-notif-filter" role="tablist" aria-label="Notification filter">
              <button
                type="button"
                role="tab"
                aria-selected={!showAll}
                className={`mk-notif-filter-btn${!showAll ? ' is-active' : ''}`}
                onClick={() => setShowAll(false)}
              >
                Unread
              </button>
              <button
                type="button"
                role="tab"
                aria-selected={showAll}
                className={`mk-notif-filter-btn${showAll ? ' is-active' : ''}`}
                onClick={() => setShowAll(true)}
              >
                All
              </button>
            </div>
          </div>
          <div className="mk-notif-body">
            {status === 'loading' && (
              [0, 1, 2].map((i) => (
                <div key={i} className="mk-notif-row is-skeleton">
                  <span className="mk-notif-row-body" />
                  <span className="mk-notif-row-meta" />
                </div>
              ))
            )}
            {status === 'error' && (
              <div className="mk-notif-empty">
                Couldn’t load notifications.{' '}
                <button type="button" className="mk-link" onClick={fetchRows}>Retry</button>
                {error && <div className="mk-notif-error-detail">{error}</div>}
              </div>
            )}
            {status === 'ready' && rows.length === 0 && (
              <div className="mk-notif-empty">
                {showAll ? 'No notifications yet.' : 'No unread notifications.'}
              </div>
            )}
            {status === 'ready' && rows.length > 0 && rows.map((r) => {
              const unread = !r.read_at;
              const linked = !!(r.issue_key && r.repo_prefix);
              return (
                <div
                  key={r.id}
                  className={`mk-notif-row${unread ? ' is-unread' : ''}${linked ? ' is-linked' : ''}`}
                  onClick={() => pickRow(r)}
                  role={linked ? 'button' : undefined}
                  tabIndex={linked ? 0 : undefined}
                  onKeyDown={linked ? (e) => { if (e.key === 'Enter') pickRow(r); } : undefined}
                >
                  <div className="mk-notif-row-body">{r.body}</div>
                  <div className="mk-notif-row-meta">
                    <span className="mk-notif-row-repo mk-card-id">{r.repo_prefix}</span>
                    {r.issue_key && (
                      <>
                        <span className="mk-notif-row-sep" aria-hidden="true">·</span>
                        <span className="mk-notif-row-issue mk-card-id">{r.issue_key}</span>
                      </>
                    )}
                    <span className="mk-notif-row-sep" aria-hidden="true">·</span>
                    <span className="mk-notif-row-when">{formatWhen(r.created_at)}</span>
                    {unread && (
                      <button
                        type="button"
                        className="mk-notif-row-read"
                        onClick={(e) => { e.stopPropagation(); markRead(r.id); }}
                      >
                        Mark read
                      </button>
                    )}
                  </div>
                </div>
              );
            })}
          </div>
          {status === 'ready' && rows.some((r) => !r.read_at) && (
            <div className="mk-notif-footer">
              <button type="button" className="mk-link" onClick={markAll}>
                Mark all read
              </button>
            </div>
          )}
        </div>
      )}
    </div>
  );
}
