// RawEventDrawer renders the original JSON envelope for one event
// (BACI-125 `[▶]` affordance). Lives inline under the card it belongs
// to. Confirms the renderer never silently mutates the source — what
// the agent emitted is one click away.

import React, { useCallback } from 'react';
import { prettyJSON } from './format';

type RawEventDrawerProps = {
  raw: unknown;
};

export default function RawEventDrawer({ raw }: RawEventDrawerProps): React.ReactElement {
  const text = prettyJSON(raw);
  const copy = useCallback(() => {
    try {
      navigator.clipboard?.writeText(text);
    } catch {
      /* clipboard access can be blocked in some web contexts; ignore */
    }
  }, [text]);
  return (
    <div className="mk-transcript-raw">
      <div className="mk-transcript-raw-head">
        <span className="mk-transcript-raw-title">Raw event</span>
        <button type="button" className="mk-btn-secondary" onClick={copy}>
          Copy event
        </button>
      </div>
      <pre className="mk-transcript-pre">{text}</pre>
    </div>
  );
}
