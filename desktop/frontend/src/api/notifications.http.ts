// Notifications-domain HTTP transport (BACI-359). Fetch wrappers + reshapers over
// the `bacio api` REST surface; the public ./api surface is the same as the
// Wails seam's. See ./client.http for the shared plumbing.
import { call } from './client.http';
import type { Notification } from './contract';

// BACI-287 notification bell endpoints — HTTP twins of api.ts's wrappers.
// Keep the names + return types in lockstep so <NotificationBell> imports
// the same names from `./api` in both modes. The list/count/read-all are
// cross-repo (the global bell); limit <= 0 omits the ?limit= parameter.
export async function listNotifications(unreadOnly = true, limit = 0): Promise<Notification[]> {
  const query: Record<string, string | number> = { state: unreadOnly ? 'unread' : 'all' };
  if (limit > 0) query.limit = limit;
  const body = await call<Notification[]>(`/notifications`, { query });
  return body ?? [];
}

export async function countUnreadNotifications(): Promise<number> {
  const body = await call<{ count: number }>(`/notifications/count`);
  return body?.count ?? 0;
}

export async function markNotificationRead(id: number): Promise<Notification | null> {
  return await call<Notification>(`/notifications/${id}/read`, { method: 'POST' });
}

export async function markAllNotificationsRead(): Promise<number> {
  const body = await call<{ count: number }>(`/notifications/read-all`, { method: 'POST' });
  return body?.count ?? 0;
}
