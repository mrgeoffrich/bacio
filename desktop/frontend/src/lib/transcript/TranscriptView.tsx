// TranscriptView is the top-level reader for a subagent .jsonl
// transcript (BACI-125). It parses + pairs the raw body once via
// useMemo and lays the result out as:
//
//   * a header strip with filename / size / event count / token totals
//     and a "copy all" affordance,
//   * a controls strip (filter chips, hide-envelope, jump-to-error,
//     per-event usage toggle),
//   * a sticky left mini-map,
//   * a vertical stream of per-event cards (dispatch, assistant, tool
//     call, system reminder, attachment).
//
// State that lives here:
//   - hideEnvelope (default true) hides `attachment` items.
//   - toolFilter is the set of tool names selected; empty means "show
//     all". Chips toggle membership.
//   - showPerEventUsage flips the assistant-card usage badge.
//   - expandedIds is a per-card collapse map shared by every
//     <CollapsibleBody>.
//   - rawOpenIds is the per-card `[▶]` raw-drawer open set.
//   - cursorIdx is the visible cursor — driven by a single shared
//     IntersectionObserver across all event cards.

import React, { useCallback, useEffect, useMemo, useRef, useState } from 'react';
import AssistantText from './AssistantText';
import ControlsBar from './ControlsBar';
import DispatchPromptCard from './DispatchPromptCard';
import { extractDispatchTags } from './dispatchTags';
import EventCard from './EventCard';
import Minimap, { type EvalTick } from './Minimap';
import { formatBytes, formatTimestamp, prettyJSON } from './format';
import { parse } from './parse';
import { pair } from './pair';
import { formatKilo, summariseTokens } from './tokenSummary';
import { rendererFor } from './tools/index';
import type { EvalComment, RenderItem } from './types';

type TranscriptViewProps = {
  source: string;
  filename?: string;
  sizeBytes?: number;
  // BACI-141: every eval comment the parent has on the issue. We
  // filter to those matching this transcript's `<dispatch_id>` tag
  // (with a defensive fallback to agentSessionId when the transcript
  // carries no dispatch tag), then bucket into per-event-anchored
  // notes and unanchored (dispatch-level) notes. Empty / absent on
  // surfaces that aren't wired for eval material.
  evalComments?: EvalComment[];
  // BACI-141: caller-supplied submit handler for the inline composer
  // surfaces. Receives the trimmed body and the per-event anchor
  // handle ('' for dispatch-level). When absent, the composer
  // affordances on every surface hide.
  onPostEval?: (body: string, eventRef: string) => Promise<void> | void;
};

function anchorIdFor(item: RenderItem): string {
  return `tr-evt-${item.id}`;
}

function copyToClipboard(text: string) {
  try {
    navigator.clipboard?.writeText(text);
  } catch {
    /* clipboard access can be blocked in some web contexts; ignore */
  }
}

