import { useCallback } from 'react';
import type { Dispatch, SetStateAction } from 'react';
import { useNavigate } from 'react-router';
import * as api from '../api';
import { usePolledResource } from '../lib/hooks/usePolledResource';
import { issuePath } from '../lib/routes';

export type NotificationsState = {
  notifUnreadCount: number;
  setNotifUnreadCount: Dispatch<SetStateAction<number>>;
  openNotificationIssue: (prefix: string, key: string) => void;
};

// useNotifications (BACI-361) owns the notification-bell unread count App.tsx
// used to carry. It is global / cross-repo (the bell lists notifications from
// every repo, no board gate) and Topbar-local — the only consumer is the
// NotificationBell — so it lives as a self-stateful hook rather than a
// context.
//
// BACI-356: usePolledResource owns the eager load + the always-on 10s poll +
// the best-effort error swallow (the badge is non-critical; the dropdown
// surfaces real failures). setNotifUnreadCount (the hook's setData) keeps the
// bell's direct after-mark-read update flowing through.
export function useNotifications(): NotificationsState {
  const navigate = useNavigate();

  const { data: notifUnreadCount, setData: setNotifUnreadCount } = usePolledResource<number>(
    () => api.countUnreadNotifications(),
    0,
    [],
    { onError: () => {} },
  );

  // BACI-287: deep-link a notification row to its issue. Cross-repo — the row
  // carries its own prefix, which may differ from the active board, so
  // navigate to that prefix's workspace route directly.
  const openNotificationIssue = useCallback((prefix: string, key: string) => {
    if (!prefix || !key) return;
    navigate(issuePath(prefix, key));
  }, [navigate]);

  return { notifUnreadCount, setNotifUnreadCount, openNotificationIssue };
}
