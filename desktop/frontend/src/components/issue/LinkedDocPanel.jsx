import React, { useEffect, useMemo, useState } from 'react';
import MarkdownView from '../../lib/markdownView';
import { isSvgDoc } from '../../lib/docFormat';

// LinkedDocPanel renders one linked document inline. Markdown docs go
// through the shared <MarkdownView> wrapper like the description and
// comment timeline; SVG docs (detected via the shared isSvgDoc —
// filename suffix first, body sniff as fallback) render via a Blob
// object URL the same way DocsView (BACI-56) does, so a wireframe
// attached to a ticket shows as an image instead of raw `<svg>`
// markup. Browsers don't execute JavaScript loaded via <img>, so
// embedded <script> / event handlers are inert without any sanitiser
// dependency.
//
// Native <details> handles the collapse — no extra Radix primitive —
// and `defaultOpen` keeps short docs expanded so the user sees them
// without a click. The 6 KB threshold is the heuristic the design doc
// names; longer docs default closed so the workspace doesn't drown in
// walls of text. SVG bodies frequently exceed 6 KB, but the same
// heuristic still applies — the open/close cue stays consistent.
//
// `linkedVia` carries the doc's origin path(s) — `issue`, `feature/<slug>`,
// or both when the same doc is reachable from the issue and its parent
// feature (deduped by client.BriefIssue). A doc reachable ONLY via the
// parent feature is not this issue's own doc — without a label it reads
// as the issue's plan (the BACI-87 confusion: a sibling issue's plan,
// linked to the shared feature, surfaced as if it belonged here). So:
// surface "(issue + feature)" when both, "(via feature/<slug>)" when
// feature-only, and nothing when it's the issue's own link.
export default function LinkedDocPanel({ doc }) {
  const defaultOpen = (doc.content?.length ?? 0) < 6000;
  const via = doc.linkedVia || [];
  const featureVia = via.find(v => v.startsWith('feature/'));
  const viaLabel = via.includes('issue')
    ? (featureVia ? '(issue + feature)' : '')
    : (featureVia ? `(via ${featureVia})` : '');

  const isSvg = useMemo(
    () => isSvgDoc(doc.filename || '', doc.content || ''),
    [doc.filename, doc.content],
  );

  // Object URL for the SVG render. Created and revoked inside one
  // effect so each createObjectURL is paired with exactly one
  // revoke (StrictMode-safe). Refreshes whenever the doc content
  // changes so a live edit (next 10 s brief poll) re-renders the image.
  const [svgUrl, setSvgUrl] = useState('');
  useEffect(() => {
    if (!isSvg || !doc.content) { setSvgUrl(''); return undefined; }
    const url = URL.createObjectURL(new Blob([doc.content], { type: 'image/svg+xml' }));
    setSvgUrl(url);
    return () => URL.revokeObjectURL(url);
  }, [isSvg, doc.content]);

  return (
    <details className="mk-linked-doc" {...(defaultOpen ? { open: true } : {})}>
      <summary className="mk-linked-doc-head">
        <span className="mk-attachment-badge">{doc.type || 'doc'}</span>
        <span className="mk-attachment-name">{doc.filename}</span>
        {doc.description && <span className="mk-attachment-why">— {doc.description}</span>}
        {viaLabel && <span className="mk-attachment-why">{viaLabel}</span>}
      </summary>
      {isSvg ? (
        <div className="mk-linked-doc-body mk-linked-doc-svg">
          {svgUrl
            ? <img className="mk-linked-doc-svg-img" src={svgUrl} alt={doc.filename} />
            : <p className="mk-meta-empty">SVG body is empty.</p>}
        </div>
      ) : doc.content ? (
        <MarkdownView className="mk-linked-doc-body mk-markdown">{doc.content}</MarkdownView>
      ) : (
        <div className="mk-linked-doc-body mk-markdown">
          <p className="mk-meta-empty">Document body is empty.</p>
        </div>
      )}
    </details>
  );
}
