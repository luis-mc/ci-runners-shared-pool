// One job : where it builds (repository, branch, commit, worktree), its run and attempts, the
// run's job graph, its steps as a waterfall, the runner, the times, the queue place and ETA, the forecast and the
// history of its job key. The one primary is "Open the run on GitHub" (LOOK27); no command changes a job.
import { HelpTerm } from '../help/HelpTerm';
import {
  ActionBar,
  Badge,
  Breadcrumbs,
  Cluster,
  Empty,
  ExternalButton,
  Facts,
  JobGraph,
  LinkButton,
  Page,
  PageHeader,
  Section,
  Sparkline,
  Stack,
  Status,
  Tabs,
  Text,
  TextLink,
  TrendChart,
  Waterfall,
  formatDateTime,
  formatDuration,
  formatNumber,
} from '@crsp/ui';
import { useNavigate, useParams } from 'react-router-dom';
import { Load } from '../common';
import type { Schemas } from '../../api/api';
import { useJobDetail, useRun } from './api';
import { ResourcesPane, StepsSource, runsOnFact } from '../insights/JobInsights';
import { graphSource } from './runBasis';
import { edgesOf, graphNodes, stepRows } from './runViews';
import {
  RUNNER_KIND_WORDS,
  basisWords,
  etaWords,
  forecastWords,
  forecastOf,
  jobStatus,
  median,
  repoPath,
  secondsOrNone,
  taskOf,
  waitWords,
  waitedSeconds,
  type Job,
} from './words';

type JobDetail = Schemas['JobDetail'];

/** Steps are kept 30 days (owner decision 2). */
const STEPS_KEPT_MS = 30 * 86_400_000;

const ghUrl = (u: string | undefined) => (u?.startsWith('https://') ? u : undefined);

/** The repository's page on the GitHub the controller is set to (OPS35): the part of the job's
 * own GitHub address up to the repository, so the console never writes a GitHub host itself. */
function repoPage(jobUrl: string | undefined, repository: string): string | undefined {
  const at = jobUrl?.indexOf(`/${repository}/`) ?? -1;
  return jobUrl && at > 0 ? jobUrl.slice(0, at + repository.length + 1) : undefined;
}

