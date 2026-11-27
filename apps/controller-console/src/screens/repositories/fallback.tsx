// The hosted fallback of a repository (github-integration R5.42 to R5.44): where its jobs go now and why, and the
// setting "When no crsp machine can take these jobs". The fields are part of the repository page's one form and
// one Save; turning it on asks first in the page (UX-9).
import {
  ChoiceField,
  Message,
  NumberField,
  Stack,
  Status,
  TextField,
  type StatusKind,
} from '@crsp/ui';
import type { Repository } from './plans';

const OS_NAME = { linux: 'Linux', windows: 'Windows', macos: 'macOS' } as const;

function osWords(oses: readonly string[] | undefined): string {
  const names = (oses ?? []).map((o) => OS_NAME[o as keyof typeof OS_NAME] ?? o);
  return names.length ? names.join(' and ') : 'this system';
}

/** Where the jobs go now, as a status (content.md, hosted fallback words). */
export function routingStatus(
  r: Repository,
): { kind: StatusKind; word: string } | undefined {
  const rt = r.routing;
  if (!rt) return undefined;
  if (rt.where === 'hosted') return { kind: 'waiting', word: 'GitHub-hosted runners' };
  if (rt.where === 'waiting') return { kind: 'off', word: 'waiting for a machine' };
  return { kind: 'ok', word: 'crsp machines' };
}

/** Why the jobs go there. */
export function routingWhy(r: Repository): string {
  const rt = r.routing;
  if (!rt) return 'The pool is off for this repository.';
  const os = osWords(rt.oses);
  switch (rt.why) {
    case 'fallback_on':
      return `No crsp machine for ${os} is up, so jobs run on GitHub-hosted runners.`;
    case 'fallback_off':
      return `No crsp machine for ${os} is up. Jobs wait for one.`;
    case 'grace':
      return `No crsp machine for ${os} is up. Jobs move to GitHub-hosted runners if none comes back soon.`;
    case 'cap_reached':
      return `The monthly cap of hosted minutes is reached. Jobs wait for a crsp machine.`;
    case 'public_repository':
      return `A public repository never runs on GitHub-hosted runners. Jobs wait for a crsp machine.`;
    default:
      return 'A crsp machine is up.';
  }
}

export function WhereJobsGo({ r }: { r: Repository }) {
  const s = routingStatus(r);
  return s ? (
    <Status kind={s.kind} word={s.word} />
  ) : (
    <Status kind="off" word="pool off" />
  );
}

const WAIT = 'wait';
const HOSTED = 'hosted';

/** The hosted fallback setting as the page form holds it. */
export interface HostedState {
  mode: typeof WAIT | typeof HOSTED;
  cap: number;
  labels: { linux: string; windows: string; macos: string };
}

export function hostedStart(r: Repository): HostedState {
  const p = r.policy;
  return {
    mode: p.hosted_fallback ? HOSTED : WAIT,
    cap: p.fallback_cap_minutes ?? 0,
    labels: {
      linux: p.hosted_labels?.['linux'] ?? '',
      windows: p.hosted_labels?.['windows'] ?? '',
      macos: p.hosted_labels?.['macos'] ?? '',
    },
  };
}

/** The policy fields this setting sends. */
export function hostedBody(h: HostedState) {
  return {
    hosted_fallback: h.mode === HOSTED,
    fallback_cap_minutes: h.cap,
    hosted_labels: h.labels,
  };
}

export function hostedChanged(a: HostedState, b: HostedState): boolean {
  return JSON.stringify(a) !== JSON.stringify(b);
}

export function hostedMistakes(h: HostedState): string[] {
  return h.cap < 0 ? ['The monthly cap must be 0 or more minutes.'] : [];
}

/** True when the form turns the fallback on now (it asks first, UX-9). */
export function turnsFallbackOn(r: Repository, h: HostedState): boolean {
  return h.mode === HOSTED && !r.policy.hosted_fallback;
}

export const FALLBACK_COST_ITEMS = (fullName: string) => [
  'GitHub may bill the minutes that run on GitHub-hosted runners for a private repository.',
  `Jobs of ${fullName} run there only while no crsp machine can take them. crsp switches back as soon as a machine is up.`,
];

/** The fields of the setting; the page form owns the state and the Save. */
export function HostedFields({
  r,
  value,
  onChange,
}: {
  r: Repository;
  value: HostedState;
  onChange: (next: HostedState) => void;
}) {
  const isPublic = r.visibility === 'public';
  const start = hostedStart(r);
  return (
    <Stack>
      <ChoiceField
        label="When no crsp machine can take these jobs"
        value={value.mode}
        changed={value.mode !== start.mode}
        onChange={(v) => onChange({ ...value, mode: v === HOSTED ? HOSTED : WAIT })}
        choices={[
          { value: WAIT, label: 'Wait for a crsp machine' },
          {
            value: HOSTED,
            label: 'Run on GitHub-hosted runners',
            disabledReason: isPublic
              ? 'A public repository never runs on GitHub-hosted runners.'
              : undefined,
          },
        ]}
        help="Waiting costs nothing. Jobs stay queued on GitHub for up to 24 hours."
      />
      {value.mode === HOSTED ? (
        <>
          <NumberField
            label="Monthly cap of hosted minutes"
            value={value.cap}
            changed={value.cap !== start.cap}
            min={0}
            onChange={(v) => onChange({ ...value, cap: v })}
            help="0 means no cap. Once the cap is reached this month, jobs wait again."
          />
          {(['linux', 'windows', 'macos'] as const).map((os) => (
            <TextField
              key={os}
              label={`Hosted label for ${OS_NAME[os]}`}
              optional
              value={value.labels[os]}
              changed={value.labels[os] !== start.labels[os]}
              onChange={(v) =>
                onChange({ ...value, labels: { ...value.labels, [os]: v } })
              }
              help={`Blank uses ${os === 'linux' ? 'ubuntu' : os}-latest.`}
            />
          ))}
          <Message tone="needs-you">
            GitHub may bill these minutes for a private repository. crsp switches back as
            soon as a machine is up.
          </Message>
        </>
      ) : null}
    </Stack>
  );
}
