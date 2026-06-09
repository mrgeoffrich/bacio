// Notification-bell Wails calls (BACI-359, BACI-287). listNotifications
// filters the nullable binding slice down to the non-null shape the bell
// consumes — the guard narrows against the binding type.
import { BoardService } from '../../bindings/github.com/mrgeoffrich/bacio/desktop';
import type { Notification as WireNotification } from '../../bindings/github.com/mrgeoffrich/bacio/internal/model';
import type { Notification } from './contract';
import { normalize } from './normalize';

// BACI-287 notification bell. The list/count/mark wrappers back the global
// (cross-repo) <NotificationBell> in the topbar — agent→user notifications
// sent via the send_user_notification channel tool. listNotifications
// defaults to unread (the bell's default view); pass unreadOnly=false for
// "show all". markAllNotificationsRead returns the count flipped.
export async function listNotifications(unreadOnly = true, limit = 0): Promise<Notification[]> {
  try {
    // The Wails binding types the slice as (Notification | null)[] because
    // the Go element is a pointer; the store never returns nil rows, so
    // filter defensively to land the non-null shape the bell consumes.
    const rows = await BoardService.ListNotifications(unreadOnly, limit);
    return (rows ?? []).filter((n): n is WireNotification => n != null);
  } catch (err) {
    throw normalize(err);
  }
}

export async function countUnreadNotifications(): Promise<number> {
  try {
    return await BoardService.CountUnreadNotifications();
  } catch (err) {
    throw normalize(err);
  }
}

export async function markNotificationRead(id: number): Promise<Notification | null> {
  try {
    return await BoardService.MarkNotificationRead(id);
  } catch (err) {
    throw normalize(err);
  }
}

export async function markAllNotificationsRead(): Promise<number> {
  try {
    return await BoardService.MarkAllNotificationsRead();
  } catch (err) {
    throw normalize(err);
  }
}
