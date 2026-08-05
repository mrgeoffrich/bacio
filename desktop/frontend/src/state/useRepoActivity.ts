import * as api from '../api';
import type { RepoActivity } from '../api';
import { usePolledResource } from '../lib/hooks/usePolledResource';

export type RepoActivityState = {
  activity: RepoActivity[];
};

// useRepoActivity (BACI-369) polls the cross-repo activity summary the
// topbar's repository picker ranks its rows by. Topbar-local and
// cross-repo with no board gate — the same shape as useNotifications, so
// it lives as a self-stateful hook rather than a context (RepoPicker is
// the only consumer).
//
// Errors are swallowed: the ordering is polish, and a failed fetch just
// leaves the picker in its historical prefix order with no job pills —
// the same policy the notification-count badge uses.
export function useRepoActivity(): RepoActivityState {
  const { data: activity } = usePolledResource<RepoActivity[]>(
    () => api.listRepoActivity(),
    [],
    [],
    { onError: () => {} },
  );
  return { activity };
}
