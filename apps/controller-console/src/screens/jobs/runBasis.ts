// The basis of a run's end forecast, and where the graph arrows come from, in words (owner decisions 3 and 4).
import type { Schemas } from '../../api/api';

type Run = Schemas['RunItem'];

export function basisFor(run: Run): string {
  const n = run.critical_path.length;
  if (run.graph.source !== 'workflow_file' || n === 0)
    return 'longest job still to run, no dependencies known';
  return `along the longest chain of ${n} ${n === 1 ? 'job' : 'jobs'}, adding the middle runs of each (quartiles for the range)`;
}

export function graphSource(run: Run): string {
  if (run.graph.source !== 'workflow_file')
    return 'The workflow file at this commit could not be read, so no arrows are drawn: jobs that ran at the same time are shown side by side. This is a guess from timing, not from the workflow.';
  return `Arrows come from the workflow file at commit ${run.head_sha.slice(0, 7)} (read once per commit and kept). A matrix or a reusable workflow is marked "expands at run time".`;
}
