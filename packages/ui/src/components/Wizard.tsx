// Component 4: the shared wizard (UX-5, UX-30, UX-54). Current step, locks, Stop, busy and Done live HERE.
// A page passes data only: the phase it reads from the server, the steps, the primary and the step content.
// The ten phases are the ten states of the wizard harness (scripts/design/wizard-behaviour.ts).
import { useEffect, useId, useState, type ReactNode } from 'react';
import { ActionBar } from './ActionBar';
import { Button } from './Button';
import { DownloadProgress } from './DownloadProgress';
import { PhaseProgress, Progress, type ProgressPhaseData } from './Progress';
import { Terminal, type TerminalRow } from './Terminal';

/** One progress event (the `ProgressEvent` of @crsp/contract, structurally). The page passes them; the wizard draws. */
export interface LiveEvent extends TerminalRow {
  phase: string;
  phaseIndex: number;
  phaseCount: number;
  phaseWeight: number;
  pct: number | null;
  reason?: string | undefined;
  bytes?: number | undefined;
  total?: number | undefined;
  rate?: number | undefined;
  etaSec?: number | undefined;
}

/** The phases the events so far describe, one per index. */
export function phasesOf(events: readonly LiveEvent[]): ProgressPhaseData[] {
  const out: ProgressPhaseData[] = [];
  for (const e of events) {
    const count = Math.max(e.phaseCount, e.phaseIndex + 1);
    while (out.length < count) out.push({ name: '', weight: 1, pct: 0 });
    out[e.phaseIndex] = {
      name: e.phase,
      weight: e.phaseWeight,
      pct: e.pct,
      reason: e.reason,
    };
  }
  return out;
}

export type WizardPhase =
  | 'loading'
  | 'first-run'
  | 'planning'
  | 'ready-to-review'
  | 'running'
  | 'paused'
  | 'stopped'
  | 'failed'
  | 'done'
  | 'done-after-reload';

export interface WizardPrimary {
  label: string;
  onPress: () => void;
  /** Shown beside a disabled primary, and the busy words while the work runs. */
  busyLabel?: string | undefined;
  disabled?: boolean | undefined;
  reason?: string | undefined;
}

export interface WizardProps {
  /** The wizard's name, used for landmarks. */
  name: string;
  /** Step names. A wizard that changes something ends with Review, the work, Done (UX-30). */
  steps: readonly string[];
  /** Index of the current step. */
  current: number;
  phase: WizardPhase;
  /** What the current step is still reading ("Reading the controller"). Loading is a state of the current step,
   * never a step of its own: one wait line, the primary locked, Back free, no step checked (UX-38). `steps` never
   * lists it. */
  reading?: string | undefined;
  /** The work step's live line: the item in hand, n of N, the time left (UX-29). */
  progress?:
    { text: string; value?: number | undefined; max?: number | undefined } | undefined;
  /** The live events of the work step (UX-88 to UX-90): the wizard draws the terminal, the download line and the
   * weighted bar from them, and keeps the terminal after the work ends. `progress` is the fallback without events. */
  live?: readonly LiveEvent[] | undefined;
  canStop?: boolean | undefined;
  /** Stop was pressed and the work has not ended yet. */
  stopping?: boolean | undefined;
  onStop?: (() => void) | undefined;
  onBack?: (() => void) | undefined;
  /** The words of the left button when it is not Back: "Not now" on a Review that only asks (TRAY4). */
  backLabel?: string | undefined;
  /** A wizard whose work goes on without its window: Hide closes the window while it runs and stops nothing (TRAY4). */
  hide?: { label: string; onPress: () => void } | undefined;
  onStep?: ((index: number) => void) | undefined;
  /** The one primary of the current step (not used in running, paused, stopped, failed, done). */
  primary: WizardPrimary;
  /** The next action on Done, when it is not Start again ("Sign in with GitHub"). */
  donePrimary?: { label: string; onPress: () => void };
  onStartAgain?: (() => void) | undefined;
  /** The primary of a paused or stopped wizard when it is not Start again ("Restart this PC now", "Continue"). */
  endPrimary?: { label: string; onPress: () => void } | undefined;
  onTryAgain?: (() => void) | undefined;
  /** The first step the user acts on (a read-only step before it has no Back). */
  firstStep?: number | undefined;
  /** The index of the step that failed. Set with the phase `failed`, that step is marked failed and is not ticked. */
  failedAt?: number | undefined;
  /** Show "Nothing has changed yet." (on the Review step). */
  review?: boolean | undefined;
  /** The words for the state a stored run is in, said once above the content. */
  stateNote?: string | undefined;
  /** The wizard sits on a screen whose own primary is elsewhere: its buttons are default, never primary (UX-4). */
  embedded?: boolean | undefined;
  children: ReactNode;
}

