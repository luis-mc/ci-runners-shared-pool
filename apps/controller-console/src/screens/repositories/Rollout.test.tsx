// Rollout pane on the repository page (operations R5.42; design system CORE-3, CORE-4, UX-13).
import { cleanup, fireEvent, screen, waitFor } from '@testing-library/react';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { renderApp, repository, signedIn } from '../../testing/mock';
import type { Schemas } from '../../api/api';
import { earlierSteps, nextStep } from './Rollout';

afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
});

const trial = (mode: Schemas['Trial']['mode']): Schemas['Trial'] => ({
  repository: 'octo/app',
  mode,
  variables: [],
  total: 0,
  hosted: { runs: 0, succeeded: 0, avg_wait_sec: 0, avg_run_sec: 0 },
  crsp: { runs: 0, succeeded: 0, avg_wait_sec: 0, avg_run_sec: 0 },
  jobs: [],
});

const page = (mode: Schemas['Trial']['mode']) => {
  const r = repository({ pool_on: true });
  const api = signedIn()
    .on('GET', '/api/v1/github/repositories', { items: [r], stale: { stale: false } })
    .on('GET', '/api/v1/repositories/:id', r)
    .on('GET', '/api/v1/repositories/:id/trial', trial(mode))
    .on('PUT', '/api/v1/repositories/:id/trial', trial('shadow'));
  renderApp(api, '/setup/pool/octo/app');
  return api;
};

const puts = (api: ReturnType<typeof page>) =>
  api.calls.filter((c) => c.method === 'PUT' && c.path.endsWith('/trial'));

describe('Rollout steps', () => {
  it('moves one step forward and back to any earlier step', () => {
    expect(nextStep('off')).toBe('observe');
    expect(nextStep('live')).toBeUndefined();
    expect(earlierSteps('canary')).toEqual(['off', 'observe', 'shadow']);
  });
});

describe('Rollout pane', () => {
  it('shows the step and offers only the next step and the way back', async () => {
    page('observe');
    expect(await screen.findByRole('button', { name: 'Go to Shadow' })).toBeTruthy();
    expect(screen.getByRole('button', { name: 'Back to Off' })).toBeTruthy();
    expect(screen.queryByRole('button', { name: 'Go to Canary' })).toBeNull();
    expect(screen.queryByRole('button', { name: 'Go to Live' })).toBeNull();
  });

  it('asks first and changes nothing until the person says yes', async () => {
    const api = page('observe');
    fireEvent.click(await screen.findByRole('button', { name: 'Go to Shadow' }));
    expect(await screen.findByText(/From Observe to Shadow/)).toBeTruthy();
    expect(puts(api).length).toBe(0);
    fireEvent.click(screen.getByRole('button', { name: 'Move' }));
    await waitFor(() => expect(puts(api).length).toBe(1));
    expect(puts(api)[0]?.body).toEqual({ mode: 'shadow', confirmed: true });
  });
});
