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
import { evalEventRefFor, resolveAnchor } from './evalAnchor';
import { formatBytes, formatTimestamp, prettyJSON } from './format';
import { parse } from './parse';
import { pair } from './pair';
import { formatKilo, summariseTokens } from './tokenSummary';
import { rendererFor } from './tools/index';
import type { EvalComment, ParseResult, RenderItem } from './types';

type TranscriptViewProps = {
  // The raw .jsonl body to parse. The default source for a Claude Code
  // subagent transcript. Mutually exclusive with `parsed`: pass exactly one.
  source?: string;
  // A pre-built ParseResult, for callers whose transcript was parsed
  // elsewhere (BACI-308: the proxy-captured AnthropicTranscript adapted via
  // anthropicTranscriptToParseResult). When present, `source` is ignored and
  // no .jsonl parsing happens — the rest of the view (minimap, filters, cards,
  // token totals) is identical.
  parsed?: ParseResult;
  filename?: string;
  sizeBytes?: number;
  // BACI-141: eval comments to render inline with the matching
  // events. The viewer filters the array by dispatch id / session id
  // before resolving each comment's anchor, so the caller can pass the
  // full issue-level eval list and rely on the viewer to keep only the
  // rows that belong to THIS transcript's run.
  evalComments?: EvalComment[];
  // Post-eval callback fired when the per-event composer submits. The
  // callback receives the typed body and the `transcriptEventRef`
  // string (empty for the unanchored fallback). Optional — read-only
  // surfaces (e.g. tests, exports) leave it unset and the composer
  // affordance hides.
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
  parsed: parsedProp,
  filename,
  sizeBytes,
  evalComments,
  onPostEval,
}: TranscriptViewProps): React.ReactElement {
  // Parse + pair is a useMemo on `source` so a no-op refetch (the
  // 10s brief poll handing us the same body string back) re-uses the
  // same arrays and React skips the per-card re-render. A caller that
  // already has a ParseResult (BACI-308's adapted proxy transcript) passes
  // it directly and the .jsonl parse is skipped entirely.
  const parsed = useMemo(
    () => parsedProp ?? parse(source ?? ''),
    [parsedProp, source],
  );
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

  // BACI-141: filter the incoming eval-comment list to those that
  // belong to THIS transcript's dispatch, then resolve each comment's
  // anchor (or bucket it as unanchored). Defensive: if the transcript
  // carries no <dispatch_id> tag, fall back to matching the session id
  // — a dispatch tag is the primary key but a hand-uploaded transcript
  // (no tag) still has a sessionId via filename / source path.
  const { anchoredByItemId, unanchoredNotes, evalTicks } = useMemo(() => {
    const anchoredByItemId = new Map<string, EvalComment[]>();
    const unanchoredNotes: EvalComment[] = [];
    const evalTicks: EvalTick[] = [];
    if (!evalComments || evalComments.length === 0) {
      return { anchoredByItemId, unanchoredNotes, evalTicks };
    }
    const dispatchID = dispatchTags?.dispatchId;
    const dispatchPrompt = items.find(i => i.kind === 'dispatch');
    const dispatchAnchorIdx = items.findIndex(i => i.kind === 'dispatch');
    for (const c of evalComments) {
      // Filter by dispatchId; fall back to agentSessionId when the
      // transcript carries no dispatch tag (legacy uploads / manual
      // attaches). A comment with neither field set never matches —
      // safe default.
      let belongs = false;
      if (dispatchID && c.dispatchId != null && String(c.dispatchId) === dispatchID) {
        belongs = true;
      } else if (!dispatchID && c.agentSessionId) {
        belongs = true;
      }
      if (!belongs) continue;
      if (c.transcriptEventRef) {
        const item = resolveAnchor(items, c.transcriptEventRef);
        if (item) {
          const list = anchoredByItemId.get(item.id);
          if (list) list.push(c);
          else anchoredByItemId.set(item.id, [c]);
          const idx = items.indexOf(item);
          if (idx >= 0) {
            evalTicks.push({
              anchorIndex: idx,
              label: c.body.slice(0, 60),
              anchorId: anchorIdFor(item),
            });
          }
          continue;
        }
        // Ref didn't resolve — fall back to unanchored bucket so we
        // never silently drop a note.
      }
      unanchoredNotes.push(c);
      if (dispatchPrompt && dispatchAnchorIdx >= 0) {
        evalTicks.push({
          anchorIndex: dispatchAnchorIdx,
          label: c.body.slice(0, 60),
          anchorId: anchorIdFor(dispatchPrompt),
        });
      }
    }
    return { anchoredByItemId, unanchoredNotes, evalTicks };
  }, [evalComments, items, dispatchTags]);

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
    visibleItems.forEach((it, _i) => {
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
        <Minimap items={items} cursorIdx={cursorIdx} anchorId={anchorIdFor} evalTicks={evalTicks} />
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
                evalNotes={anchoredByItemId.get(it.id)}
                unanchoredNotes={it.kind === 'dispatch' ? unanchoredNotes : undefined}
                onPostEval={onPostEval}
                evalEventRef={evalEventRefFor(it)}
              />
            ))
          )}
        </div>
      </div>
    </div>
  );
}

type CardForProps = {
  item: RenderItem;
  showPerEventUsage: boolean;
  expandedIds: Record<string, boolean>;
  setExpandedIds: React.Dispatch<React.SetStateAction<Record<string, boolean>>>;
  rawOpen: boolean;
  onToggleRaw: () => void;
  prevTs?: string;
  // BACI-141: eval-comment props forwarded to the per-card chrome.
  evalNotes?: EvalComment[];
  unanchoredNotes?: EvalComment[];
  onPostEval?: (body: string, eventRef: string) => Promise<void> | void;
  evalEventRef?: string;
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
  evalEventRef,
}: CardForProps): React.ReactElement {
  const anchor = anchorIdFor(item);
  const ts = item.ts;
  const tsAbs = formatTimestamp(ts);
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
        >
          {/*
            BACI-141: the dispatch prompt card owns the unanchored
            eval fallback + the dispatch-level composer. Per-event
            anchored notes (if any) ride on the outer EventCard's
            evalNotes slot via the explicit props below.
          */}
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
          onPostEval={onPostEval}
          evalEventRef={evalEventRef}
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
          onPostEval={onPostEval}
          evalEventRef={evalEventRef}
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
          onPostEval={onPostEval}
          evalEventRef={evalEventRef}
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
          onPostEval={onPostEval}
          evalEventRef={evalEventRef}
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