export function JobPage() {
  const { id = '' } = useParams();
  const { result, reload } = useJobDetail(Number(id));
  const j = result.state === 'ready' ? result.data : undefined;
  const url = ghUrl(j?.html_url);
  const repo = j ? repoPage(url, j.repository) : undefined;
  return (
    <Page>
      <Breadcrumbs
        trail={[
          { to: '/pool', label: 'Jobs' },
          ...(j
            ? [
                {
                  to: `/pool/jobs?repository=${encodeURIComponent(j.repository)}`,
                  label: j.repository,
                },
                {
                  to: `/pool/runs/${j.run_id}?attempt=${j.run_attempt}`,
                  label: `Run #${j.run_id}, attempt ${j.run_attempt}`,
                },
              ]
            : []),
          { label: j ? j.job_name : 'Job' },
        ]}
      />
      {j ? null : (
        <PageHeader title="Job" lead="One job: where it built and how long it took." />
      )}
      <Load result={result} what="the job" reload={reload}>
        {(job) => <JobBody j={job} />}
      </Load>
      {j && url ? (
        <ActionBar
          secondary={
            <>
              {repo && j.head_sha ? (
                <ExternalButton href={`${repo}/commit/${j.head_sha}`}>
                  Open the commit
                </ExternalButton>
              ) : null}
              {repo && j.pull_request ? (
                <ExternalButton href={`${repo}/pull/${j.pull_request}`}>
                  Open the pull request
                </ExternalButton>
              ) : null}
            </>
          }
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

function JobBody({ j }: { j: JobDetail }) {
  const now = Date.now();
  const s = jobStatus(j);
  const when = (t: string | undefined) => (t ? formatDateTime(t) : 'not yet');
  const waited = waitedSeconds(j, now);
  const runner = j.runner;
  const steps = stepRows(j, j.steps, now);
  const navigate = useNavigate();
  const ran =
    j.state === 'running' && j.started_at
      ? (now - Date.parse(j.started_at)) / 1000
      : j.run_seconds;
  const forecast = forecastOf(j);
  const durations = j.history.map((h) => h.seconds);
  const last = j.history[j.history.length - 1];
  return (
    <Stack>
      <PageHeader
        title={j.job_name}
        lead={`Job of run ${formatNumber(j.run_id)}, attempt ${j.run_attempt}, in ${j.repository}, workflow ${j.workflow ?? 'not known'}. It has ${j.steps.length} steps.`}
      />
      <Cluster>
        <Status kind={s.kind} word={s.word} />
        {j.state === 'queued' && j.queue_position ? (
          <Badge>{`number ${j.queue_position} in the queue`}</Badge>
        ) : null}
        {j.attempts.length > 1 ? (
          <Badge>{`attempt ${j.run_attempt} of ${j.attempts.length}`}</Badge>
        ) : null}
      </Cluster>
      {j.state === 'queued' ? (
        <Section title="Queue">
          <Facts
            items={[
              {
                name: 'Place',
                value: j.queue_position
                  ? `number ${j.queue_position}`
                  : 'not in the queue yet',
              },
              {
                name: 'Why it waits',
                value: j.queue_position
                  ? waitWords(j)
                  : (j.wait_text ?? 'waits for the jobs it needs'),
              },
              { name: 'Waiting for', value: secondsOrNone(waited) },
              {
                name: 'Should start',
                value: j.queue_eta
                  ? `${etaWords(j.queue_eta)} (a forecast, not a promise)`
                  : 'not known',
              },
            ]}
          />
        </Section>
      ) : null}
      <Section title="Project and code">
        <Facts
          items={[
            {
              name: 'Repository',
              value: <TextLink to={repoPath(j.repository)}>{j.repository}</TextLink>,
            },
            { name: 'Workflow', value: j.workflow ?? 'not known' },
            { name: 'Branch', value: j.branch ?? 'not known' },
            { name: 'Task', value: taskOf(j) },
            { name: 'Worktree', value: j.worktree ?? 'not reported' },
            { name: 'Commit', value: j.head_sha ? j.head_sha.slice(0, 7) : 'not known' },
            {
              name: 'Pull request',
              value: j.pull_request ? `#${j.pull_request}` : 'none',
            },
            { name: 'Started by', value: j.trigger ?? 'not known' },
          ]}
        />
      </Section>
      <Section title="Run and attempts">
        <Stack>
          {j.attempts.length > 1 ? (
            <Tabs
              label="Attempts of this job"
              tabs={j.attempts.map((a) => ({
                id: String(a.job_id),
                label: `Attempt ${a.attempt}${a.result ? `, ${a.result === 'success' ? 'success' : a.result.replace('_', ' ')}` : ''}`,
              }))}
              current={String(j.id)}
              onChange={(id) => void navigate(`/pool/jobs/${id}`)}
            />
          ) : null}
          <Text tone="muted">
            {`This job is one of the jobs of run ${formatNumber(j.run_id)}. The graph shows how they depend on each other; the line marked critical path is the longest chain.`}
          </Text>
          <RunGraph job={j} />
          <Cluster>
            <LinkButton to={`/pool/runs/${j.run_id}?attempt=${j.run_attempt}`}>
              Open the whole run
            </LinkButton>
          </Cluster>
        </Stack>
      </Section>
      <Section
        title="Steps"
        hint="Step times are from GitHub. Each step is on one time line, with its forecast range after the bar."
      >
        {j.state === 'queued' ? (
          <Empty title="The job has not started, so no step has run. The forecast of each step shows once it runs." />
        ) : (
          <Waterfall
            label={`Steps of ${j.job_name}`}
            rows={steps.rows}
            {...(steps.nowSec !== undefined ? { nowSec: steps.nowSec } : {})}
            emptyText={
              j.completed_at && now - Date.parse(j.completed_at) > STEPS_KEPT_MS
                ? 'Steps are no longer kept for a job this old.'
                : 'This job reported no steps.'
            }
          />
        )}
        <StepsSource j={j} />
      </Section>
      <Section title="Runner">
        <Facts items={[runsOnFact(j)]} />
        {runner ? (
          <Facts
            items={[
              { name: 'Runner', value: runner.name },
              {
                name: 'Runner type',
                value: RUNNER_KIND_WORDS[runner.kind],
                help: <HelpTerm term="runnerType" />,
              },
              { name: 'Machine', value: runner.machine_name ?? 'not known' },
              {
                name: 'Backend',
                value: `${runner.backend ?? 'not known'}${runner.isolation ? `, ${runner.isolation}` : ''}`,
              },
              {
                name: 'Cache',
                value:
                  runner.cache === 'warm'
                    ? 'warm, the caches were ready'
                    : runner.cache === 'cold'
                      ? 'cold, the caches were rebuilt'
                      : 'not reported',
              },
              { name: 'Labels', value: j.labels.join(', ') || 'none' },
            ]}
          />
        ) : j.runner_origin ? null : (
          <Empty title="No runner yet. The pool places the job when a slot is free." />
        )}
      </Section>
      {runner && j.state !== 'queued' ? <ResourcesPane jobId={j.id} /> : null}
      <Section title="Times">
        <Facts
          items={[
            { name: 'Queued at', value: when(j.queued_at) },
            {
              name: 'Waited',
              value:
                waited === undefined
                  ? 'not yet'
                  : `${formatDuration(waited)}${j.wait_reason && j.state !== 'queued' ? `, ${waitWords(j)}` : ''}`,
            },
            { name: 'Started', value: when(j.started_at) },
            { name: 'Run time', value: secondsOrNone(ran) },
            {
              name: 'Clean-up',
              value:
                j.cleanup_seconds !== undefined
                  ? formatDuration(j.cleanup_seconds)
                  : 'not yet',
            },
            { name: 'Finished', value: when(j.completed_at) },
          ]}
        />
      </Section>
      <Section title="Forecast">
        <Facts
          items={[
            {
              name: 'This job should take',
              value: `${forecastWords(forecast)}, ${basisWords(forecast)}`,
            },
            ...(j.run_eta && j.state !== 'done'
              ? [{ name: 'The run should end', value: etaWords(j.run_eta) }]
              : []),
            ...(j.state === 'done' &&
            forecast.basis !== 'not_enough_history' &&
            j.run_seconds !== undefined
              ? [
                  {
                    name: 'It took',
                    value: `${formatDuration(j.run_seconds)}, ${
                      j.run_seconds >= forecast.lo && j.run_seconds <= forecast.hi
                        ? 'inside the range'
                        : j.run_seconds > forecast.hi
                          ? 'slower than the range'
                          : 'faster than the range'
                    }`,
                  },
                ]
              : []),
          ]}
        />
      </Section>
      <Section
        title="History of this job"
        hint={`The same job in earlier runs: ${j.job_ident ?? j.job_name}.`}
      >
        {durations.length < 2 ? (
          <Empty title="Not enough runs yet to draw a history." />
        ) : (
          <Stack>
            <Facts
              items={[
                { name: 'Runs kept', value: formatNumber(durations.length) },
                { name: 'Middle run time', value: formatDuration(median(durations)) },
                {
                  name: 'Last run',
                  value: last
                    ? `${formatDuration(last.seconds)}, ${(last.result ?? 'done').replace('_', ' ')}`
                    : 'none',
                },
                {
                  name: 'Trend',
                  value: (
                    <Sparkline
                      label={`Run times of ${j.job_name}, last ${durations.length} runs`}
                      values={durations}
                      unit="seconds"
                      markLast
                    />
                  ),
                },
              ]}
            />
            <TrendChart
              label={`Run time of ${j.job_name} over its last ${durations.length} runs`}
              unit="seconds"
              emptyText="No runs yet."
              series={[
                {
                  id: 'run',
                  label: 'Run time',
                  points: j.history.map((h) => ({
                    x: Date.parse(h.at),
                    y: h.seconds,
                    note: `${(h.result ?? 'done').replace('_', ' ')} on ${h.branch}`,
                  })),
                },
              ]}
            />
            <Text tone="muted">{`Forecast range of this job: ${forecastWords(forecast)}.`}</Text>
          </Stack>
        )}
      </Section>
    </Stack>
  );
}

function RunGraph({ job }: { job: Job }) {
  const navigate = useNavigate();
  const { result, reload } = useRun(job.run_id, job.run_attempt, true);
  return (
    <Load result={result} what="the run" reload={reload}>
      {(run) => (
        <Stack>
          <Text tone="muted">{graphSource(run)}</Text>
          <JobGraph
            label={`Jobs of run ${run.id}`}
            nodes={graphNodes(
              run,
              Date.now(),
              (x) => void navigate(`/pool/jobs/${x.id}`),
              job.id,
            )}
            edges={edgesOf(run)}
            emptyText="This run has no other jobs."
          />
        </Stack>
      )}
    </Load>
  );
}
