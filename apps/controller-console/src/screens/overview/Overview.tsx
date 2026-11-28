// Overview (web-console 4.2, R5.20): machines with load bars, the queue count, the running jobs and today's
// minutes saved, live. It has no command; each item links to its screen (UX-52).
import {
  Card,
  CardGrid,
  Empty,
  ExternalButton,
  LevelBar,
  Loading,
  Message,
  Page,
  PageHeader,
  Section,
  Status,
  TextLink,
  formatPercent,
} from '@crsp/ui';
import { useEffect, useState } from 'react';
import { unwrap, useClient, type Schemas } from '../../api/api';
import { useLiveState, useLiveStore } from '../../live/LiveProvider';
import { useLoad } from '../../api/hooks';
import { useMachines } from '../../api/machines';
import { Load } from '../common';
import { BestNextChange } from '../insights/OverviewLine';
import { SavingsTiles } from '../savings/Tiles';
import { PoolCountTiles } from '../pool/PoolSection';
import { poolCounts } from '../pool/poolWords';
import { SlotTimeline } from '../pool/SlotTimeline';
import {
  NextToStart,
  OverviewMachineCard,
  RoutingLabelsPane,
  TimerAlert,
} from './OverviewPanes';
import { HowCrspWorks } from './HowCrspWorks';
import { HelpTerm } from '../help/HelpTerm';

type Machine = Schemas['Machine'];

export function machineStatus(m: Machine): {
  kind: 'ok' | 'waiting' | 'failed' | 'off';
  word: string;
} {
  if (m.recovery?.state === 'needs_recovery')
    return { kind: 'failed', word: 'needs recovery' };
  if (!m.online) return { kind: 'failed', word: 'offline' };
  if (m.state === 'draining') return { kind: 'waiting', word: 'finishing jobs' };
  if (m.state === 'drained') return { kind: 'off', word: 'not taking jobs' };
  if (m.state === 'removing') return { kind: 'waiting', word: 'removing' };
  return { kind: 'ok', word: 'online' };
}

/** The list says which machines exist; a live load event, when the stream has one, says how loaded they are now. */
export function withLiveLoad(m: Machine, live: Schemas['Overview'] | undefined): Machine {
  const l = live?.machines.find((x) => x.id === m.id);
  return l ? { ...m, ...(l.load ? { load: l.load } : {}), slots_used: l.slots_used } : m;
}

/** The memory, CPU and slot bars of a machine (shared by the Overview card and the Pool > Machines card). */
export function MachineLoadBars({ m }: { m: Machine }) {
  const mem = m.load?.memory_pct;
  const cpu = m.load?.cpu_pct;
  const budget = m.settings;
  return (
    <>
      <LevelBar
        label="Memory"
        value={mem ?? 0}
        max={budget.budget_memory_pct}
        text={
          mem === undefined
            ? 'not known'
            : `${formatPercent(mem)} of ${formatPercent(budget.budget_memory_pct)}`
        }
      />
      <LevelBar
        label="CPU"
        value={cpu ?? 0}
        max={budget.budget_cpu_pct}
        text={
          cpu === undefined
            ? 'not known'
            : `${formatPercent(cpu)} of ${formatPercent(budget.budget_cpu_pct)}`
        }
      />
      <LevelBar
        label="Slots"
        help={<HelpTerm term="slot" />}
        value={m.slots_used}
        max={Math.max(1, budget.job_slots)}
        text={`${m.slots_used} of ${budget.job_slots}`}
      />
    </>
  );
}

export function MachineCard({ m }: { m: Machine }) {
  const s = machineStatus(m);
  return (
    <Card title={m.name} machine to={`/setup/machines/${m.id}`}>
      <Status kind={s.kind} word={s.word} />
      <MachineLoadBars m={m} />
    </Card>
  );
}

/** One setting of the GitHub App is off: "Enable Device Flow". `crsp login` cannot sign anyone in until the owner of the
 * App turns it on, so the page names the setting and links to the App's settings page (github-integration R5.14). */
