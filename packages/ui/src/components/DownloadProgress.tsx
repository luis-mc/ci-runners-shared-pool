// Component 83: a download inside a step (UX-90). One line of what was measured: bytes done of the total, the
// measured speed and the time left worked out from it. No rate yet: "measuring speed", never a guess.
import { formatDuration, formatSize } from './format';

export function downloadWords(p: {
  bytes: number;
  total?: number | undefined;
  rate?: number | undefined;
  etaSec?: number | undefined;
}): string {
  const hasTotal = p.total !== undefined && p.total > 0;
  const done = hasTotal
    ? `${formatSize(p.bytes)} of ${formatSize(p.total ?? 0)}`
    : `${formatSize(p.bytes)} so far`;
  const measured = p.rate !== undefined && p.rate > 0;
  if (!measured) return hasTotal ? `${done}, measuring speed` : done;
  const speed = `${formatSize(p.rate ?? 0)}/s`;
  if (hasTotal && p.etaSec !== undefined)
    return `${done}, ${speed}, about ${formatDuration(p.etaSec)} left`;
  return `${done}, ${speed}`;
}

export function DownloadProgress(p: {
  bytes: number;
  total?: number | undefined;
  rate?: number | undefined;
  etaSec?: number | undefined;
}) {
  return (
    <p className="ds-text" role="status" data-download>
      {downloadWords(p)}
    </p>
  );
}
