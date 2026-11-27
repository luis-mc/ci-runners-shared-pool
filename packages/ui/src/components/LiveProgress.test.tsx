// CRSP247: Terminal (82), Download progress (83) and the weighted bar (16), and the wizard that draws them (UX-88 to UX-90).
import { cleanup, fireEvent, render, screen } from '@testing-library/react';
import { afterEach, describe, expect, it } from 'vitest';
import { downloadWords } from './DownloadProgress';
import { weightedPct } from './Progress';
import { Terminal, terminalBlocks, type TerminalRow } from './Terminal';
import { Wizard, type LiveEvent, type WizardPhase } from './Wizard';

afterEach(cleanup);
const pc = { name: 'HOME-PC', role: 'controller', os: 'Windows 11' } as const;
const box = { name: 'build-1', role: 'agent', os: 'Ubuntu 24.04' } as const;
const ev = (over: Partial<LiveEvent> = {}): LiveEvent => ({
  machine: pc,
  phase: 'Downloading Docker Engine',
  phaseIndex: 0,
  phaseCount: 3,
  phaseWeight: 6,
  pct: 50,
  stream: 'stdout',
  ts: Date.UTC(2026, 10, 26, 12, 0, 0),
  ...over,
});

describe('weighted bar (UX-88)', () => {
  const phases = [
    { name: 'Download', weight: 6, pct: 50 },
    { name: 'Install', weight: 3, pct: 0 },
    { name: 'Verify', weight: 1, pct: 0 },
  ];
  it('counts finished phases in full and the current one by its own percent', () => {
    expect(weightedPct(phases, 0)).toBe(30);
    expect(weightedPct([{ ...phases[0]!, pct: 100 }, ...phases.slice(1)], 1)).toBe(60);
  });
  it('is indeterminate (null) while the current phase reports none', () => {
    expect(
      weightedPct([phases[0]!, { ...phases[1]!, pct: null }, phases[2]!], 1),
    ).toBeNull();
    expect(weightedPct([], 0)).toBeNull();
  });
});

describe('download words (UX-90)', () => {
  it('says measured bytes, speed and time left', () => {
    expect(downloadWords({ bytes: 230e6, total: 410e6, rate: 12e6, etaSec: 15 })).toBe(
      '230 MB of 410 MB, 12.0 MB/s, about 15 s left',
    );
  });
  it('says measuring speed until a rate exists, never a guess', () => {
    expect(downloadWords({ bytes: 5e6, total: 10e6 })).toBe(
      '5.0 MB of 10.0 MB, measuring speed',
    );
  });
  it('without a total says bytes and speed only', () => {
    expect(downloadWords({ bytes: 5e6, rate: 1e6 })).toBe('5.0 MB so far, 1.0 MB/s');
  });
});

describe('terminal (UX-88)', () => {
  const rows: TerminalRow[] = [
    { machine: pc, command: 'wsl --install', stream: 'stdout', ts: 1 },
    { machine: pc, stream: 'stdout', line: 'Installing', ts: 2 },
    { machine: pc, stream: 'stderr', line: 'warning: reboot', ts: 3 },
    { machine: box, command: 'docker info', stream: 'stdout', ts: 4 },
  ];
  it('groups lines in blocks per command and machine', () => {
    const b = terminalBlocks(rows);
    expect(b.map((x) => [x.machine.name, x.command, x.lines.length])).toEqual([
      ['HOME-PC', 'wsl --install', 2],
      ['build-1', 'docker info', 0],
    ]);
  });
  it('shows machine, role, system, command and an error line with the word error', () => {
    render(<Terminal rows={rows} open onToggle={() => undefined} />);
    expect(screen.getByText('HOME-PC')).toBeTruthy();
    expect(screen.getByText('controller')).toBeTruthy();
    expect(screen.getByText('agent')).toBeTruthy();
    expect(screen.getByText('Ubuntu 24.04')).toBeTruthy();
    expect(screen.getByText('$ wsl --install')).toBeTruthy();
    expect(screen.getByText(/^error/)).toBeTruthy();
  });
  it('closed shows the last command and Show log; the toggle asks to open', () => {
    let to: boolean | undefined;
    render(<Terminal rows={rows} open={false} onToggle={(o) => (to = o)} />);
    expect(screen.getByText('$ docker info')).toBeTruthy();
    fireEvent.click(screen.getByRole('button', { name: 'Show log' }));
    expect(to).toBe(true);
  });
  it('says so when there is no command yet', () => {
    render(<Terminal rows={[]} open onToggle={() => undefined} />);
    expect(screen.getByText('No commands yet.')).toBeTruthy();
  });
});

describe('wizard with live events, every state', () => {
  const states: WizardPhase[] = [
    'first-run',
    'running',
    'paused',
    'stopped',
    'failed',
    'done',
    'done-after-reload',
  ];
  const mount = (phase: WizardPhase, live: LiveEvent[]) =>
    render(
      <Wizard
        name="Get ready"
        steps={['Choose', 'Review', 'Install', 'Done']}
        current={phase === 'first-run' ? 0 : 2}
        phase={phase}
        live={live}
        canStop
        onStop={() => undefined}
        onStartAgain={() => undefined}
        onTryAgain={() => undefined}
        primary={{ label: 'Install', busyLabel: 'Installing', onPress: () => undefined }}
        endPrimary={{ label: 'Continue', onPress: () => undefined }}
      >
        content
      </Wizard>,
    );
  for (const phase of states) {
    it(`${phase}: later steps cannot be pressed; the terminal shows only once there are events`, () => {
      mount(phase, [ev({ command: 'wsl --install' })]);
      const later = document.querySelectorAll<HTMLButtonElement>(
        '[data-standing="later"] button',
      );
      expect([...later].every((b) => b.disabled)).toBe(true);
      const hasLog = screen.queryByRole('log', { name: 'Command log' }) !== null;
      expect(hasLog).toBe(phase !== 'first-run');
      expect(screen.queryByRole('button', { name: 'Stop' }) !== null).toBe(
        phase === 'running',
      );
    });
  }
  it('running: whole-job percent, step name and a disabled busy primary', () => {
    mount('running', [ev({ bytes: 5e6, total: 10e6 })]);
    expect(screen.getByText('Step 1 of 3: Downloading Docker Engine')).toBeTruthy();
    expect(screen.getByText('37.5% of the whole job')).toBeTruthy();
    expect(screen.getByText('5.0 MB of 10.0 MB, measuring speed')).toBeTruthy();
    const busy = screen.getAllByRole<HTMLButtonElement>('button', {
      name: 'Installing',
    })[0];
    expect(busy?.disabled).toBe(true);
  });
  it('running with no percent: indeterminate, says why, and shows the time elapsed', () => {
    mount('running', [
      ev({
        pct: null,
        reason: 'Waiting for the installer to finish.',
        phase: 'Installing',
      }),
    ]);
    const bar = screen.getAllByRole('progressbar')[0]!;
    expect(bar.hasAttribute('aria-valuenow')).toBe(false);
    expect(
      screen.getByText(
        /^Installing\. Waiting for the installer to finish\. No % is reported\./,
      ),
    ).toBeTruthy();
  });
  it('done keeps the terminal and drops the bar', () => {
    mount('done', [ev({ command: 'docker info' })]);
    expect(screen.getByRole('log', { name: 'Command log' })).toBeTruthy();
    expect(screen.queryByText(/of the whole job/)).toBeNull();
  });
});
