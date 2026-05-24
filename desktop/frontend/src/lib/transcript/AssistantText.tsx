// AssistantText renders an assistant text / thinking block list
// (BACI-125). Text routes through <MarkdownView> per
// docs/markdown-rendering.md — assistant text is markdown-ish in
// practice. Thinking is a styled blockquote behind a "Thinking" badge,
// collapsed by default if long so it doesn't drown the timeline.

import React from 'react';
import MarkdownView from '../markdownView';
import CollapsibleBody from './CollapsibleBody';
import type { AssistantBlock } from './types';

type AssistantTextProps = {
  blocks: Extract<AssistantBlock, { type: 'text' } | { type: 'thinking' }>[];
};

export default function AssistantText({
  blocks,
}: AssistantTextProps): React.ReactElement {
  return (
    <div className="mk-transcript-assistant-blocks">
      {blocks.map((b, i) => {
        if (b.type === 'text') {
          return (
            <MarkdownView
              key={`t-${i}`}
              className="mk-markdown mk-transcript-assistant-text"
            >
              {b.text}
            </MarkdownView>
          );
        }
        // thinking
        return (
          <div key={`th-${i}`} className="mk-transcript-thinking">
            <div className="mk-transcript-thinking-head">Thinking</div>
            <CollapsibleBody text={b.thinking} label="reasoning" linesThreshold={6}>
              <MarkdownView className="mk-markdown mk-transcript-thinking-body">
                {b.thinking}
              </MarkdownView>
            </CollapsibleBody>
          </div>
        );
      })}
    </div>
  );
}
