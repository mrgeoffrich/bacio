// GrepGlobTool renders Grep / Glob calls (BACI-125). Header shows the
// pattern + path; the result block is the match list (collapsible).

import React from 'react';
import CollapsibleBody from '../CollapsibleBody';
import { countLines, flattenResultContent } from '../format';
import type { ToolResult } from '../types';

type GrepGlobInput = {
  pattern?: string;
  path?: string;
  glob?: string;
  output_mode?: string;
};

type GrepGlobToolProps = {
  toolName: 'Grep' | 'Glob' | string;
  input: unknown;
  result?: ToolResult;
};

export default function GrepGlobTool({
  toolName,
  input,
  result,
}: GrepGlobToolProps): React.ReactElement {
  const i = (input ?? {}) as GrepGlobInput;
  const pattern = i.pattern ?? '';
  const path = i.path ?? '.';
  const glob = i.glob;
  const resultText = result ? flattenResultContent(result.content) : '';
  const matches = countLines(resultText.trim());
  return (
    <div className="mk-transcript-tool-body">
      <div className="mk-transcript-section">
        <div className="mk-transcript-section-label">
          {toolName} <code className="mk-transcript-inline-code">{pattern}</code>{' '}
          in <code className="mk-transcript-inline-code">{path}</code>
          {glob && (
            <>
              {' '}— glob <code className="mk-transcript-inline-code">{glob}</code>
            </>
          )}
        </div>
      </div>
      {result && (
        <div className={`mk-transcript-section ${result.isError ? 'is-error' : ''}`}>
          <div className="mk-transcript-section-label">
            {matches > 0 ? `${matches} matches` : 'No matches'}
          </div>
          <CollapsibleBody text={resultText} label="matches" linesThreshold={12}>
            <pre className="mk-transcript-pre">{resultText || '(empty)'}</pre>
          </CollapsibleBody>
        </div>
      )}
    </div>
  );
}
