import { ChevronLeft, ChevronRight } from 'lucide-react';

// The three small navigation pieces the Documents editor pane wears, kept
// together because they answer the same question in three places:
// "where am I in the tree, and what's next to me?"
//
//   • DocsBreadcrumbs — the space › folder › folder › page trail that
//     replaced the bare filename in the viewer header. Every segment but the
//     last is clickable, so a folder page is always one click away.
//   • DocsPeerJump    — the `‹ ›` header control, for reading straight
//     through a folder without going back to the rail.
//   • DocsPeerNav     — the footer cards. Same destinations as the jump
//     control, different moment: the jump is for someone scanning, the cards
//     are for someone who has just finished reading a page.

// One breadcrumb segment. `onClick` absent ⇒ this is the current location
// (always the last segment) and renders as plain text.
export type Crumb = {
  key: string;
  label: string;
  onClick?: () => void;
  // Renders the segment in the mono face — the filename, which is a literal
  // on-disk name rather than a display title.
  mono?: boolean;
};

type DocsBreadcrumbsProps = {
  crumbs: Crumb[];
};

export function DocsBreadcrumbs({ crumbs }: DocsBreadcrumbsProps) {
  return (
    <nav className="mk-docs-crumbs" aria-label="Breadcrumb">
      {crumbs.map((c, i) => (
        <span key={c.key} className="mk-docs-crumb-seg">
          {i > 0 && <span className="mk-docs-crumb-sep" aria-hidden="true">›</span>}
          {c.onClick ? (
            <button type="button" className="mk-docs-crumb-link" onClick={c.onClick}>
              {c.label}
            </button>
          ) : (
            <span
              className={`mk-docs-crumb-here ${c.mono ? 'is-mono' : ''}`}
              aria-current="page"
              title={c.label}
            >
              {c.label}
            </span>
          )}
        </span>
      ))}
    </nav>
  );
}

export type Peer = {
  label: string;
  onSelect: () => void;
} | null;

type DocsPeerJumpProps = {
  prev: Peer;
  next: Peer;
};

export function DocsPeerJump({ prev, next }: DocsPeerJumpProps) {
  if (!prev && !next) return null;
  return (
    <div className="mk-docs-peer-jump">
      <button
        type="button"
        className="mk-icbtn mk-docs-peer-btn"
        disabled={!prev}
        onClick={() => prev?.onSelect()}
        title={prev ? `Previous: ${prev.label}` : 'No previous page'}
        aria-label={prev ? `Previous page: ${prev.label}` : 'No previous page'}
      >
        <ChevronLeft size={14} strokeWidth={2} aria-hidden="true" />
      </button>
      <button
        type="button"
        className="mk-icbtn mk-docs-peer-btn"
        disabled={!next}
        onClick={() => next?.onSelect()}
        title={next ? `Next: ${next.label}` : 'No next page'}
        aria-label={next ? `Next page: ${next.label}` : 'No next page'}
      >
        <ChevronRight size={14} strokeWidth={2} aria-hidden="true" />
      </button>
    </div>
  );
}

type DocsPeerNavProps = {
  prev: Peer;
  next: Peer;
};

export function DocsPeerNav({ prev, next }: DocsPeerNavProps) {
  if (!prev && !next) return null;
  return (
    <nav className="mk-docs-peer-nav" aria-label="Sibling pages">
      {prev ? (
        <button type="button" className="mk-docs-peer-card" onClick={prev.onSelect}>
          <span className="mk-docs-peer-dir">← Previous</span>
          <span className="mk-docs-peer-name">{prev.label}</span>
        </button>
      ) : <span className="mk-docs-peer-spacer" />}
      {next ? (
        <button type="button" className="mk-docs-peer-card is-next" onClick={next.onSelect}>
          <span className="mk-docs-peer-dir">Next →</span>
          <span className="mk-docs-peer-name">{next.label}</span>
        </button>
      ) : <span className="mk-docs-peer-spacer" />}
    </nav>
  );
}
