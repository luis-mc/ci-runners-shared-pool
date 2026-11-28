// The Debug access card of a job (docs/features/debug-access section 4, CRSP331): the one place where a debug
// session starts. The button is disabled with the server's reason beside it (UX disabled-with-reason); opening asks
// for a one-line reason first; End asks first. The terminal itself follows when the agent can enter a job's
// environment; until then the server refuses every open and says why. Shared parts only, no app CSS.
import {
  Button,
  Cluster,
  Dialog,
  Empty,
  Facts,
  Message,
  QuestionCard,
  Section,
  Stack,
  Status,
  Table,
  Text,
  TextField,
  formatDateTime,
  formatTimeLeft,
  type Column,
} from '@crsp/ui';
import { useState } from 'react';
import { unwrap, useClient, type Schemas } from '../../api/api';
import { useCommand, useLoad } from '../../api/hooks';
import { Load } from '../common';

type JobDebug = Schemas['JobDebug'];
type DebugSession = Schemas['DebugSession'];

/** The words of the card (design system content, Debug access words). */
export const DEBUG_WORDS = {
  title: 'Debug access',
  open: 'Open debug session',
  opening: 'Opening the session...',
  end: 'End',
  ending: 'Ending',
  extend: 'Extend',
  extending: 'Extending',
  reasonLabel: 'Reason',
  reasonHelp: 'One line, 3 to 200 characters. It goes into the audit log.',
  empty: 'No debug sessions yet. Open one from a running job.',
  pastTitle: 'Past sessions of this job',
} as const;

/** How a session ended, in words (R5.24). */
export const END_WORDS: Record<NonNullable<DebugSession['end_reason']>, string> = {
  time_limit: 'time limit',
  idle: 'idle limit',
  job_ended: 'job ended',
  user: 'ended by the user',
  admin: 'ended by an admin',
  kill_switch: 'debug sessions switched off',
  link_lost: 'connection lost',
  error: 'error',
};

export function reasonProblem(reason: string): string | undefined {
  const n = [...reason.trim()].length;
  return n < 3 || n > 200 ? 'Write a reason of 3 to 200 characters.' : undefined;
}

function useJobDebug(jobId: number) {
  const client = useClient();
  return useLoad(
    () =>
      unwrap(client.GET('/api/v1/jobs/{jobId}/debug', { params: { path: { jobId } } })),
    [jobId],
  );
}

const COLUMNS: Column<DebugSession>[] = [
  { id: 'who', header: 'Who', cell: (s) => s.user },
  { id: 'from', header: 'From', cell: (s) => formatDateTime(s.opened_at) },
  {
    id: 'to',
    header: 'To',
    cell: (s) => (s.ended_at ? formatDateTime(s.ended_at) : 'open'),
  },
  {
    id: 'reason',
    header: 'Reason',
    cell: () => null,
    kind: 'grow',
    long: (s) => ({ full: s.reason }),
  },
  {
    id: 'how',
    header: 'How it ended',
    cell: (s) =>
      s.state === 'open' ? (
        <Status kind="running" word="open" />
      ) : (
        <Status kind="off" word={s.end_reason ? END_WORDS[s.end_reason] : 'ended'} />
      ),
  },
];

export function DebugCard({ jobId }: { jobId: number }) {
  const { result, reload } = useJobDebug(jobId);
  return (
    <Section title={DEBUG_WORDS.title}>
      <Load result={result} what="debug access" reload={reload}>
        {(d) => <DebugBody d={d} reload={reload} />}
      </Load>
    </Section>
  );
}

