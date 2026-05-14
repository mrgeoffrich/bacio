import { useEffect, useState } from 'react';
import { FeatureService } from '../bindings/github.com/mrgeoffrich/bacio/desktop';
import { Feature } from '../bindings/github.com/mrgeoffrich/bacio/internal/model/models';

function App() {
  const [features, setFeatures] = useState<Feature[] | null>(null);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    FeatureService.List()
      .then((result) => {
        setFeatures((result ?? []).filter((f): f is Feature => f !== null));
      })
      .catch((err: any) => {
        setError(err?.message ?? String(err));
      });
  }, []);

  return (
    <div>
      <h1>Features</h1>
      {error !== null ? (
        <p>Error: {error}</p>
      ) : features === null ? (
        <p>Loading…</p>
      ) : features.length === 0 ? (
        <p>No features yet.</p>
      ) : (
        <ul>
          {features.map((f) => (
            <li key={f.uuid}>{f.slug} — {f.title}</li>
          ))}
        </ul>
      )}
    </div>
  );
}

export default App;
