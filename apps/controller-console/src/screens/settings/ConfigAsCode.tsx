// Settings, Config as code (operations R5.28 to R5.31): export the setup as one file, then plan and apply a file
// through the shared wizard: File, Review, Applying, Done. Review lists each change and changes nothing; each
// removal needs its own yes. The page passes data only; the step, lock and busy state are the wizard's.
import {
  BulletList,
  Breadcrumbs,
  CheckRow,
  ExportButton,
  FileField,
  Facts,
  LinkButton,
  Message,
  Page,
  PageHeader,
  Section,
  Stack,
  Text,
  Wizard,
  type WizardPhase,
} from '@crsp/ui';
import { useState } from 'react';
import { errorText, unwrap, useClient, type Schemas } from '../../api/api';
import { saveExport } from '../../api/download';
import { useSession } from '../../auth/session';

type Plan = Schemas['ConfigPlan'];
type Change = Schemas['ConfigChange'];

const STEPS = ['File', 'Review', 'Applying', 'Done'] as const;
const MAX_BYTES = 1024 * 1024;

const SECTION_WORDS: Record<string, string> = {
  settings: 'Settings',
  pool: 'Pool defaults',
  queue: 'Queue order',
  runner_classes: 'Runner sizes',
  machines: 'Machine',
  image_layers: 'Image layers of',
  repositories: 'Repository',
  notifications: 'Notification channel',
};

const KIND_WORDS = { added: 'Add', changed: 'Change', removed: 'Remove' } as const;

function show(v: unknown): string {
  if (v === undefined || v === null || v === '') return 'empty';
  if (typeof v === 'object') return JSON.stringify(v);
  return typeof v === 'string' ? v : JSON.stringify(v);
}

/** One line of a plan, in words: what happens to what. */
export function changeWords(c: Change): string {
  const what = [SECTION_WORDS[c.section] ?? c.section, c.name].filter(Boolean).join(' ');
  const fields = (c.fields ?? [])
    .map((f) =>
      c.kind === 'added'
        ? `${f.field} ${show(f.new)}`
        : `${f.field} from ${show(f.old)} to ${show(f.new)}`,
    )
    .join('; ');
  return `${KIND_WORDS[c.kind]} ${what}${fields ? `: ${fields}` : ''}`;
}

