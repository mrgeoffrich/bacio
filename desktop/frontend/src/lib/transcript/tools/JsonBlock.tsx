// JsonBlock is the generic fallback per-tool renderer in the
// transcript viewer (BACI-125). Anything not in `tools/index.ts`'s
// registry lands here: pretty-printed input JSON on top, flattened
// result content below, both collapsible at sensible line thresholds
// so a huge ToolSearch result still pages naturally.

import React from 'react';
import CollapsibleBody from '../CollapsibleBody';
import { flattenResultContent, prettyJSON } from '../format';
import type { ToolResult } from '../types';

type JsonBlockProps = {
  input: unknown;
  result?: ToolResult;
};

export default function JsonBlock({ input, result }: JsonBlockProps): React.ReactElement {
  const inputText = prettyJSON(input ?? {});
  const resultText = result ? flattenResultContent(result.content) : '';
  return (
    <div className="mk-transcript-tool-body">
      <div className="mk-transcript-section">
        <div className="mk-transcript-section-label">Input</div>
        <CollapsibleBody text={inputText} label="input" linesThreshold={20}>
          <pre className="mk-transcript-pre">{inputText}</pre>
        </CollapsibleBody>
      </div>
      {result && (
        <div className={`mk-transcript-section ${result.isError ? 'is-error' : ''}`}>
          <div className="mk-transcript-section-label">Result</div>
          <CollapsibleBody text={resultText} label="result" linesThreshold={20}>
            <pre className="mk-transcript-pre">{resultText || '(empty)'}</pre>
          </CollapsibleBody>
        </div>
      )}
    </div>
  );
}
