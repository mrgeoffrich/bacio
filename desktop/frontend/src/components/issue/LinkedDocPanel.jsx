import React, { useEffect, useMemo, useState } from 'react';
import MarkdownView from '../../lib/markdownView';
import TranscriptView from '../../lib/transcript/TranscriptView';
import { isJsonlTranscriptDoc, isSvgDoc } from '../../lib/docFormat';
import * as api from '../../api';
import { reportError } from '../../errors';

function formatBytes(n) {
  if (!Number.isFinite(n) || n <= 0) return '0 B';
  if (n < 1024) return `${n} B`;
  if (n < 1024 * 1024) return `${(n / 1024).toFixed(1)} KB`;
  return `${(n / (1024 * 1024)).toFixed(1)} MB`;
}

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
// Subagent JSONL transcripts (BACI-125) take the third branch —
// `isJsonlTranscriptDoc` matches the `bacio-transcript-…-agent-….jsonl`
// filename shape `attach_transcript` produces, and renders the body
// through <TranscriptView>. The body is NOT inlined in the brief
// (BACI-115 strips it), so the panel fetches it lazily on first
// expand from the per-repo /documents/{filename} route via api.getDoc.
//
// Native <details> handles the collapse — no extra Radix primitive —
// and `defaultOpen` keeps short docs expanded so the user sees them
// without a click. The 6 KB threshold is the heuristic the design doc
// names; longer docs default closed so the workspace doesn't drown in
// walls of text. SVG bodies frequently exceed 6 KB, but the same
// heuristic still applies — the open/close cue stays consistent.
// Transcript panels always default closed — they're large and the
// per-event chrome is heavy enough that we want the user's intent
// before parsing.
//
// `linkedVia` carries the doc's origin path(s) — `issue`, `feature/<slug>`,
// or both when the same doc is reachable from the issue and its parent
// feature (deduped by client.BriefIssue). A doc reachable ONLY via the
// parent feature is not this issue's own doc — without a label it reads
// as the issue's plan (the BACI-87 confusion: a sibling issue's plan,
// linked to the shared feature, surfaced as if it belonged here). So:
// surface "(issue + feature)" when both, "(via feature/<slug>)" when
// feature-only, and nothing when it's the issue's own link.
// BACI-141: evalComments + onPostEval are forwarded into TranscriptView
// only for transcript docs — the other branches (markdown, SVG, body-
// omitted) ignore them. Both are optional, so callers that pre-date
// the feature continue to work unchanged.
export default function LinkedDocPanel({ doc, activeBoard, evalComments, onPostEval }) {
  const isTranscript = useMemo(
    () => isJsonlTranscriptDoc(doc.filename || ''),
    [doc.filename],
  );
  const defaultOpen =
    !isTranscript && (doc.content?.length ?? 0) < 6000;
  const via = doc.linkedVia || [];
  const featureVia = via.find(v => v.startsWith('feature/'));
  const viaLabel = via.includes('issue')
    ? (featureVia ? '(issue + feature)' : '')
    : (featureVia ? `(via ${featureVia})` : '');

  const isSvg = useMemo(
    () => !isTranscript && isSvgDoc(doc.filename || '', doc.content || ''),
    [isTranscript, doc.filename, doc.content],
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

  // Transcript bodies are stripped from the brief (BACI-115). Fetch on
  // first open and cache per (filename, sizeBytes) so the 10s brief
  // poll doesn't re-fetch a 450 KB body each tick.
  const [transcriptBody, setTranscriptBody] = useState('');
  const [transcriptFetching, setTranscriptFetching] = useState(false);
  const [transcriptError, setTranscriptError] = useState('');
  const cacheKey = `${doc.filename}:${doc.sizeBytes ?? 0}`;
  const [cachedKey, setCachedKey] = useState('');
  const [open, setOpen] = useState(false);
  useEffect(() => {
    if (!open || !isTranscript || !activeBoard) return;
    if (cachedKey === cacheKey && transcriptBody) return;
    if (transcriptFetching) return;
    setTranscriptFetching(true);
    setTranscriptError('');
    api.getDoc(activeBoard, doc.filename)
      .then(d => {
        setTranscriptBody(d.content ?? '');
        setCachedKey(cacheKey);
        setTranscriptFetching(false);
      })
      .catch(err => {
        reportError(err, { headline: "Couldn't load transcript" });
        setTranscriptError((err && err.message) || String(err));
        setTranscriptFetching(false);
      });
  }, [open, isTranscript, activeBoard, doc.filename, cacheKey, cachedKey, transcriptBody, transcriptFetching]);

  // Inline-doc body picker — same `details` chrome, four mutually
  // exclusive branches: SVG, transcript, markdown, or "body omitted".
  const renderBody = () => {
    if (isSvg) {
      return (
        <div className="mk-linked-doc-body mk-linked-doc-svg">
          {svgUrl
            ? <img className="mk-linked-doc-svg-img" src={svgUrl} alt={doc.filename} />
            : <p className="mk-meta-empty">SVG body is empty.</p>}
        </div>
      );
    }
    if (isTranscript) {
      if (transcriptFetching) {
        return <div className="mk-linked-doc-body"><p className="mk-meta-empty">Loading transcript…</p></div>;
      }
      if (transcriptError) {
        return <div className="mk-linked-doc-body"><p className="mk-meta-empty">Couldn't load transcript: {transcriptError}</p></div>;
      }
      if (transcriptBody) {
        return (
          <div className="mk-linked-doc-body mk-linked-doc-transcript">
            <TranscriptView
              source={transcriptBody}
              filename={doc.filename}
              sizeBytes={doc.sizeBytes}
              evalComments={evalComments}
              onPostEval={onPostEval}
            />
          </div>
        );
      }
      // Open but no body yet — fall through to a small placeholder.
      return <div className="mk-linked-doc-body"><p className="mk-meta-empty">Opening transcript…</p></div>;
    }
    if (doc.content) {
      return <MarkdownView className="mk-linked-doc-body mk-markdown">{doc.content}</MarkdownView>;
    }
    return (
      <div className="mk-linked-doc-body mk-markdown">
        {/* BACI-115 strips bodies from the brief for everything except
            plan / review docs (transcripts in particular), but the
            metadata row still carries sizeBytes. Distinguish "body
            deliberately omitted" from "doc is truly empty" so a
            transcript attachment doesn't read as a broken empty link. */}
        {doc.sizeBytes > 0 ? (
          <p className="mk-meta-empty">
            Body not inlined in the brief ({formatBytes(doc.sizeBytes)}).
            Run <code>bacio doc show {doc.filename}</code> to view.
          </p>
        ) : (
          <p className="mk-meta-empty">Document body is empty.</p>
        )}
      </div>
    );
  };

  return (
    <details
      className="mk-linked-doc"
      open={defaultOpen || open}
      onToggle={(e) => setOpen(e.currentTarget.open)}
    >
      <summary className="mk-linked-doc-head">
        <span className="mk-attachment-badge">{doc.type || 'doc'}</span>
        <span className="mk-attachment-name">{doc.filename}</span>
        {doc.description && <span className="mk-attachment-why">— {doc.description}</span>}
        {viaLabel && <span className="mk-attachment-why">{viaLabel}</span>}
      </summary>
      {renderBody()}
    </details>
  );
}
