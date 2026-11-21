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

/** A card of the Questions section. All cards show at once, in the order of the box each one explains. */
export interface HelpCard {
  id: string;
  title: string;
  answer: string;
  /** The part of the picture the card explains (LOOK48); the cards are in the order of these parts. */
  box: string;
}

export const CARDS: readonly HelpCard[] = [
  {
    id: 'q-shared',
    box: 'repository',
    title: 'What does shared mean?',
    answer:
      'Every repository you turn the pool on for uses the same machines and runners.',
  },
  {
    id: 'q-run',
    box: 'run',
    title: 'What are a run and a job?',
    answer:
      'A run is one start of a workflow, for example a push. A job is one part of a run.',
  },
  {
    id: 'q-labels',
    box: 'job',
    title: 'What are labels?',
    answer:
      'Words on a job and on a runner. A job goes to a runner that has all of its labels.',
  },
  {
    id: 'q-elsewhere',
    box: 'job',
    title: 'What does Run elsewhere mean?',
    answer:
      'Another runner runs the job, not your pool, for example a GitHub-hosted runner.',
  },
  {
    id: 'q-waiting',
    box: 'queue',
    title: 'Why is my job waiting?',
    answer:
      'A job waits for a reason: no free slot, over the load budget, or a limit. The Jobs page says which, in words.',
  },
  {
    id: 'q-queue',
    box: 'queue',
    title: 'What is the queue?',
    answer:
      'Jobs wait in a queue, one for each machine, until a slot is free. The first job takes it.',
  },
  {
    id: 'q-slot',
    box: 'slot',
    title: 'What is a slot?',
    answer:
      'Room for one job at a time on a machine. Pool size makes slots: you set memory and CPUs on Pool size, and crsp turns that into slots, the number of jobs that run at once.',
  },
  {
    id: 'q-runner-type',
    box: 'runner',
    title: 'What is a runner type?',
    answer:
      'Persistent keeps its tools and caches. Ephemeral is fresh for every job. Linux or Windows is the system, not the type.',
  },
  {
    id: 'q-images',
    box: 'runner',
    title: 'What are images and caches?',
    answer:
      'Images are what a container runner starts from. Caches keep downloads, so the next job starts faster.',
  },
  {
    id: 'q-budget',
    box: 'machine',
    title: 'What is the load budget?',
    answer: 'The most memory and CPU a machine may use for jobs. Over it, new jobs wait.',
  },
  {
    id: 'q-tray',
    box: 'machine',
    title: 'What is the tray app?',
    answer:
      'A small icon on the PC. From it you open the console, pause the pool or update crsp.',
  },
  {
    id: 'q-doctor',
    box: 'machine',
    title: 'What is the Doctor?',
    answer: 'It reads the whole pool and lists the problems it finds, each with one fix.',
  },
];

/** The terms that carry a ? in the console, with the answer shown in the popover. */
export const TERMS = {
  slot: {
    term: 'Slots',
    card: 'q-slot',
    answer: CARDS.find((c) => c.id === 'q-slot')!.answer,
  },
  budget: {
    term: 'Load budget',
    card: 'q-budget',
    answer: CARDS.find((c) => c.id === 'q-budget')!.answer,
  },
  queue: {
    term: 'Queue',
    card: 'q-queue',
    answer: CARDS.find((c) => c.id === 'q-queue')!.answer,
  },
  runnerType: {
    term: 'Runner type',
    card: 'q-runner-type',
    answer: CARDS.find((c) => c.id === 'q-runner-type')!.answer,
  },
  elsewhere: {
    term: 'Run elsewhere',
    card: 'q-elsewhere',
    answer: CARDS.find((c) => c.id === 'q-elsewhere')!.answer,
  },
} as const;

export type TermKey = keyof typeof TERMS;

/** The card a part of the picture scrolls to: the first card that explains it. Nothing is ever locked. */
export const PART_CARD: Readonly<Record<string, string>> = Object.fromEntries(
  PATH_PARTS.map((p) => [p.id, CARDS.find((c) => c.box === p.id)?.id ?? '']),
);

/** The number and name of a part of the picture, for the badge of a card (`Box 5: Slot`). */
export function boxOf(id: string): { id: string; n: number; name: string } {
  const i = PATH_PARTS.findIndex((p) => p.id === id);
  return { id, n: i + 1, name: PATH_PARTS[i]?.title ?? '' };
}

/** The words around the picture and the cards (content.md, Help words). */
export const HELP_WORDS = {
  lead: 'One picture, then the questions people ask. Each card names its box.',
  pictureNote: '1 to 3 happen on GitHub. 4 to 7 happen in your pool.',
  questions: 'Questions, in the order a job meets them',
} as const;
