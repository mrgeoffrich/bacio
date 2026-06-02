import type { Board } from '../api';

// RepoNotFound (BACI-285) is the hard-404 screen for a URL whose first
// segment isn't a known repo prefix — e.g. a stale `/ui/ZZZZ/pipeline`
// link to a repo that was never added or has since been removed. App
// renders this in place of the page routes when the prefix doesn't
// match any board (and isn't a recognised prefix-less legacy page word,
// which soft-redirects instead). The user picks a real repo from the
// list to jump back into the app; `onPickBoard` is App's URL-rewriting
// repo switch, so the choice routes to that repo's Pipeline.
//
// Reuses the existing empty-state styling (`mk-app-state` /
// `mk-features-empty` idiom) so no new CSS is needed.
type RepoNotFoundProps = {
  prefix: string;
  boards: Board[];
  onPickBoard: (prefix: string) => void;
};

export default function RepoNotFound({ prefix, boards, onPickBoard }: RepoNotFoundProps) {
  return (
    <div className="mk-app-state">
      <div className="mk-features-empty" style={{ flexDirection: 'column', gap: '12px' }}>
        <div>
          <strong>Repository {prefix} not found.</strong>
        </div>
        <div>
          No repository with that prefix is registered. Pick one below or
          add it from the repository picker above.
        </div>
        {boards.length > 0 && (
          <div className="mk-repo-picker-list" style={{ maxWidth: '320px', width: '100%' }}>
            {boards.map((b) => (
              <button
                key={b.prefix}
                className="mk-repo-picker-item"
                onClick={() => onPickBoard(b.prefix)}
                role="option"
              >
                <span className="mk-repo-picker-item-name">{b.name}</span>
                <span className="mk-repo-picker-item-prefix">{b.prefix}</span>
              </button>
            ))}
          </div>
        )}
      </div>
    </div>
  );
}
