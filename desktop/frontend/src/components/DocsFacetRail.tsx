// DocsFacetRail (BACI-204, demoted by the docs-tree pivot) — the facet
// chips of the Documents page.
//
// Originally this WAS the left rail: an `<aside class="mk-docs-rail">` with
// a sticky search box on top and three single-select facet groups below it.
// The pivot makes the page tree the navigator, so the component was demoted
// rather than deleted: the search box and the collapse head moved up into
// DocsTreeRail (which owns the rail chrome now), and what remains renders
// inside a collapsed-by-default `<details>` fold pinned to the rail's foot.
// FacetSection / FacetChip are unchanged — this is the same chip surface,
// re-parented.
//
// Four single-select groups now: Space (the pivot's git-repo-vs-workspace
// signal), Type (one chip per `model.DocumentType` present, sorted by count
// desc), Links (`all` / `issue` / `feature` / `unlinked`), and Status
// (`active` / `archived`). The "all" chip on each group resets that facet.
//
// The fold is pure-derived: counts come from the parent's
// `countFacets(docs)` snapshot, the active selection is the parent's query
// bag, and clicking a chip just calls back with the new query. Activating
// any of them flips the rail body to the flat ranked list — see
// `shouldFlatten` in lib/docsFilter.ts.

import type React from 'react';
import { GitBranch, Layers } from 'lucide-react';

import { activeFacetCount } from '../lib/docsFilter';
import type { DocsCounts, DocsQuery, LinksFacet, StatusFacet } from '../lib/docsFilter';
import type { RepoKind } from '../api';

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

// One selectable space — a git repo or a non-git workspace. Modelling the
// distinction as a facet bucket (rather than a second glyph rail beside the
// topbar's RepoPicker) was the deliberate call: one way to switch space, and
// the git/workspace split gets a visible home.
export type SpaceOption = {
  prefix: string;
  name: string;
  kind: RepoKind;
};

type DocsFacetRailProps = {
  counts: DocsCounts;
  query: DocsQuery;
  onQueryChange: (patch: Partial<DocsQuery>) => void;
  // The Space group. `spaces` is every repo/workspace bacio knows;
  // `activeSpace` is the prefix whose documents are on screen. Picking
  // another space navigates — the doc list is per-repo on both transports,
  // so cross-space search is deliberately deferred (it lands with ⌘K).
  spaces: SpaceOption[];
  activeSpace: string;
  onPickSpace: (prefix: string) => void;
};

export default function DocsFacetRail({
  counts,
  query,
  onQueryChange,
  spaces,
  activeSpace,
  onPickSpace,
}: DocsFacetRailProps) {
  const setQuery = (patch: Partial<DocsQuery>) => onQueryChange(patch);

  // Sort the type chips by descending count so the dominant buckets
  // sit at the top — matches the FeaturesView behaviour where the
  // most-used filter is the easiest to click.
  const typeEntries = Object.entries(counts.byType ?? {}).sort((a, b) => b[1] - a[1]);
  const activeCount = activeFacetCount(query);

  return (
    <details className="mk-docs-facet-fold">
      <summary className="mk-docs-facet-summary">
        <span className="mk-docs-facet-caret" aria-hidden="true" />
        <span>Filters</span>
        {activeCount > 0 && (
          <span className="mk-docs-facet-active-count">{activeCount} on</span>
        )}
      </summary>
      <div className="mk-docs-facet-body">
        {spaces.length > 0 && (
          <FacetSection title="Space">
            {spaces.map((s) => (
              <FacetChip
                key={s.prefix}
                label={s.name || s.prefix}
                icon={
                  s.kind === 'workspace' ? (
                    <Layers size={12} strokeWidth={2} aria-hidden="true" />
                  ) : (
                    <GitBranch size={12} strokeWidth={2} aria-hidden="true" />
                  )
                }
                // Only the space on screen has a doc count to show — the
                // others would need one round trip each, and a wrong number
                // is worse than no number.
                count={s.prefix === activeSpace ? counts.total : null}
                active={s.prefix === activeSpace}
                className={s.kind === 'workspace' ? 'is-workspace' : 'is-git'}
                onClick={() => onPickSpace(s.prefix)}
              />
            ))}
          </FacetSection>
        )}

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
      </div>
    </details>
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
  // null renders no count at all (the inactive Space chips) — distinct from
  // 0, which is a real, meaningful "this bucket is empty".
  count: number | null;
  active: boolean;
  onClick: () => void;
  icon?: React.ReactNode;
  className?: string;
};

function FacetChip({ label, count, active, onClick, icon, className }: FacetChipProps) {
  return (
    <button
      type="button"
      role="tab"
      aria-selected={active}
      className={`mk-docs-rail-chip ${className ?? ''} ${active ? 'is-active' : ''}`}
      onClick={onClick}
    >
      <span className="mk-docs-rail-chip-label">
        {icon}
        {label}
      </span>
      {count !== null && <span className="mk-docs-rail-chip-count">{count}</span>}
    </button>
  );
}
