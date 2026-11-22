// The Overview line (spec 4.2): one link to Do next, no button (UX-52).
import { cleanup, screen } from '@testing-library/react';
import { afterEach, describe, expect, it } from 'vitest';
import { renderApp } from '../../testing/mock';
import { jobsApi } from '../../testing/jobsData';

afterEach(cleanup);

describe('Overview line', () => {
  it('links to Do next with the count and the minutes', async () => {
    renderApp(jobsApi(), '/');
    const link = await screen.findByRole('link', {
      name: /4 things to do next, about 277 min a week/,
    });
    expect(link.getAttribute('href')).toContain('/analytics/insights');
  });
});
