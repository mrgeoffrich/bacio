import type { BoardCard, KanbanColumn } from '../../api';

// Off-board derivation for the Kanban board — the other half of
// kanbanPlacement's container-side join.
//
// A card is ON the Kanban if and only if some lane's `cards` lists it
// (`issues.kanban_column_id IS NOT NULL` server-side). There is no
// "list the cards that aren't on the board" endpoint and there doesn't
// need to be one: the board view already holds every lane's card
// references AND the repo's full card list (the CardsProvider poll), so
// the off-board set is the difference between the two. Keeping that
// difference here — pure, no React, no api — is what makes it testable
// without a DOM and what stops the "Add cards" picker from growing its
// own fetch.
//
// This matters most on a git repo: `kanban_column_id` starts NULL by
// design there (that is what keeps a `todo` card on the Agentic Pipeline
// Backlog and off the Kanban), so on a fresh repo EVERY card is
// off-board and the picker is the only in-app way onto the lane.

// onBoardKeys collapses the whole board to the set of issue keys it
// holds. Lanes are containers of references, so this is a flat union —
// the store's uniqueness invariant (a card sits in at most one lane)
// means no key can appear twice, but a Set is the right shape for the
// membership test regardless.
export function onBoardKeys(columns: KanbanColumn[]): Set<string> {
  const keys = new Set<string>();
  for (const column of columns) {
    for (const ref of column.cards) keys.add(ref.key);
  }
  return keys;
}

// offBoardCards is the picker's candidate list: every card the repo has
// that no lane holds, in the card list's own order.
//
// Cards the user has chosen not to see (the archived-visibility
// preference filters them out of `cards` upstream) are absent from the
// input and therefore absent from the candidates — which is the right
// behaviour and needs no special case here.
export function offBoardCards(columns: KanbanColumn[], cards: BoardCard[]): BoardCard[] {
  const onBoard = onBoardKeys(columns);
  return cards.filter(card => !onBoard.has(card.key));
}

// filterCardsByQuery is the picker's search. Same match rule as the
// command palette (`key + title`, case-insensitive substring) so typing
// "BACI-42" or "sync badge" narrows the same way in both places — a real
// repo has hundreds of issues and an unsearchable list is unusable.
export function filterCardsByQuery(cards: BoardCard[], query: string): BoardCard[] {
  const q = query.trim().toLowerCase();
  if (!q) return cards;
  return cards.filter(card => `${card.key} ${card.title}`.toLowerCase().includes(q));
}

// removeCardFromBoard is the optimistic half of "take this card off the
// board" — the local twin of `moveIssueToKanbanColumn(prefix, key, '',
// null)`, where the empty column uuid means OFF the board. The issue is
// never deleted; only its lane membership goes.
//
// Lanes that don't hold the key are returned by identity so React can
// skip them, matching placeCardInLane's contract.
export function removeCardFromBoard(columns: KanbanColumn[], key: string): KanbanColumn[] {
  return columns.map(col => {
    const without = col.cards.filter(ref => ref.key !== key);
    if (without.length === col.cards.length) return col;
    return { ...col, cards: without.map((ref, index) => ({ ...ref, position: index })) };
  });
}

// boardHintText is the one line rendered under the board when it has
// nothing useful to show. Empty string = render no hint at all.
//
// Phase 6A pointed all three of these cases at a terminal
// (`bacio kanban move …` / `bacio kanban column add …`); Phase 7B put the
// affordances in the UI, so the copy now points at those instead. The
// genuinely-empty cases still earn a line — a blank grid with a single
// dashed slot needs a sentence saying what to do with it.
export function boardHintText(
  laneCount: number,
  onBoardCount: number,
  offBoardCount: number,
): string {
  if (laneCount === 0) {
    return 'No lanes on this Kanban yet — add one to start putting work on the board.';
  }
  if (onBoardCount > 0) return '';
  if (offBoardCount === 0) {
    return 'This repository has no issues to put on the board yet.';
  }
  const noun = offBoardCount === 1 ? 'issue' : 'issues';
  return `Nothing is on this Kanban yet — use + on a lane to put any of its ${offBoardCount} ${noun} on the board.`;
}
