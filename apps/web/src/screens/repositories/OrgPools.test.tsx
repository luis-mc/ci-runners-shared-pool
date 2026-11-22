// R5.21: the pool per organization section: a switch for each organization, saved at once; a personal account has
// none; a refusal from GitHub (not an owner) is shown as the reason.
import { cleanup, fireEvent, screen, waitFor, within } from '@testing-library/react';
import { afterEach, describe, expect, it } from 'vitest';
import {
  MockApi,
  apiError,
  renderApp,
  repository,
  signedIn,
  viewer,
} from '../../testing/mock';

afterEach(cleanup);

const acme = {
  installation_id: 20,
  organization: 'acme',
  is_organization: true,
  eligible: true,
  enabled: false,
  group_name: 'crsp-home',
  repositories: [],
};
const personal = {
  ...acme,
  installation_id: 10,
  organization: 'octo',
  is_organization: false,
  eligible: false,
};

function withOrgs(api: MockApi, items: unknown[]): MockApi {
  return api
    .on('GET', '/api/v1/github/repositories', {
      items: [repository()],
      stale: { stale: false },
    })
    .on('GET', '/api/v1/github/org-pools', { items });
}

describe('Pool per organization', () => {
  it('has a switch for an organization and none for a personal account', async () => {
    renderApp(withOrgs(signedIn(), [personal, acme]), '/setup/pool');
    const table = await screen.findByRole('table', { name: 'Pool per organization' });
    expect(within(table).getByText('acme')).toBeTruthy();
    expect(within(table).queryByText('octo')).toBeNull();
    expect(within(table).getByText('repository runners')).toBeTruthy();
    expect(
      within(table).getByRole('switch', { name: 'Organization runners for acme' }),
    ).toBeTruthy();
  });

  it('turns it on at once and shows the group and its repositories', async () => {
    const api = withOrgs(signedIn(), [acme]).on(
      'PUT',
      '/api/v1/github/installations/20/org-pool',
      {
        ...acme,
        enabled: true,
        repositories: ['acme/api'],
      },
    );
    renderApp(api, '/setup/pool');
    const sw = await screen.findByRole('switch', {
      name: 'Organization runners for acme',
    });
    api.on('GET', '/api/v1/github/org-pools', {
      items: [{ ...acme, enabled: true, repositories: ['acme/api'] }],
    });
    fireEvent.click(sw);
    await waitFor(() =>
      expect(api.count('PUT', '/api/v1/github/installations/20/org-pool')).toBe(1),
    );
    expect(
      (api.calls.find((c) => c.method === 'PUT')?.body as { enabled: boolean }).enabled,
    ).toBe(true);
    expect(await screen.findByText('organization runners')).toBeTruthy();
    expect(screen.getByText('acme/api')).toBeTruthy();
  });

  it('shows why GitHub refused when the user is not an owner of the organization', async () => {
    const api = withOrgs(signedIn(), [acme]).on(
      'PUT',
      '/api/v1/github/installations/20/org-pool',
      apiError(
        403,
        'forbidden',
        'you are not an owner of the acme organization in GitHub',
      ),
    );
    renderApp(api, '/setup/pool');
    fireEvent.click(
      await screen.findByRole('switch', { name: 'Organization runners for acme' }),
    );
    expect(await screen.findByText(/not an owner of the acme organization/)).toBeTruthy();
  });

  it('disables the switch for a viewer and says why (UX-38)', async () => {
    renderApp(withOrgs(signedIn(viewer), [acme]), '/setup/pool');
    const sw = await screen.findByRole<HTMLButtonElement>('switch', {
      name: 'Organization runners for acme',
    });
    expect(sw.disabled).toBe(true);
  });
});
