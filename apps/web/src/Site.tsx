// The frame of every page: the top menu, one page body and the footer. Only shared UI parts; no class, no style (design system UX-2).
import type { ReactNode } from 'react';
import { SiteFrame, Text, TextLink, TopMenu, type NavItem } from '@crsp/ui';
import { REPO } from './lib/release.ts';

export const DOCS_URL = '/docs';
export const REPO_URL = `https://github.com/${REPO}`;

const MENU: readonly NavItem[] = [
  { to: '/', label: 'Home', end: true },
  { to: '/product', label: 'Product' },
  { to: '/download', label: 'Download' },
  { to: '/docs', label: 'Docs' },
  { to: '/security', label: 'Security' },
];

export function Site({ children }: { children: ReactNode }) {
  return (
    <SiteFrame
      menu={<TopMenu items={MENU} label="Site" />}
      footer={
        <Text tone="muted">
          CI Runners Shared Pool is open source under the MIT licence. Read the{' '}
          <TextLink to={DOCS_URL}>docs</TextLink> or see the{' '}
          <TextLink to={REPO_URL}>repository</TextLink>.
        </Text>
      }
    >
      {children}
    </SiteFrame>
  );
}
