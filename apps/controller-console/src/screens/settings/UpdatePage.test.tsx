// TRAY4: the compact update page the tray opens in a small window. It asks first (Update now / Not now), then shows
// the stage of the update, survives the service restart and ends with "Updated to X". Its states: first run, ready
// to confirm, running, failed, done, done after reload.
import { cleanup, fireEvent, screen, waitFor, within } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { TIMING } from '../../timing';
import { renderApp, signedIn } from '../../testing/mock';

afterEach(() => {
  cleanup();
  vi.restoreAllMocks();
});
beforeEach(() => {
  TIMING.planPollMs = 10;
});

const status = (extra: object = {}) => ({
  current_version: '1.4.0',
  latest_version: '1.5.0',
  update_available: true,
  state: 'idle',
  auto_update: false,
  running_jobs: [{ repository: 'octo/app', workflow: 'ci', run_id: 7, job: 'test' }],
  drain_timeout_minutes: 60,
  ...extra,
});
const now = () => new Date().toISOString();
const stage = (name: string, extra: object = {}) => ({
  stage: { name, at: now(), version: '1.5.0', ...extra },
});

const steps = () =>
  Array.from(document.querySelectorAll<HTMLElement>('.ds-steps__item')).map((li) => ({
    name: (li.textContent ?? '').replace(/^[✓\d]+\s*/, '').trim(),
    standing: li.getAttribute('data-standing'),
  }));
/** Press Update now once the page has read the service (in the loading step the same button is disabled). */
const pressUpdateNow = async () => {
  await screen.findByText(/is ready\. Running jobs finish first/);
  fireEvent.click(screen.getByRole('button', { name: 'Update now' }));
};
const buttons = () =>
  Array.from(document.querySelectorAll<HTMLButtonElement>('.ds-action-bar button')).map(
    (b) => b.textContent?.trim(),
  );

