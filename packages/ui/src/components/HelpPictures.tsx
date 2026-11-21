// Component 69 (LOOK48): the small pictures of the Help question cards. Each one is a drawing in the theme's own
// colours (tokens only, in components.css) with a text name for a screen reader (UX-17, UX-41). A page gives the
// numbers and names it has from real data; a picture with no data draws the short example (UX-6).
import type { ReactNode } from 'react';

function Pic({
  vb,
  label,
  children,
}: {
  vb: string;
  label: string;
  children: ReactNode;
}) {
  return (
    <svg className="ds-helppic" viewBox={vb} role="img" aria-label={label}>
      {children}
    </svg>
  );
}

const arrowH = (x1: number, y: number, x2: number) => (
  <>
    <path className="ds-helppic__arrow" d={`M${x1} ${y}H${x2 - 8}`} />
    <path className="ds-helppic__head" d={`M${x2} ${y}l-9 -6v12z`} />
  </>
);

/** Repositories pointing into the Shared Pool. */
export function PictureShared({
  repositories,
  machines,
}: {
  /** Up to three repository names; a short example when empty. */
  repositories?: readonly string[] | undefined;
  /** How many machines the pool has, when known. */
  machines?: number | undefined;
}) {
  const names = repositories?.length
    ? repositories.slice(0, 3)
    : ['octo/app', 'octo/site', 'octo/docs'];
  const sub =
    machines === undefined
      ? 'your machines'
      : `${machines} ${machines === 1 ? 'machine' : 'machines'}`;
  return (
    <Pic
      vb="0 0 306 118"
      label={`${names.length} repositories use one Shared Pool of ${sub}.`}
    >
      {names.map((r, i) => (
        <g key={r}>
          <rect
            className="ds-helppic__box"
            x="4"
            y={6 + i * 36}
            width="92"
            height="28"
            rx="4"
          />
          <text
            className="ds-helppic__title"
            x="50"
            y={25 + i * 36}
            textAnchor="middle"
            fontSize="12"
          >
            {r.length > 14 ? `${r.slice(0, 13)}…` : r}
          </text>
          <path className="ds-helppic__arrow" d={`M98 ${20 + i * 36}L150 58`} />
        </g>
      ))}
      <rect
        className="ds-helppic__box"
        data-pool
        x="152"
        y="22"
        width="150"
        height="74"
        rx="6"
      />
      <text
        className="ds-helppic__title"
        x="227"
        y="52"
        textAnchor="middle"
        fontSize="14"
      >
        Shared Pool
      </text>
      <text
        className="ds-helppic__muted"
        x="227"
        y="74"
        textAnchor="middle"
        fontSize="12"
      >
        {sub}
      </text>
    </Pic>
  );
}

/** A job queue where the first job takes the free slot. */
export function PictureQueue() {
  return (
    <Pic
      vb="0 0 270 78"
      label="Three jobs in a queue. The first one takes the free slot."
    >
      {[0, 1, 2].map((i) => (
        <g key={i}>
          <rect
            className="ds-helppic__box"
            data-wait={i === 0 ? '' : undefined}
            x={6 + i * 56}
            y="10"
            width="48"
            height="34"
            rx="4"
          />
          <text
            className="ds-helppic__title"
            x={30 + i * 56}
            y="32"
            textAnchor="middle"
            fontSize="12"
          >
            {`job ${i + 1}`}
          </text>
        </g>
      ))}
      {arrowH(176, 27, 214)}
      <rect className="ds-helppic__free" x="218" y="10" width="34" height="34" />
      <text className="ds-helppic__muted" x="6" y="68" fontSize="12">
        first one takes the free slot
      </text>
    </Pic>
  );
}

/** Slot squares: `busy` of `total` filled. Says `N of M slots busy`. */
export function PictureSlots({
  busy,
  total,
  machine,
}: {
  busy: number;
  total: number;
  /** The machine the numbers belong to, for the screen reader. */
  machine?: string | undefined;
}) {
  const shown = Math.min(Math.max(total, 1), 8);
  const w = shown * 44 + 32;
  return (
    <Pic
      vb={`0 0 ${w} 72`}
      label={`${machine ? `${machine}: ` : ''}${busy} of ${total} slots busy.`}
    >
      <rect
        className="ds-helppic__box"
        x="6"
        y="6"
        width={shown * 44 + 20}
        height="58"
        rx="6"
      />
      {Array.from({ length: shown }, (_, i) => (
        <rect
          key={i}
          className={i < busy ? 'ds-helppic__busy' : 'ds-helppic__free'}
          x={16 + i * 44}
          y="16"
          width="34"
          height="26"
        />
      ))}
      <text className="ds-helppic__muted" x="16" y="58" fontSize="12">
        {`${busy} of ${total} slots busy`}
      </text>
    </Pic>
  );
}

