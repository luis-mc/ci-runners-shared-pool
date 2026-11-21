// Job sizes pane on the repository page (scheduling R5.79 to R5.87; design system CORE-2, UX-22, UX-25, UX-27).
import { cleanup, fireEvent, screen, waitFor, within } from '@testing-library/react';
import { afterEach, describe, expect, it, vi } from 'vitest';
import {
  renderApp,
  repository,
  signedIn,
  viewer,
  type MockApi,
} from '../../testing/mock';
import type { Schemas } from '../../api/api';

afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
});

const row = (over: Partial<Schemas['JobSizeRow']> = {}): Schemas['JobSizeRow'] => ({
  repository: 'octo/app',
  job: 'build',
  class: 'normal',
  source: 'default',
  weight: { cpu: 2, memory: 4 },
  reasons: [],
  services: [],
  override_console: '',
  override_file: '',
  reset_note: '',
  copy_line: 'build: heavy',
  ...over,
});

const sizes = (
  items: Schemas['JobSizeRow'][],
  extra: Partial<Schemas['JobSizeList']> = {},
  api: MockApi = signedIn(),
) => {
  const r = repository();
  return api
    .on('GET', '/api/v1/github/repositories', { items: [r], stale: { stale: false } })
    .on('GET', '/api/v1/repositories/:id', r)
    .on('PUT', '/api/v1/job-sizes', {
      repository: 'octo/app',
      pattern: 'build',
      class: 'heavy',
      copy_line: 'build: heavy',
    })
    .on('DELETE', '/api/v1/job-sizes', undefined)
    .on('GET', '/api/v1/job-sizes', {
      repository: 'octo/app',
      items,
      cost_table: [],
      file_error: '',
      ...extra,
    });
};

const open = async (api: MockApi) => {
  renderApp(api, '/repositories/octo/app');
  return screen.findByRole('table', { name: 'Job sizes' });
};

describe('Job sizes', () => {
  it('shows a row for each source in words', async () => {
    const table = await open(
      sizes([
        row({ job: 'a', source: 'override', class: 'heavy' }),
        row({ job: 'b', source: 'measured' }),
        row({ job: 'c', source: 'scan', confidence: 'low' }),
        row({ job: 'd', source: 'default' }),
      ]),
    );
    expect(within(table).getByText('Override')).toBeTruthy();
    expect(within(table).getByText('Measured')).toBeTruthy();
    expect(within(table).getByText('Scan, low confidence')).toBeTruthy();
    expect(within(table).getByText('Default')).toBeTruthy();
    expect(within(table).getAllByText('CPU 2, memory 4').length).toBeGreaterThan(0);
  });

  it('shows the file and console overrides side by side and the reset note', async () => {
    const table = await open(
      sizes([
        row({
          override_file: 'light',
          override_console: 'heavy',
          reset_note:
            'The test count moved more than twice, so the size went back to the scan.',
        }),
      ]),
    );
    expect(within(table).getAllByText('Light').length).toBeGreaterThan(0);
    expect(within(table).getAllByText('Heavy').length).toBeGreaterThan(0);
    expect(within(table).getByText(/went back to the scan/)).toBeTruthy();
  });

  it('saves an override at once and refreshes', async () => {
    const api = sizes([row()]);
    await open(api);
    fireEvent.click(screen.getByRole('button', { name: 'Heavy' }));
    await waitFor(() => expect(api.count('PUT', '/api/v1/job-sizes')).toBe(1));
    expect(api.calls.find((c) => c.method === 'PUT')?.body).toEqual({
      repository: 'octo/app',
      pattern: 'build',
      class: 'heavy',
    });
    await waitFor(() => expect(api.count('GET', '/api/v1/job-sizes')).toBe(2));
  });

  it('removes an override', async () => {
    const api = sizes([row({ override_console: 'heavy' })]);
    await open(api);
    fireEvent.click(screen.getByRole('button', { name: 'No override' }));
    await waitFor(() => expect(api.count('DELETE', '/api/v1/job-sizes')).toBe(1));
    const call = api.calls.find((c) => c.method === 'DELETE');
    expect(call?.query.get('pattern')).toBe('build');
    expect(call?.query.get('repository')).toBe('octo/app');
  });

  it('gives a viewer no field and says why', async () => {
    await open(sizes([row()], {}, signedIn(viewer)));
    expect(screen.queryByRole('group', { name: 'Size of build' })).toBeNull();
  });

  it('copies the line to the clipboard and says so', async () => {
    const writeText = vi.fn().mockResolvedValue(undefined);
    vi.stubGlobal('navigator', { ...navigator, clipboard: { writeText } });
    await open(sizes([row()]));
    fireEvent.click(screen.getByRole('button', { name: 'Copy line' }));
    expect(writeText).toHaveBeenCalledWith('build: heavy');
    expect(await screen.findByText(/Copied the line for build/)).toBeTruthy();
  });

  it('says when the copy failed', async () => {
    const writeText = vi.fn().mockRejectedValue(new Error('no'));
    vi.stubGlobal('navigator', { ...navigator, clipboard: { writeText } });
    await open(sizes([row()]));
    fireEvent.click(screen.getByRole('button', { name: 'Copy line' }));
    expect(await screen.findByText(/Could not copy/)).toBeTruthy();
  });

  it('shows the file error', async () => {
    await open(sizes([row()], { file_error: 'line 3 is not valid' }));
    expect(
      await screen.findByText(/\.crsp\.yml could not be read: line 3 is not valid/),
    ).toBeTruthy();
  });

  it('shows an empty sentence', async () => {
    renderApp(sizes([]), '/repositories/octo/app');
    expect(
      await screen.findByText(/No jobs are known for this repository yet/),
    ).toBeTruthy();
  });

  it('shows the cost table with its assumption note', async () => {
    renderApp(
      sizes([row()], {
        cost_table: [
          {
            kind: 'framework',
            key: 'playwright',
            class: 'heavy',
            cpu: 4,
            gib: 8,
            assumption: true,
          },
        ],
      }),
      '/repositories/octo/app',
    );
    const t = await screen.findByRole('table', { name: 'Cost table' });
    expect(within(t).getByText('playwright')).toBeTruthy();
    expect(within(t).getByText('Assumption')).toBeTruthy();
    expect(
      screen.getByText(/Every row is an assumption until measured sizes replace it/),
    ).toBeTruthy();
  });

  it('shows loading first', () => {
    renderApp(sizes([row()]), '/repositories/octo/app');
    expect(screen.queryByRole('table', { name: 'Job sizes' })).toBeNull();
  });
});
