import { describe, it, expect, vi } from 'vitest';
import { render, screen, fireEvent } from '@testing-library/react';
import CommandPalette from '../CommandPalette';
import type { BoardCard } from '../../api';

// CommandPalette is a stable, self-contained surface that imports the
// `./api` BoardCard type and renders an Icon — a good proof the harness
// resolves the transport seam end-to-end while exercising the quick-jump
// filtering. Only the four fields the component reads are populated; the
// cast keeps the fixture terse without redefining the wire shape.
function makeCard(key: string, title: string, column: string, columnLabel: string): BoardCard {
  return { key, title, column, columnLabel } as BoardCard;
}

const cards = [
  makeCard('BACI-1', 'Fix login flow', 'todo', 'Backlog'),
  makeCard('BACI-2', 'Refresh board cards', 'in_pipeline', 'Pipeline'),
  makeCard('MINI-9', 'Login banner copy', 'to_be_shipped', 'Shipping'),
];

describe('CommandPalette', () => {
  it('renders nothing when closed', () => {
    render(<CommandPalette open={false} cards={cards} onClose={vi.fn()} onPick={vi.fn()} />);
    expect(screen.queryByRole('dialog')).not.toBeInTheDocument();
  });

  it('lists cards when open and filters by query across title and key', () => {
    render(<CommandPalette open cards={cards} onClose={vi.fn()} onPick={vi.fn()} />);
    expect(screen.getByText('Fix login flow')).toBeInTheDocument();
    expect(screen.getByText('Login banner copy')).toBeInTheDocument();

    fireEvent.change(screen.getByPlaceholderText(/Jump to issue/), { target: { value: 'login' } });
    expect(screen.getByText('Fix login flow')).toBeInTheDocument();
    expect(screen.getByText('Login banner copy')).toBeInTheDocument();
    expect(screen.queryByText('Refresh board cards')).not.toBeInTheDocument();

    fireEvent.change(screen.getByPlaceholderText(/Jump to issue/), { target: { value: 'mini-9' } });
    expect(screen.getByText('Login banner copy')).toBeInTheDocument();
    expect(screen.queryByText('Fix login flow')).not.toBeInTheDocument();
  });

  it('shows the empty state when nothing matches', () => {
    render(<CommandPalette open cards={cards} onClose={vi.fn()} onPick={vi.fn()} />);
    fireEvent.change(screen.getByPlaceholderText(/Jump to issue/), { target: { value: 'zzz-nope' } });
    expect(screen.getByText(/no matches/)).toBeInTheDocument();
  });

  it('invokes onPick then onClose when a result is chosen', () => {
    const onPick = vi.fn();
    const onClose = vi.fn();
    render(<CommandPalette open cards={cards} onClose={onClose} onPick={onPick} />);
    fireEvent.click(screen.getByText('Fix login flow'));
    expect(onPick).toHaveBeenCalledWith(cards[0]);
    expect(onClose).toHaveBeenCalled();
  });
});
