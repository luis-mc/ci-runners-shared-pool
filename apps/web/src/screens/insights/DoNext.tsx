// Run insights, Do next (spec 4.1, design system UX-25, UX-72): what to do, ranked by minutes saved a week. The
// latest weekly digest sits folded under the table (component 47).
import {
  Disclosure,
  Empty,
  Message,
  Section,
  Stack,
  Table,
  Text,
  formatShortDateTime,
  type Column,
} from '@crsp/ui';
import { Load } from '../common';
import { useDigests, useFindings, useInsightSummary } from './api';
import { useRepositoryFilter } from './InsightsLayout';
import {
  AREA_WORDS,
  BACKLOG_WORDS,
  minutesWords,
  projectOf,
  ranked,
  type Finding,
} from './words';

export const findingPath = (f: Pick<Finding, 'id'>): string =>
  `/analytics/finding/${f.id}`;

export const doNextColumns: Column<Finding>[] = [
  {
    id: 'what',
    header: 'What to do',
    cell: (f) => (f.guardrail ? `${f.guardrail}: ${f.title}` : f.title),
    sort: (f) => f.title,
    kind: 'grow',
    grow: 3,
  },
  {
    id: 'project',
    header: 'Project',
    cell: (f) => projectOf(f),
    sort: (f) => projectOf(f),
    kind: 'grow',
    grow: 1,
  },
  {
    id: 'area',
    header: 'Area',
    cell: (f) => AREA_WORDS[f.area],
    sort: (f) => f.area,
    kind: 'fit',
    secondary: true,
  },
  {
    id: 'minutes',
    header: 'Saves',
    cell: (f) => minutesWords(f.minutes_saved_week),
    sort: (f) => f.minutes_saved_week,
    kind: 'fit',
  },
  {
    id: 'since',
    header: 'Since',
    cell: (f) => formatShortDateTime(f.first_seen),
    sort: (f) => f.first_seen,
    kind: 'fit',
    secondary: true,
  },
];

export function DoNext() {
  const repository = useRepositoryFilter();
  const findings = useFindings(undefined);
  const summary = useInsightSummary();
  const digests = useDigests();
  return (
    <Load result={findings.result} what="the things to do next" reload={findings.reload}>
      {(page) => {
        const items = ranked(
          page.items.filter((f) => !repository || f.repository === repository),
        );
        const enough =
          summary.result.state !== 'ready' || summary.result.data.enough_data !== false;
        const digest =
          digests.result.state === 'ready' ? digests.result.data.items[0] : undefined;
        return (
          <Stack>
            {page.not_read_yet ? (
              <Message tone="note" dismissible={false}>
                {BACKLOG_WORDS(page.not_read_yet.jobs, page.not_read_yet.until)}
              </Message>
            ) : null}
            <Section
              title="Do next"
              count={items.length}
              hint="About how many minutes a week each change saves. An estimate."
            >
              {items.length === 0 ? (
                <Empty
                  title={
                    enough
                      ? 'Nothing to do. crsp found no waste in the last 7 days.'
                      : 'Not enough finished jobs yet to rank anything. crsp needs a few days of CI.'
                  }
                />
              ) : (
                <Table
                  caption="Things to do next"
                  oneLine
                  columns={doNextColumns}
                  rows={items}
                  rowKey={(f) => String(f.id)}
                  rowLabel={(f) => f.title}
                  action={{ label: 'Open', to: findingPath, name: (f) => f.title }}
                />
              )}
            </Section>
            {digest ? (
              <Disclosure summary={`Weekly digest, ${digest.week}`}>
                <Text>{digest.text}</Text>
              </Disclosure>
            ) : null}
          </Stack>
        );
      }}
    </Load>
  );
}
