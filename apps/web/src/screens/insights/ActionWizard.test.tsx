// The action wizard (spec 4.7): Review says nothing has changed yet, the work step is locked and busy, Done says
// what changed.
import { cleanup, fireEvent, screen } from '@testing-library/react';
import { afterEach, describe, expect, it } from 'vitest';
import { renderApp } from '../../testing/mock';
import { jobsApi } from '../../testing/jobsData';

afterEach(cleanup);

describe('Action wizard', () => {
  it('reviews, changes and says what was done', async () => {
    renderApp(jobsApi(), '/analytics/finding/1/change');
    await screen.findByText(/Raise the job slots of linux-box/);
    expect(screen.getByText(/Nothing has changed yet/)).toBeTruthy();
    const steps = screen
      .getAllByRole('listitem')
      .filter((l) => l.className.includes('ds-steps__item'));
    expect(steps.map((s) => s.textContent?.replace(/^\S+\s/, ''))).toEqual([
      'Review',
      'Changing',
      'Done',
    ]);
    fireEvent.click(screen.getByRole('button', { name: 'Make the change' }));
    expect(await screen.findByText(/1 of 1 changes were made/)).toBeTruthy();
  });
});