export interface BudgetBar {
  /** Use now, in percent; absent when the machine has not said. */
  now?: number | undefined;
  /** The budget line, in percent. */
  budget: number;
}

/** Memory and CPU bars against the budget line, from one machine's real numbers. */
export function PictureBudget({
  machine,
  memory,
  cpu,
}: {
  machine?: string | undefined;
  memory?: BudgetBar | undefined;
  cpu?: BudgetBar | undefined;
}) {
  const W = 260;
  const bar = (y: number, name: string, b: BudgetBar | undefined) => {
    const now = b?.now;
    const text = b
      ? now === undefined
        ? `${name}: budget ${b.budget}%`
        : `${name} ${now}% of ${b.budget}%`
      : name;
    return (
      <g key={name}>
        <text className="ds-helppic__muted" x="6" y={y} fontSize="12">
          {text}
        </text>
        <rect className="ds-helppic__box" x="6" y={y + 6} width={W} height="14" rx="3" />
        {now === undefined ? null : (
          <rect
            className="ds-helppic__busy"
            x="6"
            y={y + 6}
            width={(W * Math.min(now, 100)) / 100}
            height="14"
            rx="3"
          />
        )}
        {b ? (
          <path
            className="ds-helppic__tick"
            d={`M${6 + (W * b.budget) / 100} ${y + 2}V${y + 26}`}
          />
        ) : null}
      </g>
    );
  };
  const caption = machine
    ? `${machine}'s budget. Over it, jobs wait.`
    : 'The budget line. Over it, jobs wait.';
  return (
    <Pic
      vb="0 0 280 106"
      label={
        memory || cpu
          ? `${machine ? `${machine}: ` : ''}memory ${memory?.now ?? 'not read'}${memory?.now === undefined ? '' : '%'} of its ${memory?.budget}% budget, CPU ${cpu?.now ?? 'not read'}${cpu?.now === undefined ? '' : '%'} of its ${cpu?.budget}% budget.`
          : 'Memory and CPU bars with a budget line. Over the line, jobs wait.'
      }
    >
      {bar(14, 'Memory', memory)}
      {bar(56, 'CPU', cpu)}
      <text className="ds-helppic__muted" x="6" y="98" fontSize="12">
        {caption}
      </text>
    </Pic>
  );
}

/** Persistent or ephemeral: two boxes. */
export function PictureRunnerType() {
  const box = (x: number, t: string, s: string) => (
    <g>
      <rect className="ds-helppic__box" x={x} y="6" width="150" height="56" rx="4" />
      <text
        className="ds-helppic__title"
        x={x + 75}
        y="28"
        textAnchor="middle"
        fontSize="14"
      >
        {t}
      </text>
      <text
        className="ds-helppic__muted"
        x={x + 75}
        y="48"
        textAnchor="middle"
        fontSize="11"
      >
        {s}
      </text>
    </g>
  );
  return (
    <Pic
      vb="0 0 320 92"
      label="Runner type: persistent keeps its tools, ephemeral is fresh for every job. Linux or Windows is the system, a separate choice."
    >
      {box(4, 'Persistent', 'keeps its tools')}
      {box(166, 'Ephemeral', 'fresh every job')}
      <text className="ds-helppic__muted" x="4" y="84" fontSize="12">
        Runner type. Linux or Windows is the OS.
      </text>
    </Pic>
  );
}

