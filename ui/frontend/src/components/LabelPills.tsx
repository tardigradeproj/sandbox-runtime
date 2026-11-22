export function LabelPills({ labels }: { labels: Record<string, string> | undefined }) {
  const entries = Object.entries(labels ?? {});
  if (!entries.length) {
    return <span className="hint" style={{ margin: 0 }}>None set.</span>;
  }
  return (
    <div className="label-row">
      {entries.map(([k, v]) => (
        <span className="label-pill" key={k}>
          {k}={v}
        </span>
      ))}
    </div>
  );
}
