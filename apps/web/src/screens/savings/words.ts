// The words of minutes saved and money saved (docs/design-system/next-version/content.md, "Minutes and money saved
// words"; observability R5.15 to R5.19). One place for every sentence the user reads about savings.
import { formatNumber } from '@crsp/ui';

/** US dollars as the console shows them: cents for a figure of a dollar or more, tenths of a cent below. */
export function money(usd: number): string {
  const digits = usd >= 1 ? 2 : 3;
  return `$${usd.toLocaleString('en-US', {
    minimumFractionDigits: digits,
    maximumFractionDigits: digits,
  })}`;
}

export const SAVINGS_WORDS = {
  tileMinutes: 'Minutes saved today',
  tileMoney: 'Money saved today',
  estimate: 'estimate',
  minutesUnit: 'min',
  rangeLabel: 'Range',
  ranges: [
    { value: 'today', label: 'Today' },
    { value: '7d', label: '7 days' },
    { value: '30d', label: '30 days' },
    { value: 'custom', label: 'A range' },
  ] as const,
  from: 'From',
  to: 'To',
  customHint: 'Pick the first and the last day.',
  byRepository: 'By repository',
  byOs: 'By system',
  byMachine: 'By machine',
  repository: 'Repository',
  system: 'System',
  machine: 'Machine',
  jobs: 'Jobs',
  minutes: 'Minutes',
  money: 'Money',
  empty: 'No job ran on the pool in this range.',
  publicNote: 'Public repositories count their minutes but save no money.',
  notPriced: (n: number) =>
    `${formatNumber(n)} ${n === 1 ? 'job has' : 'jobs have'} no rate for its system, so it adds no money.`,
  source: (url: string, date: string, changed: boolean) =>
    `Rates copied from ${url} on ${date}${changed ? '. You changed some rates.' : '.'}`,
  ratesTitle: 'Savings rates',
  ratesLead:
    'The price of one minute of a GitHub-hosted runner, used to work out the money saved. The shipped rates are copied from the GitHub page named below; change a rate when your plan differs.',
  rate: (os: string, size: string) => `${os} ${size}, dollars per minute`,
  machineSize: (name: string) => `Price ${name} as`,
  reset: 'Reset to shipped rates',
  resetting: 'Resetting',
  resetDone: 'The shipped rates are back.',
  saved: 'Savings rates saved.',
  compareTitle: 'crsp against GitHub-hosted runners',
  compareRepository: 'Repository',
  compareHelp:
    'Write it as owner/repo. The comparison reads finished jobs from GitHub and starts nothing.',
  compareDays: 'Days',
  compareRun: 'Compare',
  compareRunning: 'Comparing',
  compareEmpty: 'No job of this repository ran on both kinds of runner in this window.',
  compareExcluded:
    'Hosted minutes are free for a public repository, so there is nothing to compare.',
  compareJob: 'Job',
  compareCrsp: 'crsp, median run',
  compareHosted: 'Hosted, median run',
  compareSaved: 'Minutes saved',
  compareCost: 'Cost avoided',
  compareTotal: (min: number, usd: number) =>
    `In this window: ${formatNumber(min, 1)} minutes saved, ${money(usd)} avoided (estimate).`,
};