function DebugBody({ d, reload }: { d: JobDebug; reload: () => void }) {
  const client = useClient();
  const [asking, setAsking] = useState(false);
  const [ending, setEnding] = useState(false);
  const [reason, setReason] = useState('');
  const open = useCommand(async (text: string) => {
    await unwrap(
      client.POST('/api/v1/jobs/{jobId}/debug-sessions', {
        params: { path: { jobId: d.job_id } },
        body: { reason: text.trim() },
      }),
    );
    setAsking(false);
    setReason('');
    reload();
  });
  const extend = useCommand(async (id: string) => {
    await unwrap(
      client.POST('/api/v1/debug-sessions/{sessionId}/extend', {
        params: { path: { sessionId: id } },
      }),
    );
    reload();
  });
  const end = useCommand(async (id: string) => {
    await unwrap(
      client.POST('/api/v1/debug-sessions/{sessionId}/end', {
        params: { path: { sessionId: id } },
      }),
    );
    setEnding(false);
    reload();
  });
  const s = d.open_session;
  const problem = reasonProblem(reason);
  return (
    <Stack>
      <Text>{d.sees}</Text>
      {d.warnings.map((w) => (
        <Message key={w} tone="needs-you" dismissible={false}>
          {w}
        </Message>
      ))}
      {s ? (
        <Stack>
          <Facts
            items={[
              { name: 'Opened by', value: s.user },
              { name: 'Reason', value: s.reason },
              { name: 'Opened', value: formatDateTime(s.opened_at) },
              {
                name: 'Time left',
                value: formatTimeLeft(Date.parse(s.ends_at) - Date.now()),
              },
            ]}
          />
          <Cluster>
            <Button
              onPress={() => void extend.run(s.id)}
              busy={extend.busy}
              busyLabel={DEBUG_WORDS.extending}
              reason={s.extended ? 'A session can be extended once.' : undefined}
              disabled={s.extended}
            >
              {DEBUG_WORDS.extend}
            </Button>
            <Button onPress={() => setEnding(true)} kind="danger">
              {DEBUG_WORDS.end}
            </Button>
          </Cluster>
          {extend.error ? (
            <Message tone="problem">Could not extend the session. {extend.error}</Message>
          ) : null}
        </Stack>
      ) : (
        <Stack>
          <Cluster>
            <Button
              onPress={() => {
                open.clear();
                setAsking(true);
              }}
              disabled={!d.allowed}
              reason={d.allowed ? undefined : d.why_not}
              busy={open.busy}
              busyLabel={DEBUG_WORDS.opening}
            >
              {DEBUG_WORDS.open}
            </Button>
          </Cluster>
          {d.limit_min ? (
            <Text tone="muted">
              {`A session ends after ${d.limit_min} min, or after ${d.idle_min ?? 10} min with no key pressed.`}
            </Text>
          ) : null}
        </Stack>
      )}
      {d.sessions.length === 0 ? (
        <Empty title={DEBUG_WORDS.empty} />
      ) : (
        <Table
          caption={DEBUG_WORDS.pastTitle}
          title={DEBUG_WORDS.pastTitle}
          columns={COLUMNS}
          rows={d.sessions}
          rowKey={(r) => r.id}
          expandLabel="Full reason"
        />
      )}
      {asking ? (
        <Dialog
          title={DEBUG_WORDS.open}
          onClose={() => setAsking(false)}
          primary={{
            label: DEBUG_WORDS.open,
            onPress: () => void open.run(reason),
            busy: open.busy,
            busyLabel: DEBUG_WORDS.opening,
            disabled: problem !== undefined,
            reason: problem,
          }}
        >
          <Stack>
            <Text>{d.sees}</Text>
            <TextField
              label={DEBUG_WORDS.reasonLabel}
              help={DEBUG_WORDS.reasonHelp}
              value={reason}
              onChange={setReason}
            />
            {open.error ? (
              <Message tone="problem">Could not open the session. {open.error}</Message>
            ) : null}
          </Stack>
        </Dialog>
      ) : null}
      {ending && s ? (
        <QuestionCard
          title="End this debug session?"
          intro={`The terminal of ${s.user} on job ${d.job_id} closes at once.`}
          items={[
            'Every process the session started is stopped.',
            ...(end.error ? [`Problem: ${end.error}`] : []),
          ]}
          action={DEBUG_WORDS.end}
          busyLabel={DEBUG_WORDS.ending}
          busy={end.busy}
          onCancel={() => {
            end.clear();
            setEnding(false);
          }}
          onConfirm={() => void end.run(s.id)}
        />
      ) : null}
    </Stack>
  );
}
