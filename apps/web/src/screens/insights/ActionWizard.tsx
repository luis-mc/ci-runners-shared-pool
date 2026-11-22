// The action wizard (spec 4.7, UX-30, UX-54, UX-5): Review, Changing, Done. A setup change is an action, never a
// silent change. Review lists each change in numbers and words and says "Nothing has changed yet."; a change that
// lowers slots names the jobs it waits for (UX-62); Done says what really changed. The step, the lock, Stop and the
// busy state come from the shared wizard; this page passes data only (UX-13).
import {
  BulletList,
  Breadcrumbs,
  Message,
  Page,
  PageHeader,
  Stack,
  Table,
  Text,
  Wizard,
  type Column,
  type WizardPhase,
} from '@crsp/ui';
import { useEffect, useState } from 'react';
import { useNavigate, useParams } from 'react-router-dom';
import { errorText, type Schemas } from '../../api/api';
import { planFor, startPlan, useFinding } from './api';
import { Load } from '../common';

type Plan = Schemas['InsightPlan'];
type Change = Schemas['InsightPlanChange'];
const STEPS = ['Review', 'Changing', 'Done'] as const;

const changeColumns: Column<Change>[] = [
  { id: 'target', header: 'What', cell: (c) => c.target_name ?? c.target, kind: 'grow' },
  { id: 'field', header: 'Setting', cell: (c) => c.field.replaceAll('_', ' ') },
  { id: 'from', header: 'Now', cell: (c) => String(c.from) },
  { id: 'to', header: 'After', cell: (c) => String(c.to) },
  {
    id: 'words',
    header: 'In words',
    cell: (c) => c.words,
    kind: 'grow',
    grow: 2,
    secondary: true,
  },
];

export function ActionWizard() {
  const { id } = useParams();
  const finding = useFinding(Number(id));
  const navigate = useNavigate();
  const [plan, setPlan] = useState<Plan | undefined>(undefined);
  const [phase, setPhase] = useState<WizardPhase>('loading');
  const [problem, setProblem] = useState<string | undefined>(undefined);

  useEffect(() => {
    let on = true;
    planFor(Number(id))
      .then((p) => {
        if (!on) return;
        setPlan(p);
        setPhase('ready-to-review');
      })
      .catch((e: unknown) => {
        if (!on) return;
        setProblem(errorText(e));
        setPhase('failed');
      });
    return () => {
      on = false;
    };
  }, [id]);

  const start = async () => {
    if (!plan) return;
    setPhase('running');
    try {
      setPlan(await startPlan(plan));
      setPhase('done');
    } catch (e) {
      setProblem(errorText(e));
      setPhase('failed');
    }
  };

  const step = phase === 'running' ? 1 : phase === 'done' || phase === 'failed' ? 2 : 0;
  const waits = plan?.changes.flatMap((c) => c.jobs_waited_for ?? []) ?? [];
  return (
    <Page>
      <Load result={finding.result} what="this finding" reload={finding.reload}>
        {(f) => (
          <>
            <Breadcrumbs
              trail={[
                { label: 'Jobs', to: '/pool' },
                { label: 'Suggestions', to: '/analytics' },
                { label: f.title, to: `/analytics/finding/${f.id}` },
                { label: 'Review the change' },
              ]}
            />
            <PageHeader title="Review the change" lead={f.title} />
            <Wizard
              name="Change the setup"
              steps={STEPS}
              current={step}
              phase={phase}
              reading="Reading the plan"
              review={phase === 'ready-to-review'}
              primary={{
                label: 'Make the change',
                busyLabel: 'Changing',
                onPress: () => void start(),
              }}
              progress={{ text: 'Making the change' }}
              onBack={() => void navigate(`/analytics/finding/${f.id}`)}
              backLabel="Not now"
              onTryAgain={() => void navigate(0)}
              onStartAgain={() => void navigate('/analytics')}
              donePrimary={{
                label: 'Back to Do next',
                onPress: () => void navigate('/analytics'),
              }}
              failedAt={phase === 'failed' ? 1 : undefined}
            >
              <Stack>
                {plan ? (
                  <>
                    <Table
                      caption="Changes"
                      columns={changeColumns}
                      rows={plan.changes}
                      rowKey={(c) => `${c.target}-${c.field}`}
                    />
                    {plan.estimate ? <Text>{`Estimate: ${plan.estimate}.`}</Text> : null}
                    {waits.length > 0 && phase === 'ready-to-review' ? (
                      <Message tone="note" dismissible={false}>
                        This waits for running jobs to finish:
                        <BulletList items={waits} />
                      </Message>
                    ) : null}
                  </>
                ) : null}
                {phase === 'done' && plan ? (
                  <Text>{`Done. ${plan.results.filter((r) => r.state === 'done').length} of ${plan.changes.length} changes were made.`}</Text>
                ) : null}
                {phase === 'failed' ? (
                  <Message tone="problem" dismissible={false}>
                    {problem ?? 'The change failed.'}
                  </Message>
                ) : null}
              </Stack>
            </Wizard>
          </>
        )}
      </Load>
    </Page>
  );
}
