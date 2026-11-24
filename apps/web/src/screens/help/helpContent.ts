// The words of the Help page (LOOK44, LOOK47), in one place: the parts of the job path and the answers.
// English only. Each answer is one or two short sentences (content.md, Help words).
import type { JobPathGroup, JobPathPart } from '@crsp/ui';

export const PATH_GROUPS: readonly JobPathGroup[] = [
  { id: 'github', label: 'On GitHub' },
  { id: 'pool', label: 'In your pool' },
];

export const PATH_PARTS: readonly JobPathPart[] = [
  { id: 'repository', title: 'Repository', line: 'a GitHub project', group: 'github' },
  { id: 'run', title: 'Run', line: 'one start of a workflow', group: 'github' },
  { id: 'job', title: 'Job', line: 'asks for a runner', group: 'github' },
  { id: 'queue', title: 'Queue', line: 'waits for a slot', group: 'pool' },
  { id: 'slot', title: 'Slot', line: 'room for one job', group: 'pool' },
  { id: 'runner', title: 'Runner', line: 'does the work', group: 'pool' },
  { id: 'machine', title: 'Machine', line: 'your computer', group: 'pool' },
];

/** A card of the Questions section: the question, a short answer and one link into the console (LOOK63). */
export interface HelpCard {
  id: string;
  title: string;
  answer: string;
  link: { label: string; to: string };
}

export const CARDS: readonly HelpCard[] = [
  {
    id: 'q-shared',
    title: 'What does shared mean?',
    answer:
      'Every repository you turn the pool on for uses the same machines and runners.',
    link: { label: 'Open Setup > Pool setup', to: '/setup/pool' },
  },
  {
    id: 'q-run',
    title: 'What are a run and a job?',
    answer: 'A run is one start of a workflow. A job is one part of a run.',
    link: { label: 'Open Pool > Jobs', to: '/pool/jobs' },
  },
  {
    id: 'q-labels',
    title: 'What are labels?',
    answer:
      'Words on a job and on a runner. A job goes to a runner that has all of its labels.',
    link: { label: 'Open Pool > Overview', to: '/pool' },
  },
  {
    id: 'q-elsewhere',
    title: 'What does Run elsewhere mean?',
    answer: 'Another runner runs the job, not your pool.',
    link: { label: 'Open Analytics > Run elsewhere', to: '/analytics/elsewhere' },
  },
  {
    id: 'q-waiting',
    title: 'Why is my job waiting?',
    answer: 'Open Why waiting on its row. Queue Analysis ranks every waiting job.',
    link: { label: 'Open Pool > Queue Analysis', to: '/pool/queue' },
  },
  {
    id: 'q-slot',
    title: 'How big is a slot?',
    answer: 'The pool is what is left after the load budget.',
    link: { label: 'Open Setup > Size and backends', to: '/setup/size' },
  },
  {
    id: 'q-now',
    title: 'Where do I see what runs now?',
    answer: 'The picture of running and queued jobs.',
    link: { label: 'Open Pool > Live', to: '/pool/live' },
  },
  {
    id: 'q-wrong',
    title: 'Something is wrong',
    answer: 'Doctor lists what needs you and checks the pool.',
    link: { label: 'Open Doctor', to: '/doctor' },
  },
  {
    id: 'q-faster',
    title: 'How do I make it faster?',
    answer: 'Suggestions are ranked by the time they save.',
    link: { label: 'Open Analytics > Suggestions', to: '/analytics' },
  },
];