/** Your pool, an arrow, elsewhere. */
export function PictureElsewhere() {
  return (
    <Pic
      vb="0 0 300 70"
      label="A job that another runner runs is listed apart from your pool."
    >
      <rect
        className="ds-helppic__box"
        data-pool
        x="4"
        y="8"
        width="110"
        height="50"
        rx="4"
      />
      <text className="ds-helppic__title" x="59" y="38" textAnchor="middle" fontSize="14">
        your pool
      </text>
      <rect className="ds-helppic__box" x="176" y="8" width="120" height="50" rx="4" />
      <text
        className="ds-helppic__title"
        x="236"
        y="30"
        textAnchor="middle"
        fontSize="14"
      >
        elsewhere
      </text>
      <text
        className="ds-helppic__muted"
        x="236"
        y="48"
        textAnchor="middle"
        fontSize="11"
      >
        another runner
      </text>
      <path className="ds-helppic__arrow" data-dashed d="M118 33H166" />
      <path className="ds-helppic__head" d="M172 33l-9 -6v12z" />
    </Pic>
  );
}

/** The tray menu row. */
export function PictureTray() {
  return (
    <Pic
      vb="0 0 260 70"
      label="The tray app: an icon with a menu to open the console, pause or update."
    >
      <rect className="ds-helppic__box" x="4" y="8" width="252" height="40" rx="4" />
      <circle className="ds-helppic__busy" cx="236" cy="28" r="9" />
      <text className="ds-helppic__muted" x="14" y="33" fontSize="12">
        Open console · Pause · Update
      </text>
      <text className="ds-helppic__muted" x="4" y="64" fontSize="11">
        the tray icon, bottom right of the PC
      </text>
    </Pic>
  );
}

/** A Doctor finding with its fix. */
export function PictureDoctor({
  finding = 'No machine is up for Windows',
  fix = 'start win-box',
}: {
  finding?: string;
  fix?: string;
}) {
  return (
    <Pic
      vb="0 0 260 70"
      label={`A Doctor finding with its fix: ${finding}. Fix: ${fix}.`}
    >
      <rect className="ds-helppic__box" x="4" y="6" width="250" height="52" rx="4" />
      <text className="ds-helppic__title" x="14" y="28" fontSize="13">
        {`✕ ${finding}`}
      </text>
      <text className="ds-helppic__muted" x="14" y="46" fontSize="11">
        {`Fix: ${fix}`}
      </text>
    </Pic>
  );
}

/** A job and a runner with the same labels. */
export function PictureLabels({ labels = 'self-hosted linux' }: { labels?: string }) {
  return (
    <Pic
      vb="0 0 282 66"
      label={`A job and a runner with the same labels (${labels}) match.`}
    >
      <rect className="ds-helppic__box" x="4" y="8" width="104" height="48" rx="4" />
      <text className="ds-helppic__title" x="56" y="28" textAnchor="middle" fontSize="14">
        Job
      </text>
      <text className="ds-helppic__muted" x="56" y="46" textAnchor="middle" fontSize="11">
        {labels}
      </text>
      <path className="ds-helppic__arrow" d="M112 32H168" />
      <text
        className="ds-helppic__title"
        x="140"
        y="26"
        textAnchor="middle"
        fontSize="16"
      >
        =
      </text>
      <rect
        className="ds-helppic__box"
        data-pool
        x="172"
        y="8"
        width="104"
        height="48"
        rx="4"
      />
      <text
        className="ds-helppic__title"
        x="224"
        y="28"
        textAnchor="middle"
        fontSize="14"
      >
        Runner
      </text>
      <text
        className="ds-helppic__muted"
        x="224"
        y="46"
        textAnchor="middle"
        fontSize="11"
      >
        {labels}
      </text>
    </Pic>
  );
}

/** One run with its jobs. */
export function PictureRun() {
  return (
    <Pic vb="0 0 260 90" label="One run, started by a push, holds several jobs.">
      <rect className="ds-helppic__box" x="4" y="8" width="90" height="74" rx="6" />
      <text className="ds-helppic__title" x="49" y="40" textAnchor="middle" fontSize="14">
        Run
      </text>
      <text className="ds-helppic__muted" x="49" y="60" textAnchor="middle" fontSize="11">
        a push
      </text>
      {arrowH(98, 45, 130)}
      {['lint', 'build', 'e2e'].map((j, i) => (
        <g key={j}>
          <rect
            className="ds-helppic__box"
            x="134"
            y={8 + i * 26}
            width="110"
            height="22"
            rx="4"
          />
          <text
            className="ds-helppic__title"
            x="189"
            y={24 + i * 26}
            textAnchor="middle"
            fontSize="12"
          >
            {j}
          </text>
        </g>
      ))}
    </Pic>
  );
}
