import React from 'react';
import { reportError } from '../errors';

type ErrorBoundaryProps = {
  children: React.ReactNode;
  headline?: string;
  label?: string;
};

type ErrorBoundaryState = {
  hasError: boolean;
};

// ErrorBoundary wraps a subtree (a top-level view, an overlay) so a
// render-time throw inside it doesn't crash the whole renderer.
// componentDidCatch forwards the error to the global bus so the
// ErrorModal surfaces it; the local fallback offers a "Reload this
// view" button that resets the boundary's state and re-renders the
// subtree.
//
// Pass `headline` to customise the modal headline for the wrapped
// subtree (e.g. "Something went wrong in the board"). Pass `label`
// to customise the fallback heading text.
export default class ErrorBoundary extends React.Component<ErrorBoundaryProps, ErrorBoundaryState> {
  state: ErrorBoundaryState = { hasError: false };

  static getDerivedStateFromError(): ErrorBoundaryState {
    return { hasError: true };
  }

  componentDidCatch(err: unknown, info: React.ErrorInfo) {
    const componentStack = info?.componentStack || '';
    reportError(err, {
      headline: this.props.headline || 'Something went wrong in this view',
      source: 'react-boundary' + (componentStack ? `:${componentStack.trim().split('\n')[0]}` : ''),
    });
  }

  reset = () => this.setState({ hasError: false });

  render() {
    if (this.state.hasError) {
      return (
        <div className="mk-view-error">
          <div className="mk-view-error-title">
            {this.props.label || 'This view crashed'}
          </div>
          <div className="mk-view-error-hint">
            The rest of the app is still running. Use the button below
            to try this view again, or switch views from the top bar.
          </div>
          <button className="mk-btn-secondary" onClick={this.reset}>
            Reload this view
          </button>
        </div>
      );
    }
    return this.props.children;
  }
}
