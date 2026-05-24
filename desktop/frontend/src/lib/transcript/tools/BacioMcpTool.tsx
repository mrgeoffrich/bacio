// BacioMcpTool renders calls to the bacio channel MCP tools (BACI-125)
// — reply / register / ask_user_question / attach_transcript — with
// the same agent-action tone the AgentsView uses. The intent is to
// make the bacio-specific signal pop out of a transcript that's mostly
// Bash / Read / Edit traffic.

import React from 'react';
import CollapsibleBody from '../CollapsibleBody';
import { flattenResultContent, prettyJSON } from '../format';
import type { ToolResult } from '../types';

type BacioMcpToolProps = {
  toolName: string;
  input: unknown;
  result?: ToolResult;
};

type ReplyInput = { dispatch_id?: number; note?: string };
type RegisterInput = { name?: string; mode?: string; claude_pid?: number };
type AttachTranscriptInput = { issue_key?: string; agent_id?: string; note?: string };
type AskUserQuestionInput = { questions?: { question?: string; header?: string }[] };

export default function BacioMcpTool({
  toolName,
  input,
  result,
}: BacioMcpToolProps): React.ReactElement {
  const i = (input ?? {}) as Record<string, unknown>;
  const resultText = result ? flattenResultContent(result.content) : '';
  let summary: React.ReactNode;

  switch (toolName) {
    case 'mcp__bacio__reply': {
      const r = i as ReplyInput;
      summary = (
        <span>
          Reply to dispatch <code className="mk-transcript-inline-code">#{r.dispatch_id ?? '?'}</code>
          {r.note && <>: <em>{r.note}</em></>}
        </span>
      );
      break;
    }
    case 'mcp__bacio__register': {
      const r = i as RegisterInput;
      summary = (
        <span>
          Register session as <strong>{r.name ?? '(unnamed)'}</strong>
          {r.mode && <> in mode <code className="mk-transcript-inline-code">{r.mode}</code></>}
        </span>
      );
      break;
    }
    case 'mcp__bacio__attach_transcript': {
      const r = i as AttachTranscriptInput;
      summary = (
        <span>
          Attach transcript for <code className="mk-transcript-inline-code">{r.issue_key ?? '?'}</code>
          {r.agent_id && <> ← agent <code className="mk-transcript-inline-code">{r.agent_id}</code></>}
          {r.note && <> — <em>{r.note}</em></>}
        </span>
      );
      break;
    }
    case 'mcp__bacio__ask_user_question': {
      const r = i as AskUserQuestionInput;
      const qs = r.questions ?? [];
      summary = (
        <span>
          Ask user — {qs.length} question{qs.length === 1 ? '' : 's'}
          {qs.length > 0 && qs[0]?.question && (
            <>: <em>{qs[0].question}</em></>
          )}
        </span>
      );
      break;
    }
    default:
      summary = <span>{toolName}</span>;
      break;
  }

  return (
    <div className="mk-transcript-tool-body">
      <div className="mk-transcript-section">
        <div className="mk-transcript-bacio-summary">{summary}</div>
        <CollapsibleBody text={prettyJSON(input)} label="payload" linesThreshold={8}>
          <pre className="mk-transcript-pre">{prettyJSON(input)}</pre>
        </CollapsibleBody>
      </div>
      {result && resultText && (
        <div className={`mk-transcript-section ${result.isError ? 'is-error' : ''}`}>
          <div className="mk-transcript-section-label">Result</div>
          <CollapsibleBody text={resultText} label="response" linesThreshold={6}>
            <pre className="mk-transcript-pre">{resultText}</pre>
          </CollapsibleBody>
        </div>
      )}
    </div>
  );
}
