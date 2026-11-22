// Where each old Settings address lives now (six tabs, owner decision D8; Updates is its own tab, LOOK43).
export const SECTIONS: Record<string, { tab: string; section?: string }> = {
  users: { tab: 'access', section: 'users' },
  app: { tab: 'access', section: 'github-app' },
  webhooks: { tab: 'access', section: 'webhooks' },
  repositories: { tab: 'repositories', section: 'public-repositories' },
  budgets: { tab: 'repositories', section: 'budgets' },
  appearance: { tab: 'console', section: 'appearance' },
  notifications: { tab: 'console', section: 'notifications' },
  network: { tab: 'console', section: 'network' },
  updates: { tab: 'updates' },
  retention: { tab: 'data', section: 'retention' },
  backups: { tab: 'data', section: 'backups' },
  export: { tab: 'data', section: 'config-export' },
  audit: { tab: 'audit' },
};

/** The old one-page addresses that now redirect (an address named like a tab is that tab). */
const TAB_IDS = ['access', 'repositories', 'console', 'updates', 'data', 'audit'];

export const OLD_SETTINGS = Object.keys(SECTIONS).filter((k) => !TAB_IDS.includes(k));

/** Where an in-app link to a Settings section goes. */
export function settingsPath(old: string): string {
  const at = SECTIONS[old] ?? { tab: 'access' };
  return `/settings/${at.tab}${at.section ? `#${at.section}` : ''}`;
}
