type CardLabelsProps = { tags?: string[] };

export default function CardLabels({ tags }: CardLabelsProps) {
  if (!tags || tags.length === 0) return null;
  return (
    <div className="mk-pl-card-labels">
      {tags.map(t => <span key={t} className="mk-pl-label">{t}</span>)}
    </div>
  );
}
