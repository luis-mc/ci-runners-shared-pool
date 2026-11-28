// The busy queue of CRSP391, shared by the layout spec and the queue rows spec.

/** 16 waiting jobs, 7 of them for a label no runner carries, and 3 running jobs with long names. */
export function busyQueue() {
  const queuedJob = (id: number, noFit: boolean) => ({
    id,
    repository: 'luis-mc/comics-organizer',
    run_id: 44723818000 + id,
    run_attempt: 1,
    github_job_id: id,
    job_name: id % 3 === 0 ? 'build-and-test-the-whole-solution' : 'lint',
    labels: ['self-hosted', 'linux', 'pool-main', 'repo-comics-organizer'],
    state: 'queued',
    queue_position: id,
    queued_at: '2026-11-24T10:00:00Z',
    wait_reason: noFit ? 'no_runner_with_labels' : 'free_slot',
    wait_text: noFit ? 'pool-main' : undefined,
  });
  const runningJob = (id: number) => ({
    ...queuedJob(id, false),
    job_name: 'main-comics-organizer-build-windows-x64',
    state: 'running',
    machine_id: 'm1',
    runner_name: 'main-comics-organizer-pool-runner-1',
    started_at: new Date(Date.now() - 5 * 60_000).toISOString(),
  });
  return {
    machines: [],
    queued_count: 16,
    queued: Array.from({ length: 16 }, (_, i) => queuedJob(i + 1, i < 7)),
    running: [101, 102, 103].map(runningJob),
    last_event_id: '1',
  };
}
