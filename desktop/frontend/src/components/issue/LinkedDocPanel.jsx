import React from 'react';
import { Link } from 'react-router';
import { documentPath } from '../../lib/routes';

function formatBytes(n) {
  if (!Number.isFinite(n) || n <= 0) return '0 B';
  if (n < 1024) return `${n} B`;
  if (n < 1024 * 1024) return `${(n / 1024).toFixed(1)} KB`;
  return `${(n / (1024 * 1024)).toFixed(1)} MB`;
}

// BACI-203: LinkedDocPanel collapses to a metadata header + a single
// link into the canonical document page. The previous shape (BACI-115
// / BACI-125 / BACI-141) inlined markdown bodies, SVG renderings, and
// JSONL transcripts directly into the issue workspace — duplicating
// content that already had its own home at `/documents/<filename>`.
//
// The brief continues to carry the linked-doc metadata (filename,
// type, size, description, linkedVia, sourcePath), just not the body
// content — see internal/api/handlers_brief.go's linked-doc strip.
//
// BACI-141 eval comments anchored to transcript events still surface
// in the issue workspace's main Activity timeline; the per-event
// composer that used to live inside the inline transcript view now
// belongs on the document detail page itself (a follow-up — see the
// PR description for the deferred work).
//
// `linkedVia` carries the doc's origin path(s) — `issue`, `feature/<slug>`,
// or both when the same doc is reachable from the issue and its parent
// feature (deduped by client.BriefIssue). A doc reachable ONLY via the
// parent feature is not this issue's own doc — without a label it reads
// as the issue's plan (the BACI-87 confusion). So: surface "(issue +
// feature)" when both, "(via feature/<slug>)" when feature-only, and
// nothing when it's the issue's own link.
export default function LinkedDocPanel({ doc }) {
  const via = doc.linkedVia || [];
  const featureVia = via.find(v => v.startsWith('feature/'));
  const viaLabel = via.includes('issue')
    ? (featureVia ? '(issue + feature)' : '')
    : (featureVia ? `(via ${featureVia})` : '');

  return (
    <div className="mk-linked-doc">
      <div className="mk-linked-doc-head">
        <span className="mk-attachment-badge">{doc.type || 'doc'}</span>
        <Link
          to={documentPath(doc.filename)}
          className="mk-attachment-name mk-attachment-link"
        >
          {doc.filename}
        </Link>
        {doc.sizeBytes > 0 && (
          <span className="mk-attachment-size">{formatBytes(doc.sizeBytes)}</span>
        )}
        {doc.description && <span className="mk-attachment-why">— {doc.description}</span>}
        {viaLabel && <span className="mk-attachment-why">{viaLabel}</span>}
      </div>
    </div>
  );
}
