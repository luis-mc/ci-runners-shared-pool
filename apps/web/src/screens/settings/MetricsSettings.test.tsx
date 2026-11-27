// observability R5.40 to R5.47: Settings > Console > Metrics. Off by default, copy-paste scrape text, a key shown once.
import { cleanup, fireEvent, screen, waitFor } from '@testing-library/react';
import { afterEach, describe, expect, it } from 'vitest';
import { renderApp, signedIn, viewer } from '../../testing/mock';
import { metricsProblems } from './MetricsSettings';

afterEach(cleanup);

const metrics = (extra: object = {}) => ({
  enabled: false,
  port: 9464,
  expose: false,
  key_required: false,
  push_enabled: false,
  push_endpoint: '',
  push_seconds: 60,
  key_made: false,
  running: false,
  pushing: false,
  scrape_config: 'scrape_configs:\n  - job_name: crsp\n',
  ...extra,
});

describe('Settings > Console > Metrics', () => {
  it('is off by default and shows no scrape text', async () => {
    renderApp(
      signedIn().on('GET', '/api/v1/settings/metrics', metrics()),
      '/settings/console',
    );
    expect(await screen.findByText('Metrics')).toBeTruthy();
    expect(
      await screen.findByRole<HTMLInputElement>('checkbox', {
        name: /Prometheus page/,
      }),
    ).toHaveProperty('checked', false);
    expect(screen.queryByLabelText('Prometheus scrape configuration')).toBeNull();
  });

  it('shows the scrape text when the page is on', async () => {
    renderApp(
      signedIn().on(
        'GET',
        '/api/v1/settings/metrics',
        metrics({ enabled: true, running: true, address: '127.0.0.1:9464' }),
      ),
      '/settings/console',
    );
    expect(await screen.findByLabelText('Prometheus scrape configuration')).toBeTruthy();
    expect(screen.getByRole('button', { name: 'Copy' })).toBeTruthy();
    expect(screen.getByText(/Now on 127\.0\.0\.1:9464/)).toBeTruthy();
  });

  it('shows the key once after Make a key', async () => {
    const a = signedIn()
      .on('GET', '/api/v1/settings/metrics', metrics({ enabled: true }))
      .on('POST', '/api/v1/settings/metrics/token', () => ({ key: 'abc123key' }));
    renderApp(a, '/settings/console');
    fireEvent.click(await screen.findByRole('button', { name: 'Make a key' }));
    expect(await screen.findByLabelText('The metrics key')).toBeTruthy();
    await waitFor(() => expect(screen.getByText('abc123key')).toBeTruthy());
  });

  it('turns a switch on and saves with the tab Save', async () => {
    const a = signedIn()
      .on('GET', '/api/v1/settings/metrics', metrics())
      .on('PUT', '/api/v1/settings/metrics', (c) => metrics(c.body as object));
    renderApp(a, '/settings/console');
    fireEvent.click(await screen.findByRole('checkbox', { name: /Prometheus page/ }));
    fireEvent.click(await screen.findByRole('button', { name: /^Save/ }));
    await waitFor(() =>
      expect(a.calls.find((c) => c.method === 'PUT')?.body).toMatchObject({
        enabled: true,
        expose: false,
        port: 9464,
      }),
    );
  });

  it('keeps the switches off for a viewer, with the reason', async () => {
    renderApp(
      signedIn(viewer).on('GET', '/api/v1/settings/metrics', metrics()),
      '/settings/console',
    );
    const box = await screen.findByRole<HTMLInputElement>('checkbox', {
      name: /Prometheus page/,
    });
    expect(box.disabled).toBe(true);
  });
});

describe('metricsProblems', () => {
  const ok = {
    enabled: true,
    port: 9464,
    expose: false,
    key_required: false,
    push_enabled: false,
    push_endpoint: '',
    push_seconds: 60,
  };
  it('accepts the defaults', () => expect(metricsProblems(ok, false)).toEqual([]));
  it('names each mistake', () => {
    expect(
      metricsProblems(
        {
          ...ok,
          port: 80,
          push_enabled: true,
          push_endpoint: 'x',
          push_seconds: 1,
          key_required: true,
        },
        false,
      ),
    ).toHaveLength(4);
  });
});
