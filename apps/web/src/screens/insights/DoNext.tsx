// Run insights, Do next (spec 4.1, design system UX-25, UX-72): what to do, ranked by minutes saved a week. The
// latest weekly digest sits folded under the table (component 47).
import {
  ActionBar,
  Disclosure,
  Empty,
  Message,
  LinkButton,
  Section,
  Stack,
  Strong,
  Table,
  Tag,
  Text,
  TextLink,
  type Column,
} from '@crsp/ui';
import { Load } from '../common';
import { useDigests, useFindings, useInsightSummary } from './api';
import { useRepositoryFilter } from './InsightsLayout';
import {
  BACKLOG_WORDS,
  CONFIDENCE,
  FIX_LABEL,
  confidenceOf,
  decidesWords,
  minutesWeekWords,
  ranked,
  titleWithRepository,
  type Finding,
} from './words';

export const findingPath = (f: Pick<Finding, 'id'>): string =>
  `/analytics/finding/${f.id}`;

/** The columns of Suggestions (LOOK65): #, Change with its fix inline, Sure?, Saves a week, Who decides. */
export const doNextColumns = (items: readonly Finding[]): Column<Finding>[] => [
  {
    id: 'rank',
    header: '#',
    cell: (f) => String(items.indexOf(f) + 1),
    sort: (f) => items.indexOf(f),
    kind: 'fit',
  },
  {
    id: 'change',
    header: 'Change',
    cell: (f) => (
      <Stack gap="tight">
        <TextLink to={findingPath(f)}>
          <Strong>
            {f.guardrail
              ? `${f.guardrail}: ${titleWithRepository(f)}`
              : titleWithRepository(f)}
          </Strong>
        </TextLink>
        <Text tone="muted">{`${FIX_LABEL} ${f.fix}`}</Text>
      </Stack>
    ),
    title: (f) => `${f.title}. ${FIX_LABEL} ${f.fix}`,
    sort: (f) => f.title,
    kind: 'grow',
    grow: 4,
  },
  {
    id: 'sure',
    header: 'Sure?',
    cell: (f) => <Tag>{confidenceOf(f)}</Tag>,
    sort: (f) => CONFIDENCE.indexOf(confidenceOf(f)),
    kind: 'fit',
  },
  {
    id: 'saves',
    header: 'Saves a week',
    cell: (f) => minutesWeekWords(f.minutes_saved_week),
    sort: (f) => f.minutes_saved_week,
    kind: 'fit',
  },
  {
    id: 'decides',
    header: 'Who decides',
    cell: (f) => decidesWords(f),
    sort: (f) => decidesWords(f),
    kind: 'fit',
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
              title="Suggestions, best first"
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
                  caption="Suggestions"
                  oneLine
                  columns={doNextColumns(items)}
                  rows={items}
                  rowKey={(f) => String(f.id)}
                  rowLabel={(f) => f.title}
                />
              )}
            </Section>
            {items.length > 0 ? (
              <ActionBar
                primary={
                  <LinkButton to={findingPath(items[0]!)} kind="primary">
                    Review the first change
                  </LinkButton>
                }
              />
            ) : null}
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
