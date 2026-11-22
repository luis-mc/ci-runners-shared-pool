// Pool > Live (LOOK56, option A): the jobs of the pool on one time axis and the load of each machine. It changes as
// events arrive and its clock counts every second (R5.150).
import { Empty, Section, Stack, Waterfall, useNow, type WaterfallRow } from '@crsp/ui';
import { MachineCard, withLiveLoad } from '../overview/Overview';
import { useMachines } from '../../api/machines';
import { useLiveState } from '../../live/LiveProvider';
import { Load } from '../common';
import { jobStatus, waitWords, type Job } from '../jobs/words';
import { WithOverview } from './OverviewState';
import { placeWords } from './poolWords';
import { CardGrid } from '@crsp/ui';
import { useNavigate } from 'react-router-dom';

/** Rows of the live timeline: seconds from the earliest queue time; a queued job shows only its wait. */
export function liveRows(
  jobs: readonly Job[],
  now: number,
  open: (j: Job) => void,
): { rows: WaterfallRow[]; totalSec: number; nowSec: number } {
  const stamp = (j: Job) => Date.parse(j.queued_at ?? j.started_at ?? '') || now;
  const t0 = jobs.reduce((m, j) => Math.min(m, stamp(j)), now);
  const sec = (ms: number) => Math.max(0, (ms - t0) / 1000);
  const nowSec = sec(now);
  const rows = jobs.map((j): WaterfallRow => {
    const queued = j.state === 'queued';
    const s = jobStatus(j);
    const startMs = queued ? now : Date.parse(j.started_at ?? '') || now;
    return {
      id: String(j.id),
      label: j.job_name,
      sublabel: `${j.repository} · ${placeWords(j)}`,
      status: s.kind,
      startSec: sec(startMs),
      endSec: nowSec,
      ...(j.queued_at ? { waitFromSec: sec(Date.parse(j.queued_at)) } : {}),
      ...(j.forecast && !queued
        ? {
            forecast: {
              loSec: sec(startMs) + j.forecast.lo,
              hiSec: sec(startMs) + j.forecast.hi,
            },
          }
        : {}),
      detail: queued ? waitWords(j) : s.word,
      onOpen: () => open(j),
    };
  });
  return {
    rows,
    totalSec: Math.max(nowSec + 60, ...rows.map((r) => r.forecast?.hiSec ?? 0)),
    nowSec,
  };
}

export function PoolLive() {
  const now = useNow();
  const navigate = useNavigate();
  const live = useLiveState();
  const machines = useMachines();
  return (
    <WithOverview>
      {(o) => {
        const jobs = [...o.running, ...(o.queued ?? [])];
        const v = liveRows(jobs, now, (j) => void navigate(`/pool/jobs/${j.id}`));
        return (
          <Stack gap="section">
            <Section
              title="Jobs over time"
              count={jobs.length}
              hint="Each row is a job. The light part is waiting. A bracket is the forecast of the end."
            >
              {jobs.length === 0 ? (
                <Empty title="No job runs or waits. Jobs show here as they come." />
              ) : (
                <Waterfall
                  label="Jobs of the pool"
                  rows={v.rows}
                  totalSec={v.totalSec}
                  nowSec={v.nowSec}
                  emptyText="No job runs or waits."
                />
              )}
            </Section>
            <Section title="Machines">
              <Load result={machines.result} what="machines" reload={machines.reload}>
                {(d) => (
                  <CardGrid>
                    {d.items.map((m) => (
                      <MachineCard key={m.id} m={withLiveLoad(m, live.overview)} />
                    ))}
                  </CardGrid>
                )}
              </Load>
            </Section>
          </Stack>
        );
      }}
    </WithOverview>
  );
}
