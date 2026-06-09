import { Link } from 'react-router';
import type { FeatureLinkedDoc } from '../../api';
import { documentPath } from '../../lib/routes';

// FeatureLinkedDocsSection (BACI-214) lists documents linked to the feature
// via `bacio doc link <file> <feature-slug>`. Each row carries a type badge, a
// `<Link>` into the canonical `/documents/<filename>` viewer (the same route
// the Documents screen uses), and an inline `— description` when the link was
// attached with `--why`. Always-render shape mirrors the Issues section's "No
// issues linked yet." idiom so the section is present-as-empty and doesn't pop
// in when the first doc lands. Uses .mk-linked-doc + .mk-attachment-* from
// app.css / desktop.css so no new CSS is needed.
type FeatureLinkedDocsSectionProps = {
  activeBoard: string;
  documents: FeatureLinkedDoc[];
};

export default function FeatureLinkedDocsSection({ activeBoard, documents }: FeatureLinkedDocsSectionProps) {
  return (
    <section className="mk-features-section">
      <div className="mk-features-label">Documents · {documents.length}</div>
      {documents.length === 0 ? (
        <p className="mk-features-text mk-meta-empty">No documents linked.</p>
      ) : (
        <div className="mk-linked-doc-list">
          {documents.map((d: FeatureLinkedDoc) => (
            <div key={d.filename} className="mk-linked-doc">
              <div className="mk-linked-doc-head">
                <span className="mk-attachment-badge">{d.type || 'doc'}</span>
                <Link
                  to={documentPath(activeBoard, d.filename)}
                  className="mk-attachment-name mk-attachment-link"
                >
                  {d.filename}
                </Link>
                {d.description && (
                  <span className="mk-attachment-why">— {d.description}</span>
                )}
              </div>
            </div>
          ))}
        </div>
      )}
    </section>
  );
}