const END: readonly WizardPhase[] = ['done', 'done-after-reload', 'stopped', 'failed'];

function LivePart({
  events,
  running,
  statusId,
}: {
  events: readonly LiveEvent[];
  running: boolean;
  statusId: string;
}) {
  const [open, setOpen] = useState(true);
  const [now, setNow] = useState(() => Date.now());
  useEffect(() => {
    if (!running) return;
    const t = setInterval(() => setNow(Date.now()), 1000);
    return () => clearInterval(t);
  }, [running]);
  const last = events[events.length - 1];
  if (!last) return null;
  const phases = phasesOf(events);
  const phaseSince = [...events]
    .reverse()
    .findIndex((e) => e.phaseIndex !== last.phaseIndex);
  const started =
    (phaseSince < 0 ? events[0] : events[events.length - phaseSince])?.ts ?? last.ts;
  return (
    <div className="ds-stack" data-layout-group>
      {running ? (
        <PhaseProgress
          phases={phases}
          current={last.phaseIndex}
          statusId={statusId}
          elapsedSec={Math.max(0, Math.round((now - started) / 1000))}
        />
      ) : null}
      {running && last.bytes !== undefined ? (
        <DownloadProgress
          bytes={last.bytes}
          total={last.total}
          rate={last.rate}
          etaSec={last.etaSec}
        />
      ) : null}
      <Terminal rows={events} open={open} onToggle={setOpen} />
    </div>
  );
}