/** The terms that carry a ? in the console: the short answer in the popover and the Help section it links to. */
export const TERMS = {
  slot: {
    term: 'Slots',
    to: '/help/pool-and-load-budget',
    answer:
      'Room for one job at a time on a machine. Pool size makes slots: you set memory and CPUs on Pool size, and crsp turns that into slots, the number of jobs that run at once.',
  },
  budget: {
    term: 'Load budget',
    to: '/help/pool-and-load-budget',
    answer: 'The most memory and CPU a machine may use for jobs. Over it, new jobs wait.',
  },
  queue: {
    term: 'Queue',
    to: '/help/before-assignment',
    answer:
      'Jobs wait in a queue, one for each machine, until a slot is free. The first job takes it.',
  },
  runnerType: {
    term: 'Runner type',
    to: '/help/runner-types',
    answer:
      'Persistent keeps its tools and caches. Ephemeral is fresh for every job. Linux or Windows is the system, not the type.',
  },
  elsewhere: {
    term: 'Run elsewhere',
    to: '/help#q-elsewhere',
    answer:
      'Another runner runs the job, not your pool, for example a GitHub-hosted runner.',
  },
} as const;

export type TermKey = keyof typeof TERMS;

/** The topics of the Help side menu, in the order of the approved prototype (LOOK54, LOOK63). */
export const TOPICS = [
  { to: '/help', label: 'Start here' },
  { to: '/help/where', label: 'Where things are' },
  { to: '/help/pool-and-load-budget', label: 'Pool and load budget' },
  { to: '/help/runner-types', label: 'Runner types' },
  { to: '/help/size-classes', label: 'Size classes' },
  { to: '/help/admission', label: 'Admission and passing' },
  { to: '/help/reservation', label: 'Reservation' },
  { to: '/help/job-size', label: 'Job size' },
  { to: '/help/before-assignment', label: 'Before assignment' },
  { to: '/help/gating', label: 'Gating' },
  { to: '/help/routing-labels', label: 'Routing labels' },
  { to: '/help/timers', label: 'Timers' },
  { to: '/help/queue', label: 'Reading Queue Analysis' },
] as const;

/** A guide section: what it is in one sentence, the facts in a short list, and a link when a screen shows it. */
export interface GuideSection {
  title: string;
  lead: string;
  points: readonly string[];
  link?: { label: string; to: string };
}

