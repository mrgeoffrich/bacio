import { describe, it, expect } from 'vitest';
import { render, screen, act, fireEvent } from '@testing-library/react';
import ErrorModal from '../ErrorModal';
import { reportError } from '../../errors';

// ErrorModal is mounted once at the App root and subscribes to the
// module-level error bus in errors.ts. These tests drive the bus the
// same way the real app does (reportError) and assert the modal surfaces
// the result — covering the React side of the BACI-43 error path that
// the errors.ts smoke tests can't reach.

describe('ErrorModal', () => {
  it('renders nothing until an error is reported', () => {
    render(<ErrorModal />);
    expect(screen.queryByRole('dialog')).not.toBeInTheDocument();
  });

  it('surfaces a reported error headline and message', () => {
    render(<ErrorModal />);
    act(() => {
      reportError(new Error('board refresh failed'), { headline: 'Could not refresh board' });
    });
    expect(screen.getByText('Could not refresh board')).toBeInTheDocument();
    expect(screen.getByText('board refresh failed')).toBeInTheDocument();
  });

  it('queues multiple errors and advances on dismiss', () => {
    render(<ErrorModal />);
    act(() => {
      reportError(new Error('first failure'), { headline: 'First headline' });
      reportError(new Error('second failure'), { headline: 'Second headline' });
    });

    expect(screen.getByText('First headline')).toBeInTheDocument();
    expect(screen.getByText(/1 more error queued/)).toBeInTheDocument();

    fireEvent.click(screen.getByRole('button', { name: 'Dismiss' }));

    expect(screen.queryByText('First headline')).not.toBeInTheDocument();
    expect(screen.getByText('Second headline')).toBeInTheDocument();
  });
});
