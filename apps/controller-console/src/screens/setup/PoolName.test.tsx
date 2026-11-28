// Runners R5.141 to R5.145: name the pool at the first setup, rename it later. Review says what changes; the run
// can be stopped; stopped, failed and done show what was done and what is left; done after a reload reads the
// stored outcome.
import { cleanup, fireEvent, screen, waitFor } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it } from 'vitest';
import { renderApp, signedIn } from '../../testing/mock';
import { TIMING } from '../../timing';
import { nameProblem } from './PoolName';

afterEach(cleanup);
beforeEach(() => {
  TIMING.planPollMs = 10;
});

const step = (id: string, text: string, state = 'pending', error?: string) => ({
  id,
  kind: id.split(':')[0] ?? id,
  text,
  state,
  ...(error ? { error } : {}),
});

const A = 'Add the label crsp-lab to runner box-1';
const S = 'Store the new name crsp-lab';

const rename = (
  state: string,
  steps = [step('add_label:r1', A), step('store_name', S)],
) => ({
  id: 'pr1',
  old: 'crsp-home',
  new: 'crsp-lab',
  state,
  steps,
  counts: { runners: 1, registered: 1, ephemeral: 0, repositories: 2, scale_set: false },
  created_at: '2026-11-28T10:00:00Z',
  updated_at: '2026-11-28T10:00:00Z',
});

const info = (over: object = {}) => ({
  name: 'crsp-home',
  source: 'default',
  env_override: false,
  ...over,
});

async function toReview(api: ReturnType<typeof signedIn>) {
  renderApp(api, '/setup/pool/rename');
  fireEvent.change(await screen.findByLabelText('Pool name'), {
    target: { value: 'crsp-lab' },
  });
  fireEvent.click(screen.getByRole('button', { name: 'Next' }));
  return screen.findByRole('button', { name: 'Rename the pool' });
}

describe('name rules', () => {
  it('say what is wrong in plain words', () => {
    expect(nameProblem('crsp-home')).toBeUndefined();
    expect(nameProblem('')).toBe('Give the pool a name.');
    expect(nameProblem('home')).toMatch(/starts with crsp-/);
    expect(nameProblem('crsp-Home')).toMatch(/starts with crsp-/);
    expect(nameProblem('crsp-' + 'a'.repeat(40))).toMatch(/at most 40/);
  });
});

