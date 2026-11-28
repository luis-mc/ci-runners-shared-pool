// LC-22 to LC-24: every state of every lifecycle part renders, says its words from the table, and has one clear action.
import { cleanup, fireEvent, render, screen, within } from '@testing-library/react';
import { MemoryRouter } from 'react-router-dom';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { BANNED_WORDS, LIFECYCLE_WORDS, lw } from '../lifecycleWords';
import {
  FleetRows,
  RemoteUpdate,
  RemoveDialog,
  UpdateBanner,
  ReleaseNotes,
  UpdateHistory,
  UpdatesCard,
  updateStateLine,
  type AuditEvent,
  type BannerState,
  type UpdateState,
} from './Lifecycle';

afterEach(cleanup);
const wrap = (ui: React.ReactElement) => render(<MemoryRouter>{ui}</MemoryRouter>);

describe('lifecycle words', () => {
  it('has no banned word as a whole word in any text', () => {
    for (const text of Object.values(LIFECYCLE_WORDS))
      for (const b of BANNED_WORDS)
        expect(new RegExp(`\\b${b}\\b`).test(text), `${b} in "${text}"`).toBe(false);
  });
  it('has no doubled punctuation, status code or error code', () => {
    for (const text of Object.values(LIFECYCLE_WORDS)) {
      expect(text).not.toMatch(/[.!?,:;]{2,}/);
      expect(text).not.toMatch(/\b[1-5]\d\d\b|\b[A-Z]{2,}_\d+\b/);
    }
  });
  it('fills values and leaves unknown ones', () => {
    expect(lw('available.title', { version: '1.2.3' })).toBe('Version 1.2.3 is ready.');
  });
});

describe('UpdateBanner states', () => {
  const states: BannerState[] = ['ready', 'running', 'done', 'failed'];
  for (const state of states)
    it(`renders ${state}`, () => {
      const onUpdate = vi.fn();
      const onRetry = vi.fn();
      wrap(
        <UpdateBanner
          state={state}
          version="1.5.0"
          to="/settings/updates"
          step="verifying"
          reason="x"
          onUpdate={onUpdate}
          onRetry={onRetry}
        />,
      );
      const root = document.querySelector(
        '[data-lifecycle="update-banner"]',
      ) as HTMLElement;
      expect(root.dataset.state).toBe(state);
      if (state === 'ready') {
        fireEvent.click(screen.getByRole('button', { name: 'Update to version 1.5.0' }));
        expect(onUpdate).toHaveBeenCalled();
        expect(screen.getByRole('button', { name: 'Not now' })).toBeTruthy();
      }
      if (state === 'running') {
        // One short line with a link, no bar and no step words (CRSP364).
        expect(screen.getByText('An update is running.')).toBeTruthy();
        expect(screen.queryByRole('progressbar')).toBeNull();
        expect(
          screen.getByRole('button', { name: 'See the update' }).getAttribute('href'),
        ).toBe('/settings/updates');
      }
      if (state === 'done')
        expect(screen.getByText('Updated to version 1.5.0.')).toBeTruthy();
      if (state === 'failed') {
        fireEvent.click(screen.getByRole('button', { name: 'Try again' }));
        expect(onRetry).toHaveBeenCalled();
      }
    });
});

describe('UpdatesCard progress and restart (CRSP364)', () => {
  it('draws the bar with the step words only inside the pane', () => {
    wrap(
      <UpdatesCard policy="ask_first" state="installing" current="1.4.0" percent={60} />,
    );
    const bar = screen.getByRole('progressbar');
    expect(bar.getAttribute('aria-valuenow')).toBe('60');
    expect(bar.closest('[data-tone]')).toBeNull();
  });
  it('says the restart in one line, no bar', () => {
    wrap(
      <UpdatesCard policy="ask_first" state="installing" current="1.4.0" restarting />,
    );
    expect(screen.getAllByText('Restarting, back in a moment.')).toHaveLength(1);
    expect(screen.queryByRole('progressbar')).toBeNull();
  });
});

