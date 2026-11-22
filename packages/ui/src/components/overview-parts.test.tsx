// Components 58, 59 and 60: the stat tile, the ranked findings strip and the bar list.
import { cleanup, render, screen } from '@testing-library/react';
import { MemoryRouter } from 'react-router-dom';
import { afterEach, describe, expect, it } from 'vitest';
import { BarList } from './BarList';
import { FindingList } from './FindingList';
import { StatGrid, StatTile } from './StatTile';

afterEach(cleanup);
const wrap = (ui: React.ReactNode) => render(<MemoryRouter>{ui}</MemoryRouter>);

describe('StatTile', () => {
  it('is one link that says the number, its change and where it leads', () => {
    wrap(
      <StatGrid>
        <StatTile
          label="Waiting now"
          value="3"
          unit="jobs"
          change="worse"
          changeText="up 2 vs the previous 7 days"
          trend={[1, 2, 3]}
          to="/pool/jobs?state=queued"
          linkName="Open the jobs that wait now"
        />
      </StatGrid>,
    );
    const link = screen.getByRole('link');
    expect(link.getAttribute('href')).toBe('/pool/jobs?state=queued');
    // The visible words are the name, in the order a person reads them, plus the hidden tone word and target.
    expect(link.textContent).toBe(
      'Waiting now 3 jobs ◷ up 2 vs the previous 7 days, worse. Open the jobs that wait now',
    );
    expect(link.getAttribute('aria-label')).toBeNull();
    expect(screen.getByText(/up 2 vs the previous/).textContent).toContain('◷');
  });
  it('says no data yet without a value', () => {
    wrap(<StatTile label="Pool busy" />);
    expect(screen.getByText('no data yet')).toBeTruthy();
    expect(screen.getByText('Pool busy')).toBeTruthy();
  });
});

describe('FindingList', () => {
  it('ranks the items and links each one', () => {
    wrap(
      <FindingList
        label="Needs you"
        items={[
          {
            key: 'a',
            title: 'Add a runner',
            minutes: 'about 40 min a week',
            fix: 'Add one.',
            to: '/analytics/finding/1',
            linkLabel: 'Open: Add a runner',
          },
        ]}
      />,
    );
    expect(screen.getByRole('listitem').getAttribute('aria-label')).toBe(
      '1 of 1: Add a runner, about 40 min a week',
    );
    expect(screen.getByRole('link', { name: 'Open: Add a runner' })).toBeTruthy();
  });
  it('says so when nothing needs you', () => {
    wrap(<FindingList label="Needs you" items={[]} />);
    expect(screen.getByText('Nothing needs you now.')).toBeTruthy();
  });
});

describe('BarList', () => {
  it('makes every row a link that names its target', () => {
    wrap(
      <BarList
        label="Busiest repositories"
        items={[
          {
            key: 'a',
            label: 'repo-a',
            value: 212,
            text: '212 min',
            linkName: 'Open the jobs of repo-a',
            to: '/pool/jobs?repository=repo-a',
          },
        ]}
      />,
    );
    expect(
      screen.getByRole('link', { name: /^repo-a 212 min.*Open the jobs of repo-a/ }),
    ).toBeTruthy();
  });
  it('says so when there is nothing to rank', () => {
    wrap(<BarList label="x" items={[]} />);
    expect(screen.getByText('Nothing to rank in this range.')).toBeTruthy();
  });
});