describe('Update page (/update)', () => {
  it('first run: no release looked for yet, one primary that looks', async () => {
    renderApp(
      signedIn().on(
        'GET',
        '/api/v1/update',
        status({ latest_version: undefined, update_available: false }),
      ),
      '/update',
    );
    expect(await screen.findByText(/has not looked for a release yet/)).toBeTruthy();
    expect(buttons()).toContain('Look for updates');
  });

  it('asks first: the owner words, Update now and Not now, and nothing starts before the yes', async () => {
    const api = signedIn()
      .on('GET', '/api/v1/update', status())
      .on('POST', '/api/v1/update/apply', status({ state: 'waiting_for_jobs' }));
    renderApp(api, '/update');
    expect(
      await screen.findByText(
        'Version 1.5.0 is ready. Running jobs finish first; the service restarts (about 1 min).',
      ),
    ).toBeTruthy();
    expect(buttons()).toEqual(['Not now', 'Update now']);
    expect(api.count('POST', '/api/v1/update/apply')).toBe(0);
    // The step list: the review is current, the stages after it wait.
    const s = steps();
    expect(s.map((x) => x.name)).toEqual([
      'Review',
      'Downloading',
      'Waiting for jobs',
      'Installing',
      'Restarting',
      'Done',
    ]);
    expect(s.find((x) => x.name === 'Review')?.standing).toBe('now');
    expect(s.filter((x) => x.standing === 'later')).toHaveLength(5);
  });

  it('Not now starts nothing', async () => {
    const api = signedIn().on('GET', '/api/v1/update', status());
    const close = vi.spyOn(window, 'close').mockImplementation(() => undefined);
    renderApp(api, '/update');
    fireEvent.click(await screen.findByRole('button', { name: 'Not now' }));
    expect(close).toHaveBeenCalled();
    expect(api.count('POST', '/api/v1/update/apply')).toBe(0);
  });

  it('Update now sends the yes and shows the stage with its percent, Hide, and the safe line', async () => {
    let polls = 0;
    const api = signedIn()
      .on('GET', '/api/v1/update', () => {
        if (polls++ === 0) return status();
        return status({ state: 'downloading', ...stage('downloading', { percent: 42 }) });
      })
      .on('POST', '/api/v1/update/apply', () =>
        status({ state: 'downloading', ...stage('downloading', { percent: 0 }) }),
      );
    renderApp(api, '/update');
    await pressUpdateNow();
    await waitFor(() => expect(screen.getByRole('progressbar')).toBeTruthy());
    await waitFor(() =>
      expect(screen.getByRole('progressbar').getAttribute('aria-valuenow')).toBe('42'),
    );
    expect(screen.getAllByText(/Downloading 42%/).length).toBeGreaterThan(0);
    expect(screen.getByText('Running jobs are safe.')).toBeTruthy();
    expect(String(api.calls.find((c) => c.method === 'POST')?.query)).toBe(
      'confirmed=true',
    );
    // Later steps cannot be pressed; Hide is there, Stop is not (nothing here can be stopped).
    expect(steps().find((x) => x.name === 'Downloading')?.standing).toBe('now');
    const g = within(screen.getByRole('group', { name: 'Update' }));
    expect(g.getByRole('button', { name: 'Hide' })).toBeTruthy();
    expect(g.queryByRole('button', { name: 'Stop' })).toBeNull();
    for (const b of document.querySelectorAll<HTMLButtonElement>('.ds-steps__button')) {
      if (b.closest('[data-standing="later"]')) expect(b.disabled).toBe(true);
    }
    // The button that started the work says it works and is disabled.
    const busy = g.getByRole<HTMLButtonElement>('button', {
      name: /Update now|Working|Updating/,
    });
    expect(busy.disabled).toBe(true);
  });

  it('opened on a running update it follows without asking again', async () => {
    renderApp(
      signedIn().on(
        'GET',
        '/api/v1/update',
        status({ state: 'installing', ...stage('installing', { percent: 60 }) }),
      ),
      '/update',
    );
    await waitFor(() =>
      expect(screen.getByRole('progressbar').getAttribute('aria-valuenow')).toBe('60'),
    );
    expect(steps().find((x) => x.name === 'Installing')?.standing).toBe('now');
    expect(steps().find((x) => x.name === 'Downloading')?.standing).toBe('done');
  });

  it('waits for the service while it restarts and ends with Updated to X', async () => {
    let polls = 0;
    const api = signedIn()
      .on('GET', '/api/v1/update', () => {
        polls++;
        if (polls === 1) return status();
        if (polls === 2)
          return status({ state: 'installing', ...stage('installing', { percent: 30 }) });
        if (polls <= 4) throw new TypeError('Failed to fetch'); // the service is down for the install
        return status({
          current_version: '1.5.0',
          update_available: false,
          running_jobs: [],
          ...stage('done', { percent: 100 }),
        });
      })
      .on('POST', '/api/v1/update/apply', () =>
        status({ state: 'waiting_for_jobs', ...stage('waiting_for_jobs') }),
      );
    renderApp(api, '/update');
    await pressUpdateNow();
    expect(
      await screen.findByText('Updated to 1.5.0.', {}, { timeout: 3000 }),
    ).toBeTruthy();
    expect(buttons()).toContain('Close');
    expect(
      steps()
        .slice(0, -1)
        .every((x) => x.standing === 'done'),
    ).toBe(true);
  });

  it('failed: says what went wrong, where the log is, and offers Try again', async () => {
    renderApp(
      signedIn().on(
        'GET',
        '/api/v1/update',
        status({
          state: 'failed',
          error: 'the installer failed with exit code 1603',
          ...stage('failed', {
            failed_at: 'installing',
            error: 'the installer failed with exit code 1603',
            log_path: 'C:\\ProgramData\\crsp\\logs\\msi-update.log',
          }),
        }),
      ),
      '/update',
    );
    expect(await screen.findByText(/The update did not finish\./)).toBeTruthy();
    expect(screen.getByText(/exit code 1603/)).toBeTruthy();
    expect(
      screen.getByText(
        /The log is C:\\ProgramData\\crsp\\logs\\msi-update\.log\. Open the log folder from the tray menu/,
      ),
    ).toBeTruthy();
    expect(buttons()).toContain('Try again');
    // The step bar marks where it stopped.
    expect(steps().find((x) => x.standing === 'now')?.name).toBe('Installing');
  });

  it('done after reload: a finished update shows Updated to X and no Update now', async () => {
    renderApp(
      signedIn().on(
        'GET',
        '/api/v1/update',
        status({
          current_version: '1.5.0',
          update_available: false,
          running_jobs: [],
          ...stage('done', { percent: 100 }),
        }),
      ),
      '/update?started=1&to=1.5.0',
    );
    expect(await screen.findByText('Updated to 1.5.0.')).toBeTruthy();
    expect(buttons()).not.toContain('Update now');
  });

  it('a done stage from long ago is not shown as news', async () => {
    renderApp(
      signedIn().on(
        'GET',
        '/api/v1/update',
        status({
          current_version: '1.5.0',
          latest_version: '1.5.0',
          update_available: false,
          running_jobs: [],
          stage: { name: 'done', version: '1.5.0', at: '2026-01-01T00:00:00Z' },
        }),
      ),
      '/update',
    );
    expect(
      await screen.findByText(
        /Nothing changed: 1\.5\.0 is the newest stable release|Stopped\. Nothing changed/,
      ),
    ).toBeTruthy();
  });
});