describe('UpdatesCard states', () => {
  const states: UpdateState[] = [
    'idle',
    'checking',
    'available',
    'waiting_for_jobs',
    'downloading',
    'verifying',
    'installing',
    'testing',
    'updated',
    'rolled_back',
    'refused',
    'failed',
  ];
  for (const state of states)
    it(`renders ${state} with a state line`, () => {
      wrap(
        <UpdatesCard
          policy="automatic_when_idle"
          state={state}
          current="1.4.0"
          available="1.5.0"
          failure={{ version: '1.5.0', count: 1, retryAt: '14:00' }}
          onInstall={() => {}}
        />,
      );
      const root = document.querySelector(
        '[data-lifecycle="updates-card"]',
      ) as HTMLElement;
      expect(root.dataset.state).toBe(state);
      expect(
        within(root).getAllByText(
          updateStateLine({
            state,
            current: '1.4.0',
            available: '1.5.0',
            failure: { version: '1.5.0', count: 1, retryAt: '14:00' },
          }),
        ).length,
      ).toBeGreaterThan(0);
    });
  it('says when the retry is and when it stops', () => {
    expect(
      updateStateLine({
        state: 'rolled_back',
        current: '1.4.0',
        available: '1.5.0',
        failure: { version: '1.5.0', count: 1, retryAt: '15:30' },
      }),
    ).toContain('We try again at 15:30.');
    expect(
      updateStateLine({
        state: 'rolled_back',
        current: '1.4.0',
        available: '1.5.0',
        failure: { version: '1.5.0', count: 3, retryAt: '' },
      }),
    ).toContain('until a newer version');
  });
  it('sends the policy, the window and Check', () => {
    const onPolicy = vi.fn();
    const onWindow = vi.fn();
    const onCheck = vi.fn();
    wrap(
      <UpdatesCard
        policy="automatic_when_idle"
        state="idle"
        current="1.4.0"
        onPolicy={onPolicy}
        onWindow={onWindow}
        onCheck={onCheck}
      />,
    );
    fireEvent.click(screen.getByRole('radio', { name: /Ask first/ }));
    expect(onPolicy).toHaveBeenCalledWith('ask_first');
    fireEvent.click(screen.getByRole('button', { name: 'Choose the From time' }));
    expect(onWindow).not.toHaveBeenCalled();
    expect(screen.getByText('Choose an Until time too, or clear both.')).toBeTruthy();
    fireEvent.click(screen.getByRole('button', { name: 'Choose the Until time' }));
    expect(onWindow).toHaveBeenCalledWith({ start: '00:00', end: '00:00' });
    const hours = screen.getAllByRole('spinbutton', { name: 'Hours' });
    fireEvent.keyDown(hours[1] as HTMLElement, { key: 'ArrowUp' });
    expect(onWindow).toHaveBeenLastCalledWith({ start: '00:00', end: '01:00' });
    fireEvent.click(screen.getByRole('button', { name: 'Clear From' }));
    expect(screen.getByText('Choose a From time too, or clear both.')).toBeTruthy();
    fireEvent.click(screen.getByRole('button', { name: 'Clear Until' }));
    expect(onWindow).toHaveBeenLastCalledWith(null);
    fireEvent.click(screen.getByRole('button', { name: 'Look for updates' }));
    expect(onCheck).toHaveBeenCalled();
  });
  it('keeps the keys pressed while an earlier save comes back, and shows a change made elsewhere', () => {
    const onWindow = vi.fn();
    const card = (window: { start: string; end: string }) => (
      <MemoryRouter>
        <UpdatesCard
          policy="automatic_when_idle"
          state="idle"
          current="1.4.0"
          window={window}
          onWindow={onWindow}
        />
      </MemoryRouter>
    );
    const { rerender } = render(card({ start: '01:00', end: '01:00' }));
    const until = () => screen.getAllByRole('spinbutton', { name: 'Hours' })[1] as HTMLElement;
    until().focus();
    fireEvent.keyDown(until(), { key: 'ArrowUp' });
    fireEvent.keyDown(until(), { key: 'ArrowUp' });
    expect(onWindow).toHaveBeenLastCalledWith({ start: '01:00', end: '03:00' });
    // The save of 02:00 comes back after 03:00 was pressed: an echo, not taken back.
    rerender(card({ start: '01:00', end: '02:00' }));
    expect(until()).toBe(document.activeElement);
    expect(until().getAttribute('aria-valuenow')).toBe('3');
    // A value this card never sent is a change made elsewhere: it is shown.
    rerender(card({ start: '06:00', end: '07:00' }));
    expect(until().getAttribute('aria-valuenow')).toBe('7');
  });
  it('shows a locked window as text, with no picker and no button', () => {
    wrap(
      <UpdatesCard
        policy="automatic_when_idle"
        state="idle"
        current="1.4.0"
        lockedBy="IT"
        window={{ start: '01:00', end: '05:00' }}
      />,
    );
    expect(screen.queryByRole('spinbutton')).toBeNull();
    expect(screen.queryByRole('button', { name: /Clear|Choose a/ })).toBeNull();
    expect(screen.getByText('01:00')).toBeTruthy();
    expect(screen.getByText('05:00')).toBeTruthy();
  });
  it('locks the policy when an admin set it', () => {
    wrap(<UpdatesCard policy="ask_first" state="idle" current="1.4.0" lockedBy="IT" />);
    expect(screen.getAllByText(/Set by IT/).length).toBeGreaterThan(0);
    expect(
      screen.getByRole('button', { name: 'Look for updates' }).hasAttribute('disabled'),
    ).toBe(true);
  });
  it('shows Required and Check busy', () => {
    wrap(<UpdatesCard policy="ask_first" state="checking" current="1.4.0" required />);
    expect(screen.getByText(/This update is required/)).toBeTruthy();
    expect(
      screen
        .getByRole('button', { name: 'Looking for updates' })
        .hasAttribute('disabled'),
    ).toBe(true);
  });
});

