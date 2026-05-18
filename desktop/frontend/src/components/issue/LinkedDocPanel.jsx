import React from 'react';
import ReactMarkdown from 'react-markdown';

// LinkedDocPanel renders one linked document inline with its body
// markdown-rendered, so a plan doc the agent will work from doesn't
// require a separate Docs-tab hop. Native <details> handles the
// collapse — no extra Radix primitive — and `defaultOpen` keeps short
// docs expanded so the user sees them without a click. The 6 KB
// threshold is the heuristic the design doc names; longer docs default
// closed so the workspace doesn't drown in walls of text.
//
// `linkedVia` carries the doc's origin path(s) — `issue`, `feature/<slug>`,
// or both when the same doc is reachable from the issue and its parent
// feature (deduped by client.BriefIssue). When both are present, surface
// "(issue + feature)" so the source is obvious.
export default function LinkedDocPanel({ doc }) {
  const defaultOpen = (doc.content?.length ?? 0) < 6000;
  const viaBoth = (doc.linkedVia || []).length === 2;
  return (
    <details className="mk-linked-doc" {...(defaultOpen ? { open: true } : {})}>
      <summary className="mk-linked-doc-head">
        <span className="mk-attachment-badge">{doc.type || 'doc'}</span>
        <span className="mk-attachment-name">{doc.filename}</span>
        {doc.description && <span className="mk-attachment-why">— {doc.description}</span>}
        {viaBoth && <span className="mk-attachment-why">(issue + feature)</span>}
      </summary>
      <div className="mk-linked-doc-body mk-markdown">
        {doc.content
          ? <ReactMarkdown>{doc.content}</ReactMarkdown>
          : <p className="mk-meta-empty">Document body is empty.</p>}
      </div>
    </details>
  );
}
