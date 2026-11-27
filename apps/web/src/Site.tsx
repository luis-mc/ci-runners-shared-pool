// The frame of every page: the top menu and one page body. Only shared UI parts; no class, no style (design system UX-2).
import type { ReactNode } from 'react';
import { Page, Text, TextLink, TopMenu, type NavItem } from '@crsp/ui';
import { REPO } from './lib/release.ts';

export const DOCS_URL = `https://github.com/${REPO}/tree/main/docs`;
export const REPO_URL = `https://github.com/${REPO}`;

const MENU: readonly NavItem[] = [
  { to: '/', label: 'Home', end: true },
  { to: '/download', label: 'Download' },
];

export function Site({ children }: { children: ReactNode }) {
  return (
    <>
      <TopMenu items={MENU} label="Site" />
      <main id="main">
        <Page>
          {children}
          <Text tone="muted">
            CI Runners Shared Pool is open source under the MIT licence. Read the{' '}
            <TextLink to={DOCS_URL}>docs</TextLink> or see the{' '}
            <TextLink to={REPO_URL}>repository</TextLink>.
          </Text>
        </Page>
      </main>
    </>
  );
}
