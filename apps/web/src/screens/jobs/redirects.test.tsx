// LOOK56: the old addresses are removed with no redirects (owner decision 22 Nov 2026). Each one is a page that does
// not exist; the new addresses carry the screens. (Kept at this path: design rule UX-74 names it as a guard.)
import { cleanup, screen } from '@testing-library/react';
import { afterEach, describe, expect, it } from 'vitest';
import { jobsApi } from '../../testing/jobsData';
import { renderApp } from '../../testing/mock';

afterEach(cleanup);

describe('old addresses', () => {
  it.each([
    '/jobs',
    '/jobs/done',
    '/jobs/live',
    '/jobs/insights',
    '/repositories',
    '/shared-pool',
    '/machines',
    '/runners',
    '/pool/runners',
    '/pool/images',
  ])('%s is not a page', async (path) => {
    renderApp(jobsApi(), path);
    expect(await screen.findByText('This page does not exist.')).toBeTruthy();
  });
});

describe('new addresses', () => {
  it.each([
    ['/pool', 'Pool'],
    ['/pool/jobs', 'Pool'],
    ['/analytics', 'Analytics'],
    ['/setup/pool', 'Setup'],
  ])('%s opens its screen', async (path, title) => {
    renderApp(jobsApi(), path);
    expect(await screen.findByRole('heading', { level: 1, name: title })).toBeTruthy();
  });

  it('keeps the first-run page at /setup when the controller sends a code', async () => {
    renderApp(jobsApi(), '/setup?code=abc');
    expect(await screen.findByRole('heading', { level: 1 })).toBeTruthy();
    expect(window.location.pathname).not.toContain('/setup/summary');
  });
});