export default function TranscriptView({
  source,
  filename,
  sizeBytes,
  evalComments,
  onPostEval,
}: TranscriptViewProps): React.ReactElement {
  // Parse + pair is a useMemo on `source` so a no-op refetch (the
  // 10s brief poll handing us the same body string back) re-uses the
  // same arrays and React skips the per-card re-render.
  const parsed = useMemo(() => parse(source), [source]);
  const items = useMemo<RenderItem[]>(() => pair(parsed.events), [parsed.events]);
  const tokens = useMemo(() => summariseTokens(parsed.events), [parsed.events]);

  // Tool names actually used in this transcript — used to seed the
  // filter chip row. De-duped, sorted alpha.
  const toolNamesUsed = useMemo(() => {
    const names = new Set<string>();
    for (const it of items) {
      if (it.kind === 'tool-call' && it.toolName && !it.orphanedResult) {
        names.add(it.toolName);
      }
    }
    return [...names].sort();
  }, [items]);

  const dispatchTags = useMemo(() => {
    const first = items.find(i => i.kind === 'dispatch');
    if (!first || first.kind !== 'dispatch') return null;
    return extractDispatchTags(first.ev.text);
  }, [items]);

  // BACI-141: filter eval comments to those matching this transcript.
  // Primary match: dispatchId === dispatch tag (the canonical case).
  // Fallback: agentSessionId match — covers legacy transcripts that
  // were attached without a `<dispatch_id>` tag, plus the
  // `attach_transcript`-after-the-fact case. The per-issue filter at
  // the caller level (IssueWorkspace passes brief.comments.filter(c
  // => c.eval)) keeps cross-issue leakage off the table.
  const filteredEvalComments = useMemo<EvalComment[]>(() => {
    if (!evalComments || evalComments.length === 0) return [];
    const dispatchId = dispatchTags?.dispatchId;
    return evalComments.filter(c => {
      if (dispatchId) {
        return c.dispatchId !== undefined && String(c.dispatchId) === dispatchId;
      }
      // Tag-less transcript fallback: any eval comment with a
      // matching session id rides through. agentSessionId is itself
      // optional on the wire (older payloads may omit it).
      return false;
    });
  }, [evalComments, dispatchTags]);

  // Build the per-event anchor map. Key is the RenderItem.id the
  // EventCard mounts under; the value is the list of notes attached
  // to that item (newest-last by createdAt). An item collects a
  // note via two anchor formats:
  //   * tool_use_id:<id> — matches the tool-call item whose use.id
  //     equals the trailing id (the post-tool-use anchor — durable
  //     across re-renders because the same id appears in both the
  //     assistant tool_use block and its user-tool-result counterpart).
  //   * line_index:<n>  — matches any item whose underlying event's
  //     lineIndex equals the trailing integer (the fallback for
  //     assistant / dispatch / system-reminder / attachment events).
  //
  // Notes that anchor to nothing fall into `unanchoredNotes` —
  // pinned to the dispatch prompt card as the catch-all surface.
  const { evalNotesByItemId, unanchoredNotes } = useMemo(() => {
    const byId = new Map<string, EvalComment[]>();
    const unanchored: EvalComment[] = [];
    if (filteredEvalComments.length === 0) {
      return { evalNotesByItemId: byId, unanchoredNotes: unanchored };
    }
    // Build the two lookups once over the items list.
    const itemByToolUseId = new Map<string, RenderItem>();
    const itemByLineIndex = new Map<number, RenderItem>();
    for (const it of items) {
      if (it.kind === 'tool-call' && it.use.id) {
        // Only the first occurrence wins on the rare repeat case —
        // matches how RawEventDrawer dedupes by use.id.
        if (!itemByToolUseId.has(it.use.id)) itemByToolUseId.set(it.use.id, it);
      }
      const ev = 'ev' in it ? it.ev : ('assistantEv' in it ? it.assistantEv : undefined);
      if (ev && typeof (ev as { lineIndex?: number }).lineIndex === 'number') {
        const line = (ev as { lineIndex: number }).lineIndex;
        if (!itemByLineIndex.has(line)) itemByLineIndex.set(line, it);
      }
    }
    for (const note of filteredEvalComments) {
      const ref = (note.transcriptEventRef ?? '').trim();
      if (ref === '') {
        unanchored.push(note);
        continue;
      }
      let item: RenderItem | undefined;
      if (ref.startsWith('tool_use_id:')) {
        item = itemByToolUseId.get(ref.slice('tool_use_id:'.length));
      } else if (ref.startsWith('line_index:')) {
        const n = Number(ref.slice('line_index:'.length));
        if (Number.isFinite(n)) item = itemByLineIndex.get(n);
      }
      if (item) {
        const existing = byId.get(item.id);
        if (existing) existing.push(note);
        else byId.set(item.id, [note]);
      } else {
        // Anchor handle didn't resolve — fall through to the dispatch-
        // level surface so the note is still visible rather than
        // silently dropped.
        unanchored.push(note);
      }
    }
    return { evalNotesByItemId: byId, unanchoredNotes: unanchored };
  }, [items, filteredEvalComments]);

  // ----- UI state -----
  const [hideEnvelope, setHideEnvelope] = useState(true);
  const [toolFilter, setToolFilter] = useState<Set<string>>(new Set());
  const [showPerEventUsage, setShowPerEventUsage] = useState(false);
  const [expandedIds, setExpandedIds] = useState<Record<string, boolean>>({});
  const [rawOpenIds, setRawOpenIds] = useState<Set<string>>(new Set());
  const [cursorIdx, setCursorIdx] = useState(0);

  const toggleTool = useCallback((name: string) => {
    setToolFilter(prev => {
      const next = new Set(prev);
      if (next.has(name)) next.delete(name);
      else next.add(name);
      return next;
    });
  }, []);
  const clearFilter = useCallback(() => setToolFilter(new Set()), []);
  const toggleRaw = useCallback((id: string) => {
    setRawOpenIds(prev => {
      const next = new Set(prev);
      if (next.has(id)) next.delete(id);
      else next.add(id);
      return next;
    });
  }, []);

  // Filtered stream — applied at render time so the underlying items
  // / minimap stay 1:1 (a tick the user clicked-to-jump should still
  // exist after toggling a filter, even if the card is now hidden in
  // the stream). Note: that means the minimap shows ticks for hidden
  // events; the cursor still highlights the visible card.
  const visibleItems = useMemo(() => {
    return items.filter(it => {
      if (hideEnvelope && it.kind === 'attachment') return false;
      if (toolFilter.size > 0 && it.kind === 'tool-call') {
        if (!toolFilter.has(it.toolName)) return false;
      }
      return true;
    });
  }, [items, hideEnvelope, toolFilter]);

  const hasErrors = useMemo(
    () =>
      items.some(
        it =>
          it.kind === 'tool-call' && (it.result?.isError || it.orphanedResult),
      ),
    [items],
  );

  const jumpToNextError = useCallback(() => {
    // Walk forward from cursor; wrap to the start.
    const total = items.length;
    if (total === 0) return;
    for (let off = 1; off <= total; off++) {
      const i = (cursorIdx + off) % total;
      const it = items[i];
      if (it.kind === 'tool-call' && (it.result?.isError || it.orphanedResult)) {
        const el = document.getElementById(anchorIdFor(it));
        if (el) el.scrollIntoView({ block: 'center', behavior: 'smooth' });
        setCursorIdx(i);
        return;
      }
    }
  }, [items, cursorIdx]);

  // Shared IntersectionObserver across every visible card. Picks the
  // top-most intersecting card as the cursor target. Re-attaches when
  // the visible list changes.
  const streamRef = useRef<HTMLDivElement>(null);
  useEffect(() => {
    if (typeof IntersectionObserver === 'undefined') return undefined;
    const root = streamRef.current;
    if (!root) return undefined;
    const els: HTMLElement[] = [];
    const indexOfId = new Map<string, number>();
    visibleItems.forEach((it, i) => {
      const el = document.getElementById(anchorIdFor(it));
      if (el) {
        els.push(el);
        indexOfId.set(it.id, items.indexOf(it));
      }
    });
    if (els.length === 0) return undefined;
    const obs = new IntersectionObserver(
      entries => {
        // Visible entries → choose the one with the smallest top
        // (most likely "current"). Falls back to the most-recently
        // intersecting entry.
        const visible = entries.filter(e => e.isIntersecting);
        if (visible.length === 0) return;
        visible.sort(
          (a, b) => a.boundingClientRect.top - b.boundingClientRect.top,
        );
        const target = visible[0];
        const id = target.target.id.replace(/^tr-evt-/, '');
        const idx = indexOfId.get(id);
        if (typeof idx === 'number') setCursorIdx(idx);
      },
      { root, threshold: 0.1 },
    );
    els.forEach(el => obs.observe(el));
    return () => obs.disconnect();
  }, [visibleItems, items]);

  // Build the "prev timestamp" trail so each card can show a +Δs
  // delta. Indexed by item.id.
  const prevTsById = useMemo(() => {
    const m = new Map<string, string | undefined>();
    let prev: string | undefined;
    for (const it of items) {
      m.set(it.id, prev);
      if (it.ts) prev = it.ts;
    }
    return m;
  }, [items]);

  const copyAll = useCallback(() => {
    copyToClipboard(parsed.rawLines.join('\n'));
  }, [parsed.rawLines]);

  // BACI-141: compose the minimap eval ticks from the per-item map
  // plus the unanchored bucket. Each anchored item contributes one
  // tick at its index regardless of the number of notes attached (the
  // count goes into the tooltip); the unanchored bucket contributes
  // one tick at index 0 (the dispatch prompt card).
  const evalTicks = useMemo<EvalTick[]>(() => {
    const ticks: EvalTick[] = [];
    const dispatchItem = items[0];
    if (unanchoredNotes.length > 0 && dispatchItem) {
      ticks.push({
        key: `eval-unanchored-${unanchoredNotes.map(n => n.uuid).join('-')}`,
        anchorItemIndex: 0,
        count: unanchoredNotes.length,
        scrollTargetId: anchorIdFor(dispatchItem),
      });
    }
    items.forEach((item, idx) => {
      const notes = evalNotesByItemId.get(item.id);
      if (!notes || notes.length === 0) return;
      ticks.push({
        key: `eval-${item.id}`,
        anchorItemIndex: idx,
        count: notes.length,
        scrollTargetId: anchorIdFor(item),
      });
    });
    return ticks;
  }, [items, evalNotesByItemId, unanchoredNotes]);

  return (
    <div className="mk-transcript">
      <header className="mk-transcript-head">
        <div className="mk-transcript-head-row">
          {filename && (
            <span className="mk-transcript-head-name">{filename}</span>
          )}
          <span className="mk-transcript-head-spacer" />
          <button type="button" className="mk-btn-secondary" onClick={copyAll}>
            Copy all
          </button>
        </div>
        <div className="mk-transcript-head-meta">
          {dispatchTags?.issue && (
            <span className="mk-transcript-head-meta-item">
              {dispatchTags.issue}
            </span>
          )}
          {dispatchTags?.mode && (
            <span className="mk-transcript-head-meta-item">
              {dispatchTags.mode} mode
            </span>
          )}
          {dispatchTags?.dispatchId && (
            <span className="mk-transcript-head-meta-item">
              dispatch #{dispatchTags.dispatchId}
            </span>
          )}
          <span className="mk-transcript-head-meta-item">
            {items.length} events
          </span>
          {typeof sizeBytes === 'number' && sizeBytes > 0 && (
            <span className="mk-transcript-head-meta-item">
              {formatBytes(sizeBytes)}
            </span>
          )}
          <span className="mk-transcript-head-meta-item">
            {formatKilo(tokens.totalInput)} in / {formatKilo(tokens.totalOutput)}{' '}
            out
          </span>
          {(tokens.totalCacheRead > 0 || tokens.totalCacheCreate > 0) && (
            <span className="mk-transcript-head-meta-item">
              {tokens.cacheHitRatio.toFixed(2)} cache hit
            </span>
          )}
        </div>
      </header>
      <ControlsBar
        toolNamesUsed={toolNamesUsed}
        toolFilter={toolFilter}
        onToggleTool={toggleTool}
        onClearFilter={clearFilter}
        hideEnvelope={hideEnvelope}
        onToggleHideEnvelope={setHideEnvelope}
        showPerEventUsage={showPerEventUsage}
        onToggleUsage={setShowPerEventUsage}
        hasErrors={hasErrors}
        onJumpToNextError={jumpToNextError}
        warnings={parsed.warnings}
      />
      <div className="mk-transcript-grid">
        <Minimap
          items={items}
          cursorIdx={cursorIdx}
          anchorId={anchorIdFor}
          evalTicks={evalTicks}
        />
        <div ref={streamRef} className="mk-transcript-stream">
          {visibleItems.length === 0 ? (
            <div className="mk-meta-empty">
              No events match the current filters.
            </div>
          ) : (
            visibleItems.map(it => (
              <CardFor
                key={it.id}
                item={it}
                showPerEventUsage={showPerEventUsage}
                expandedIds={expandedIds}
                setExpandedIds={setExpandedIds}
                rawOpen={rawOpenIds.has(it.id)}
                onToggleRaw={() => toggleRaw(it.id)}
                prevTs={prevTsById.get(it.id)}
                evalNotes={evalNotesByItemId.get(it.id)}
                unanchoredNotes={it.kind === 'dispatch' ? unanchoredNotes : undefined}
                onPostEval={onPostEval}
              />
            ))
          )}
        </div>
      </div>
    </div>
  );
}

