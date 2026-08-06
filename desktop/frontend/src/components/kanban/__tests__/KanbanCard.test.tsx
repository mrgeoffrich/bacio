import { describe, it, expect, vi } from 'vitest';
import { render, screen, fireEvent } from '@testing-library/react';
import KanbanCard from '../KanbanCard';
import type { BoardCard } from '../../../api';

// The card menu is the only way back OFF the Kanban — drag can move a
// card between lanes but never out of all of them, and the seam's "off
// the board" write is an empty column uuid rather than a delete. Two
// things are worth pinning: the menu reaches the callback at all, and
// opening it does not also trip the card's own click-to-open.

function makeCard(key: string, title: string): BoardCard {
  return {
    key,
    column: 'todo',
    columnLabel: 'Todo',
    title,
    tags: [],
    assignees: [],
    claude: false,
    taken: false,
  };
}

const card = makeCard('BACI-7', 'Kanban lane CRUD');

describe('KanbanCard', () => {
  it('renders no menu when the board did not supply the handler', () => {
    render(<KanbanCard card={card} />);
    expect(screen.queryByRole('button', { name: 'Actions for BACI-7' })).not.toBeInTheDocument();
  });

  it('takes the card off the board from its menu', () => {
    const onTakeOffBoard = vi.fn();
    render(<KanbanCard card={card} onTakeOffBoard={onTakeOffBoard} />);
    fireEvent.keyDown(screen.getByRole('button', { name: 'Actions for BACI-7' }), { key: 'Enter' });
    fireEvent.click(screen.getByText('Take off board'));
    expect(onTakeOffBoard).toHaveBeenCalledWith('BACI-7');
  });

  it('does not open the issue when the menu button is clicked', () => {
    const onOpen = vi.fn();
    render(<KanbanCard card={card} onOpen={onOpen} onTakeOffBoard={vi.fn()} />);
    fireEvent.click(screen.getByRole('button', { name: 'Actions for BACI-7' }));
    expect(onOpen).not.toHaveBeenCalled();
  });

  it('still opens the issue when the card body is clicked', () => {
    const onOpen = vi.fn();
    render(<KanbanCard card={card} onOpen={onOpen} onTakeOffBoard={vi.fn()} />);
    fireEvent.click(screen.getByText('Kanban lane CRUD'));
    expect(onOpen).toHaveBeenCalledWith(card);
  });
});
