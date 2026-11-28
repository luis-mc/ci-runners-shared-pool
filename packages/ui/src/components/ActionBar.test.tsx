// The action bar (component 44, UX-71): one place, one order, one primary.
import { cleanup, render, screen, within } from '@testing-library/react';
import { afterEach, describe, expect, it } from 'vitest';
import { ActionBar, selectedStatus, unsavedStatus } from './ActionBar';
import { Button } from './Button';

afterEach(cleanup);

describe('ActionBar', () => {
  it('is a named region with the status, then the secondary actions, then the primary', () => {
    render(
      <ActionBar
        status={selectedStatus(2)}
        secondary={<Button>Cancel</Button>}
        primary={<Button kind="primary">Use pool</Button>}
      />,
    );
    const bar = screen.getByRole('region', { name: 'Actions' });
    expect(within(bar).getByText('2 selected')).toBeTruthy();
    const names = within(bar)
      .getAllByRole('button')
      .map((b) => b.textContent);
    expect(names).toEqual(['Cancel', 'Use pool']);
    expect(bar.querySelectorAll('[data-primary]')).toHaveLength(1);
  });

  it('draws no status line when there is nothing to say', () => {
    render(<ActionBar primary={<Button kind="primary">Add runners</Button>} />);
    expect(document.querySelector('.ds-action-bar__status')).toBeNull();
  });

  it('keeps a disabled primary with its reason', () => {
    render(
      <ActionBar
        primary={
          <Button kind="primary" disabled reason="Nothing has changed yet.">
            Save
          </Button>
        }
      />,
    );
    expect(screen.getByRole<HTMLButtonElement>('button', { name: 'Save' }).disabled).toBe(
      true,
    );
    expect(screen.getByText('Nothing has changed yet.')).toBeTruthy();
  });

  it('words the selection and the unsaved changes', () => {
    expect(selectedStatus(1)).toBe('1 selected');
    expect(unsavedStatus(0)).toBeUndefined();
    expect(unsavedStatus(3)).toBe('Unsaved changes (3)');
  });
});

describe('ActionBar in a Frame (RES21 T1)', () => {
  it('is drawn right after the main column, whatever wrapper holds it', async () => {
    const { Frame } = await import('./Shell');
    const { MemoryRouter } = await import('react-router-dom');
    render(
      <MemoryRouter>
        <Frame header={<header />} menu={<nav />}>
          <div>
            <div>
              <ActionBar primary={<Button kind="primary">Save</Button>} />
            </div>
          </div>
        </Frame>
      </MemoryRouter>,
    );
    const bar = await screen.findByRole('region', { name: 'Actions' });
    expect(bar.parentElement?.className).toBe('ds-bar-slot');
    // A child of the frame, so it sticks against the whole frame and not the main column (UX-71, CRSP381).
    expect(bar.parentElement?.parentElement?.className).toBe('ds-frame');
    expect(bar.parentElement?.previousElementSibling?.id).toBe('main');
  });

  it('stays in the flow when it is not sticky', async () => {
    const { Frame } = await import('./Shell');
    const { MemoryRouter } = await import('react-router-dom');
    render(
      <MemoryRouter>
        <Frame header={<header />} menu={<nav />}>
          <section data-testid="wrap">
            <ActionBar sticky={false} primary={<Button>Next</Button>} />
          </section>
        </Frame>
      </MemoryRouter>,
    );
    const bar = screen.getByRole('region', { name: 'Actions' });
    expect(bar.parentElement?.dataset.testid).toBe('wrap');
  });

  it('a submit button in the bar still sends the form it was written in', async () => {
    const { Frame } = await import('./Shell');
    const { MemoryRouter } = await import('react-router-dom');
    let sent = 0;
    render(
      <MemoryRouter>
        <Frame header={<header />} menu={<nav />}>
          <form
            onSubmit={(e) => {
              e.preventDefault();
              sent += 1;
            }}
          >
            <input aria-label="Name" />
            <ActionBar
              primary={
                <Button kind="primary" submit>
                  Save
                </Button>
              }
            />
          </form>
        </Frame>
      </MemoryRouter>,
    );
    const { fireEvent } = await import('@testing-library/react');
    fireEvent.click(await screen.findByRole('button', { name: 'Save' }));
    expect(sent).toBe(1);
  });
});