/** The guide sections by the last part of their address. Each is written from what the scheduler really does. */
export const GUIDE: Readonly<Record<string, GuideSection>> = {
  'pool-and-load-budget': {
    title: 'Pool and load budget',
    lead: 'The pool is the memory and CPUs crsp may use for jobs on a machine. The load budget is the most the whole machine may use.',
    points: [
      'The default budget is 85% of memory and 60% of CPU, for everything on the machine, not only jobs.',
      'You set the pool size on Size and backends. A size that does not fit the budget is refused, and the page names the sizes that do.',
      'Over the budget, a new job waits until the load is under it. Jobs that already run are never stopped for it.',
      'When crsp cannot measure the load, a new job waits until it can.',
      'A machine over its budget while jobs run shows a warning. It clears after a minute under the budget.',
    ],
    link: { label: 'Open Setup > Size and backends', to: '/setup/size' },
  },
  'runner-types': {
    title: 'Runner types',
    lead: 'A runner is persistent or ephemeral. That is its type. Linux, Windows or macOS is its system, not its type.',
    points: [
      'Persistent keeps its tools, caches and folder between jobs. It runs on the machine itself, as a service.',
      'Ephemeral is a fresh container or VM for one job. crsp removes it when the job ends.',
      'You choose the type when you add a runner. The list shows only the machines that can run that system and type.',
      'A persistent runner on the machine itself is gated: see Gating.',
    ],
    link: { label: 'Open Setup > Runners', to: '/setup/runners' },
  },
  'size-classes': {
    title: 'Size classes',
    lead: 'A runner has a size class: small, medium, large or xl. The class is the biggest job that runner takes.',
    points: [
      'Each class has CPUs and GiB of memory. The Standard preset is small 2 CPUs and 4 GiB, medium 4 and 8, large 8 and 16, xl 16 and 32.',
      'The classes grow from small to xl, in CPUs and in memory.',
      'A job that needs more than the class of a runner does not go to that runner.',
    ],
  },
  admission: {
    title: 'Admission and passing',
    lead: 'A job is admitted when its CPU and memory fit the free pool and the class ceiling. Lighter jobs may pass a big one that does not fit yet.',
    points: [
      'Check 1: class ceiling. Check 2: free CPU. Check 3: free memory.',
      'A big job can be passed up to 3 times, or for 10 min.',
      'Then it is reserved (see Reservation).',
    ],
    link: { label: 'See it live in Pool > Live', to: '/pool/live' },
  },
  reservation: {
    title: 'Reservation',
    lead: 'A job that is passed too often is reserved: its machine keeps room for it.',
    points: [
      'After 3 passes, or 10 minutes of waiting, the job becomes the reserved job of its machine. A machine has one at a time.',
      'Its line reads reserved since a time, expected start a time. The expected start is when a running job ends and leaves room.',
      'A later job starts only if it ends before that time, or if the pool still has room for the reserved job.',
      'The reservation ends when the job starts.',
    ],
    link: { label: 'Open Pool > Queue Analysis', to: '/pool/queue' },
  },
  'job-size': {
    title: 'Job size',
    lead: 'crsp gives every job a size: the CPUs and memory it needs. Admission reads it.',
    points: [
      'The size comes from the first source that knows the job: your override in the console, the sizes in .crsp.yml, the size crsp measured, a scan of the repository, the job name, then 1 CPU and 1 GiB.',
      'A measured size counts after 5 runs.',
      'In .crsp.yml, a job name or pattern is light, normal or heavy.',
      'Sizing never holds a job. A new job with no history uses the scan or the default.',
    ],
    link: { label: 'Open Setup > Pool setup', to: '/setup/pool' },
  },
  'before-assignment': {
    title: 'Before assignment',
    lead: 'Before GitHub gives a job to a runner, the job waits in the queue. crsp decides when it may start.',
    points: [
      'crsp learns of a job when GitHub tells it, and by looking again every so often.',
      'By default the order is: repository priority, then pull requests that are ready and the default branch, then fair share between repositories, then the one that waited longest.',
      'The first job that has room gets a runner. A job that has none waits for one reason, and Why waiting names it.',
      'A repository may have a limit on parallel jobs. A job over it waits, and jobs of other repositories may take the free slot.',
      'crsp tells you when a job has waited 12 hours. GitHub cancels a job that waits 24 hours.',
    ],
    link: { label: 'Open Pool > Queue Analysis', to: '/pool/queue' },
  },
  gating: {
    title: 'Gating',
    lead: 'A persistent runner on the machine itself is gated: it is offline by design, and crsp switches it on only when a job needs it.',
    points: [
      'When a job is admitted for it, crsp adds the job name as a label and starts the runner. The runner takes the job, and crsp makes no other runner.',
      'When the job ends the runner stops and the label is removed. A runner with a job is never stopped.',
      'Once a week each gated runner is switched on for about a minute, so GitHub never removes it for staying offline.',
      'Gating starts in observe mode: crsp writes what it would do, and the job takes the normal way.',
    ],
    link: { label: 'Open Setup > Runners', to: '/setup/runners' },
  },
  'routing-labels': {
    title: 'Routing labels',
    lead: 'Routing labels send a job to your pool. The workflow reads them from a variable of the repository.',
    points: [
      'For each system crsp writes CRSP_RUNS_ON_LINUX, CRSP_RUNS_ON_WINDOWS or CRSP_RUNS_ON_MACOS: a list of labels, for example self-hosted, linux, x64.',
      'A variable exists only while the pool is on for that system. With no variable the job runs on a GitHub-hosted runner.',
      'A job may have a variable of its own, CRSP_ and its name, for example CRSP_E2E. The workflow reads it first and the system variable second.',
      'Labels are not case sensitive. If a variable is changed outside crsp, crsp puts it back and the repository row shows it.',
    ],
    link: { label: 'Open Setup > Pool setup', to: '/setup/pool' },
  },
  timers: {
    title: 'Timers',
    lead: 'crsp times every running job and every run. When a timer runs out, crsp waits 30 seconds, then acts.',
    points: [
      'The job timer is 30 minutes from the first real step of the job. The run timer is 45 minutes from the start of its first job. A repository may set its own job times in .crsp.yml.',
      'In the 30 seconds you can press Stop now, Cancel now or Extend 10 min. Extend works once for each job.',
      'A job that ends in those 30 seconds is not stopped.',
      'When the job timer ends, crsp stops that job. When the run timer ends, crsp cancels the run.',
      'Timers start in observe mode: they show and write what they would do, and stop nothing until the owner turns Enforce on.',
      'A job that holds a start waits at most 2 minutes.',
    ],
  },
};

