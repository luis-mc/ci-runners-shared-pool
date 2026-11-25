// Rollout of one repository (operations R5.42, github-integration R5.66): the step the repository is on,
// one forward button and the way back. Every move asks first and names what it does. One pane, shared parts only.
// Design system CORE-1 to CORE-7, UX-4, UX-13, UX-38; words: content.md, Rollout words.
import { Button, Cluster, Facts, Message, NumberField, Pane, Stack, Text, TextField } from '@crsp/ui';
import { useState } from 'react';
import { unwrap, useClient, type Schemas } from '../../api/api';
import { useLoad, useQuestion } from '../../api/hooks';
import { useSession } from '../../auth/session';
import { Ask, Load } from '../common';

type Mode = Schemas['Trial']['mode'];

/** The steps in order; a repository moves one step forward at a time and back to any earlier one. */
export const STEPS: readonly Mode[] = ['off', 'observe', 'shadow', 'canary', 'live'];

export const STEP_WORDS: Record<Mode, string> = {
  off: 'Off',
  observe: 'Observe',
  shadow: 'Shadow',
  canary: 'Canary',
  live: 'Live',
};

export const STEP_HELP: Record<Mode, string> = {
  off: 'No trial. Jobs go where the repository sends them.',
  observe: 'Only watches. crsp writes down what it would do for each queued job and starts nothing.',
  shadow: 'crsp copies the marked workflow while the real jobs stay on GitHub.',
  canary: 'The chosen jobs run on the pool. The other jobs stay on GitHub.',
  live: 'Every job of this repository runs on the pool.',
};

/** The one step forward, or undefined at the end. */
export function nextStep(m: Mode): Mode | undefined {
  return STEPS[STEPS.indexOf(m) + 1];
}

/** The steps a repository may go back to. */
export function earlierSteps(m: Mode): Mode[] {
  return STEPS.slice(0, STEPS.indexOf(m));
}

/** The words of the question before a move. */
export function moveItems(from: Mode, to: Mode): string[] {
  return [`From ${STEP_WORDS[from]} to ${STEP_WORDS[to]}.`, STEP_HELP[to]];
}

export function RolloutPane({ repositoryId, fullName, poolOn }: { repositoryId: number; fullName: string; poolOn: boolean }) {
  const client = useClient();
  const session = useSession();
  const { result, reload } = useLoad(
    () =>
      unwrap(
        client.GET('/api/v1/repositories/{repositoryId}/trial', {
          params: { path: { repositoryId } },
        }),
      ),
    [repositoryId],
  );
  const [target, setTarget] = useState<Mode | undefined>(undefined);
  const [jobs, setJobs] = useState('');
  const [percent, setPercent] = useState(0);
  const move = useQuestion(
    async () => {
      if (!target) return;
      const body: Schemas['TrialSet'] = { mode: target, confirmed: true };
      if (target === 'canary') {
        const names = jobs.split(',').map((j) => j.trim()).filter(Boolean);
        if (names.length > 0) body.jobs = names;
        if (percent > 0) body.percent = percent;
      }
      await unwrap(
        client.PUT('/api/v1/repositories/{repositoryId}/trial', {
          params: { path: { repositoryId } },
          body,
        }),
      );
    },
    () => {
      setTarget(undefined);
      reload();
    },
  );
  const why = session.whyNot('admin');

  return (
    <Pane title="Rollout">
      <Load result={result} what="the rollout" reload={reload}>
        {(t) => {
          const forward = nextStep(t.mode);
          const back = earlierSteps(t.mode);
          const ask = (to: Mode) => {
            setTarget(to);
            move.ask(moveItems(t.mode, to));
          };
          return (
            <Stack>
              <Text>
                {`How much of ${fullName} the pool takes. Each move asks first. You go one step forward at a time and back whenever you like.`}
              </Text>
              <Facts
                items={[
                  { name: 'Step', value: STEP_WORDS[t.mode] },
                  { name: 'What it means', value: STEP_HELP[t.mode] },
                ]}
              />
              {!poolOn && t.mode === 'off' ? (
                <Message tone="note">The pool is off for this repository. Turn it on first.</Message>
              ) : null}
              {forward === 'canary' ? (
                <Cluster>
                  <TextField
                    label="Jobs that use the pool"
                    help="Job names, separated by commas."
                    value={jobs}
                    onChange={setJobs}
                  />
                  <NumberField
                    label="Share of the other jobs (%)"
                    value={percent}
                    min={0}
                    max={100}
                    onChange={setPercent}
                  />
                </Cluster>
              ) : null}
              <Cluster>
                {forward ? (
                  <Button
                    onPress={() => ask(forward)}
                    disabled={why !== undefined || !poolOn}
                    reason={why ?? 'The pool is off for this repository.'}
                  >
                    {`Go to ${STEP_WORDS[forward]}`}
                  </Button>
                ) : null}
                {back.map((b) => (
                  <Button key={b} onPress={() => ask(b)} disabled={why !== undefined} reason={why}>
                    {`Back to ${STEP_WORDS[b]}`}
                  </Button>
                ))}
              </Cluster>
              <Ask
                title={target ? `Move ${fullName} to ${STEP_WORDS[target]}?` : 'Move?'}
                action="Move"
                busyLabel="Moving"
                q={move.question}
                busy={move.busy}
                error={move.error}
                onCancel={move.cancel}
                onConfirm={() => void move.confirm()}
              />
            </Stack>
          );
        }}
      </Load>
    </Pane>
  );
}
