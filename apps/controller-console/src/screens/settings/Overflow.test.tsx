// github-integration R5.114 (CRSP281): the owner sets the cloud overflow priority rules.
import { cleanup, fireEvent, screen, waitFor } from '@testing-library/react';
import { afterEach, describe, expect, it } from 'vitest';
import { renderApp, signedIn } from '../../testing/mock';
import { completeTargets, overflowProblems } from './Overflow';

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
