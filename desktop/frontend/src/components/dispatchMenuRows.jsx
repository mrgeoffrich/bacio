import React from 'react';
import * as DropdownMenu from '@radix-ui/react-dropdown-menu';
import { computeZapMenuTree } from './dispatchMenuRows.ts';

// BACI-252: the React renderer for the kanban zap-button popover body.
// The pure compound-matrix shape lives in the sibling
// `dispatchMenuRows.ts` (`computeZapMenuTree`); this file wraps each
// primary in the Radix `DropdownMenu.Item`s that own click-to-dispatch
// + close-on-select.
//
// Every visible non-reserved mode renders as a primary row — there is
// no per-state filtering or "Show all" disclosure. The BACI-209
// compound-chain affordance is preserved as a per-primary `<details>`
// expander: clicking the primary label fires onDispatch (single
// mode); opening the caret reveals one "<primary>, then <q>" compound
// row per other visible mode, each firing onDispatchChain. Default
// state is collapsed so the menu reads at single-mode density.
//
// The inner `DropdownMenu.Item` inside `<summary>` stops click
// propagation so clicking the primary label fires onSelect (and Radix
// closes the menu) without also toggling the `<details>` as a
// side-effect — the caret is the only `<summary>`-toggling target.
export function renderZapMenuRows({
  visible,
  cardKey,
  onDispatch,
  onDispatchChain,
}) {
  const tree = computeZapMenuTree({
    visible,
    hasOnDispatchChain: !!onDispatchChain,
  });

  if (tree.primaries.length === 0) {
    return (
      <div className="mk-dispatch-menu-empty">
        No matching modes.
      </div>
    );
  }

  return (
    <>
      {tree.primaries.map(({ prompt: p, compounds }) => {
        const primaryLabel = p.actionLabel || p.label;
        return (
          <details key={p.mode} className="mk-dispatch-menu-compound">
            <summary className="mk-dispatch-menu-compound-summary">
              <DropdownMenu.Item
                data-dispatch-row=""
                className="mk-card-action-item"
                onSelect={() => onDispatch(cardKey, p.mode)}
                onClick={(e) => e.stopPropagation()}
              >
                {primaryLabel}
              </DropdownMenu.Item>
              <span className="mk-dispatch-menu-compound-caret" aria-hidden="true" />
            </summary>
            {compounds.length > 0 && (
              <div className="mk-dispatch-menu-compound-list">
                {compounds.map(q => {
                  const followOnLabel = q.actionLabel || q.label;
                  return (
                    <DropdownMenu.Item
                      key={`${p.mode}-then-${q.mode}`}
                      data-dispatch-row=""
                      className="mk-card-action-item is-compound"
                      onSelect={() => onDispatchChain(cardKey, p.mode, q.mode)}
                      onClick={(e) => e.stopPropagation()}
                    >
                      {primaryLabel}, then {followOnLabel}
                    </DropdownMenu.Item>
                  );
                })}
              </div>
            )}
          </details>
        );
      })}
    </>
  );
}