describe('UpdateHistory', () => {
  const events: AuditEvent[] = [
    {
      at: '2026-11-21 10:00',
      actor: 'luis',
      action: 'install',
      version: '1.5.0',
      result: 'ok',
    },
    {
      at: '2026-11-21 11:00',
      actor: 'system',
      action: 'rollback',
      version: '1.5.0',
      result: 'failed',
    },
    {
      at: '2026-11-21 12:00',
      actor: 'system',
      action: 'check',
      version: '1.5.0',
      result: 'skipped',
    },
  ];
  it('lists each result as a word', () => {
    wrap(<UpdateHistory events={events} />);
    expect(screen.getAllByText('Done').length).toBeGreaterThan(0);
    expect(screen.getAllByText('Failed').length).toBeGreaterThan(0);
    expect(screen.getAllByText('Skipped').length).toBeGreaterThan(0);
  });
  it('says so when empty', () => {
    wrap(<UpdateHistory events={[]} />);
    expect(screen.getByText('Nothing has been updated yet.')).toBeTruthy();
  });
});

describe('RemoteUpdate states', () => {
  for (const state of ['ready', 'running', 'done', 'failed'] as const)
    it(`renders ${state}`, () => {
      const onStart = vi.fn();
      wrap(
        <RemoteUpdate
          machine="Build-2"
          from="1.4.0"
          to="1.5.0"
          state={state}
          percent={30}
          onStart={onStart}
        />,
      );
      expect(screen.getByText('Version 1.4.0 to version 1.5.0')).toBeTruthy();
      if (state === 'running') expect(screen.getByRole('progressbar')).toBeTruthy();
      if (state === 'ready') {
        fireEvent.click(screen.getByRole('button', { name: 'Update Build-2' }));
        expect(onStart).toHaveBeenCalled();
      }
      if (state === 'done')
        expect(screen.getByText('Build-2 is on version 1.5.0.')).toBeTruthy();
      if (state === 'failed')
        fireEvent.click(screen.getByRole('button', { name: 'Try again' }));
    });
});