describe('Rename the pool', () => {
  it('asks for the name, shows what changes, and changes nothing before the yes', async () => {
    const api = signedIn()
      .on('GET', '/api/v1/pool/name', info({ source: 'stored' }))
      .on('POST', '/api/v1/pool/renames', rename('planned'));
    renderApp(api, '/setup/pool/rename');
    const field = await screen.findByLabelText('Pool name');
    expect((field as HTMLInputElement).value).toBe('crsp-home');
    fireEvent.change(field, { target: { value: 'Bad' } });
    fireEvent.click(screen.getByRole('button', { name: 'Next' }));
    expect(await screen.findByText(/starts with crsp-/)).toBeTruthy();
    expect(api.count('POST', '/api/v1/pool/renames')).toBe(0);
    fireEvent.change(field, { target: { value: 'crsp-lab' } });
    fireEvent.click(screen.getByRole('button', { name: 'Next' }));
    expect(
      await screen.findByText(/still finds a runner until the last step/),
    ).toBeTruthy();
    expect(screen.getByText(A)).toBeTruthy();
    expect(api.count('POST', '/api/v1/pool/renames/pr1/apply')).toBe(0);
  });

  it('runs to Done, then Done after a reload reads the stored outcome', async () => {
    let reads = 0;
    const api = signedIn()
      .on('GET', '/api/v1/pool/name', info({ source: 'stored' }))
      .on('POST', '/api/v1/pool/renames', rename('planned'))
      .on('POST', '/api/v1/pool/renames/:id/apply', rename('applying'))
      .on('GET', '/api/v1/pool/renames/:id', () =>
        ++reads < 2
          ? rename('applying', [
              step('add_label:r1', A, 'running'),
              step('store_name', S),
            ])
          : rename('done', [
              step('add_label:r1', A, 'done'),
              step('store_name', S, 'done'),
            ]),
      );
    fireEvent.click(await toReview(api));
    expect(await screen.findByText('The pool is called crsp-lab.')).toBeTruthy();
    cleanup();
    const again = signedIn()
      .on('GET', '/api/v1/pool/name', info({ name: 'crsp-lab', source: 'stored' }))
      .on(
        'GET',
        '/api/v1/pool/renames/:id',
        rename('done', [step('store_name', S, 'done')]),
      );
    renderApp(again, '/setup/pool/rename?rename=pr1');
    expect(await screen.findByText('The pool is called crsp-lab.')).toBeTruthy();
    expect(again.count('POST', '/api/v1/pool/renames')).toBe(0);
  });

  it('shows Stop while it runs and what is left when it stopped; Run again resumes', async () => {
    let stopAsked = false;
    const api = signedIn()
      .on('GET', '/api/v1/pool/name', info({ source: 'stored' }))
      .on('POST', '/api/v1/pool/renames', rename('planned'))
      .on('POST', '/api/v1/pool/renames/:id/apply', rename('applying'))
      .on('POST', '/api/v1/pool/renames/:id/stop', () => {
        stopAsked = true;
        return rename('applying');
      })
      .on('GET', '/api/v1/pool/renames/:id', () =>
        stopAsked
          ? rename('stopped', [
              step('add_label:r1', A, 'done'),
              step('store_name', S, 'skipped'),
            ])
          : rename('applying'),
      );
    fireEvent.click(await toReview(api));
    fireEvent.click(await screen.findByRole('button', { name: 'Stop' }));
    expect(await screen.findByText(/1 step was done and 1 is left/)).toBeTruthy();
    expect(screen.getByRole('button', { name: 'Run again' })).toBeTruthy();
    stopAsked = false;
    api.on(
      'GET',
      '/api/v1/pool/renames/:id',
      rename('done', [step('add_label:r1', A, 'done'), step('store_name', S, 'done')]),
    );
    fireEvent.click(screen.getByRole('button', { name: 'Run again' }));
    await waitFor(() => expect(api.count('POST', '/api/v1/pool/renames')).toBe(2));
    expect(await screen.findByText('The pool is called crsp-lab.')).toBeTruthy();
  });

  it('shows a failed step with its reason and the count left', async () => {
    const api = signedIn()
      .on('GET', '/api/v1/pool/name', info({ source: 'stored' }))
      .on(
        'GET',
        '/api/v1/pool/renames/:id',
        rename('failed', [
          step('add_label:r1', A, 'done'),
          step('store_name', S, 'failed', 'GitHub could not be reached'),
          step('finish', 'Finish', 'skipped'),
        ]),
      );
    renderApp(api, '/setup/pool/rename?rename=pr1');
    expect(await screen.findByText(/1 step failed/)).toBeTruthy();
    expect(screen.getByText(/1 done, 1 left/)).toBeTruthy();
    expect(screen.getAllByText(/GitHub could not be reached/).length).toBeGreaterThan(0);
  });

  it('says the service wins when CRSP_POOL_NAME sets the name', async () => {
    const api = signedIn().on(
      'GET',
      '/api/v1/pool/name',
      info({ source: 'env', env_override: true }),
    );
    renderApp(api, '/setup/pool/rename');
    expect(await screen.findByText(/CRSP_POOL_NAME, which wins/)).toBeTruthy();
  });
});

describe('First setup', () => {
  it('asks for the pool name with crsp-home filled in, between Connect GitHub and the first machine', async () => {
    const api = signedIn()
      .on('GET', '/api/v1/setup', {
        has_app: true,
        has_owner: true,
        setup_link_redeemed: true,
        steps: {
          connect_github: 'done',
          first_machine: 'todo',
          first_repository: 'todo',
        },
      })
      .on('GET', '/api/v1/pool/name', info())
      .on('PUT', '/api/v1/pool/name', info({ source: 'stored' }));
    renderApp(api, '/setup?wizard=1');
    const field = await screen.findByLabelText('Pool name');
    expect((field as HTMLInputElement).value).toBe('crsp-home');
    fireEvent.click(screen.getByRole('button', { name: 'Next' }));
    fireEvent.click(await screen.findByRole('button', { name: 'Name the pool' }));
    await waitFor(() => expect(api.count('PUT', '/api/v1/pool/name')).toBe(1));
    expect(
      (api.calls.find((c) => c.method === 'PUT')?.body as { name: string }).name,
    ).toBe('crsp-home');
  });
});
