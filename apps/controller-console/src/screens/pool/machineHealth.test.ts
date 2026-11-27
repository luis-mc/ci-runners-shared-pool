// LOOK58c: the words of the machine health: known values, unknown values that name the system, badges and stats.
import { describe, expect, it } from 'vitest';
import {
  fullHealth,
  machineStats,
  statsSet,
  unknownHealth,
} from '../../testing/machineHealthData';
import {
  badgesOf,
  metricsOf,
  orUnknown,
  runnerLine,
  runnerRowsOf,
  statLines,
  unknownOn,
  uptimeWords,
} from './machineHealth';

const value = (id: string, os: string, h = fullHealth()) =>
  metricsOf(h, os).find((m) => m.id === id)?.value;

describe('metricsOf', () => {
  it('gives every key number of a machine that reported everything', () => {
    const all = metricsOf(fullHealth(), 'linux');
    expect(all).toHaveLength(13);
    expect(all.every((m) => m.known)).toBe(true);
    expect(value('uptime', 'linux')).toBe('3 days 4 h');
    expect(value('disk', 'linux')).toBe('25% on the Docker disk');
    expect(value('days', 'linux')).toBe('about 6.5 days (Docker disk)');
    expect(value('swap', 'linux')).toBe('1.0 GB of 8.0 GB used');
    expect(value('connectivity', 'linux')).toBe(
      'reaches GitHub in 80 ms, 12 ms to the controller',
    );
    expect(value('restart', 'linux')).toBe('yes');
    expect(value('skew', 'linux')).toBe('2 s ahead of the controller');
    expect(value('docker', 'linux')).toBe('26.5 GB in use');
    expect(value('temp', 'linux')).toBe('61.5 °C');
    expect(value('load', 'linux')).toBe('1.2, 0.9, 0.7');
  });

  it('reads unknown on the machine system, with the reason, never blank or 0', () => {
    const all = metricsOf(unknownHealth(), 'windows');
    const unknown = all.filter((m) => !m.known);
    expect(unknown.map((m) => m.id)).toEqual(
      expect.arrayContaining([
        'uptime',
        'disk',
        'swap',
        'restart',
        'power',
        'sleep',
        'docker',
        'temp',
        'load',
      ]),
    );
    for (const m of unknown) expect(m.value).toBe('unknown on Windows');
    expect(all.find((m) => m.id === 'load')?.why).toBe('Windows has no load average');
    expect(unknownOn('macos')).toBe('unknown on macOS');
  });

  it('reads unknown for every value while the machine has reported nothing', () => {
    const all = metricsOf(undefined, 'linux');
    expect(all.every((m) => !m.known && m.value === 'unknown on Linux')).toBe(true);
  });

  it('keeps a real zero: no swap set up is not unknown', () => {
    const h = fullHealth({ swap: { total_bytes: 0, used_bytes: 0 } });
    expect(value('swap', 'linux', h)).toBe('none set up');
  });

  it('says why a disk has no days figure, from the API note', () => {
    const h = fullHealth({
      volumes: [
        {
          role: 'system',
          path: '/',
          free_bytes: 1,
          total_bytes: 2,
          free_pct: 50,
          days_note: 'free space is not falling',
        },
      ],
    });
    expect(value('days', 'linux', h)).toBe('free space is not falling');
  });
});

describe('badgesOf', () => {
  it('says health is not reported yet', () => {
    expect(badgesOf(undefined)).toEqual([
      { kind: 'info', word: 'health not reported yet' },
    ]);
    expect(badgesOf(fullHealth({ reported: false }))[0]?.word).toBe(
      'health not reported yet',
    );
  });

  it('flags what is wrong and says healthy otherwise', () => {
    expect(badgesOf(fullHealth({ updates: { restart_pending: false } }))).toEqual([
      { kind: 'ok', word: 'healthy' },
    ]);
    const bad = badgesOf(
      fullHealth({
        stale: true,
        oom: { total_kills: 2, recent: true },
        connectivity: { github: { reachable: false }, clock_skew_seconds: 90 },
        versions: { agent_out_of_date: true },
        updates: { restart_pending: true, restart_may_hit_jobs: true },
      }),
    ).map((b) => b.word);
    expect(bad).toEqual(
      expect.arrayContaining([
        'report is old',
        'ran out of memory',
        'cannot reach GitHub',
        'clock is off',
        'agent out of date',
        'restart may stop jobs',
      ]),
    );
  });

  it('flags a disk that is almost full or fills in under 3 days', () => {
    const full = fullHealth({
      volumes: [
        { role: 'system', path: '/', free_pct: 6, free_bytes: 1, total_bytes: 20 },
      ],
    });
    expect(badgesOf(full).map((b) => b.word)).toContain('disk almost full');
    const soon = fullHealth({
      volumes: [
        {
          role: 'system',
          path: '/',
          free_pct: 40,
          free_bytes: 1,
          total_bytes: 2,
          days_until_full: 1.5,
        },
      ],
    });
    expect(badgesOf(soon).map((b) => b.word)).toContain('disk fills soon');
  });
});

describe('job stats words', () => {
  it('lists the counts, the averages and the busy percent', () => {
    const lines = Object.fromEntries(statLines(statsSet()).map((l) => [l.id, l.value]));
    expect(lines).toMatchObject({
      succeeded: '18',
      failed: '2',
      timed_out: '1',
      over_memory: '1',
      stopped: '1',
      run: '4 min 12 s',
      wait: '40 s',
      busy: '35%',
    });
  });

  it('says no jobs yet for an average that is absent, never 0', () => {
    const lines = Object.fromEntries(
      statLines(statsSet({}, ['avg_run_seconds', 'avg_queue_wait_seconds'])).map((l) => [
        l.id,
        l.value,
      ]),
    );
    expect(lines.run).toBe('no jobs yet');
    expect(lines.wait).toBe('no jobs yet');
  });

  it('joins the runner list with the stats, and keeps a runner only the stats name', () => {
    const rows = runnerRowsOf([{ id: 'r2', name: 'crsp-linux-2' }], machineStats());
    expect(rows.map((r) => [r.id, r.stats !== undefined])).toEqual([
      ['r2', false],
      ['r1', true],
    ]);
    expect(runnerLine(undefined)).toBe('no jobs in this window');
    expect(runnerLine(statsSet())).toBe(
      '24 jobs, 4 failed or cut short, average run 4 min 12 s, busy 35%',
    );
  });

  it('formats uptime and the unknown helper', () => {
    expect(uptimeWords(5 * 3600 + 600)).toBe('5 h 10 min');
    expect(uptimeWords(86_400)).toBe('1 day');
    expect(orUnknown(undefined, 'windows', 'no sensor')).toBe(
      'unknown on Windows: no sensor',
    );
    expect(orUnknown('5', 'windows')).toBe('5');
  });
});
