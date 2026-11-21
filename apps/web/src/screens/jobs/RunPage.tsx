// One run : every job of the run as a waterfall (queue wait, then the run) and as a graph.
import {
  ActionBar,
  Badge,
  Breadcrumbs,
  Cluster,
  ExternalButton,
  Facts,
  JobGraph,
  Page,
  PageHeader,
  Section,
  Stack,
  Status,
  Tabs,
  Text,
  TextLink,
  Waterfall,
  formatDateTime,
  formatNumber,
} from '@crsp/ui';
import { useNavigate, useParams, useSearchParams } from 'react-router-dom';
import { SavingsPane } from '../insights/JobInsights';
import { Load } from '../common';
import type { Schemas } from '../../api/api';
import { useRun } from './api';
import { edgesOf, graphNodes, runRows } from './runViews';
import { RUN_JOBS_ID, RunJobsTable } from './RunJobs';
import { basisFor, graphSource } from './runBasis';
import {
  FOLLOW_UP_RUNNING,
  etaWords,
  followUpWords,
  jobStatus,
  repoPath,
  sourceWords,
  taskOf,
} from './words';

type RunItem = Schemas['RunItem'];

export function RunPage() {
  const { runId = '' } = useParams();
  const [params, setParams] = useSearchParams();
  const attempt = Number(params.get('attempt')) || undefined;
  const { result, reload } = useRun(Number(runId), attempt, true);
  const run = result.state === 'ready' ? result.data : undefined;
  const url = run?.html_url.startsWith('https://') ? run.html_url : undefined;
  return (
    <Page>
      <Breadcrumbs
        trail={[
          { to: '/jobs', label: 'Jobs' },
          ...(run
            ? [
                {
                  to: `/jobs/done?repository=${encodeURIComponent(run.repository)}`,
                  label: run.repository,
                },
                { label: `Run #${run.id}, attempt ${run.attempt}` },
              ]
            : [{ label: 'Run' }]),
        ]}
      />
      {run ? null : (
        <PageHeader title="Run" lead="One workflow run and all of its jobs." />
      )}
      <Load result={result} what="the run" reload={reload}>
        {(r) => <RunBody run={r} onAttempt={(a) => setParams({ attempt: String(a) })} />}
      </Load>
      {url ? (
        <ActionBar
          primary={
            <ExternalButton href={url} kind="primary">
              Open the run on GitHub
            </ExternalButton>
          }
        />
      ) : null}
    </Page>
  );
}

function RunBody({ run, onAttempt }: { run: RunItem; onAttempt: (a: number) => void }) {
  const navigate = useNavigate();
  const now = Date.now();
  const open = (j: { id: number }) => void navigate(`/jobs/${j.id}`);
  const view = runRows(run, now, open);
  const { kind, word } = jobStatus(run);
  return (
    <Stack>
      <PageHeader
        title={`${run.workflow} on ${run.branch}`}
        lead={`${run.repository}, run ${formatNumber(run.id)}, attempt ${run.attempt}. This run has ${run.jobs.length} ${run.jobs.length === 1 ? 'job' : 'jobs'}; ${run.jobs.length === 1 ? 'the job has steps' : 'each job has steps'}.`}
      />
      <Cluster>
        <Status kind={kind} word={word} />
        {run.overall_state === 'follow_up_running' ? (
          <Status kind="running" word={FOLLOW_UP_RUNNING} />
        ) : null}
        {run.attempts.length > 1 ? (
          <Badge>{`${run.attempts.length} attempts`}</Badge>
        ) : null}
      </Cluster>
      <Section title="Run">
        <Stack>
          {run.attempts.length > 1 ? (
            <Tabs
              label="Attempts of this run"
              tabs={run.attempts.map((a) => ({ id: String(a), label: `Attempt ${a}` }))}
              current={String(run.attempt)}
              onChange={(a) => onAttempt(Number(a))}
            />
          ) : null}
          <Facts
            items={[
              {
                name: 'Repository',
                value: (
                  <TextLink to={repoPath(run.repository)}>{run.repository}</TextLink>
                ),
              },
              { name: 'Branch', value: run.branch },
              { name: 'Task', value: taskOf(run) },
              ...(run.worktree !== run.branch
                ? [{ name: 'Worktree', value: run.worktree ?? 'not reported' }]
                : []),
              { name: 'Commit and event', value: sourceWords(run) },
              {
                name: 'Pull request',
                value: run.pull_request ? `#${run.pull_request}` : 'none',
              },
              { name: 'Started', value: formatDateTime(run.created_at) },
              ...(run.started_next.length > 0
                ? [
                    {
                      name: 'Started next',
                      value: (
                        <Stack>
                          {run.started_next.map((f) => (
                            <Text key={f.run_id}>
                              <TextLink to={`/jobs/run/${f.run_id}`}>
                                {f.workflow_name}
                              </TextLink>
                              {followUpWords(f).slice(f.workflow_name.length)}
                            </Text>
                          ))}
                        </Stack>
                      ),
                    },
                  ]
                : []),
              ...(run.eta
                ? [
                    {
                      name: 'Should end',
                      value: `${etaWords(run.eta)}, ${basisFor(run)}`,
                    },
                  ]
                : []),
            ]}
          />
        </Stack>
      </Section>
      <Section
        title="Job graph"
        hint="An arrow means the job on its right needs the job on its left. The thick path is the longest chain. The table under Jobs says the same in words."
      >
        <Stack>
          <Text tone="muted">{graphSource(run)}</Text>
          <JobGraph
            label={`Jobs of run ${run.id}`}
            nodes={graphNodes(run, now, open)}
            edges={edgesOf(run)}
            emptyText="This run has no jobs."
            altTable={RUN_JOBS_ID}
          />
        </Stack>
      </Section>
      <SavingsPane runId={run.id} />
      <Section
        title="Timeline"
        hint="Each job waits in the queue (light part), then runs. A bracket is the forecast of the end. The table under Jobs says the same in words."
      >
        <Waterfall
          label={`Jobs of run ${run.id}`}
          rows={view.rows}
          totalSec={view.totalSec}
          {...(view.nowSec !== undefined ? { nowSec: view.nowSec } : {})}
          emptyText="This run has no jobs."
          altTable={RUN_JOBS_ID}
        />
      </Section>
      {run.jobs.length > 0 ? (
        <Section
          title="Jobs"
          hint="Every job of the run once, with the critical path marked."
        >
          <RunJobsTable run={run} now={now} onOpen={open} />
        </Section>
      ) : null}
    </Stack>
  );
}
