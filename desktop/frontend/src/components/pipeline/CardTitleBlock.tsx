import type { BoardCard } from '../../api';

// CardTitleBlock (BACI-349) renders a card's head text. Default
// (impactPrimary off, or the card has no customer_impact) is the plain
// imperative title in the `<titleClass>` <h3>. When the board's
// impact-primary toggle is on AND the card carries a customer impact, the
// impact becomes the headline <h3> and the title is demoted to a muted
// `.mk-pl-card-subtitle` line below it. Shared by PipelineCard (Backlog /
// Shipping) and StageCard (In Pipeline) so both columns flip together.
type CardTitleBlockProps = {
  card: BoardCard;
  titleClass: string;
  impactPrimary?: boolean;
};

export default function CardTitleBlock({ card, titleClass, impactPrimary }: CardTitleBlockProps) {
  if (impactPrimary && card.customerImpact) {
    return (
      <>
        <h3 className={titleClass}>{card.customerImpact}</h3>
        <p className="mk-pl-card-subtitle">{card.title}</p>
      </>
    );
  }
  return <h3 className={titleClass}>{card.title}</h3>;
}