export function Wizard(p: WizardProps) {
  const runId = useId();
  const hasLive = (p.live?.length ?? 0) > 0;
  const last = p.steps.length - 1;
  const locked = p.phase === 'running' || p.phase === 'loading';
  const atEnd = END.includes(p.phase);
  const failedStep = p.phase === 'failed' ? p.failedAt : undefined;
  const current =
    failedStep !== undefined
      ? failedStep
      : atEnd && p.phase !== 'failed'
        ? last
        : p.current;
  const isDone = p.phase === 'done' || p.phase === 'done-after-reload';

  // A wizard inside a screen that has its own primary (a machine page) draws default buttons (UX-4).
  const kind = p.embedded ? 'default' : 'primary';
  let primary: ReactNode;
  if (p.phase === 'loading') {
    primary = (
      <Button
        kind={kind}
        part={p.embedded ? 'primary' : undefined}
        disabled
        reason={p.reading ?? 'Reading'}
        reasonIsWait
      >
        {p.primary.label}
      </Button>
    );
  } else if (p.phase === 'running') {
    primary = (
      <Button
        kind={kind}
        part={p.embedded ? 'primary' : undefined}
        busy
        busyLabel={p.primary.busyLabel ?? 'Working'}
        disabled
        describedBy={`${runId}-line`}
      >
        {p.primary.label}
      </Button>
    );
  } else if (isDone) {
    primary = p.donePrimary ? (
      <Button
        kind={kind}
        part={p.embedded ? 'primary' : undefined}
        onPress={p.donePrimary.onPress}
      >
        {p.donePrimary.label}
      </Button>
    ) : (
      <Button
        kind={kind}
        part={p.embedded ? 'primary' : undefined}
        onPress={p.onStartAgain}
      >
        Start again
      </Button>
    );
  } else if (p.phase === 'stopped' || p.phase === 'paused') {
    primary = (
      <Button
        kind={kind}
        part={p.embedded ? 'primary' : undefined}
        onPress={p.endPrimary?.onPress ?? p.onStartAgain}
      >
        {p.endPrimary?.label ?? 'Start again'}
      </Button>
    );
  } else if (p.phase === 'failed') {
    primary = (
      <Button
        kind={kind}
        part={p.embedded ? 'primary' : undefined}
        onPress={p.onTryAgain}
      >
        Try again
      </Button>
    );
  } else {
    primary = (
      <Button
        kind={kind}
        part={p.embedded ? 'primary' : undefined}
        onPress={p.primary.onPress}
        disabled={p.primary.disabled}
        reason={p.primary.reason}
      >
        {p.primary.label}
      </Button>
    );
  }

  const showBack = !locked || p.phase === 'loading';
  const canStopNow = p.phase === 'running' && p.canStop;

  return (
    <div className="ds-wizard" data-phase={p.phase} role="group" aria-label={p.name}>
      <ol className="ds-steps" aria-label="Steps" data-layout-group>
        {p.steps.map((name, i) => {
          const standing = i < current ? 'done' : i === current ? 'now' : 'later';
          const pressable =
            standing === 'done' && !locked && p.phase !== 'paused' && Boolean(p.onStep);
          return (
            <li key={name} className="ds-steps__item" data-standing={standing}>
              <button
                type="button"
                className="ds-steps__button"
                data-failed={i === failedStep || undefined}
                aria-current={standing === 'now' ? 'step' : undefined}
                disabled={!pressable && standing !== 'now'}
                onClick={() => p.onStep?.(i)}
              >
                <span className="ds-steps__n" aria-hidden="true">
                  {i === failedStep ? '✕' : standing === 'done' ? '✓' : i + 1}
                </span>{' '}
                {name}
              </button>
            </li>
          );
        })}
      </ol>
      <div className="ds-wizard__body" data-layout-group>
        {p.stateNote ? <p className="ds-text">{p.stateNote}</p> : null}
        {p.phase === 'loading' ? <Progress label={p.reading ?? 'Reading'} /> : null}
        {p.phase === 'running' && !hasLive ? (
          <div className="ds-stack" data-gap="tight" data-layout-group>
            <p className="ds-text" role="status" id={`${runId}-line`}>
              {p.progress?.text ?? p.primary.busyLabel ?? 'Working'}
            </p>
            <Progress
              label={p.progress?.text ?? p.primary.busyLabel ?? 'Working'}
              value={p.progress?.value}
              max={p.progress?.max}
            />
          </div>
        ) : null}
        {hasLive &&
        (p.phase === 'running' || p.phase === 'paused' || END.includes(p.phase)) ? (
          <LivePart
            events={p.live ?? []}
            running={p.phase === 'running'}
            statusId={`${runId}-line`}
          />
        ) : null}
        {p.phase !== 'loading' ? (
          <div
            className="ds-wizard__step"
            data-done={isDone || undefined}
            data-wizard-done={atEnd || undefined}
          >
            {p.children}
          </div>
        ) : null}
        {p.review && !atEnd && p.phase !== 'running' && p.phase !== 'loading' ? (
          <p className="ds-text">Nothing has changed yet.</p>
        ) : null}
      </div>
      <ActionBar
        label={`${p.name} actions`}
        sticky={!p.embedded}
        secondary={
          showBack || canStopNow || (p.phase === 'running' && p.hide) ? (
            <>
              {showBack ? (
                <Button
                  part="back"
                  onPress={p.onBack}
                  disabled={p.phase !== 'loading' && current <= (p.firstStep ?? 0)}
                >
                  {p.backLabel ?? 'Back'}
                </Button>
              ) : null}
              {p.phase === 'running' && p.hide ? (
                <Button part="other" onPress={p.hide.onPress}>
                  {p.hide.label}
                </Button>
              ) : null}
              {canStopNow ? (
                <Button
                  part="stop"
                  kind="stop"
                  onPress={p.onStop}
                  busy={p.stopping}
                  busyLabel="Stopping"
                >
                  Stop
                </Button>
              ) : null}
            </>
          ) : undefined
        }
        primary={primary}
      />
    </div>
  );
}