// eventRefFor (BACI-141) builds the durable anchor handle the per-
// event composer pins onto a new note. tool-call events anchor by
// `tool_use_id:<id>` (which appears in both the assistant tool_use
// block and its user-tool-result counterpart, so a note posted on
// either side of the pair surfaces against the same card). Other
// kinds fall back to `line_index:<n>` — durable today because
// `.jsonl` transcripts are append-only on disk and never re-streamed.
function eventRefFor(item: RenderItem): string {
  if (item.kind === 'tool-call' && item.use.id) {
    return `tool_use_id:${item.use.id}`;
  }
  const ev = 'ev' in item ? item.ev : ('assistantEv' in item ? item.assistantEv : undefined);
  const line = ev && typeof (ev as { lineIndex?: number }).lineIndex === 'number'
    ? (ev as { lineIndex: number }).lineIndex
    : undefined;
  if (typeof line === 'number') return `line_index:${line}`;
  // Fall-through belt: an item with neither a tool_use_id nor a
  // line index — shouldn't happen in practice, but the empty handle
  // is harmless (the resolver treats it as unanchored).
  return '';
}

type CardForProps = {
  item: RenderItem;
  showPerEventUsage: boolean;
  expandedIds: Record<string, boolean>;
  setExpandedIds: React.Dispatch<React.SetStateAction<Record<string, boolean>>>;
  rawOpen: boolean;
  onToggleRaw: () => void;
  prevTs?: string;
  // BACI-141: eval notes anchored to THIS event (per-event-anchored
  // notes only; unanchored notes ride on the dispatch card via
  // `unanchoredNotes`).
  evalNotes?: EvalComment[];
  // BACI-141: unanchored eval notes the dispatch card shows as a
  // catch-all (only meaningful when item.kind === 'dispatch').
  unanchoredNotes?: EvalComment[];
  // BACI-141: caller-supplied submit handler for the per-card
  // composer affordances. When absent the composer hides.
  onPostEval?: (body: string, eventRef: string) => Promise<void> | void;
};

