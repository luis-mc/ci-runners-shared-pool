// github-integration R5.114 (CRSP281): the owner sets the cloud overflow priority rules.
import { cleanup, fireEvent, screen, waitFor } from '@testing-library/react';
import { afterEach, describe, expect, it } from 'vitest';
import { renderApp, signedIn } from '../../testing/mock';
import { budgetWords, completeTargets, overflowProblems, whereNow } from './Overflow';

afterEach(cleanup);

describe('cloud overflow words and checks', () => {
  it('lists both targets, the saved order first', () => {
    const t = completeTargets([{ kind: 'aws_ec2', enabled: true, label: 'ec2-x' }]);
    expect(t.map((x) => x.kind)).toEqual(['aws_ec2', 'github_hosted']);
    expect(t[1]?.enabled).toBe(false);
  });

  it('refuses a label with a space and an AWS EC2 target with no label', () => {
    const t = completeTargets([{ kind: 'aws_ec2', enabled: true, label: '' }]);
    expect(overflowProblems(t)).toEqual([
      'AWS EC2 runners need a label while they are on.',
    ]);
    const bad = completeTargets([{ kind: 'github_hosted', enabled: true, label: 'a b' }]);
    expect(overflowProblems(bad)[0]).toMatch(/must be one label/);
  });
});

describe('cloud overflow settings', () => {
  it('saves the order and the label with the tab Save', async () => {
    const a = signedIn()
      .on('GET', '/api/v1/settings', {})
      .on('PATCH', '/api/v1/settings', (c) => c.body);
    renderApp(a, '/settings/repositories');
    await screen.findByRole('heading', { name: 'Cloud overflow' });
    fireEvent.click(await screen.findByRole('checkbox', { name: /Use AWS EC2 runners/ }));
    fireEvent.change(screen.getByLabelText(/Label of AWS EC2 runners/), {
      target: { value: 'ec2-pool-4x' },
    });
    fireEvent.click(screen.getByRole('button', { name: 'Move AWS EC2 runners up' }));
    expect(await screen.findByText(/Unsaved changes/)).toBeTruthy();
    fireEvent.click(screen.getByRole('button', { name: 'Save' }));
    await waitFor(() => expect(a.count('PATCH', '/api/v1/settings')).toBe(1));
    const body = a.calls.find((c) => c.method === 'PATCH')?.body as {
      overflow_targets: {
        kind: string;
        enabled: boolean;
        label: string;
        cap_minutes: number;
      }[];
    };
    expect(body.overflow_targets).toMatchObject([
      { kind: 'aws_ec2', enabled: true, label: 'ec2-pool-4x', cap_minutes: 0 },
      { kind: 'github_hosted', enabled: false, cap_minutes: 0 },
    ]);
    expect(await screen.findByText('Cloud overflow saved.')).toBeTruthy();
  });

  it('keeps Save off and says why when AWS EC2 is on without a label', async () => {
    const a = signedIn().on('GET', '/api/v1/settings', {});
    renderApp(a, '/settings/repositories');
    fireEvent.click(await screen.findByRole('checkbox', { name: /Use AWS EC2 runners/ }));
    fireEvent.click(await screen.findByRole('button', { name: 'Save' }));
    expect(
      await screen.findByText('AWS EC2 runners need a label while they are on.'),
    ).toBeTruthy();
    expect(a.count('PATCH', '/api/v1/settings')).toBe(0);
  });
});

describe('cloud overflow now and minutes used (R5.115)', () => {
  const repo = (full: string, routing: object) =>
    ({ id: 1, full_name: full, routing }) as never;

  it('says where each repository on overflow goes', () => {
    expect(
      whereNow(repo('a/b', { where: 'crsp', why: 'machine_up', used_minutes: 0 })),
    ).toBeUndefined();
    expect(
      whereNow(
        repo('a/b', {
          where: 'hosted',
          why: 'fallback_on',
          used_minutes: 1,
          target: 'aws_ec2',
        }),
      )?.word,
    ).toBe('AWS EC2 runners');
    expect(
      whereNow(repo('a/b', { where: 'waiting', why: 'cap_reached', used_minutes: 1 }))
        ?.word,
    ).toBe('Waiting, minutes used up');
  });

  it('shows the repositories on overflow, the minutes used and the total limit', async () => {
    const a = signedIn()
      .on('GET', '/api/v1/settings', {
        overflow_targets: [
          {
            kind: 'github_hosted',
            enabled: true,
            total_cap_minutes: 500,
            cap_minutes: 100,
            budget_usd: 20,
          },
        ],
        overflow_usage: [
          { kind: 'github_hosted', used_minutes: 42, used_usd: 3.36, priced: true },
        ],
      })
      .on('GET', '/api/v1/github/repositories', {
        items: [
          repo('acme/api', {
            where: 'hosted',
            why: 'fallback_on',
            used_minutes: 3,
            target: 'github_hosted',
          }),
          {
            id: 2,
            full_name: 'acme/web',
            routing: { where: 'crsp', why: 'machine_up', used_minutes: 0 },
          },
        ],
        stale: { stale: false },
      });
    renderApp(a, '/settings/repositories');
    expect(await screen.findByText('acme/api')).toBeTruthy();
    expect(screen.queryByText('acme/web')).toBeNull();
    expect(await screen.findByText('42 min')).toBeTruthy();
    expect(screen.getAllByText('500 min').length).toBeGreaterThan(0);
    expect(screen.getByLabelText(/Total minutes of GitHub-hosted runners/)).toBeTruthy();
    expect(await screen.findByText('$3.36')).toBeTruthy();
    expect(screen.getByText('$20.00')).toBeTruthy();
    expect(screen.getByLabelText(/Monthly budget of GitHub-hosted runners/)).toBeTruthy();
  });

  it('says a target with no rate does not enforce its budget', () => {
    const t = completeTargets([
      { kind: 'aws_ec2', enabled: true, label: 'x', budget_usd: 9 },
    ]);
    expect(
      budgetWords(t[0] as never, { kind: 'aws_ec2', used_minutes: 0, priced: false }),
    ).toBe('$9.00, no rate, budget not enforced');
    expect(budgetWords({ kind: 'github_hosted', enabled: true }, undefined)).toBe(
      'No budget',
    );
  });
});
