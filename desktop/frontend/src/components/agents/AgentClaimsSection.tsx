import type { ClaimDTO } from '../../api';

// AgentClaimsSection — the session's open claims (issue key + the prompt it
// was claimed under). Absent when the agent holds no claims.
type AgentClaimsSectionProps = {
  claims: ClaimDTO[];
};

export default function AgentClaimsSection({ claims }: AgentClaimsSectionProps) {
  if (claims.length === 0) return null;
  return (
    <section className="mk-agent-section">
      <div className="mk-agent-section-label">Open claims</div>
      {claims.map((c) => (
        <div key={c.issueKey} className="mk-agent-claim">
          <span className="mk-mono">{c.issueKey}</span>
          {c.prompt && (
            <span className="mk-agent-claim-prompt" title={c.prompt}>
              {c.prompt}
            </span>
          )}
        </div>
      ))}
    </section>
  );
}