function CardFor({
  item,
  showPerEventUsage,
  rawOpen,
  onToggleRaw,
  prevTs,
  evalNotes,
  unanchoredNotes,
  onPostEval,
}: CardForProps): React.ReactElement {
  const anchor = anchorIdFor(item);
  const ts = item.ts;
  const tsAbs = formatTimestamp(ts);
  // BACI-141: only items with a resolvable durable handle get an
  // event-level composer. The dispatch card has its own composer
  // (mounted by DispatchPromptCard with eventRef=''), so we omit the
  // EventCard composer there to avoid two side-by-side composers on
  // the same surface.
  const eventRef = item.kind === 'dispatch' ? undefined : eventRefFor(item);
  switch (item.kind) {
    case 'dispatch':
      return (
        <EventCard
          id={item.id}
          modifiers="is-dispatch"
          scrollAnchorId={anchor}
          headLeft={<>dispatch prompt</>}
          headRight={tsAbs && <span title={ts}>{tsAbs}</span>}
          ts={ts}
          prevTs={prevTs}
          raw={item.ev.raw}
          rawOpen={rawOpen}
          onToggleRaw={onToggleRaw}
          evalNotes={evalNotes}
        >
          <DispatchPromptCard
            text={item.ev.text}
            evalNotes={unanchoredNotes}
            onPostEval={onPostEval}
          />
        </EventCard>
      );
    case 'assistant': {
      const usage = item.usage;
      const usageBadge =
        showPerEventUsage && usage ? (
          <span className="mk-transcript-event-usage" title="Per-turn usage">
            {formatKilo(usage.input_tokens ?? 0)} in /{' '}
            {formatKilo(usage.output_tokens ?? 0)} out
          </span>
        ) : null;
      return (
        <EventCard
          id={item.id}
          modifiers="is-assistant"
          scrollAnchorId={anchor}
          headLeft={<>assistant</>}
          headMiddle={usageBadge}
          headRight={tsAbs && <span title={ts}>{tsAbs}</span>}
          ts={ts}
          prevTs={prevTs}
          raw={item.ev.raw}
          rawOpen={rawOpen}
          onToggleRaw={onToggleRaw}
          evalNotes={evalNotes}
          evalEventRef={eventRef}
          onPostEval={onPostEval}
        >
          <AssistantText blocks={item.blocks} />
        </EventCard>
      );
    }
    case 'tool-call': {
      const Renderer = rendererFor(item.toolName);
      const isError = !!item.result?.isError;
      const pill = isError ? (
        <span className="mk-transcript-pill is-error">ERROR</span>
      ) : item.orphanedResult ? (
        <span className="mk-transcript-pill is-warn">ORPHAN RESULT</span>
      ) : !item.result ? (
        <span className="mk-transcript-pill is-warn">NO RESULT</span>
      ) : null;
      return (
        <EventCard
          id={item.id}
          modifiers={`is-tool ${isError ? 'is-error' : ''}`}
          scrollAnchorId={anchor}
          headLeft={
            <code className="mk-transcript-tool-name">{item.toolName || '?'}</code>
          }
          pill={pill}
          headRight={tsAbs && <span title={ts}>{tsAbs}</span>}
          ts={ts}
          prevTs={prevTs}
          raw={item.assistantEv.raw}
          rawOpen={rawOpen}
          onToggleRaw={onToggleRaw}
          evalNotes={evalNotes}
          evalEventRef={eventRef}
          onPostEval={onPostEval}
        >
          <Renderer
            toolName={item.toolName}
            input={item.use.input}
            result={item.result}
          />
        </EventCard>
      );
    }
    case 'system-reminder':
      return (
        <EventCard
          id={item.id}
          modifiers="is-meta"
          scrollAnchorId={anchor}
          headLeft={<>system reminder</>}
          pill={<span className="mk-transcript-pill is-meta">SYSTEM</span>}
          headRight={tsAbs && <span title={ts}>{tsAbs}</span>}
          ts={ts}
          prevTs={prevTs}
          raw={item.ev.raw}
          rawOpen={rawOpen}
          onToggleRaw={onToggleRaw}
          evalNotes={evalNotes}
          evalEventRef={eventRef}
          onPostEval={onPostEval}
        >
          <details className="mk-transcript-collapse-native">
            <summary>Show reminder</summary>
            <pre className="mk-transcript-pre">{item.ev.text}</pre>
          </details>
        </EventCard>
      );
    case 'attachment':
      return (
        <EventCard
          id={item.id}
          modifiers="is-attachment"
          scrollAnchorId={anchor}
          headLeft={
            <>
              attachment&nbsp;
              <code className="mk-transcript-inline-code">
                {item.ev.attachmentType}
              </code>
            </>
          }
          headRight={tsAbs && <span title={ts}>{tsAbs}</span>}
          ts={ts}
          prevTs={prevTs}
          raw={item.ev.raw}
          rawOpen={rawOpen}
          onToggleRaw={onToggleRaw}
          evalNotes={evalNotes}
          evalEventRef={eventRef}
          onPostEval={onPostEval}
        >
          <div className="mk-meta-empty">
            Harness-emitted attachment ({item.ev.attachmentType}). Open Raw for
            the full payload.
          </div>
        </EventCard>
      );
  }
}

// Export the small helpers for tests / future callers.
export { anchorIdFor, prettyJSON };
