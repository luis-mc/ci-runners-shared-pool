// Which runner kinds a machine offers for a system, and which one Add runners preselects (runners R5.91,
// R5.92; owner decisions D93, D94(b)). Pure functions, so the words and the rules are tested without a screen.
import type { Schemas } from '../../api/api';
import type { Repository } from '../repositories/plans';

type OS = Schemas['OS'];
type Machine = Schemas['Machine'];
export type Kind = Schemas['RunnerKind'];

export const KIND_NAMES: Record<Kind, string> = {
  persistent: 'Persistent',
  ephemeral: 'Ephemeral',
};

export const KIND_HELP: Record<Kind, string> = {
  persistent: 'Keeps its tools and caches between jobs. Starts jobs faster.',
  ephemeral:
    'A fresh container for every job. Nothing carries over. Recommended for public repositories.',
};

const OS_WORD: Record<OS, string> = {
  linux: 'Linux',
  windows: 'Windows',
  macos: 'macOS',
};

export const PERSISTENT_ELSEWHERE = (machineOs: OS, os: OS): string =>
  `Persistent runners run on the machine itself, and this ${OS_WORD[machineOs]} machine cannot run a persistent ${OS_WORD[os]} runner there. ${OS_WORD[os]} runners on this machine are ephemeral.`;

function container(m: Machine): Machine['backends'][number] | undefined {
  return m.backends.find((b) => b.name === 'docker');
}

/** The container backend is there, works and is allowed on the machine (machines R5.27). */
export function containerReady(m: Machine): boolean {
  const b = container(m);
  return b !== undefined && b.usable && b.allowed;
}

function containerWhy(m: Machine): string {
  const b = container(m);
  return b && b.allowed
    ? 'which does not work on this machine'
    : 'which is not allowed on this machine';
}

/** A kind the machine cannot run for the system has its reason; one it can run has none. */
export function kindReasons(
  m: Machine | undefined,
  os: OS,
): Record<Kind, string | undefined> {
  if (!m) return { persistent: undefined, ephemeral: undefined };
  const crosses = m.os !== os;
  const ready = containerReady(m);
  let ephemeral: string | undefined;
  if (os !== 'linux') {
    ephemeral = `Ephemeral runners are not available for ${OS_WORD[os]} yet. ${OS_WORD[os]} runners are persistent.`;
  } else if (!ready) {
    ephemeral = `Ephemeral needs the container backend, ${containerWhy(m)}.`;
  }
  // OPS87 (runners R5.93, R5.139): Persistent is offered on the machine itself and, for Linux on a Windows
  // machine, as a long-lived container when the container backend is ready. A WSL2 distro, a Hyper-V VM or a
  // macOS VM is not offered; those places use ephemeral runners.
  let persistent: string | undefined;
  if (crosses && m.os === 'windows' && os === 'linux' && m.container_os === 'linux') {
    persistent = ready
      ? undefined
      : `Persistent Linux runners on this Windows machine need the container backend, ${containerWhy(m)}.`;
  } else if (crosses) {
    persistent = PERSISTENT_ELSEWHERE(m.os, os);
  }
  return { persistent, ephemeral };
}

type Visible = Pick<Repository, 'visibility' | 'pool_on'>;

/** D94(b): Ephemeral while any public repository uses the pool, else Persistent. */
export function preferredKind(repos: readonly Visible[]): Kind {
  return repos.some((r) => r.visibility === 'public' && r.pool_on)
    ? 'ephemeral'
    : 'persistent';
}

export interface KindChoice {
  kind: Kind;
  /** The one line that says why this kind stands chosen, when the user did not pick it. */
  line: string | undefined;
  reasons: Record<Kind, string | undefined>;
  /** Neither kind can run here. */
  none: boolean;
}

/** The kind that stands: the user's pick if the machine offers it, else the D94(b) default or the other kind. */
export function chooseKind(
  m: Machine | undefined,
  os: OS,
  repos: readonly Visible[],
  picked: Kind | undefined,
): KindChoice {
  const reasons = kindReasons(m, os);
  const none = Boolean(reasons.persistent && reasons.ephemeral);
  if (picked && !reasons[picked]) {
    return { kind: picked, line: undefined, reasons, none };
  }
  const want = preferredKind(repos);
  const other: Kind = want === 'ephemeral' ? 'persistent' : 'ephemeral';
  if (!reasons[want]) {
    return {
      kind: want,
      line:
        want === 'ephemeral'
          ? 'Ephemeral is chosen because a public repository uses the pool.'
          : 'Persistent is chosen because no public repository uses the pool.',
      reasons,
      none,
    };
  }
  return {
    kind: other,
    line: none
      ? undefined
      : `${KIND_NAMES[other]} is chosen because ${KIND_NAMES[want]} is not offered here.`,
    reasons,
    none,
  };
}
