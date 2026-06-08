// Dispatch-domain wire types + reshaper (BACI-358).
//
// ApiDispatch mirrors model.Dispatch as the `bacio api` server serialises
// it; reshapeDispatch maps it into the camelCase DispatchDTO the React
// tree consumes. ApiUserMessage is the BACI-286 steer-message wire shape
// returned by the POST endpoint (callers consume only `id`). Moved out of
// api.http.ts so the reshape is unit testable — see ./issue.ts for the
// pattern + the Phase 2b note.

import type { DispatchDTO } from '../contract';

export interface ApiDispatch {
  id: number;
  issue_key?: string;
  target_agent_name?: string;
  mode?: string;
  status: string;
  payload?: string;
  created_by?: string;
  created_at: string;
}

// ApiUserMessage is the wire shape of a BACI-286 steer message returned
// by the POST endpoint. The callers are fire-and-forget (success vs
// toasted error), so only id is consumed — the rest mirrors the Go
// model.UserMessage for completeness.
export interface ApiUserMessage {
  id: number;
  session_id: string;
  body: string;
  created_by: string;
  created_at: string;
}

export function reshapeDispatch(d: ApiDispatch): DispatchDTO {
  return {
    id: d.id,
    issueKey: d.issue_key ?? '',
    targetAgent: d.target_agent_name ?? '',
    mode: d.mode ?? '',
    status: d.status,
    payload: d.payload ?? '',
    createdBy: d.created_by ?? '',
    createdAt: d.created_at,
  };
}
