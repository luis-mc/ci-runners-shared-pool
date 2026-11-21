// A run of four jobs with long names for the run page guards (OPS42): a diamond of needs and a critical path.
import { doneJob } from './jobs-doctor-fixtures';

const job = (id: number, name: string, needs: string[], over = {}) =>
  doneJob(id, { job_name: name, node_id: name, needs, run_id: 100, ...over });

export const RUN = {
  id: 100,
  attempt: 1,
  attempts: [1],
  repository: 'octo/app',
  repository_id: 7,
  workflow: 'ci.yml',
  branch: 'OPS42-run-page-one-jobs-table',
  head_sha: 'a1b2c3d4e5f6',
  task_id: 'OPS42',
  trigger: 'pull_request',
  pull_request: 52,
  worktree: 'OPS42-run-page-one-jobs-table',
  html_url: 'https://github.com/octo/app/actions/runs/100',
  state: 'done',
  result: 'success',
  created_at: '2026-11-13T09:00:00Z',
  completed_at: '2026-11-13T09:08:00Z',
  overall_state: 'done',
  started_next: [],
  jobs: [
    job(1, 'lint', []),
    job(2, 'unit tests of the web console and the shared parts', ['lint']),
    job(3, 'build [linux, arm64]', ['lint']),
    job(4, 'package', [
      'unit tests of the web console and the shared parts',
      'build [linux, arm64]',
    ]),
  ],
  edges: [
    ['lint', 'unit tests of the web console and the shared parts'],
    ['lint', 'build [linux, arm64]'],
    ['unit tests of the web console and the shared parts', 'package'],
    ['build [linux, arm64]', 'package'],
  ],
  critical_path: ['lint', 'build [linux, arm64]', 'package'],
  graph: { source: 'workflow_file', dynamic_nodes: [] },
};
