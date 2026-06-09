import { useState } from 'react';
import Tooltip from '../Tooltip';
import CardHead from './CardHead';
import CardTitleBlock from './CardTitleBlock';
import CardLabels from './CardLabels';
import ProcessMenu from './ProcessMenu';
import StageCardBody from './StageCardBody';
import StageCardFooter from './StageCardFooter';
import { useStageCardState } from './useStageCardState';
import { blockDragClass, blockDropProps } from './blockDrag';
import type { BlockKind } from './blockDrag';
import type { BoardCard, ProcessSelection } from '../../api';

// StageCard — the in-pipeline card that grows to fill the column. Issue
// header on top; the processing area (job chain + active job / question)
// in the body; all controls along the bottom. When no process has been
// chosen yet, a process-pick menu sits over the card.
type StageCardProps = {
  card: BoardCard;
  activeBoard?: string;
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
  onSetProcess?: (key: string, selection: ProcessSelection) => void;
  onSetProcessAuto?: (key: string, selection: ProcessSelection) => void;
  onResetProcess?: (key: string) => void;
  onEditProcess?: (key: string) => void;
  onStartJob?: (key: string) => void;
  onStopJob?: (key: string) => void;
  onRerunJob?: (key: string, seq: number) => void;
  onSetEngineMode?: (key: string, mode: string) => void;
  onShip?: (key: string) => void;
  onMarkDone?: (key: string) => void;
  onOpenQuestion?: (id: number) => void;
};

export default function StageCard({
  card,
  activeBoard,
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
  onSetProcess,
  onSetProcessAuto,
  onResetProcess,
  onEditProcess,
  onStartJob,
  onStopJob,
  onRerunJob,
  onSetEngineMode,
  onShip,
  onMarkDone,
  onOpenQuestion,
}: StageCardProps) {
  const [picking, setPicking] = useState(false);
  const state = useStageCardState(card);
  const { jobs, hasProcess, locked, paused } = state;

  const showProcessMenu = picking || !hasProcess;

  // BACI-342: the block-drag drop-target classes/handlers. Like the move
  // drag (gated by `locked`), the chip is only a block-drag *source* on an
  // unlocked card — blocking a card mid-job is a strange action, so the
  // grab handle follows the same Stop-first gate. A locked card can still
  // be a drop *target* (it just becomes blocked-by another card, which is
  // purely informational and doesn't move it).
  const blockClass = blockDragClass(blockKind);
  const blockProps = blockDropProps(blockKind, onBlockDrop);

  return (
    <Tooltip label={locked ? 'Stop the running job before moving this card' : undefined}>
    <article
      className={`mk-pl-stage${isDragging ? ' is-dragging' : ''}${paused ? ' is-attn' : ''}${isHighlighted ? ' is-blocker-hl' : ''}${locked ? ' is-locked' : ''}${blockClass}`}
      draggable={!locked}
      onDragStart={locked ? undefined : onDragStart}
      onDragEnd={locked ? undefined : onDragEnd}
      onDragOver={blockKind ? blockProps.onDragOver : undefined}
      onDrop={blockKind ? blockProps.onDrop : undefined}
    >
      <header className="mk-pl-stage-head" onClick={onOpen}>
        <CardHead
          card={card}
          activeBoard={activeBoard}
          onOpenIssue={onOpenIssue}
          onHighlight={onHighlight}
          canBlock={canBlock && !locked}
          onBlockDragStart={onBlockDragStart}
          onBlockDragEnd={onBlockDragEnd}
        />
        <CardTitleBlock card={card} titleClass="mk-pl-stage-title" impactPrimary={impactPrimary} />
        <CardLabels tags={card.tags} />
      </header>

      {showProcessMenu ? (
        <ProcessMenu
          dimmedHasProcess={hasProcess}
          jobs={jobs}
          onPick={(selection) => { setPicking(false); onSetProcess?.(card.key, selection); }}
          onPickAuto={(selection) => { setPicking(false); onSetProcessAuto?.(card.key, selection); }}
          onCancel={hasProcess ? () => setPicking(false) : null}
        />
      ) : (
        <>
          <StageCardBody
            card={card}
            state={state}
            onRerunJob={onRerunJob}
            onOpenQuestion={onOpenQuestion}
          />
          <StageCardFooter
            card={card}
            state={state}
            onResetProcess={onResetProcess}
            onEditProcess={onEditProcess}
            onStartJob={onStartJob}
            onStopJob={onStopJob}
            onSetEngineMode={onSetEngineMode}
            onShip={onShip}
            onMarkDone={onMarkDone}
          />
        </>
      )}
    </article>
    </Tooltip>
  );
}