/** Where things are: each part of the console and what it is for. */
export const WHERE_AREAS: readonly { name: string; line: string; to: string }[] = [
  { name: 'Overview', line: 'The pool at a glance.', to: '/' },
  {
    name: 'Setup',
    line: 'Machines, runners, sizes, Pool setup. Change things here.',
    to: '/setup/summary',
  },
  {
    name: 'Pool',
    line: 'Overview, Live, Queue Analysis, Jobs, Machines. Watch and act.',
    to: '/pool',
  },
  { name: 'Doctor', line: 'Needs you and checks. Something is wrong.', to: '/doctor' },
  {
    name: 'Analytics',
    line: 'Suggestions, Insights, Charts, Run elsewhere. Could be better.',
    to: '/analytics',
  },
  { name: 'Settings', line: 'Access, repositories, console.', to: '/settings' },
  { name: 'Help', line: 'This guide.', to: '/help' },
];

export const WHERE_TABS: readonly {
  section: string;
  tab: string;
  line: string;
  to: string;
}[] = [
  {
    section: 'Setup',
    tab: 'Summary',
    line: 'See the setup at a glance.',
    to: '/setup/summary',
  },
  {
    section: 'Setup',
    tab: 'Machines',
    line: 'Change a machine and its pool size.',
    to: '/setup/machines',
  },
  {
    section: 'Setup',
    tab: 'Runners',
    line: 'Add and remove runners.',
    to: '/setup/runners',
  },
  {
    section: 'Setup',
    tab: 'Images and caches',
    line: 'Clean images and caches.',
    to: '/setup/images',
  },
  {
    section: 'Setup',
    tab: 'Size and backends',
    line: 'Set size classes and backends.',
    to: '/setup/size',
  },
  {
    section: 'Setup',
    tab: 'Pool setup',
    line: 'Turn the pool on for a repository.',
    to: '/setup/pool',
  },
  {
    section: 'Pool',
    tab: 'Overview',
    line: 'Read the queue plan and the running jobs.',
    to: '/pool',
  },
  {
    section: 'Pool',
    tab: 'Live',
    line: 'See running and queued jobs on a timeline.',
    to: '/pool/live',
  },
  {
    section: 'Pool',
    tab: 'Queue Analysis',
    line: 'See why each job waits and when it starts.',
    to: '/pool/queue',
  },
  {
    section: 'Pool',
    tab: 'Jobs',
    line: 'Find any job, and ask why it waits.',
    to: '/pool/jobs',
  },
  {
    section: 'Pool',
    tab: 'Machines',
    line: 'See what runs where.',
    to: '/pool/machines',
  },
];

/** The words around the picture and the cards (content.md, Help words). */
export const HELP_WORDS = {
  lead: 'How crsp runs jobs. Pictures use your own pool.',
  questions: 'Questions',
  whereLead:
    'Setup is where you change things. Pool is where you watch and act. Doctor is for what is wrong now. Analytics is for what could be better.',
  queueLead:
    'Queue Analysis ranks every waiting job in the order crsp will start it. Read it from the top.',
  sections: 'Help sections',
  nowTitle: 'In your pool right now',
  nowNone: 'Nothing is waiting. Every job has a slot.',
} as const;
