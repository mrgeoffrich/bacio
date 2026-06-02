// DocsFacetRail (BACI-204) — left rail of the redesigned Documents
// view. Sticky search box on top, then three single-select facet
// groups: Type (one chip per `model.DocumentType` present, sorted by
// count desc), Links (`all` / `issue` / `feature` / `unlinked`), and
// Status (`active` / `archived`). The "all" chip on each group resets
// that facet.
//
// The rail is pure-derived: counts come from the parent's
// `countFacets(docs)` snapshot, the active selection is the parent's
// query bag, and clicking a chip just calls back with the new query.
// Visual template lifted from FeaturesView's filter chips so the
// styling stays consistent across desktop screens.

import type React from 'react';
import { Search, X, PanelLeftClose } from 'lucide-react';

import type { DocsCounts, DocsQuery, LinksFacet, StatusFacet } from '../lib/docsFilter';

// Only the scalar (number) buckets are addressable as a chip count;
// `byType` is a Record and is iterated separately, so exclude it.
type NumericCountKey = {
  [K in keyof DocsCounts]: DocsCounts[K] extends number ? K : never;
}[keyof DocsCounts];

type LinkOption = { id: LinksFacet; label: string; countKey: NumericCountKey };
type StatusOption = { id: StatusFacet; label: string; countKey: NumericCountKey };

const LINK_OPTIONS: LinkOption[] = [
  { id: 'all',      label: 'All',          countKey: 'total' },
  { id: 'issue',    label: 'Has issue',    countKey: 'withIssueLink' },
  { id: 'feature',  label: 'Has feature',  countKey: 'withFeatureLink' },
  { id: 'unlinked', label: 'Unlinked',     countKey: 'unlinked' },
];

const STATUS_OPTIONS: StatusOption[] = [
  { id: 'active',   label: 'Active',   countKey: 'active' },
  { id: 'archived', label: 'Archived', countKey: 'archived' },
];

function typeLabel(t: string): string {
  return t.replace(/_/g, ' ');
}

type DocsFacetRailProps = {
  counts: DocsCounts;
  query: DocsQuery;
  onQueryChange: (patch: Partial<DocsQuery>) => void;
  onCollapse?: () => void; // BACI-219: collapse the rail
};

export default function DocsFacetRail({
  counts,
  query,
  onQueryChange,
  onCollapse,
}: DocsFacetRailProps) {
  const setQuery = (patch: Partial<DocsQuery>) => onQueryChange(patch);

  // Sort the type chips by descending count so the dominant buckets
  // sit at the top — matches the FeaturesView behaviour where the
  // most-used filter is the easiest to click.
  const typeEntries = Object.entries(counts.byType ?? {}).sort((a, b) => b[1] - a[1]);

  return (
    <aside className="mk-docs-rail">
      {onCollapse && (
        <div className="mk-docs-rail-head">
          <button
            type="button"
            className="mk-icbtn mk-docs-rail-collapse"
            onClick={onCollapse}
            title="Hide filter sidebar"
            aria-label="Hide filter sidebar"
          >
            <PanelLeftClose size={14} strokeWidth={2} aria-hidden="true" />
          </button>
        </div>
      )}
      <label className="mk-features-search">
        <Search className="mk-features-search-icon" strokeWidth={2} aria-hidden="true" />
        <input
          type="search"
          className="mk-features-search-input"
          value={query.search}
          onChange={(e) => setQuery({ search: e.target.value })}
          placeholder="Filter docs"
          aria-label="Filter documents"
        />
        {query.search && (
          <button
            type="button"
            className="mk-features-search-clear"
            aria-label="Clear filter"
            onClick={() => setQuery({ search: '' })}
          >
            <X strokeWidth={2} aria-hidden="true" />
          </button>
        )}
      </label>

      <FacetSection title="Type">
        <FacetChip
          label="All"
          count={counts.total}
          active={query.type === ''}
          onClick={() => setQuery({ type: '' })}
        />
        {typeEntries.map(([t, n]) => (
          <FacetChip
            key={t}
            label={typeLabel(t)}
            count={n}
            active={query.type === t}
            onClick={() => setQuery({ type: t })}
          />
        ))}
      </FacetSection>

      <FacetSection title="Links">
        {LINK_OPTIONS.map((o) => (
          <FacetChip
            key={o.id}
            label={o.label}
            count={counts[o.countKey] ?? 0}
            active={query.links === o.id}
            onClick={() => setQuery({ links: o.id })}
          />
        ))}
      </FacetSection>

      <FacetSection title="Status">
        {STATUS_OPTIONS.map((o) => (
          <FacetChip
            key={o.id}
            label={o.label}
            count={counts[o.countKey] ?? 0}
            active={query.status === o.id}
            onClick={() => setQuery({ status: o.id })}
          />
        ))}
      </FacetSection>
    </aside>
  );
}

type FacetSectionProps = {
  title: string;
  children: React.ReactNode;
};

function FacetSection({ title, children }: FacetSectionProps) {
  return (
    <div className="mk-docs-rail-section">
      <h4 className="mk-docs-rail-section-title">{title}</h4>
      <div className="mk-docs-rail-chips" role="tablist" aria-label={`Filter documents by ${title.toLowerCase()}`}>
        {children}
      </div>
    </div>
  );
}

type FacetChipProps = {
  label: string;
  count: number;
  active: boolean;
  onClick: () => void;
};

function FacetChip({ label, count, active, onClick }: FacetChipProps) {
  return (
    <button
      type="button"
      role="tab"
      aria-selected={active}
      className={`mk-docs-rail-chip ${active ? 'is-active' : ''}`}
      onClick={onClick}
    >
      <span className="mk-docs-rail-chip-label">{label}</span>
      <span className="mk-docs-rail-chip-count">{count}</span>
    </button>
  );
}
