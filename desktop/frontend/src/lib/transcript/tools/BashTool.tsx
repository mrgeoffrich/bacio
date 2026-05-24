// BashTool renders a Bash call (BACI-125): `description` as a `#`
// comment above the `$ command`, result in a separate fenced block.
// is_error flips the result block to the error style and the parent
// card head adds an ERROR pill via the registry's `isError` hint.

import React from 'react';
import CollapsibleBody from '../CollapsibleBody';
import { flattenResultContent } from '../format';
import type { ToolResult } from '../types';

type BashInput = {
  command?: string;
  description?: string;
};

type BashToolProps = {
  input: unknown;
  result?: ToolResult;
};

export default function BashTool({ input, result }: BashToolProps): React.ReactElement {
  const i = (input ?? {}) as BashInput;
  const cmd = (i.command ?? '').trim();
  const desc = (i.description ?? '').trim();
  const resultText = result ? flattenResultContent(result.content) : '';
  return (
    <div className="mk-transcript-tool-body">
      <div className="mk-transcript-section">
        <pre className="mk-transcript-pre">
          {desc && <span className="mk-transcript-shell-comment">{`# ${desc}`}{'\n'}</span>}
          <span className="mk-transcript-shell-prompt">$ </span>
          {cmd || '(empty command)'}
        </pre>
      </div>
      {result && (
        <div className={`mk-transcript-section ${result.isError ? 'is-error' : ''}`}>
          <div className="mk-transcript-section-label">
            {result.isError ? 'Error' : 'Result'}
          </div>
          <CollapsibleBody text={resultText} label="output" linesThreshold={12}>
            <pre className="mk-transcript-pre">{resultText || '(empty)'}</pre>
          </CollapsibleBody>
        </div>
      )}
    </div>
  );
}
