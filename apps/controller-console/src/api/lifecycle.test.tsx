// CRSP225: every state, policy, action, result and fleet state the contract allows has a word, and the Updates
// card, history and machine list render each one without throwing. The values are read from openapi.yaml.
import { UpdateHistory, FleetRows, UpdatesCard } from '@crsp/ui/lifecycle';
import { cleanup, render } from '@testing-library/react';
import { existsSync, readFileSync } from 'node:fs';
import { dirname, resolve } from 'node:path';
import { afterEach, describe, expect, it, vi } from 'vitest';
import {
  ACTION_FROM_WIRE,
  FLEET_FROM_WIRE,
  POLICY_FROM_WIRE,
  RESULT_FROM_WIRE,
  STATE_FROM_WIRE,
  fleetFromWire,
  historyFromWire,
  statusFromWire,
  type WireStatus,
} from './lifecycle';

afterEach(cleanup);

// vitest runs from apps/controller-console or from the repository root: walk up to the contract.
function findContract(): string {
  let dir = process.cwd();
  for (;;) {
    const f = resolve(dir, 'packages/contract/openapi.yaml');
    if (existsSync(f)) return f;
    const up = dirname(dir);
    if (up === dir) throw new Error('packages/contract/openapi.yaml not found');
    dir = up;
  }
}
const yaml = readFileSync(findContract(), 'utf8');

/** The `enum: [a, b]` list under `prop:` inside the schema `name` (flow style, one or many lines). */
function enumOf(schema: string, prop: string): string[] {
  const start = yaml.indexOf(`\n    ${schema}:\n`);
  expect(start).toBeGreaterThan(-1);
  const rest = yaml.slice(start + 1);
  const end = rest.search(/\n {4}[A-Za-z]/);
  const block = end < 0 ? rest : rest.slice(0, end);
  const m = new RegExp(
    `\\n {8}${prop}:\\n {10}type: string\\n {10}enum:\\s*\\[([^\\]]*)\\]`,
  ).exec(block);
  expect(m, `${schema}.${prop} enum`).not.toBeNull();
  return (m?.[1] ?? '')
    .split(',')
    .map((v) => v.trim())
    .filter(Boolean);
}

const wire = (state: string, policy = 'automatic_when_idle'): WireStatus =>
  ({
    state,
    current: '0.1.22',
    available: null,
    required: false,
    policy,
    window: null,
    next_check_at: null,
    failures: null,
    waiting_for_jobs: false,
  }) as WireStatus;

describe('lifecycle wire values', () => {
  it('has a mapping for every status state in the contract', () => {
    const states = enumOf('LifecycleStatus', 'state');
    expect(states.length).toBeGreaterThan(10);
    expect(Object.keys(STATE_FROM_WIRE).sort()).toEqual([...states].sort());
  });

  it('renders the Updates card for every status state and policy without throwing', () => {
    const warn = vi.spyOn(console, 'warn').mockImplementation(() => undefined);
    for (const policy of enumOf('LifecycleStatus', 'policy')) {
      expect(Object.keys(POLICY_FROM_WIRE)).toContain(policy);
      for (const state of enumOf('LifecycleStatus', 'state')) {
        const s = statusFromWire({
          ...wire(state, policy),
          available: '0.1.23',
          failures: { version: '0.1.23', count: 1, retry_at: null },
        });
        const { container, unmount } = render(
          <UpdatesCard
            policy={s.policy}
            state={s.state}
            current={s.current}
            available={s.available ?? undefined}
          />,
        );
        expect(container.textContent).not.toMatch(/state\.|undefined/);
        unmount();
      }
    }
    expect(warn).not.toHaveBeenCalled();
    warn.mockRestore();
  });

  it('maps the history actions and renders every action and result', () => {
    const actions = enumOf('UpdateHistoryRow', 'action');
    expect(Object.keys(ACTION_FROM_WIRE).sort()).toEqual([...actions].sort());
    const warn = vi.spyOn(console, 'warn').mockImplementation(() => undefined);
    const events = actions.flatMap((action) =>
      Object.keys(RESULT_FROM_WIRE).map((result) =>
        historyFromWire({
          at: '2026-11-22T08:00:00Z',
          actor: 'luis',
          action: action as never,
          version: '0.1.22',
          result,
        }),
      ),
    );
    const { container } = render(<UpdateHistory events={events} />);
    expect(container.textContent).not.toContain('audit.');
    expect(container.textContent).not.toContain('result.');
    expect(warn).not.toHaveBeenCalled();
    warn.mockRestore();
  });

  it('renders every fleet state', () => {
    const rows = Object.keys(FLEET_FROM_WIRE).map((state, i) =>
      fleetFromWire({ machine_id: `m${i}`, name: `m${i}`, version: '1', state }),
    );
    const { container } = render(
      <FleetRows
        rows={rows.map((r) => ({
          id: r.machine_id,
          machine: r.name,
          version: r.version,
          state: r.state,
        }))}
      />,
    );
    expect(container.textContent).not.toContain('fleet.');
  });

  it('survives an unknown server value with one warning', () => {
    const warn = vi.spyOn(console, 'warn').mockImplementation(() => undefined);
    expect(statusFromWire(wire('brand_new_state')).state).toBe('idle');
    statusFromWire(wire('brand_new_state'));
    expect(warn).toHaveBeenCalledTimes(1);
    expect(
      fleetFromWire({ machine_id: 'x', name: 'x', version: '1', state: '??' }).state,
    ).toBe('waiting');
    warn.mockRestore();
  });
});
