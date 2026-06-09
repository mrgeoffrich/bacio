import { describe, it, expect, vi } from 'vitest';
import { cardPropsEqual } from '../memoCard';
import type { BoardCard } from '../../../api';

// mkCard builds a minimal BoardCard for the comparator tests.
function mkCard(over: Partial<BoardCard> = {}): BoardCard {
  return { key: 'BACI-1', column: 'todo', columnLabel: 'todo', title: 'T', tags: [], ...over } as BoardCard;
}

const noop = () => {};

describe('cardPropsEqual', () => {
  it('treats two distinct card objects with the same value as equal', () => {
    // The poll hands back a fresh object every tick — value-equal cards must
    // compare equal so the memo skips the re-render.
    const a = mkCard({ tags: ['x'] });
    const b = mkCard({ tags: ['x'] });
    expect(a).not.toBe(b);
    expect(cardPropsEqual({ card: a, onOpen: noop }, { card: b, onOpen: noop })).toBe(true);
  });

  it('reports a card whose value changed as not equal', () => {
    const a = mkCard({ title: 'before' });
    const b = mkCard({ title: 'after' });
    expect(cardPropsEqual({ card: a }, { card: b })).toBe(false);
  });

  it('catches a deep field change (a nested blockedBy edge)', () => {
    const a = mkCard({ blockedBy: [] });
    const b = mkCard({ blockedBy: [{ key: 'BACI-2', state: 'todo' }] });
    expect(cardPropsEqual({ card: a }, { card: b })).toBe(false);
  });

  it('reports a changed scalar sibling prop as not equal', () => {
    const card = mkCard();
    expect(cardPropsEqual({ card, isDragging: false }, { card, isDragging: true })).toBe(false);
  });

  it('reports a changed callback identity as not equal', () => {
    const card = mkCard();
    expect(cardPropsEqual({ card, onOpen: noop }, { card, onOpen: () => {} })).toBe(false);
  });

  it('treats a stable callback identity + value-equal card as equal', () => {
    const onOpen = vi.fn();
    const a = mkCard();
    const b = mkCard();
    expect(cardPropsEqual({ card: a, onOpen }, { card: b, onOpen })).toBe(true);
  });
});
