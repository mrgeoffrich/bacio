import { useState, useEffect, useMemo } from 'react';
import * as api from '../../api';
import type { AnthropicTranscript } from '../../api';
import TranscriptView from './TranscriptView';
import { anthropicTranscriptToParseResult } from './anthropicAdapter';

type JobTranscriptBodyProps = {
  dispatchId: number;
};

// JobTranscriptBody fetches a dispatch's assembled transcript and feeds it
// through the adapter into the reused <TranscriptView>. A 404 (no parsed
// captures for the dispatch) shows an inline message rather than an error toast.
//
// Extracted from MonitorCaptureSheet (BACI-308) into the shared transcript lib
// so both the BACI-308 capture sheet's "job transcript" step and the BACI-322
// deep-link TranscriptRoute render the same body from one copy.
export default function JobTranscriptBody({ dispatchId }: JobTranscriptBodyProps) {
  const [transcript, setTranscript] = useState<AnthropicTranscript | null>(null);
  const [state, setState] = useState<'loading' | 'ready' | 'empty'>('loading');

  useEffect(() => {
    let cancelled = false;
    setState('loading');
    setTranscript(null);
    api.jobTranscript(dispatchId)
      .then(tr => {
        if (cancelled) return;
        setTranscript(tr);
        setState('ready');
      })
      .catch(() => {
        if (cancelled) return;
        setState('empty');
      });
    return () => { cancelled = true; };
  }, [dispatchId]);

  const parsed = useMemo(
    () => (transcript ? anthropicTranscriptToParseResult(transcript) : null),
    [transcript],
  );

  if (state === 'loading') return <div className="mk-monitor-empty">Loading transcript…</div>;
  if (state === 'empty' || !parsed) {
    return <div className="mk-monitor-empty">No assembled transcript for this dispatch.</div>;
  }
  return <TranscriptView parsed={parsed} filename={`dispatch #${dispatchId}`} />;
}
