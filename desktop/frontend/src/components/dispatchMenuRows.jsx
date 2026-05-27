import React from 'react';
import * as DropdownMenu from '@radix-ui/react-dropdown-menu';
import { computeZapMenuTree } from './dispatchMenuRows.ts';

// BACI-247: the React renderer for the kanban zap-button popover
// body. The pure bucketing + compound-matrix shape lives in the
// sibling `dispatchMenuRows.ts` (`computeZapMenuTree`); this file
// wraps each bucket in the Radix `DropdownMenu.Item`s that own
// click-to-dispatch + close-on-select.
//
// Mirrors the follow-on chip's renderRows body (KanbanCard.jsx:491-525):
// primary modes render as plain rows under the menu's filter shell,
// the unusual bucket sits inside a `<details>Show all</details>`
// block. The wrinkle the zap menu owns over the follow-on chip is
// the BACI-209 compound-chain affordance — each primary is wrapped
// in its own `<details>` whose `<summary>` is the dispatch row plus
// a small caret; opening reveals one `"<primary>, then <q>"`
// compound row per other visible mode (each firing
// `onDispatchChain`, unchanged from BACI-209). Default state is
// collapsed so the menu reads at single-mode density.
//
// The inner `DropdownMenu.Item` inside `<summary>` stops click
// propagation so clicking the primary label fires onSelect (and
// Radix closes the menu) without also toggling the `<details>` as a
// side-effect — the caret is the only `<summary>`-toggling target.
export function renderZapMenuRows({
  visible,
  promptConfig,
  cardColumn,
  stateGraph,
  cardKey,
  onDispatch,
  onDispatchChain,
}) {
  const tree = computeZapMenuTree({
    visible,
    promptConfig,
    cardColumn,
    stateGraph,
    hasOnDispatchChain: !!onDispatchChain,
  });

  if (tree.primaries.length === 0 && tree.unusual.length === 0) {
    return (
      <div className="mk-dispatch-menu-empty">
        No matching modes for this card&apos;s state.
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
      {tree.unusual.length > 0 && (
        <details className="mk-dispatch-menu-unusual">
          <summary className="mk-dispatch-menu-unusual-summary">
            Show all
          </summary>
          {tree.unusual.map(p => (
            <DropdownMenu.Item
              key={p.mode}
              data-dispatch-row=""
              className="mk-card-action-item"
              onSelect={() => onDispatch(cardKey, p.mode)}
              onClick={(e) => e.stopPropagation()}
            >
              {p.actionLabel || p.label}
            </DropdownMenu.Item>
          ))}
        </details>
      )}
    </>
  );
}
