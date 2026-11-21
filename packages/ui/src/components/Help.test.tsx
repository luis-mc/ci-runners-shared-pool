// Components 64, 66 (LOOK44, LOOK47): job path, term help.
import { cleanup, fireEvent, render, screen, within } from '@testing-library/react';
import { MemoryRouter } from 'react-router-dom';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { HelpQuestionCard, JobPath, TermHelp } from './Help';
import { PictureBudget, PictureSlots } from './HelpPictures';

afterEach(cleanup);

const groups = [
  { id: 'a', label: 'On GitHub' },
  { id: 'b', label: 'In your pool' },
];
const parts = [
  { id: 'run', title: 'Run', line: 'one start of a workflow', group: 'a' },
  { id: 'slot', title: 'Slot', line: 'room for one job', group: 'b' },
];

describe('JobPath', () => {
  it('numbers the parts across the groups and lights the chosen ones', () => {
    render(<JobPath label="Path" groups={groups} parts={parts} lit={['slot']} />);
    const list = screen.getByRole('list', { name: 'Path' });
    expect(within(list).getByText('On GitHub')).toBeTruthy();
    expect(list.querySelectorAll('[data-lit]')).toHaveLength(1);
    expect(list.querySelectorAll('.ds-jobpath__n')[1]?.textContent).toBe('2');
  });

  it('is read only without onSelect and presses with it', () => {
    const { rerender } = render(<JobPath label="Path" groups={groups} parts={parts} />);
    expect(screen.queryAllByRole('button')).toHaveLength(0);
    const pick = vi.fn();
    rerender(<JobPath label="Path" groups={groups} parts={parts} onSelect={pick} />);
    fireEvent.click(
      screen.getByRole('button', { name: /Run\s*one start of a workflow/ }),
    );
    expect(pick).toHaveBeenCalledWith('run');
  });
});

describe('TermHelp', () => {
  it('opens and closes the answer and links to Help', () => {
    render(
      <MemoryRouter>
        <TermHelp term="Slots" answer="Room for one job." to="/help#q-slot" />
      </MemoryRouter>,
    );
    const ask = screen.getByRole('button', { name: 'What does Slots mean?' });
    expect(ask.hasAttribute('data-term-help')).toBe(true);
    fireEvent.click(ask);
    expect(
      screen.getByRole('link', { name: 'More about this in Help' }).getAttribute('href'),
    ).toBe('/help#q-slot');
    fireEvent.click(ask);
    expect(screen.queryByRole('group', { name: 'Slots' })).toBeNull();
  });
});

describe('JobPath examples (LOOK48)', () => {
  it('shows the example under the name and the meaning when there is none', () => {
    const withExample = [{ ...parts[0]!, example: '#8133, push' }, parts[1]!];
    render(<JobPath label="Path" groups={groups} parts={withExample} />);
    expect(screen.getByText('#8133, push')).toBeTruthy();
    expect(screen.queryByText('one start of a workflow')).toBeNull();
    expect(screen.getByText('room for one job')).toBeTruthy();
  });
});

describe('HelpQuestionCard and the pictures (LOOK48)', () => {
  it('names its box with a badge link and calls onBox', () => {
    const onBox = vi.fn();
    render(
      <HelpQuestionCard
        id="q-slot"
        title="What is a slot?"
        box={{ id: 'slot', n: 5, name: 'Slot' }}
        onBox={onBox}
        picture={<PictureSlots busy={2} total={4} machine="build-1" />}
      >
        <p>Room for one job.</p>
      </HelpQuestionCard>,
    );
    const link = screen.getByRole('link', { name: 'Box 5: Slot' });
    expect(link.getAttribute('href')).toBe('#box-slot');
    fireEvent.click(link);
    expect(onBox).toHaveBeenCalledWith('slot');
    expect(screen.getByText('2 of 4 slots busy')).toBeTruthy();
    expect(screen.getByRole('img', { name: 'build-1: 2 of 4 slots busy.' })).toBeTruthy();
  });

  it('draws the budget bars from the numbers it is given and none when it has none', () => {
    const { rerender } = render(
      <PictureBudget
        machine="build-1"
        memory={{ now: 62, budget: 85 }}
        cpu={{ now: 31, budget: 60 }}
      />,
    );
    expect(screen.getByText('Memory 62% of 85%')).toBeTruthy();
    expect(screen.getByText('CPU 31% of 60%')).toBeTruthy();
    rerender(<PictureBudget />);
    expect(screen.queryByText(/%/)).toBeNull();
  });
});