function DeviceFlowOffNote({ url }: { url?: string | undefined }) {
  return (
    <Message
      tone="needs-you"
      action={
        url ? (
          <ExternalButton href={url}>Open the App settings</ExternalButton>
        ) : undefined
      }
    >
      Device Flow is off for the GitHub App, so crsp login cannot sign anyone in. Turn on
      &quot;Enable Device Flow&quot; in the App settings on GitHub.
    </Message>
  );
}

/** The App-gone banner (github-integration R5.6): GitHub work is stopped, jobs that run keep running, and the
 * setup wizard is offered again. Nothing is drawn while the App answers or while the answer is not known. */
export function AppGoneBanner() {
  const client = useClient();
  const { result } = useLoad(
    () => unwrap(client.GET('/api/v1/github/app-health')),
    [client],
  );
  const health = result.state === 'ready' ? result.data : undefined;
  if (health && !health.gone && health.device_flow_off)
    return <DeviceFlowOffNote url={health.settings_url} />;
  if (!health?.gone) return null;
  return (
    <Message
      tone="problem"
      action={<TextLink to="/setup?wizard=1">Run the setup wizard again</TextLink>}
    >
      The GitHub App is gone. {health.reason ?? ''} crsp has stopped its GitHub work. Jobs
      that are running keep running.
    </Message>
  );
}

export function Overview() {
  const client = useClient();
  const live = useLiveState();
  const liveStore = useLiveStore();
  const [firstPaint, setFirstPaint] = useState<'loading' | 'ready' | 'failed'>('loading');
  const [why, setWhy] = useState('');
  const store = live.overview;

  // The stream starts with a snapshot. If it is slow, load the overview once so the first screen is quick (R6.12).
  useEffect(() => {
    if (store) return;
    let on = true;
    unwrap(client.GET('/api/v1/overview'))
      .then((first) => {
        if (!on) return;
        liveStore.seed(first);
        setFirstPaint('ready');
      })
      .catch((e: unknown) => {
        if (!on) return;
        setFirstPaint('failed');
        setWhy(e instanceof Error ? e.message : 'The controller did not answer.');
      });
    return () => {
      on = false;
    };
  }, [client, store, liveStore]);

  const o = store;
  // The machines come from the one machine list the header and the Machines page read (UX-19).
  const machines = useMachines();
  return (
    <Page>
      <PageHeader
        title="Overview"
        lead="The pool at a glance. Open a card for the detail."
      />
      <AppGoneBanner />
      <HowCrspWorks />
      {!o && firstPaint !== 'failed' ? (
        <Section title="The pool">
          <Loading what="Loading the overview" />
        </Section>
      ) : null}
      {!o && firstPaint === 'failed' ? (
        <Section title="The pool">
          <Message tone="problem">
            Could not load the overview. {why} The console keeps trying.
          </Message>
        </Section>
      ) : null}
      {o ? (
        <>
          <PoolCountTiles counts={poolCounts(o)} />
          <SavingsTiles />
          <TimerAlert running={o.running} />
          <BestNextChange />
          <Load result={machines.result} what="machines" reload={machines.reload}>
            {(data) =>
              data.items.length === 0 ? (
                <Empty
                  title="No machine is in the pool yet. Add a machine so jobs have somewhere to run."
                  action={<TextLink to="/setup/machines/add">Add a machine</TextLink>}
                />
              ) : (
                <CardGrid>
                  {data.items.map((m) => (
                    <OverviewMachineCard
                      key={m.id}
                      m={withLiveLoad(m, store)}
                      running={o.running}
                    />
                  ))}
                </CardGrid>
              )
            }
          </Load>
          <CardGrid>
            <NextToStart
              queued={o.queued ?? []}
              count={o.queued_count}
              readAt={live.lastEventAt ?? Date.now()}
            />
            <RoutingLabelsPane jobs={[...o.running, ...(o.queued ?? [])]} />
          </CardGrid>
          <Section
            title="Jobs over time"
            hint="Each row is a slot of a machine. Scroll left for the past, right for the forecast."
          >
            <SlotTimeline compact />
          </Section>
        </>
      ) : null}
    </Page>
  );
}
