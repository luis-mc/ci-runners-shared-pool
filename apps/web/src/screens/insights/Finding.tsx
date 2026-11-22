// Run insights, one finding (spec 4.1): the headline in words, the fix, a copyable snippet, the evidence as a
// table, the estimate and first and last seen. A fix that changes the crsp setup has one primary, Review the change
// (UX-4, UX-71); a fix in a repository has none (UX-8). Hide for 7 days is a secondary action: the finding comes
// back if it is still true (owner decision D17).
import {
  ActionBar,
  Breadcrumbs,
  Button,
  CodeBlock,
  Facts,
  LinkButton,
  Message,
  Page,
  PageHeader,
  Section,
  Stack,
  Table,
  Text,
  formatShortDateTime,
  type Column,
} from '@crsp/ui';
import { useState } from 'react';
import { useParams } from 'react-router-dom';
import { useCommand } from '../../api/hooks';
import type { Schemas } from '../../api/api';
import { Load } from '../common';
import { dismissFinding, useFinding } from './api';
import { AREA_WORDS, minutesWords, projectOf, type Finding } from './words';

interface Evidence {
  name: string;
  value: string;
}

const evidenceRows = (f: Finding): Evidence[] =>
  Object.entries(f.evidence).map(([name, value]) => ({
    name: name.replaceAll('_', ' '),
    value: Array.isArray(value) ? value.join(', ') : String(value),
  }));

const evidenceColumns: Column<Evidence>[] = [
  { id: 'name', header: 'What was measured', cell: (e) => e.name, sort: (e) => e.name },
  { id: 'value', header: 'Value', cell: (e) => e.value, kind: 'grow', grow: 2 },
];

export const changePath = (f: Pick<Finding, 'id'>): string =>
  `/analytics/finding/${f.id}/change`;

function FindingView({ f }: { f: Schemas['InsightFinding'] }) {
  const [until, setUntil] = useState<string | undefined>(f.dismissed_until);
  const hide = useCommand(async () => {
    const hidden = await dismissFinding(f.id);
    setUntil(hidden.dismissed_until);
  });
  return (
    <>
      <Breadcrumbs
        trail={[
          { label: 'Jobs', to: '/pool' },
          { label: 'Suggestions', to: '/analytics' },
          { label: f.title },
        ]}
      />
      <PageHeader title={f.title} lead={`${projectOf(f)}, ${AREA_WORDS[f.area]}`} />
      <Stack>
        {until ? (
          <Message tone="done">
            Hidden until {formatShortDateTime(until)}. It comes back then if it is still
            true.
          </Message>
        ) : null}
        {hide.error ? (
          <Message tone="problem">Could not hide this finding. {hide.error}</Message>
        ) : null}
        <Section title="The fix">
          <Text>{f.fix}</Text>
          {f.snippet ? <CodeBlock text={f.snippet} label="Workflow snippet" /> : null}
        </Section>
        <Section title="Evidence">
          <Table
            caption="Evidence"
            columns={evidenceColumns}
            rows={evidenceRows(f)}
            rowKey={(e) => e.name}
          />
        </Section>
        <Section title="Estimate">
          <Facts
            items={[
              {
                name: 'Saves',
                value: `${minutesWords(f.minutes_saved_week)} (estimate)`,
              },
              { name: 'First seen', value: formatShortDateTime(f.first_seen) },
              { name: 'Last seen', value: formatShortDateTime(f.last_seen) },
            ]}
          />
        </Section>
      </Stack>
      <ActionBar
        secondary={
          f.resolved_at ? undefined : (
            <Button
              onPress={() => void hide.run()}
              busy={hide.busy}
              busyLabel="Hiding"
              disabled={Boolean(until)}
              reason={until ? 'This finding is hidden for now.' : undefined}
            >
              Hide for 7 days
            </Button>
          )
        }
        primary={
          f.action_kind ? (
            <LinkButton to={changePath(f)} kind="primary">
              Review the change
            </LinkButton>
          ) : (
            <LinkButton to="/analytics/insights">Back to Suggestions</LinkButton>
          )
        }
      />
    </>
  );
}

export function FindingPage() {
  const { id } = useParams();
  const { result, reload } = useFinding(Number(id));
  return (
    <Page>
      <Load result={result} what="this finding" reload={reload}>
        {(f) => <FindingView key={f.id} f={f} />}
      </Load>
    </Page>
  );
}
