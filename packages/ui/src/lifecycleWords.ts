// The lifecycle words table (install, update, remove). Every word a lifecycle part shows comes from here and
// nowhere else; a part calls `lw(key, values)`. Source: the dev-standards lifecycle words table
// (design-system/lifecycle/words.json). Banned: OK, Yes, No, Install now, Later, daemon, binary, payload.
export const LIFECYCLE_WORDS = {
  check: 'Look for updates',
  checking: 'Looking for updates',
  'available.title': 'Version {version} is ready.',
  'available.primary': 'Update to version {version}',
  'available.secondary': 'Not now',
  'step.downloading': 'Downloading',
  'step.verifying': 'Checking the download',
  'step.installing': 'Installing',
  'step.testing': 'Testing',
  'updated.title': 'Updated to version {version}.',
  'updated.action': "What's new",
  'rolled_back.title':
    'The update to {version} failed its test. Version {previous} is running again. We try again at {time}.',
  'rolled_back.skipped':
    'The update to {version} failed its test three times. Version {previous} keeps running until a newer version is out.',
  'refused.title':
    'The download for {version} did not pass its signature check. Nothing changed.',
  'waiting.title': 'Version {version} waits for running work to finish.',
  up_to_date: 'Up to date. Version {version} is the newest.',
  not_looked: 'Not looked for yet.',
  required: 'This update is required. It installs even when you choose Ask first.',
  'banner.ready': 'Update ready',
  'banner.running': 'Updating',
  'banner.done': 'Updated',
  'banner.failed': 'Update failed',
  'action.retry': 'Try again',
  'action.close': 'Close',
  'action.cancel': 'Cancel',
  'policy.title': 'When to update',
  'policy.automatic_when_idle': 'Automatic when idle',
  'policy.automatic_when_idle.detail': 'Installs when no job is running on this machine.',
  'policy.ask_first': 'Ask first',
  'policy.ask_first.detail': 'Tells you and waits for you to start it.',
  'policy.window': 'Only between',
  'policy.window_from': 'From (hh:mm)',
  'policy.window_to': 'Until (hh:mm)',
  'policy.window_none': 'Any time',
  'policy.window_help': 'Leave both empty to allow any time.',
  'updates.title': 'Updates',
  'updates.current': 'Version',
  'updates.state': 'State',
  'updates.next_check': 'Next check',
  'updates.locked_by': 'Set by {who}. You cannot change it here.',
  'history.title': 'Update history',
  'history.empty': 'Nothing has been updated yet.',
  'history.when': 'When',
  'history.who': 'Who',
  'history.what': 'What',
  'history.version': 'Version',
  'history.result': 'Result',
  'action.check': 'Check',
  'audit.check': 'Checked for updates',
  'audit.install': 'Installed',
  'audit.rollback': 'Rolled back',
  'audit.remove': 'Removed',
  'result.ok': 'Done',
  'result.failed': 'Failed',
  'result.skipped': 'Skipped',
  'remote.title': 'Update {machine}',
  'remote.from_to': 'Version {from} to version {to}',
  'remote.start': 'Update {machine}',
  'remote.running': 'Updating {machine}',
  'remote.done': '{machine} is on version {to}.',
  'remote.failed': 'The update of {machine} failed. It still runs version {from}.',
  'remove.title': 'Remove {product}',
  'remove.choose': 'Choose what happens to your data.',
  'remove.keep': 'Keep my data',
  'remove.keep.detail':
    'Removes the program and the service. Your settings and data stay.',
  'remove.purge': 'Delete my data too ({size} in {path})',
  'remove.purge_short': 'Delete my data too',
  'remove.purge.detail':
    'Also deletes your settings, database, logs, caches, runner images, containers and the crsp WSL distro.',
  'remove.cancel': 'Cancel',
  'remove.confirm': 'Remove {product}',
  'remove.confirm_disabled_until': 'Keep my data or Delete my data too is chosen',
  'remove.busy': 'Removing',
  'remove.started': 'Removing {product}. This page stops working when it is done.',
  'remove.runners': 'Both choices unregister the runners from GitHub.',
  'fleet.title': 'Machines',
  'fleet.up_to_date': 'Up to date',
  'fleet.waiting': 'Waiting',
  'fleet.needs_you': 'Needs you',
  'fleet.updating': 'Updating',
  'fleet.failed': 'Update failed',
  'fleet.version': 'Version {version}',
  'fleet.empty': 'This is the only machine.',
} as const;

export type LifecycleWord = keyof typeof LIFECYCLE_WORDS;

/** The words of the table that a user may never see. */
export const BANNED_WORDS = [
  'OK',
  'Yes',
  'No',
  'Install now',
  'Later',
  'daemon',
  'binary',
  'payload',
] as const;

/** A lifecycle word with its `{values}` filled in. */
export function lw(
  key: LifecycleWord,
  values: Readonly<Record<string, string | number>> = {},
): string {
  return LIFECYCLE_WORDS[key].replace(/\{(\w+)\}/g, (m, k: string) =>
    k in values ? String(values[k]) : m,
  );
}
