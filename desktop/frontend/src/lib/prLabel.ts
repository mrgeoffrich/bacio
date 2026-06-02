// prLabel shortens a GitHub PR URL to "owner/repo#N" for the rail
// (IssueWorkspace) and the board-card chip (PipelineCard, BACI-239).
// Non-GitHub URLs fall through to the raw URL — readable enough for the
// tooltip target.
export default function prLabel(url: string): string {
  const m = url.match(/github\.com\/([^/]+)\/([^/]+)\/pull\/(\d+)/);
  return m ? `${m[1]}/${m[2]}#${m[3]}` : url;
}
