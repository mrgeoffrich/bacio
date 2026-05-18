// MarkdownView is the single read-side markdown renderer for the React
// tree. Every surface that renders markdown for reading (issue
// description read mode, comment timeline, linked-doc panel,
// feature description) goes through here — never import
// `react-markdown` directly elsewhere.
//
// Centralising the import keeps three things true:
//   1. GFM features (tables, task lists, strikethrough, autolinks)
//      render uniformly across every read surface. The "missing
//      remark-gfm" regression that left tables silently dropped is
//      structurally impossible to repeat — there's one wiring site.
//   2. Future plugin additions (e.g. syntax-highlighted code) land in
//      one file.
//   3. Read and edit are kept deliberately separate. The DocsView
//      WYSIWYG (TipTap) stays the only edit-time markdown renderer;
//      everywhere else, edit mode is a plain textarea so agent-edited
//      bodies round-trip byte-identically.
//
// See docs/markdown-rendering.md for the full per-surface decision.
import React from 'react';
import ReactMarkdown from 'react-markdown';
import remarkGfm from 'remark-gfm';

type MarkdownViewProps = {
  children?: string | null;
  className?: string;
};

// remark-gfm enables GFM tables, task lists, strikethrough and
// autolinks. react-markdown v10 ships with raw HTML rendering off by
// default (no `rehype-raw`), so `<script>` tags in a description are
// rendered as literal text rather than evaluated — safe for our local-
// owner-trusted inputs without needing an explicit sanitiser.
const remarkPlugins = [remarkGfm];

export default function MarkdownView({ children, className }: MarkdownViewProps): React.ReactElement | null {
  const body = children ?? '';
  if (className) {
    return (
      <div className={className}>
        <ReactMarkdown remarkPlugins={remarkPlugins}>{body}</ReactMarkdown>
      </div>
    );
  }
  return <ReactMarkdown remarkPlugins={remarkPlugins}>{body}</ReactMarkdown>;
}
