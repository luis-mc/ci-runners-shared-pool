// Settings, Updates, Import agent installers (operations R5.132): files, review, importing, done.
import { cleanup, fireEvent, screen, waitFor } from '@testing-library/react';
import { File as NodeFile } from 'node:buffer';
import { afterEach, describe, expect, it } from 'vitest';
import { MockApi, apiError, renderApp, signedIn, viewer } from '../../testing/mock';
import { listedNames, sendOrder } from './AgentInstallers';

afterEach(cleanup);

const hash = (c: string) => c.repeat(64);
const installer = (name: string, stored = false) => ({
  name,
  os: 'linux',
  arch: 'amd64',
  format: 'tar.gz',
  sha256: hash('a'),
  stored,
});
const list = (extra: Record<string, unknown> = {}) => ({
  version: '1.4.0',
  have_sums: false,
  from_release: false,
  installers: [],
  ...extra,
});
const NAME = 'CrspAgent-1.4.0-linux-x64.tar.gz';
const sumsText = `${hash('a')}  ${NAME}\n`;
const file = (name: string, body = 'x') => new NodeFile([body], name) as unknown as File;

const baseApi = (extra: Partial<Record<string, unknown>> = {}): MockApi =>
  signedIn().on('GET', '/api/v1/agent-installers', list(extra));

async function choose(files: File[]) {
  const input = await screen.findByLabelText<HTMLInputElement>('Release files');
  fireEvent.change(input, { target: { files } });
}
const press = async (label: string | RegExp) =>
  fireEvent.click(await screen.findByRole('button', { name: label }));
const PATH = '/settings/updates/agent-installers/import';

describe('sendOrder and listedNames', () => {
  it('puts SHA256SUMS first and the others by name', () => {
    expect(
      sendOrder([file('b.zip'), file('SHA256SUMS'), file('a.zip')]).map((f) => f.name),
    ).toEqual(['SHA256SUMS', 'a.zip', 'b.zip']);
  });
  it('reads the names a SHA256SUMS lists, binary marker or not', () => {
    const names = listedNames(
      `${hash('1')}  one.zip\r\n${hash('2')} *two.msi\nnot a line\n`,
    );
    expect([...names]).toEqual(['one.zip', 'two.msi']);
  });
});

describe('Import agent installers page', () => {
  it('starts on Files, with Review disabled and saying why until a file is chosen', async () => {
    renderApp(baseApi(), PATH);
    await screen.findByLabelText('Release files');
    const review = screen.getByRole<HTMLButtonElement>('button', { name: 'Next' });
    expect(review.disabled).toBe(true);
    expect(screen.getByText('Choose the release files first.')).toBeTruthy();
    await choose([file('SHA256SUMS', sumsText)]);
    await waitFor(() =>
      expect(
        screen.getByRole<HTMLButtonElement>('button', { name: 'Next' }).disabled,
      ).toBe(false),
    );
  });

  it('sends SHA256SUMS first, then each file as octet-stream, and ends at Done', async () => {
    const api = baseApi().on('PUT', '/api/v1/agent-installers/:name', (_c, p) =>
      list({ have_sums: true, installers: [installer(NAME, p.name === NAME)] }),
    );
    renderApp(api, PATH);
    await choose([file(NAME, 'binary'), file('SHA256SUMS', sumsText)]);
    await press('Next');
    await screen.findByText(/Nothing has changed yet/);
    await press('Import 2 files');
    await screen.findByText('2 files stored and checked against SHA256SUMS.');
    const puts = api.calls.filter((c) => c.method === 'PUT');
    expect(puts.map((c) => c.path)).toEqual([
      '/api/v1/agent-installers/SHA256SUMS',
      `/api/v1/agent-installers/${NAME}`,
    ]);
    expect(puts[0]?.body).toBe(sumsText);
    expect(puts[1]?.body).toBe('binary');
    expect(screen.getByRole('button', { name: 'Open Updates' })).toBeTruthy();
  });

  it('warns on Review about a file SHA256SUMS does not list', async () => {
    renderApp(baseApi(), PATH);
    await choose([
      file('SHA256SUMS', sumsText),
      file('CrspAgent-1.4.0-linux-arm64.tar.gz'),
    ]);
    await press('Next');
    await screen.findByText(
      /SHA256SUMS does not list CrspAgent-1.4.0-linux-arm64.tar.gz/,
    );
  });

  it('names a refused file, keeps going, and ends Failed with Try again', async () => {
    const api = baseApi().on('PUT', '/api/v1/agent-installers/:name', (_c, p) =>
      p.name === NAME
        ? apiError(400, 'invalid', 'The file does not match its SHA256SUMS line.')
        : list({ have_sums: true }),
    );
    renderApp(api, PATH);
    await choose([file('SHA256SUMS', sumsText), file(NAME)]);
    await press('Next');
    await press('Import 2 files');
    await screen.findByText(/1 of 2 files were not stored/);
    expect(
      screen.getByText(`${NAME}: The file does not match its SHA256SUMS line.`),
    ).toBeTruthy();
    expect(screen.getByRole('button', { name: 'Try again' })).toBeTruthy();
  });

  it('Stop aborts the file in flight and ends Stopped', async () => {
    const api = baseApi().on(
      'PUT',
      '/api/v1/agent-installers/:name',
      () => new Promise<Response>(() => undefined),
    );
    renderApp(api, PATH);
    await choose([file('SHA256SUMS', sumsText), file(NAME)]);
    await press('Next');
    await press('Import 2 files');
    await screen.findByText('Sending 1 of 2: SHA256SUMS');
    fireEvent.click(await screen.findByRole('button', { name: 'Stop' }));
    await screen.findByText(/Stopped\. 0 of 2 files were stored; the rest were not sent/);
    expect(api.calls.filter((c) => c.method === 'PUT')).toHaveLength(1);
    expect(screen.getByRole('button', { name: 'Start again' })).toBeTruthy();
  });

  it('a viewer sees the import button disabled with the reason', async () => {
    renderApp(signedIn(viewer).on('GET', '/api/v1/agent-installers', list()), PATH);
    await choose([file('SHA256SUMS', sumsText)]);
    await press('Next');
    const button = await screen.findByRole<HTMLButtonElement>('button', {
      name: 'Import 1 file',
    });
    expect(button.disabled).toBe(true);
    expect(button.getAttribute('aria-describedby')).toBeTruthy();
  });

  it('after a reload mid-import it says Paused; with everything stored it says Done', async () => {
    renderApp(
      baseApi({ have_sums: true, installers: [installer(NAME, false)] }),
      `${PATH}?started=1`,
    );
    await screen.findByText(/Paused\. The page closed/);
    cleanup();
    renderApp(
      baseApi({ have_sums: true, installers: [installer(NAME, true)] }),
      `${PATH}?started=1`,
    );
    await screen.findByText('The agent installers are stored.');
  });

  it('the Updates tab offers the import', async () => {
    renderApp(
      signedIn()
        .on('GET', '/api/v1/update/status', {
          state: 'up_to_date',
          current: '1.4.0',
          available: null,
          required: false,
          policy: 'automatic_when_idle',
          window: null,
        })
        .on('GET', '/api/v1/update/fleet', () => [])
        .on('GET', '/api/v1/agent-installers', list()),
      '/settings/updates',
    );
    await press('Import agent installers');
    await screen.findByLabelText('Release files');
  });
});
