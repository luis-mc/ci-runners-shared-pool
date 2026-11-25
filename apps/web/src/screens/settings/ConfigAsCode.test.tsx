// Config as code (operations R5.28 to R5.31): export, plan, apply through the wizard. Plan changes nothing; each
// removal needs its own yes; a stale plan says so; a viewer sees the buttons disabled with the reason.
import { cleanup, fireEvent, screen, waitFor } from '@testing-library/react';
import { afterEach, describe, expect, it } from 'vitest';
import { apiError, renderApp, signedIn, viewer } from '../../testing/mock';
import { changeWords } from './ConfigAsCode';
import { section } from './testSection';

afterEach(cleanup);

const FILE = 'format: crsp-config\nversion: 1\npool:\n  budget_memory_pct: 70\n';

const PLAN = {
  id: 'plan-abc',
  created_at: '2026-11-25T10:00:00Z',
  changes: [
    {
      section: 'pool',
      name: '',
      kind: 'changed',
      fields: [{ field: 'budget_memory_pct', old: 85, new: 70 }],
    },
    { section: 'notifications', name: 'pager', kind: 'removed' },
  ],
  removals: ['notifications/pager'],
};

function choose(text: string) {
  const input = screen.getByLabelText<HTMLInputElement>('Config file');
  fireEvent.change(input, {
    target: { files: [new File([text], 'setup.yaml', { type: 'application/yaml' })] },
  });
}

async function openWizard(a: ReturnType<typeof signedIn>) {
  renderApp(a, '/settings/data');
  await screen.findByRole('button', { name: 'Plan the file' });
}

describe('Config as code (R5.28 to R5.31)', () => {
  it('says each change in words', () => {
    expect(changeWords(PLAN.changes[0] as never)).toBe(
      'Change Pool defaults: budget_memory_pct from 85 to 70',
    );
    expect(changeWords(PLAN.changes[1] as never)).toBe(
      'Remove Notification channel pager',
    );
  });

  it('plans first, needs a yes for the removal, then applies and says what was done', async () => {
    const a = signedIn()
      .on('POST', '/api/v1/config/plans', PLAN)
      .on('POST', '/api/v1/config/plans/plan-abc/apply', {
        plan_id: 'plan-abc',
        applied: PLAN.changes,
      });
    await openWizard(a);
    expect(
      screen.getByRole('button', { name: 'Plan the file' }).hasAttribute('disabled'),
    ).toBe(true);
    choose(FILE);
    fireEvent.click(screen.getByRole('button', { name: 'Plan the file' }));
    expect(await screen.findByText(/2 changes will be made/)).toBeTruthy();
    expect(a.calls.find((c) => c.path.endsWith('/config/plans'))?.body).toEqual({
      file: FILE,
    });
    expect(a.calls.some((c) => c.path.endsWith('/apply'))).toBe(false);
    const apply = screen.getByRole('button', { name: 'Apply 2 changes' });
    expect(apply.hasAttribute('disabled')).toBe(true);
    fireEvent.click(screen.getByLabelText('Yes, remove notifications/pager'));
    await waitFor(() => expect(apply.hasAttribute('disabled')).toBe(false));
    fireEvent.click(apply);
    expect(await screen.findByText('Applied 2 changes.')).toBeTruthy();
    expect(a.calls.find((c) => c.path.endsWith('/apply'))?.body).toEqual({
      confirm_removals: ['notifications/pager'],
    });
  });

  it('lists the problems of a file and does not offer to apply it', async () => {
    const a = signedIn().on('POST', '/api/v1/config/plans', {
      created_at: '2026-11-25T10:00:00Z',
      changes: [],
      problems: ['pool.credential: keep that value in the secret store'],
    });
    await openWizard(a);
    choose(FILE);
    fireEvent.click(screen.getByRole('button', { name: 'Plan the file' }));
    expect(await screen.findByText(/pool.credential/)).toBeTruthy();
    expect(
      screen.getByRole('button', { name: 'Apply 0 changes' }).hasAttribute('disabled'),
    ).toBe(true);
  });

  it('says a stale plan went out of date', async () => {
    const a = signedIn()
      .on('POST', '/api/v1/config/plans', {
        ...PLAN,
        changes: [PLAN.changes[0]],
        removals: [],
      })
      .on('POST', '/api/v1/config/plans/plan-abc/apply', () =>
        apiError(409, 'conflict', 'the setup changed since the plan; make a new plan'),
      );
    await openWizard(a);
    choose(FILE);
    fireEvent.click(screen.getByRole('button', { name: 'Plan the file' }));
    fireEvent.click(await screen.findByRole('button', { name: 'Apply 1 change' }));
    expect(await screen.findByText(/the setup changed since the plan/)).toBeTruthy();
  });

  it('downloads the export and shows a viewer why it cannot', async () => {
    renderApp(signedIn(viewer), '/settings/data');
    const s = await screen.findByRole('button', { name: 'Export setup file' });
    expect(s.hasAttribute('disabled')).toBe(true);
    expect(
      section('config-as-code').getAllByText(/admin/i, { selector: '.ds-button-reason' })
        .length,
    ).toBeGreaterThan(0);
  });
});