function ConfigApplyWizard() {
  const client = useClient();
  const session = useSession();
  const [phase, setPhase] = useState<WizardPhase>('first-run');
  const [step, setStep] = useState(1);
  const [file, setFile] = useState<File | null>(null);
  const [plan, setPlan] = useState<Plan | undefined>(undefined);
  const [yes, setYes] = useState<Set<string>>(new Set());
  const [applied, setApplied] = useState<Change[]>([]);
  const [problem, setProblem] = useState<string | undefined>(undefined);

  const admin = session.can('admin');
  const owner = session.can('owner');
  const removals = plan?.removals ?? [];
  const problems = plan?.problems ?? [];
  const changes = plan?.changes ?? [];
  const allYes = removals.every((r) => yes.has(r));

  const toReview = async () => {
    if (!file) return;
    setStep(2);
    setPhase('planning');
    try {
      const p = await unwrap(
        client.POST('/api/v1/config/plans', { body: { file: await file.text() } }),
      );
      setPlan(p);
      setYes(new Set());
      setPhase('ready-to-review');
    } catch (e) {
      setProblem(errorText(e));
      setStep(4);
      setPhase('failed');
    }
  };

  const apply = async () => {
    if (!plan?.id) return;
    setStep(3);
    setPhase('running');
    try {
      const r = await unwrap(
        client.POST('/api/v1/config/plans/{configPlanId}/apply', {
          params: { path: { configPlanId: plan.id } },
          body: { confirm_removals: [...yes] },
        }),
      );
      setApplied(r.applied);
      setStep(4);
      setPhase('done');
    } catch (e) {
      setProblem(errorText(e));
      setStep(4);
      setPhase('failed');
    }
  };

  const startAgain = () => {
    setFile(null);
    setPlan(undefined);
    setApplied([]);
    setYes(new Set());
    setProblem(undefined);
    setStep(1);
    setPhase('first-run');
  };

  let body;
  if (step <= 1) {
    body = (
      <Stack>
        <Text>
          Choose a config file you exported here, then edit it. The next step lists each
          change and changes nothing.
        </Text>
        <FileField
          label="Config file"
          accept=".yaml,.yml"
          onChange={setFile}
          validate={(f) => (f.size > MAX_BYTES ? 'The file is over 1 MB.' : undefined)}
        />
      </Stack>
    );
  } else if (step === 2) {
    body = (
      <Stack>
        {problems.length > 0 ? (
          <Message tone="problem">
            The file cannot be applied. Fix these, then choose it again.
          </Message>
        ) : null}
        {problems.length > 0 ? <BulletList items={problems} /> : null}
        {problems.length === 0 && changes.length === 0 ? (
          <Message tone="done">
            Nothing to change: the setup already matches the file.
          </Message>
        ) : null}
        {changes.length > 0 ? (
          <>
            <Text>
              {changes.length} {changes.length === 1 ? 'change' : 'changes'} will be made.
              Each one is written to the audit log.
            </Text>
            <BulletList items={changes.map(changeWords)} />
          </>
        ) : null}
        {removals.map((r) => (
          <CheckRow
            key={r}
            label={`Yes, remove ${r}`}
            checked={yes.has(r)}
            onChange={(on) =>
              setYes((cur) => {
                const n = new Set(cur);
                if (on) n.add(r);
                else n.delete(r);
                return n;
              })
            }
          />
        ))}
      </Stack>
    );
  } else if (step === 3) {
    body = <Text>Applying the changes to the setup.</Text>;
  } else {
    body = (
      <Stack>
        {phase === 'failed' ? (
          <Message tone="problem">
            The file was not applied. {problem ?? ''} Plan the file again to see what is
            left.
          </Message>
        ) : (
          <Message tone="done">
            Applied {applied.length} {applied.length === 1 ? 'change' : 'changes'}.
          </Message>
        )}
        {applied.length > 0 ? (
          <Facts items={applied.map((c) => ({ name: changeWords(c), value: 'done' }))} />
        ) : null}
      </Stack>
    );
  }

  const canNext = file !== null && admin;
  return (
    <>
      <Wizard
        name="Apply a config file"
        steps={STEPS}
        current={step - 1}
        firstStep={0}
        phase={phase}
        reading="Planning the file"
        onBack={() => {
          setStep(1);
          setPhase('first-run');
        }}
        primary={
          step <= 1
            ? {
                label: 'Plan the file',
                onPress: () => void toReview(),
                disabled: !canNext,
                reason: admin ? 'Choose a config file.' : session.whyNot('admin'),
              }
            : {
                label: `Apply ${changes.length} ${changes.length === 1 ? 'change' : 'changes'}`,
                busyLabel: 'Applying',
                onPress: () => void apply(),
                disabled: !plan?.id || changes.length === 0 || !allYes || !owner,
                reason: !owner
                  ? session.whyNot('owner')
                  : !plan?.id
                    ? 'The file has problems.'
                    : changes.length === 0
                      ? 'There is nothing to change.'
                      : 'Tick each removal to say yes.',
              }
        }
        review={step === 2}
        onStartAgain={startAgain}
        onTryAgain={startAgain}
      >
        {body}
      </Wizard>
    </>
  );
}

export function ConfigApplyPage() {
  return (
    <Page>
      <Breadcrumbs
        trail={[
          { to: '/settings/data', label: 'Settings' },
          { label: 'Apply a config file' },
        ]}
      />
      <PageHeader title="Apply a config file" />
      <ConfigApplyWizard />
    </Page>
  );
}

export function ConfigAsCodeSettings() {
  const client = useClient();
  const session = useSession();
  const exportFile = () =>
    saveExport(
      client.GET('/api/v1/config/export', { parseAs: 'blob' }),
      'crsp-setup.yaml',
    );
  const admin = session.can('admin');
  return (
    <Section title="Config as code">
      <Stack>
        <Text>
          The whole setup as one file: settings, pool defaults, queue order, machine
          settings, runner sizes, image layers, repository policies and notification
          channels. No secret is written in it: each one is named by reference only.
        </Text>
        <ExportButton
          label="Export setup file"
          what="the setup file"
          onExport={exportFile}
          disabled={!admin}
          reason={session.whyNot('admin')}
        />
        <LinkButton to="/config/apply">Apply a config file</LinkButton>
      </Stack>
    </Section>
  );
}
