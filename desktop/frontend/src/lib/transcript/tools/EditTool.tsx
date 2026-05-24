// EditTool renders Edit / Write tool calls (BACI-125). Edit shows a
// synthetic two-block diff (old_string / new_string) — we don't have
// the surrounding file context the way `git diff` would, so this is
// the closest faithful representation. Write shows as a single `+`
// block of the body.
//
// `replace_all` on Edit and the `old_string === ""` shape are surfaced
// in the header so a reviewer can spot a full-file replace vs. a
// surgical patch at a glance.

import React from 'react';
import CollapsibleBody from '../CollapsibleBody';
import { flattenResultContent } from '../format';
import type { ToolResult } from '../types';

type EditInput = {
  file_path?: string;
  old_string?: string;
  new_string?: string;
  replace_all?: boolean;
  content?: string; // Write only
};

type EditToolProps = {
  toolName: 'Edit' | 'Write' | string;
  input: unknown;
  result?: ToolResult;
};

function renderDiffLines(text: string, prefix: '+' | '-'): React.ReactNode {
  if (!text) return null;
  const lines = text.split('\n');
  const cls = prefix === '+' ? 'is-add' : 'is-remove';
  return lines.map((ln, i) => (
    <div key={`${prefix}-${i}`} className={`mk-transcript-diff-line ${cls}`}>
      <span className="mk-transcript-diff-prefix">{prefix}</span>
      <span className="mk-transcript-diff-text">{ln || ' '}</span>
    </div>
  ));
}

export default function EditTool({
  toolName,
  input,
  result,
}: EditToolProps): React.ReactElement {
  const i = (input ?? {}) as EditInput;
  const path = i.file_path ?? '(no path)';
  const resultText = result ? flattenResultContent(result.content) : '';
  const isWrite = toolName === 'Write';
  const replaceAll = i.replace_all === true;
  const body = isWrite ? i.content ?? '' : '';
  const lineCount = isWrite
    ? body
    : `${i.old_string ?? ''}\n${i.new_string ?? ''}`;

  return (
    <div className="mk-transcript-tool-body">
      <div className="mk-transcript-section">
        <div className="mk-transcript-section-label">
          {isWrite ? 'Write' : replaceAll ? 'Edit (replace_all)' : 'Edit'}{' '}
          <code className="mk-transcript-inline-code">{path}</code>
        </div>
        <CollapsibleBody text={lineCount} label="diff" linesThreshold={20}>
          <div className="mk-transcript-diff">
            {isWrite ? (
              renderDiffLines(body, '+')
            ) : (
              <>
                {renderDiffLines(i.old_string ?? '', '-')}
                {renderDiffLines(i.new_string ?? '', '+')}
              </>
            )}
          </div>
        </CollapsibleBody>
      </div>
      {result && (
        <div className={`mk-transcript-section ${result.isError ? 'is-error' : ''}`}>
          <div className="mk-transcript-section-label">
            {result.isError ? 'Error' : 'Result'}
          </div>
          <CollapsibleBody text={resultText} label="output" linesThreshold={10}>
            <pre className="mk-transcript-pre">{resultText || '(empty)'}</pre>
          </CollapsibleBody>
        </div>
      )}
    </div>
  );
}
