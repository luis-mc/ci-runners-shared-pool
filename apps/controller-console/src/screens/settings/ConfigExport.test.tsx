// Config export (web-console R5.29, operations R5.26): admin only, a YAML file with no secret.
import { cleanup, fireEvent, screen, waitFor } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { renderApp, signedIn, viewer } from '../../testing/mock';
import { section } from './testSection';

afterEach(() => {
  cleanup();
  vi.restoreAllMocks();
});

let saved: { name: string; text: Promise<string> }[] = [];

beforeEach(() => {
  saved = [];
  URL.createObjectURL = (b: Blob) => {
    saved.push({ name: '', text: b.text() });
    return 'blob:x';
  };
  URL.revokeObjectURL = () => undefined;
  vi.spyOn(HTMLAnchorElement.prototype, 'click').mockImplementation(function (
    this: HTMLAnchorElement,
  ) {
    const last = saved[saved.length - 1];
    if (last) last.name = this.download;
  });
});

describe('Config export (R5.29)', () => {
  it('downloads the file named for what it holds, then the stamp', async () => {
    const yaml =
      'product: crsp\nsecrets:\n  - name: GitHub App private key\n    state: set\n';
    const a = signedIn().on(
      'GET',
      '/api/v1/exports/config',
      () =>
        new Response(yaml, {
          headers: {
            'Content-Type': 'application/yaml',
            'Content-Disposition':
              'attachment; filename="crsp-config-2026-11-14-1005.yaml"',
          },
        }),
    );
    renderApp(a, '/settings/export');
    fireEvent.click(await screen.findByRole('button', { name: 'Export config' }));
    await waitFor(() => expect(saved).toHaveLength(1));
    expect(saved[0]?.name).toBe('crsp-config-2026-11-14-1005.yaml');
    expect(await saved[0]?.text).toBe(yaml);
    expect(screen.getByText(/It holds no secret/)).toBeTruthy();
  });

  it('a viewer sees Export config disabled with the reason', async () => {
    renderApp(signedIn(viewer), '/settings/export');
    const button = await screen.findByRole('button', { name: 'Export config' });
    expect(button.hasAttribute('disabled')).toBe(true);
    expect(
      section('config-export').getByText(/admin/i, { selector: '.ds-button-reason' }),
    ).toBeTruthy();
  });
});
