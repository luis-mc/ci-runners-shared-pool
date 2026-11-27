// The path forward of a suggestion (LOOK56, owner change 2): the numbered steps the user takes to carry it out. A
// suggestion about a project names that repository in its first step. Every sentence is in the design system's
// content.md ("Suggestion steps words"); a kind that has no steps of its own gets the three general ones.
import type { Finding } from './words';
import { projectOf } from './words';

type Make = (where: string, subject: string) => readonly string[];

const WORKFLOW = (where: string, what: string, how: string): readonly string[] => [
  `Open the workflow file of ${where} on GitHub (${what}).`,
  how,
  'Commit the change on a branch and open a pull request.',
  'When it is merged, read this suggestion again after the next runs: it goes away when the change works.',
];

const BY_KIND: Record<string, Make> = {
  'wait.hold': (w, s) => [
    `Open Pool > Jobs and filter by ${w}${s ? ` and the job ${s}` : ''}.`,
    'Open Why waiting on a row of that job and read what holds it.',
    'Change what the reason names: the runner, the image, or the load budget in Setup > Size and backends.',
    'Run the job again and check that its wait is shorter.',
  ],
  'housekeeping.lane': (w, s) => [
    `Open Setup > Runners and add a runner for the light jobs of ${w}${s ? ` (${s})` : ''}.`,
    'Give it the label that these jobs ask for.',
    'Watch Pool > Queue Analysis: the light jobs should no longer wait behind heavy ones.',
  ],
  'pool.size': () => [
    'Open Setup > Size and backends.',
    'Raise the job slots of the machine this suggestion names, or add a machine in Setup > Machines.',
    'Save, then watch Pool > Queue Analysis for a shorter queue.',
  ],
  'pool.idle_next_to_queue': () => [
    'Open Setup > Runners and find the runners that sit idle while jobs wait.',
    'Give them the label of the waiting jobs, or add the label to the jobs in the workflow.',
    'Watch Pool > Live: the waiting jobs should move to the idle runners.',
  ],
  'repo.slots': (w) => [
    `Open Setup > Pool setup and open ${w}.`,
    'Raise the number of jobs that repository may run at once.',
    'Save, then watch Pool > Queue Analysis for fewer jobs that wait on the repository limit.',
  ],
  'run.critical_path': (w, s) =>
    WORKFLOW(
      w,
      s || 'the workflow in this suggestion',
      'Split the longest job, or let the jobs that do not depend on it start first (needs:).',
    ),
  'run.superseded': (w, s) =>
    WORKFLOW(
      w,
      s || 'the workflow in this suggestion',
      'Add a concurrency group with cancel-in-progress: true, so an older run stops when a newer push arrives.',
    ),
  'run.duplicate_event': (w, s) =>
    WORKFLOW(
      w,
      s || 'the workflow in this suggestion',
      'Make the workflow start on one event only (push or pull_request), not both for the same commit.',
    ),
  'run.retested_tree': (w, s) =>
    WORKFLOW(
      w,
      s || 'the workflow in this suggestion',
      'Skip the run when the tree did not change, for example with a paths filter.',
    ),
  'run.fail_fast': (w, s) =>
    WORKFLOW(
      w,
      s || 'the workflow in this suggestion',
      'Set fail-fast: true on the matrix, so the other jobs stop after the first failure.',
    ),
  'workflow.timeout': (w, s) =>
    WORKFLOW(
      w,
      s || 'the workflow in this suggestion',
      'Add timeout-minutes to the job, a little above its slow end.',
    ),
  'workflow.retry': (w, s) =>
    WORKFLOW(
      w,
      s || 'the workflow in this suggestion',
      'Remove the retry that hides a failing step, or keep it on the one step that needs it.',
    ),
  'workflow.matrix_axis': (w, s) =>
    WORKFLOW(
      w,
      s || 'the workflow in this suggestion',
      'Remove the matrix value that adds no coverage, or run it on the main branch only.',
    ),
  'workflow.step_order': (w, s) =>
    WORKFLOW(
      w,
      s || 'the workflow in this suggestion',
      'Move the quick checks before the slow steps, so a failure shows early.',
    ),
  'workflow.cache': (w, s) =>
    WORKFLOW(
      w,
      s || 'the workflow in this suggestion',
      'Add the cache step from the snippet on this page to the job.',
    ),
  'test.slow': (w, s) => [
    `Open the test${s ? ` ${s}` : ''} in ${w}.`,
    'Find what makes it slow: a wait, a download, or a large setup.',
    'Make it faster, or move it to a job that runs after the quick ones.',
    'Merge the change, then read this suggestion again after the next runs.',
  ],
  'test.flaky': (w, s) => [
    `Open the test${s ? ` ${s}` : ''} in ${w}.`,
    'Run it ten times on your machine to see when it fails.',
    'Fix the cause (a shared file, a clock, an order), or mark it as known flaky until you do.',
    'Merge the change, then read this suggestion again after the next runs.',
  ],
  'test.duplicate': (w, s) => [
    `Open the tests${s ? ` named ${s}` : ''} in ${w}.`,
    'Keep the one that checks more, and delete the other.',
    'Merge the change, then read this suggestion again after the next runs.',
  ],
  'test.no_assertions': (w, s) => [
    `Open the test${s ? ` ${s}` : ''} in ${w}.`,
    'Add a check of what it should do, or delete it if it checks nothing.',
    'Merge the change, then read this suggestion again after the next runs.',
  ],
};

const GENERAL = (where: string): readonly string[] => [
  `Read the fix on this page and the evidence under it${where === 'the pool' ? '' : ` for ${where}`}.`,
  'Make the change it names, in the place it names.',
  'Read this suggestion again after the next runs: it goes away when the change works.',
];

/** The numbered steps that carry out a suggestion, the first one naming its repository when it has one. */
export function stepsFor(
  f: Pick<Finding, 'kind' | 'repository' | 'scope' | 'subject'>,
): string[] {
  const where = projectOf(f);
  const make = BY_KIND[f.kind];
  return [...(make ? make(where, f.subject ?? '') : GENERAL(where))];
}
