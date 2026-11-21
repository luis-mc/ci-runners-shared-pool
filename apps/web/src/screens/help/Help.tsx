// The Help page (LOOK47, layout G, D58): the picture of how a job gets run on top, then every question card in one
// grid, all visible at once (UX-10, UX-51). No wizard, no lock. The cards of a machine read its real slots and load
// budget from the API. Pressing a part, or a link ending in `#q-slot`, scrolls to its card.
// The Help page (LOOK47, LOOK48, layout G, D58): the picture of how a job gets run on top, with a live example in each
// box, then every question card in one grid in the order of the box it explains, each with a badge for its box and a
// small picture. All cards are visible at once (UX-10, UX-51). No wizard, no lock. The cards of a machine read its real
// slots and load budget from the API. Pressing a box scrolls to its first card; pressing a badge lights its box.
import {
  CardGrid,
  Facts,
  Page,
  PageHeader,
  formatPercent,
  Pane,
  PictureBudget,
  PictureDoctor,
  PictureElsewhere,
  PictureLabels,
  PictureQueue,
  PictureRun,
  PictureRunnerType,
  PictureShared,
  PictureSlots,
  PictureTray,
  HelpQuestionCard,
  Text,
  TextLink,
} from '@crsp/ui';
import { useEffect, useState, type ReactNode } from 'react';
import { useLocation } from 'react-router-dom';
import { Load } from '../common';
import { useMachines } from '../../api/machines';
import { boxOf, CARDS, HELP_WORDS, PART_CARD } from './helpContent';
import { JobPathPicture } from './JobPathPicture';
import { useHelpData, type HelpData } from './useHelpData';

/** The real numbers of every machine, read from the API: never a fixed 85% or 60%. Shown when there are two or more. */
function MachineNumbers({ kind }: { kind: 'slots' | 'budget' }) {
  const { result, reload } = useMachines();
  return (
    <Load result={result} what="your machines" reload={reload}>
      {(data) =>
        data.items.length === 0 ? (
          <Text tone="muted">
            No machine yet. <TextLink to="/pool/machines/add">Add a machine</TextLink> to
            see its {kind === 'slots' ? 'slots' : 'load budget'}.
          </Text>
        ) : data.items.length === 1 ? null : (
          <Facts
            items={data.items.map((m) => ({
              name: m.name,
              value:
                kind === 'slots'
                  ? `${m.slots_used} of ${m.settings.job_slots} slots in use`
                  : `memory ${formatPercent(m.settings.budget_memory_pct)}, CPU ${formatPercent(m.settings.budget_cpu_pct)}`,
            }))}
          />
        )
      }
    </Load>
  );
}

function extra(id: string): ReactNode {
  switch (id) {
    case 'q-slot':
      return (
        <>
          <MachineNumbers kind="slots" />
          <TextLink to="/pool/size">Open Pool size</TextLink>
        </>
      );
    case 'q-budget':
      return <MachineNumbers kind="budget" />;
    case 'q-waiting':
      return <TextLink to="/jobs">Open Jobs</TextLink>;
    case 'q-doctor':
      return <TextLink to="/doctor">Open the Doctor</TextLink>;
    case 'q-images':
      return <TextLink to="/pool/images">Open Images and caches</TextLink>;
    default:
      return null;
  }
}

/** The small picture of a card, drawn from real data when there is some (LOOK48). */
function picture(id: string, d: HelpData): ReactNode {
  const m = d.machine;
  switch (id) {
    case 'q-shared':
      return (
        <PictureShared
          repositories={d.repositories}
          machines={d.machines.length > 0 ? d.machines.length : undefined}
        />
      );
    case 'q-run':
      return <PictureRun />;
    case 'q-labels':
      return <PictureLabels />;
    case 'q-elsewhere':
      return <PictureElsewhere />;
    case 'q-waiting':
    case 'q-queue':
      return <PictureQueue />;
    case 'q-slot':
      return m ? (
        <PictureSlots busy={m.slots_used} total={m.settings.job_slots} machine={m.name} />
      ) : (
        <PictureSlots busy={2} total={4} />
      );
    case 'q-runner-type':
      return <PictureRunnerType />;
    case 'q-budget':
      return m ? (
        <PictureBudget
          machine={m.name}
          memory={{
            budget: m.settings.budget_memory_pct,
            now: m.load?.memory_known ? m.load.memory_pct : undefined,
          }}
          cpu={{
            budget: m.settings.budget_cpu_pct,
            now: m.load?.cpu_known ? m.load.cpu_pct : undefined,
          }}
        />
      ) : (
        <PictureBudget />
      );
    case 'q-tray':
      return <PictureTray />;
    case 'q-doctor':
      return <PictureDoctor />;
    default:
      return null;
  }
}

export function HelpScreen() {
  const { hash } = useLocation();
  const data = useHelpData();
  const [lit, setLit] = useState<string[] | undefined>(undefined);

  // An address that ends in a card brings the card into view. Nothing is hidden, so nothing has to open.
  useEffect(() => {
    const id = hash.replace(/^#/, '');
    if (!CARDS.some((c) => c.id === id)) return;
    const t = setTimeout(
      () => document.getElementById(id)?.scrollIntoView?.({ block: 'center' }),
      0,
    );
    return () => clearTimeout(t);
  }, [hash]);

  return (
    <Page>
      <PageHeader title="Help" lead={HELP_WORDS.lead} />
      <Pane title="How a job gets run">
        <JobPathPicture
          lit={lit}
          examples={data.examples}
          onSelect={(id) => {
            setLit(undefined);
            const card = PART_CARD[id];
            if (card)
              document.getElementById(card)?.scrollIntoView?.({ block: 'center' });
          }}
        />
        <Text tone="muted">{HELP_WORDS.pictureNote}</Text>
      </Pane>
      <Pane title={HELP_WORDS.questions} id="questions">
        <CardGrid columns={4}>
          {CARDS.map((c) => (
            <HelpQuestionCard
              key={c.id}
              id={c.id}
              title={c.title}
              box={boxOf(c.box)}
              onBox={(box) => {
                setLit([box]);
                document
                  .getElementById(`box-${box}`)
                  ?.scrollIntoView?.({ block: 'center' });
              }}
              picture={picture(c.id, data)}
            >
              <Text>{c.answer}</Text>
              {extra(c.id)}
            </HelpQuestionCard>
          ))}
        </CardGrid>
      </Pane>
    </Page>
  );
}