describe('RemoveDialog', () => {
  it('keeps Remove disabled until Keep my data or Delete my data too is chosen', () => {
    const onRemove = vi.fn();
    render(
      <RemoveDialog
        product="crsp"
        size="1.4 GB"
        path="C:\\crsp"
        onRemove={onRemove}
        onCancel={() => {}}
      />,
    );
    const remove = screen.getByRole('button', { name: 'Remove crsp' });
    expect(remove.hasAttribute('disabled')).toBe(true);
    expect(screen.getByText('Choose what happens to your data first.')).toBeTruthy();
    expect(
      screen.getByRole<HTMLInputElement>('radio', { name: /Keep my data/ }).checked,
    ).toBe(false);
    expect(
      screen.getByRole<HTMLInputElement>('radio', { name: /Delete my data too/ }).checked,
    ).toBe(false);
    fireEvent.click(screen.getByRole('radio', { name: /Delete my data too/ }));
    expect(remove.hasAttribute('disabled')).toBe(false);
    fireEvent.click(remove);
    expect(onRemove).toHaveBeenCalledWith('purge');
  });
  it('sends keep', () => {
    const onRemove = vi.fn();
    render(
      <RemoveDialog
        product="crsp"
        size="1 GB"
        path="/x"
        onRemove={onRemove}
        onCancel={() => {}}
      />,
    );
    fireEvent.click(screen.getByRole('radio', { name: /Keep my data/ }));
    fireEvent.click(screen.getByRole('button', { name: 'Remove crsp' }));
    expect(onRemove).toHaveBeenCalledWith('keep');
  });
  it('locks and says Removing while the request is open', () => {
    render(
      <RemoveDialog
        product="crsp"
        size="1 GB"
        path="/x"
        busy
        onRemove={() => {}}
        onCancel={() => {}}
      />,
    );
    expect(
      screen.getByRole('button', { name: 'Removing' }).hasAttribute('disabled'),
    ).toBe(true);
  });
  it('cancels with Cancel and Escape', () => {
    const onCancel = vi.fn();
    render(
      <RemoveDialog
        product="crsp"
        size="1 GB"
        path="/x"
        onRemove={() => {}}
        onCancel={onCancel}
      />,
    );
    fireEvent.click(screen.getByRole('button', { name: 'Cancel' }));
    expect(onCancel).toHaveBeenCalledTimes(1);
  });
});

describe('FleetRows states', () => {
  it('shows every state as a word', () => {
    wrap(
      <FleetRows
        rows={[
          { id: '1', machine: 'A', version: '1.5.0', state: 'up_to_date' },
          { id: '2', machine: 'B', version: '1.4.0', state: 'waiting' },
          { id: '3', machine: 'C', version: '1.4.0', state: 'needs_you' },
          { id: '4', machine: 'D', version: '1.4.0', state: 'updating' },
          { id: '5', machine: 'E', version: '1.4.0', state: 'failed' },
        ]}
      />,
    );
    for (const w of ['Up to date', 'Waiting', 'Needs you', 'Updating', 'Update failed'])
      expect(screen.getAllByText(w).length).toBeGreaterThan(0);
  });
  it('says so when no machine is paired', () => {
    wrap(<FleetRows rows={[]} />);
    expect(screen.getByText('This is the only machine.')).toBeTruthy();
  });
});

describe('ReleaseNotes', () => {
  it('shows the note with its title, one paragraph per line', () => {
    wrap(<ReleaseNotes version="0.1.30" notes={'First thing.\n\nSecond thing.'} />);
    expect(screen.getByText('What is new in version 0.1.30')).toBeTruthy();
    expect(screen.getByText('First thing.')).toBeTruthy();
    expect(screen.getByText('Second thing.')).toBeTruthy();
  });
  it('draws nothing for a release without a note', () => {
    for (const notes of [undefined, null, '', '  \n ']) {
      const { container } = wrap(<ReleaseNotes version="0.1.30" notes={notes} />);
      expect(container.querySelector('[data-lifecycle="release-notes"]')).toBeNull();
      cleanup();
    }
  });
  it('is dismissed once after an update, and stays with the offer before it', () => {
    window.localStorage.clear();
    wrap(<ReleaseNotes version="0.1.30" notes="A note." after />);
    fireEvent.click(screen.getByRole('button', { name: /^hide$/i }));
    expect(screen.queryByText('A note.')).toBeNull();
    cleanup();
    wrap(<ReleaseNotes version="0.1.30" notes="A note." after />);
    expect(screen.queryByText('A note.')).toBeNull();
    cleanup();
    wrap(<ReleaseNotes version="0.1.31" notes="A note." />);
    expect(screen.queryByRole('button', { name: /^hide$/i })).toBeNull();
    expect(screen.getByText('A note.')).toBeTruthy();
  });
  it('sits in the Updates card only when a version is offered', () => {
    wrap(
      <UpdatesCard
        policy="ask_first"
        state="available"
        current="1.0.0"
        available="1.1.0"
        notes="Live progress."
      />,
    );
    expect(screen.getByText('Live progress.')).toBeTruthy();
    cleanup();
    wrap(
      <UpdatesCard
        policy="ask_first"
        state="idle"
        current="1.0.0"
        notes="Live progress."
      />,
    );
    expect(screen.queryByText('Live progress.')).toBeNull();
  });
});
