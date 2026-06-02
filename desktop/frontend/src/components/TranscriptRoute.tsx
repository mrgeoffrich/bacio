import { Link, useParams } from 'react-router';
import { monitorTranscriptsPath } from '../lib/routes';
import JobTranscriptBody from '../lib/transcript/JobTranscriptBody';

// TranscriptRoute is the BACI-322 deep-linkable full-transcript page —
// `/<prefix>/monitor/transcript/<dispatchId>`. It renders the same assembled
// per-dispatch transcript the BACI-308 capture sheet shows, but as its own
// addressable route so the URL can be deep-linked from other surfaces /
// comments. The back-header renders immediately off the URL params (so the
// chrome is up before the transcript fetch resolves); JobTranscriptBody owns
// the loading / ready / not-found body (a pruned or unparsed dispatch hits its
// "No assembled transcript" empty state, no extra not-found handling needed).
export default function TranscriptRoute() {
  const { prefix, id } = useParams();
  const dispatchId = Number(id);

  return (
    <div className="mk-transcript-route">
      <header className="mk-transcript-route-head">
        <Link to={monitorTranscriptsPath(prefix ?? '')} className="mk-btn-secondary">
          ← Transcripts
        </Link>
        <span className="mk-transcript-route-title mk-mono">dispatch #{id}</span>
      </header>
      <div className="mk-transcript-route-body">
        {Number.isFinite(dispatchId) && dispatchId > 0 ? (
          <JobTranscriptBody dispatchId={dispatchId} />
        ) : (
          <div className="mk-monitor-empty">Invalid transcript id.</div>
        )}
      </div>
    </div>
  );
}
