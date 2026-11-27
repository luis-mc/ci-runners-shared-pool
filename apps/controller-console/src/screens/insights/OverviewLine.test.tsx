// The Overview's best next change (LOOK56, option D): the top suggestion, linked, and the way to the whole list.
import { cleanup, screen, within } from '@testing-library/react';
import { afterEach, describe, expect, it } from 'vitest';
import { jobsApi } from '../../testing/jobsData';
import { renderApp } from '../../testing/mock';

afterEach(cleanup);

describe('Best next change', () => {
  it('links to the top suggestion and to Analytics > Suggestions', async () => {
    renderApp(
      jobsApi().on('GET', '/api/v1/timeline', {
        from: 'x',
        to: 'x',
        more_before: false,
        rows: [],
        bars: [],
      }),
      '/',
    );
    const pane = await screen.findByRole('region', { name: /Best next change/ });
    const links = Array.from(pane.querySelectorAll('a')).map((a) =>
      a.getAttribute('href'),
    );
    expect(links).toHaveLength(1);
    expect(links[0]).toMatch(/^\/analytics\/finding\/\d+$/);
    expect(
      within(pane).getByRole('link', { name: 'Open it in Analytics > Suggestions' }),
    ).toBeTruthy();
  });
});
