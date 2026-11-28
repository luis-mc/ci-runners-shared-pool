// CRSP132, web-console R5.67: every list has one shared toolbar row (search, filters, selection, count, actions).
import { cleanup, screen } from '@testing-library/react';
import { afterEach, describe, expect, it } from 'vitest';
import { renderApp, signedIn } from '../testing/mock';

afterEach(cleanup);

describe('List toolbar (CRSP132)', () => {
  it('is on the machines list too', async () => {
    renderApp(signedIn(), '/setup/machines');
    expect(await screen.findByRole('group', { name: 'Machines toolbar' })).toBeTruthy();
  });
});
