// DispatchPromptCard renders the first user-prompt event — the bacio
// dispatch payload (BACI-125). The payload carries `<issue_id>` /
// `<mode>` / `<dispatch_id>` tags the supervisor's stub emits; we
// highlight those as chips at the top, then drop the rest of the
// prompt as monospace below so the worker's brief reads cleanly.
//
// A non-dispatch user message routes through the same component for
// rendering consistency; the chip slot is then empty.

import React from 'react';
import CollapsibleBody from './CollapsibleBody';
import { extractDispatchTags } from './dispatchTags';

// Re-export the helper for callers that pulled it from this module
// before BACI-125 split it out.
export { extractDispatchTags };
export type { DispatchTags } from './dispatchTags';

type DispatchPromptCardProps = {
  text: string;
};

export default function DispatchPromptCard({
  text,
}: DispatchPromptCardProps): React.ReactElement {
  const tags = extractDispatchTags(text);
  return (
    <div className="mk-transcript-dispatch">
      {(tags.issue || tags.mode || tags.dispatchId) && (
        <div className="mk-transcript-dispatch-tags">
          {tags.issue && (
            <span className="mk-transcript-chip is-issue">{tags.issue}</span>
          )}
          {tags.mode && (
            <span className="mk-transcript-chip is-mode">{tags.mode} mode</span>
          )}
          {tags.dispatchId && (
            <span className="mk-transcript-chip is-dispatch">
              dispatch #{tags.dispatchId}
            </span>
          )}
        </div>
      )}
      <CollapsibleBody text={text} label="prompt" linesThreshold={10}>
        <pre className="mk-transcript-pre">{text}</pre>
      </CollapsibleBody>
    </div>
  );
}
