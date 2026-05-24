// ReadTool renders a Read call (BACI-125): one-line header
// "Read <path> (lines a–b)" and the file content in a collapsible
// <pre>. The line-range slot is omitted when offset/limit aren't
// supplied so a whole-file read reads cleanly.

import React from 'react';
import CollapsibleBody from '../CollapsibleBody';
import { flattenResultContent } from '../format';
import type { ToolResult } from '../types';

type ReadInput = {
  file_path?: string;
  offset?: number;
  limit?: number;
};

type ReadToolProps = {
  input: unknown;
  result?: ToolResult;
};

export default function ReadTool({ input, result }: ReadToolProps): React.ReactElement {
  const i = (input ?? {}) as ReadInput;
  const path = i.file_path ?? '(no path)';
  const rangeBits: string[] = [];
  if (typeof i.offset === 'number') rangeBits.push(`from line ${i.offset}`);
  if (typeof i.limit === 'number') rangeBits.push(`limit ${i.limit}`);
  const range = rangeBits.length > 0 ? ` (${rangeBits.join(', ')})` : '';
  const resultText = result ? flattenResultContent(result.content) : '';
  return (
    <div className="mk-transcript-tool-body">
      <div className="mk-transcript-section">
        <div className="mk-transcript-section-label">
          Read <code className="mk-transcript-inline-code">{path}</code>
          {range}
        </div>
      </div>
      {result && (
        <div className={`mk-transcript-section ${result.isError ? 'is-error' : ''}`}>
          <CollapsibleBody text={resultText} label="contents" linesThreshold={20}>
            <pre className="mk-transcript-pre">{resultText || '(empty)'}</pre>
          </CollapsibleBody>
        </div>
      )}
    </div>
  );
}
