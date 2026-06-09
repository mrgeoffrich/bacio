import { Link } from 'react-router';
import Icon from '../Icon';
import Tooltip from '../Tooltip';
import BlockedByBadge from './BlockedByBadge';
import { documentPath } from '../../lib/routes';
import prLabel from '../../lib/prLabel';
import type { BoardCard } from '../../api';

// CardHead — feature glyph · issue key · plan / PR icon buttons · blocked-by
// badge (each only when it applies). Shared by the compact card and the
// stage card's header so the anatomy stays identical, which is how the
// blocked-by badge lands on all three card types at once.
type CardHeadProps = {
  card: BoardCard;
  activeBoard?: string;
  onOpenIssue?: (key: string) => void;
  onHighlight?: (key: string | null) => void;
  canBlock?: boolean;
  onBlockDragStart?: (key: string) => void;
  onBlockDragEnd?: () => void;
};

export default function CardHead({ card, activeBoard, onOpenIssue, onHighlight, canBlock, onBlockDragStart, onBlockDragEnd }: CardHeadProps) {
  const latestPlan = card.latestPlan || null;
  const latestPR = card.latestPR || null;
  return (
    <div className="mk-pl-card-top">
      {card.featureEmoji && (
        <span className="mk-pl-card-emoji" aria-hidden="true">{card.featureEmoji}</span>
      )}
      <span className="mk-pl-card-id">{card.key}</span>
      <BlockedByBadge
        blockedBy={card.blockedBy}
        onOpenIssue={onOpenIssue}
        onHighlight={onHighlight}
        sourceKey={card.key}
        canBlock={canBlock}
        onBlockDragStart={onBlockDragStart}
        onBlockDragEnd={onBlockDragEnd}
      />
      <span className="mk-pl-card-icons">
        {latestPlan && (
          <Tooltip label={`Open plan: ${latestPlan.filename}`}>
            <Link
              to={documentPath(activeBoard ?? '', latestPlan.filename)}
              className="mk-pl-icobtn"
              aria-label={`Open plan: ${latestPlan.filename}`}
              onClick={(e) => e.stopPropagation()}
            >
              <Icon name="plan" />
            </Link>
          </Tooltip>
        )}
        {latestPR && (
          <Tooltip label={`Open PR: ${prLabel(latestPR.url)}`}>
            <a
              href={latestPR.url}
              target="_blank"
              rel="noreferrer noopener"
              className="mk-pl-icobtn"
              aria-label={`Open PR: ${prLabel(latestPR.url)}`}
              onClick={(e) => e.stopPropagation()}
            >
              <Icon name="pull-request" />
            </a>
          </Tooltip>
        )}
      </span>
    </div>
  );
}
