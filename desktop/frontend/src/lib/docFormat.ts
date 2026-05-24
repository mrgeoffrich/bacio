// isSvgDoc detects whether a Documents-view payload should render as an
// SVG image rather than be passed to the markdown editor. Filename suffix
// is the primary signal; for filenames without `.svg` (e.g. an SVG
// imported under a generic name) we sniff the first ~2 KiB of the body
// for an `<svg` root.
export function isSvgDoc(filename: string, content: string): boolean {
  if (filename.toLowerCase().endsWith('.svg')) return true;
  const head = content.slice(0, 2048).trimStart().toLowerCase();
  if (head.startsWith('<svg')) return true;
  if (head.startsWith('<?xml') && head.includes('<svg')) return true;
  return false;
}

// isJsonlTranscriptDoc detects a bacio-attached subagent transcript
// (BACI-125). Conservative-by-filename: requires the
// `bacio-transcript-…-agent-….jsonl` shape that `attach_transcript`
// produces, so a hand-uploaded `.jsonl` doc doesn't accidentally
// trigger the transcript renderer (it'd parse but the dispatch-prompt
// card UX wouldn't match anything sensible). Mirrors
// model.IsBacioTranscriptFilename in Go so the predicate stays in
// lockstep with the read-side fallback the brief route uses.
export function isJsonlTranscriptDoc(filename: string): boolean {
  const f = filename.trim().toLowerCase();
  return (
    f.startsWith('bacio-transcript-') &&
    f.endsWith('.jsonl') &&
    f.includes('-agent-')
  );
}
