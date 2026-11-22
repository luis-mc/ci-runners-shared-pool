// Run insights, one finding (spec 4.1): the fix, a copyable snippet, evidence, the estimate; one primary only when
// the fix changes the crsp setup (UX-4, UX-8); Hide for 7 days is a secondary action (D17).
import { cleanup, fireEvent, screen } from '@testing-library/react';
import { afterEach, describe, expect, it } from 'vitest';
import { renderApp } from '../../testing/mock';
import { jobsApi } from '../../testing/jobsData';

afterEach(cleanup);

describe('Finding page', () => {
  it('offers Review the change for a setup fix', async () => {
    renderApp(jobsApi(), '/analytics/finding/1');
    expect(await screen.findByRole('button', { name: 'Review the change' })).toBeTruthy();
    expect(screen.getByText(/estimate/)).toBeTruthy();
    expect(screen.getByRole('button', { name: 'Hide for 7 days' })).toBeTruthy();
  });

  it('hides the finding for 7 days and says when it comes back', async () => {
    renderApp(jobsApi(), '/analytics/finding/1');
    fireEvent.click(await screen.findByRole('button', { name: 'Hide for 7 days' }));
    expect(await screen.findByText(/Hidden until .* if it is still true/)).toBeTruthy();
    expect(
      screen.getByRole<HTMLButtonElement>('button', { name: 'Hide for 7 days' }).disabled,
    ).toBe(true);
  });

  it('shows the snippet and no change primary for a repository fix', async () => {
    renderApp(jobsApi(), '/analytics/finding/2');
    await screen.findByLabelText('Workflow snippet');
    expect(screen.getByRole('button', { name: 'Copy' })).toBeTruthy();
    expect(screen.queryByRole('button', { name: 'Review the change' })).toBeNull();
  });

  it('says so when the finding is not there', async () => {
    renderApp(jobsApi(), '/analytics/finding/99');
    expect(await screen.findByText(/Could not load this finding/)).toBeTruthy();
  });
});
