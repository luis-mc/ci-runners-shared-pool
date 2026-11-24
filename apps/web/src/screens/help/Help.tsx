// The Help page (LOOK47, LOOK63): a side menu of 13 sections as in the approved prototype. Start here shows the picture
// of how a job gets run, with a live example in each box, and nine question cards with a link each. Where things are
// and Reading Queue Analysis read the console and the pool. The other ten are short guides written from how the
// scheduler really works (helpContent.ts GUIDE); Admission and passing also reads the first waiting job of the pool.
import {
  BulletList,
  CardGrid,
  Card,
  Facts,
  Page,
  PageHeader,
  Pane,
  SideMenu,
  Table,
  type Column,
  Text,
  TextLink,
} from '@crsp/ui';
import { useEffect } from 'react';
import { useLocation } from 'react-router-dom';
import {
  CARDS,
  GUIDE,
  HELP_WORDS,
  TOPICS,
  WHERE_AREAS,
  WHERE_TABS,
  type GuideSection,
} from './helpContent';
import { WithOverview } from '../pool/OverviewState';
import { needsYou, startsAbout, waitWords } from '../pool/poolWords';
import { JobPathPicture } from './JobPathPicture';
import { useHelpData } from './useHelpData';

function StartHere() {
  const { hash } = useLocation();
  const data = useHelpData();

  // An address that ends in a card brings the card into view. Nothing is hidden, so nothing has to open.
  useEffect(() => {
    const id = hash.replace(/^#/, '');
    if (!CARDS.some((c) => c.id === id)) return;
    const t = setTimeout(
      () => document.getElementById(id)?.scrollIntoView?.({ block: 'center' }),
      0,
    );
    return () => clearTimeout(t);
  }, [hash]);

  return (
    <>
      <Pane title="How a job gets run">
        <JobPathPicture examples={data.examples} />
      </Pane>
      <Pane title={HELP_WORDS.questions} count={CARDS.length} id="questions">
        <CardGrid>
          {CARDS.map((c) => (
            <Card key={c.id} id={c.id} title={c.title}>
              <Text>{c.answer}</Text>
              <TextLink to={c.link.to}>{c.link.label}</TextLink>
            </Card>
          ))}
        </CardGrid>
      </Pane>
    </>
  );
}

type Area = (typeof WHERE_AREAS)[number];
type Tab = (typeof WHERE_TABS)[number];

const areaColumns: Column<Area>[] = [
  { id: 'name', header: 'Part', cell: (a) => <TextLink to={a.to}>{a.name}</TextLink> },
  { id: 'line', header: 'What it is for', cell: (a) => a.line, kind: 'grow', grow: 3 },
];
const tabColumns: Column<Tab>[] = [
  { id: 'tab', header: 'Tab', cell: (t) => <TextLink to={t.to}>{t.tab}</TextLink> },
  { id: 'line', header: 'Use it to', cell: (t) => t.line, kind: 'grow', grow: 3 },
];

function WhereThingsAre() {
  return (
    <>
      <Pane title="Where things are">
        <Text>{HELP_WORDS.whereLead}</Text>
        <Table
          caption="The parts of the console"
          columns={areaColumns}
          rows={WHERE_AREAS}
          rowKey={(a) => a.name}
        />
      </Pane>
      {(['Setup', 'Pool'] as const).map((section) => (
        <Pane key={section} title={`${section} tabs`}>
          <Table
            caption={`${section} tabs`}
            columns={tabColumns}
            rows={WHERE_TABS.filter((t) => t.section === section)}
            rowKey={(t) => t.tab}
          />
        </Pane>
      ))}
    </>
  );
}

/** Option F: the guide reads the pool as it is now. */
function ReadingQueue() {
  return (
    <>
      <Text>{HELP_WORDS.queueLead}</Text>
      <Pane title={HELP_WORDS.nowTitle}>
        <WithOverview>
          {(o, readAt) => {
            const first = [...(o.queued ?? [])].sort(
              (a, b) => (a.queue_position ?? 1e9) - (b.queue_position ?? 1e9),
            )[0];
            return first ? (
              <Facts
                items={[
                  {
                    name: 'Next to start',
                    value: `${first.job_name} ${first.repository}`,
                  },
                  { name: 'Why it waits', value: waitWords(first) },
                  {
                    name: 'Start',
                    value: needsYou(first)
                      ? 'Does not start by itself'
                      : startsAbout(first.queue_eta, readAt),
                  },
                ]}
              />
            ) : (
              <Text tone="muted">{HELP_WORDS.nowNone}</Text>
            );
          }}
        </WithOverview>
        <TextLink to="/pool/queue">See it live in Pool &gt; Queue Analysis</TextLink>
      </Pane>
    </>
  );
}

/** The first waiting job of the pool, with what the scheduler said about it: the live part of Admission and passing. */
function AdmissionNow() {
  return (
    <Pane title={HELP_WORDS.nowTitle}>
      <WithOverview>
        {(o, readAt) => {
          const first = [...(o.queued ?? [])].sort(
            (a, b) => (a.queue_position ?? 1e9) - (b.queue_position ?? 1e9),
          )[0];
          return first ? (
            <Facts
              items={[
                { name: 'Job', value: `${first.job_name} ${first.repository}` },
                { name: 'Why it waits', value: waitWords(first) },
                {
                  name: 'Result',
                  value: needsYou(first)
                    ? 'Does not start by itself'
                    : startsAbout(first.queue_eta, readAt),
                },
              ]}
            />
          ) : (
            <Text tone="muted">{HELP_WORDS.nowNone}</Text>
          );
        }}
      </WithOverview>
    </Pane>
  );
}

function Guide({ section, live }: { section: GuideSection; live?: boolean }) {
  return (
    <>
      <Pane title={section.title}>
        <Text>{section.lead}</Text>
        <BulletList items={section.points} />
        {section.link ? <TextLink to={section.link.to}>{section.link.label}</TextLink> : null}
      </Pane>
      {live ? <AdmissionNow /> : null}
    </>
  );
}

export function HelpScreen() {
  const { pathname } = useLocation();
  const topic = pathname.replace(/\/+$/, '').split('/')[2];
  const section = topic ? GUIDE[topic] : undefined;
  return (
    <Page>
      <PageHeader title="Help" lead={HELP_WORDS.lead} />
      <SideMenu label={HELP_WORDS.sections} items={TOPICS}>
        {topic === 'where' ? (
          <WhereThingsAre />
        ) : topic === 'queue' ? (
          <ReadingQueue />
        ) : section ? (
          <Guide section={section} live={topic === 'admission'} />
        ) : (
          <StartHere />
        )}
      </SideMenu>
    </Page>
  );
}
