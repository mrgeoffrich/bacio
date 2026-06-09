import { useState } from 'react';
import Icon from '../Icon';
import CardHead from './CardHead';
import CardTitleBlock from './CardTitleBlock';
import CardLabels from './CardLabels';
import { blockDragClass, blockDropProps } from './blockDrag';
import type { BlockKind } from './blockDrag';
import type { BoardCard } from '../../api';

// PipelineCard — the compact issue card for Backlog and Shipping. Issue
// info only (feature glyph, key, plan/PR icon buttons, blocked-by badge,
// title, labels) — the issue card only ever shows the issue itself.
// Shipping adds a position badge + the Next-to-ship SHIP row / waiting
// status.
type PipelineCardProps = {
  card: BoardCard;
  activeBoard?: string;
  index?: number;
  showBadge?: boolean;
  shipping?: boolean;
  backlog?: boolean;
  isNextToShip?: boolean;
  autoShip?: boolean;
  impactPrimary?: boolean;
  isDragging?: boolean;
  isHighlighted?: boolean;
  canBlock?: boolean;
  blockKind?: BlockKind;
  onBlockDragStart?: (key: string) => void;
  onBlockDragEnd?: () => void;
  onBlockDrop?: () => void;
  onOpen?: () => void;
  onOpenIssue?: (key: string) => void;
  onHighlight?: (key: string | null) => void;
  onDragStart?: () => void;
  onDragEnd?: () => void;
  onDropCard?: () => void;
  onMoveCard?: (key: string, col: string) => void;
  onFastTrack?: (key: string) => void;
  onShipDispatch?: (key: string, mode: string) => void;
  onCancelWaiting?: (key: string) => void;
};

export default function PipelineCard({
  card,
  activeBoard,
  index,
  showBadge,
  shipping,
  backlog,
  isNextToShip,
  autoShip,
  impactPrimary,
  isDragging,
  isHighlighted,
  canBlock,
  blockKind,
  onBlockDragStart,
  onBlockDragEnd,
  onBlockDrop,
  onOpen,
  onOpenIssue,
  onHighlight,
  onDragStart,
  onDragEnd,
  onDropCard,
  onMoveCard,
  onFastTrack,
  onShipDispatch,
  onCancelWaiting,
}: PipelineCardProps) {
  const [over, setOver] = useState(false);
  const waiting = !!card.waitingState && !card.taken;
  const shippingInFlight = shipping && (card.taken || waiting);
  // BACI-342: while a block-drag is in flight, this card's drag-over /
  // drop route to the block gesture (cls) instead of the move/reorder one.
  // A 'target' card paints the coral drop highlight; 'source'/'dup' paint
  // the muted not-allowed variant (so the no-op is visible before the
  // drop). blockKind is null whenever no block-drag is active.
  const blockClass = blockDragClass(blockKind);
  const blockProps = blockDropProps(blockKind, onBlockDrop);

  return (
    <article
      className={`mk-pl-card${isDragging ? ' is-dragging' : ''}${over ? ' is-drop-before' : ''}${shippingInFlight ? ' is-shipping' : ''}${isHighlighted ? ' is-blocker-hl' : ''}${blockClass}`}
      draggable
      onDragStart={onDragStart}
      onDragEnd={onDragEnd}
      onDragOver={blockKind ? blockProps.onDragOver : (e) => { e.preventDefault(); e.stopPropagation(); setOver(true); }}
      onDragLeave={blockKind ? undefined : () => setOver(false)}
      onDrop={blockKind ? blockProps.onDrop : (e) => { e.preventDefault(); e.stopPropagation(); setOver(false); onDropCard?.(); }}
      onClick={onOpen}
    >
      {showBadge && (
        <span
          className={`mk-pl-badge${shipping && isNextToShip ? ' is-next' : ''}`}
          aria-hidden="true"
        >
          {(index ?? 0) + 1}
        </span>
      )}
      <CardHead
        card={card}
        activeBoard={activeBoard}
        onOpenIssue={onOpenIssue}
        onHighlight={onHighlight}
        canBlock={canBlock}
        onBlockDragStart={onBlockDragStart}
        onBlockDragEnd={onBlockDragEnd}
      />
      <CardTitleBlock card={card} titleClass="mk-pl-card-title" impactPrimary={impactPrimary} />
      <CardLabels tags={card.tags} />
      {backlog && (
        <div className="mk-pl-card-foot">
          <span className="mk-pl-spacer" />
          {/* Fast-track (BACI-311): one click moves the card into the
              pipeline, assigns Plan → Implement → Ship, and turns Auto on.
              BACI-335: a ghost/secondary button (matching the sibling "Move
              into pipeline" ghost) so it stands out via its rainbow-outlined
              zap rather than heavy primary fill. The zap's stroke is painted
              with the shared #mk-zap-rainbow gradient def (rendered once on
              the board). */}
          <button
            type="button"
            className="mk-pl-btn is-ghost is-sm mk-pl-fasttrack"
            title="Fast-track: into pipeline, Plan → Implement → Ship, Auto on"
            aria-label="Fast-track: into pipeline, Plan → Implement → Ship, Auto on"
            onClick={(e) => { e.stopPropagation(); onFastTrack?.(card.key); }}
          >
            <Icon name="zap" />
          </button>
          <button
            type="button"
            className="mk-pl-btn is-ghost is-sm"
            title="Move into the pipeline"
            aria-label="Move into the pipeline"
            onClick={(e) => { e.stopPropagation(); onMoveCard?.(card.key, 'in_pipeline'); }}
          >
            <Icon name="forward" />
          </button>
        </div>
      )}
      {shipping && (
        <div className="mk-pl-ship-row">
          {shippingInFlight ? (
            <>
              <span className="mk-pl-ship-status">
                <span className="mk-pl-spin" /> Shipping…
              </span>
              <span className="mk-pl-spacer" />
              {waiting && (
                <button
                  type="button"
                  className="mk-pl-btn is-ghost is-danger is-sm"
                  onClick={(e) => { e.stopPropagation(); onCancelWaiting?.(card.key); }}
                >
                  Cancel
                </button>
              )}
            </>
          ) : isNextToShip ? (
            <>
              <span className="mk-pl-next">Next to ship</span>
              <span className="mk-pl-spacer" />
              {!autoShip && (
                <button
                  type="button"
                  className="mk-pl-btn is-primary is-sm"
                  onClick={(e) => { e.stopPropagation(); onShipDispatch?.(card.key, 'ship'); }}
                >
                  ⏏ SHIP
                </button>
              )}
            </>
          ) : (
            <span className="mk-pl-queued">⏳ Waiting in queue</span>
          )}
        </div>
      )}
    </article>
  );
}
