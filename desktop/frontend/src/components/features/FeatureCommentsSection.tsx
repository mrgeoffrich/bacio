import * as api from '../../api';
import type { FeatureDetail, FeatureCommentDTO } from '../../api';
import { reportError } from '../../errors';
import MarkdownView from '../../lib/markdownView';
import CommentComposer from '../issue/CommentComposer';
import { commentTimestamp } from './format';

// FeatureCommentsSection renders the BACI-124 handoff timeline plus an
// inline composer. Reuses the same MarkdownView + CommentComposer used
// by the issue drawer so the markdown rendering rule (`<MarkdownView>`
// is the canonical reader, never `react-markdown` directly) holds.
type FeatureCommentsSectionProps = {
  repoPrefix: string;
  detail: FeatureDetail;
  onChange: (detail: FeatureDetail) => void;
};

export default function FeatureCommentsSection({ repoPrefix, detail, onChange }: FeatureCommentsSectionProps) {
  const comments = detail.comments ?? [];
  const onSubmit = async (author: string, body: string) => {
    try {
      const updated = await api.addFeatureComment(
        repoPrefix,
        detail.slug,
        author,
        body,
      );
      onChange(updated);
    } catch (err) {
      reportError(err, { headline: "Couldn't add comment" });
    }
  };
  const onDelete = async (uuid: string) => {
    try {
      const updated = await api.deleteFeatureComment(
        repoPrefix,
        detail.slug,
        uuid,
      );
      onChange(updated);
    } catch (err) {
      reportError(err, { headline: "Couldn't delete comment" });
    }
  };
  return (
    <section className="mk-features-section">
      <div className="mk-features-label">Comments · {comments.length}</div>
      {comments.length === 0 ? (
        <p className="mk-features-text mk-meta-empty">No comments yet.</p>
      ) : (
        // BACI-213: reuse the issue drawer's Activity timeline shape so
        // the per-comment delete affordance (hover-revealed mk-tl-delete
        // + window.confirm) matches across surfaces. Feature comments
        // don't carry eval / mode / agent metadata, so no eval pill or
        // footer here — just author + timestamp + body + delete.
        <ul className="mk-timeline">
          {comments.map((c: FeatureCommentDTO) => (
            <li key={c.uuid} className="mk-tl-item">
              <span className="mk-tl-dot" />
              <div className="mk-tl-text">
                <b className="mk-tl-author">{c.author}</b>
                <span>{commentTimestamp(c.createdAt)}</span>
                <MarkdownView className="mk-markdown mk-tl-body">
                  {c.body}
                </MarkdownView>
              </div>
              {c.uuid && (
                <button
                  type="button"
                  className="mk-tl-delete"
                  title="Delete comment"
                  aria-label="Delete comment"
                  onClick={() => {
                    if (window.confirm('Delete this comment? This cannot be undone.')) {
                      onDelete(c.uuid);
                    }
                  }}
                >
                  ✕
                </button>
              )}
            </li>
          ))}
        </ul>
      )}
      <CommentComposer onSubmit={onSubmit} />
    </section>
  );
}
