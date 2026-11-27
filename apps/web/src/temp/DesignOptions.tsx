// TEMPORARY (CRSP342, CRSP343): design option screens for the owner's review, built from the shared parts with demo
// data only. Shown behind `?task=342|343&option=1|2|3`. Delete this folder and its block in main.tsx; never merge.
import {
  Button,
  Card,
  CardGrid,
  ChoiceField,
  Facts,
  Message,
  Meter,
  Page,
  PageHeader,
  Section,
  Stack,
  StatGrid,
  StatTile,
  Status,
  SummaryLine,
  Switch,
  Table,
  Text,
} from '@crsp/ui';
import { useState, type ReactNode } from 'react';
import { MemoryRouter } from 'react-router-dom';

const noop = (): void => undefined;

function Toggle({ label, start }: { label: string; start: boolean }) {
  const [on, setOn] = useState(start);
  return <Switch label={label} on={on} onChange={setOn} />;
}

// ---------- CRSP342 option 1: one hub in the controller, one budget ----------
function Caches1() {
  const repos = [
    { id: 'a', repo: 'luis-mc/ci-runners-shared-pool', hits: 412, misses: 38, warmth: 92, size: '9.4 GB' },
    { id: 'b', repo: 'luis-mc/dev-standards', hits: 171, misses: 40, warmth: 61, size: '3.1 GB' },
    { id: 'c', repo: 'luis-mc/site', hits: 12, misses: 31, warmth: 8, size: '0.6 GB' },
  ];
  return (
    <Page>
      <PageHeader title="Caches" lead="One shared cache on the controller, used by every runner of the pool." />
      <SummaryLine total={[{ count: 4, word: 'cache', plural: 'caches' }]} states={[{ count: 3, word: 'on' }, { count: 1, word: 'off' }]} />
      <StatGrid>
        <StatTile label="Hit rate" value="88" unit="%" change="better" changeText="up 4 vs the previous 7 days" trend={[70, 74, 80, 83, 88]} />
        <StatTile label="Served" value="41" unit="GB" trend={[4, 6, 5, 9, 12]} />
        <StatTile label="Used" value="18.6" unit="of 60 GB" />
        <StatTile label="Warm machines" value="2" unit="of 3" />
      </StatGrid>
      <Section title="One budget" hint="All caches share this space. When it is full, the least recently used entries go first.">
        <Meter label="Cache budget" value={31} text="18.6 of 60 GB used" />
      </Section>
      <CardGrid>
        <Card title="Actions cache" footer={<Button onPress={noop}>Wipe</Button>}>
          <Toggle label="Actions cache" start />
          <Facts items={[{ name: 'Entries', value: '212' }, { name: 'Size', value: '9.1 GB' }, { name: 'Last hit', value: '2 min ago' }]} />
        </Card>
        <Card title="Repository mirrors" footer={<Button onPress={noop}>Wipe</Button>}>
          <Toggle label="Repository mirrors" start />
          <Facts items={[{ name: 'Repositories', value: '3' }, { name: 'Size', value: '4.2 GB' }, { name: 'Refreshed', value: '6 min ago' }]} />
        </Card>
        <Card title="Registry mirror" footer={<Button onPress={noop}>Wipe</Button>}>
          <Toggle label="Registry mirror" start />
          <Facts items={[{ name: 'Images', value: '14' }, { name: 'Size', value: '3.8 GB' }, { name: 'Upstream', value: 'reachable' }]} />
        </Card>
        <Card title="Package proxy" footer={<Button onPress={noop}>Wipe</Button>}>
          <Toggle label="Package proxy (npm, PyPI)" start={false} />
          <Facts items={[{ name: 'Covers', value: 'npm, PyPI' }, { name: 'Apt and crates', value: 'not yet' }]} />
        </Card>
      </CardGrid>
      <Table
        caption="Warmth by repository"
        title="Warmth by repository"
        rows={repos}
        rowKey={(r) => r.id}
        columns={[
          { id: 'repo', header: 'Repository', cell: (r) => r.repo, grow: 2 },
          { id: 'hits', header: 'Hits', cell: (r) => String(r.hits) },
          { id: 'miss', header: 'Misses', cell: (r) => String(r.misses), secondary: true },
          { id: 'warm', header: 'Warmth', cell: (r) => <Meter label={`Warmth ${r.repo}`} value={r.warmth} cells={5} warnAt={101} badAt={101} /> },
          { id: 'size', header: 'Size', cell: (r) => r.size, secondary: true },
        ]}
      />
    </Page>
  );
}

// ---------- CRSP342 option 2: dedicated holders ----------
function Caches2() {
  const holders = [
    { id: 'h1', name: 'cache-nas', role: 'Cache holder', disk: 44, state: 'ok' as const, word: 'Online', services: 'Actions, mirrors, registry, packages' },
    { id: 'h2', name: 'build-01', role: 'Runs jobs', disk: 0, state: 'off' as const, word: 'Not a holder', services: 'Uses cache-nas' },
    { id: 'h3', name: 'cache-spare', role: 'Cache holder', disk: 12, state: 'waiting' as const, word: 'Offline since 09:40', services: 'Mirrors, registry' },
  ];
  return (
    <Page>
      <PageHeader title="Caches" lead="Pick which machines keep the caches. The controller only tells runners where to go." action={<Button kind="primary" onPress={noop}>Make a machine a cache holder</Button>} />
      <Message tone="needs-you">cache-spare has been offline for 3 hours. Jobs on its repositories go to GitHub until it is back, and they run a little slower.</Message>
      <Table
        caption="Cache holders"
        title="Machines"
        rows={holders}
        rowKey={(r) => r.id}
        columns={[
          { id: 'n', header: 'Machine', cell: (r) => r.name },
          { id: 'role', header: 'Role', cell: (r) => r.role, secondary: true },
          { id: 's', header: 'Status', cell: (r) => <Status kind={r.state} word={r.word} /> },
          { id: 'd', header: 'Disk', cell: (r) => (r.disk ? <Meter label={`Disk ${r.name}`} value={r.disk} cells={5} /> : '–'), secondary: true },
          { id: 'sv', header: 'Keeps', cell: (r) => r.services, grow: 2 },
        ]}
      />
      <CardGrid>
        <Card title="cache-nas" machine footer={<Button onPress={noop}>Wipe</Button>}>
          <Facts items={[{ name: 'Actions cache', value: '9.1 GB' }, { name: 'Mirrors', value: '4.2 GB' }, { name: 'Registry', value: '3.8 GB' }, { name: 'Packages', value: '2.2 GB' }]} />
          <Toggle label="Keep the package proxy here" start />
        </Card>
        <Card title="Where jobs go">
          <ChoiceField label="When no holder answers" value="github" choices={[{ value: 'github', label: 'Go to GitHub' }, { value: 'wait', label: 'Wait up to 2 minutes' }]} onChange={noop} />
          <Text tone="muted">Moving a cache to another holder starts it empty.</Text>
        </Card>
      </CardGrid>
    </Page>
  );
}

// ---------- CRSP342 option 3: checked services crsp wires ----------
function Caches3() {
  const rows = [
    { id: 'n', name: 'npm proxy', tool: 'Verdaccio 6.1.2', pin: 'sha256:9f3c..a1', state: 'ok' as const, word: 'Running', size: '2.4 GB' },
    { id: 'p', name: 'PyPI proxy', tool: 'devpi 6.14', pin: 'sha256:41be..77', state: 'ok' as const, word: 'Running', size: '1.1 GB' },
    { id: 'a', name: 'Apt proxy', tool: 'apt-cacher-ng 3.7', pin: 'sha256:c80d..2e', state: 'failed' as const, word: 'Stopped', size: '5.0 GB' },
    { id: 'c', name: 'Crates proxy', tool: 'Not chosen', pin: '–', state: 'off' as const, word: 'Off', size: '–' },
  ];
  return (
    <Page>
      <PageHeader title="Caches" lead="Ready-made cache programs that crsp starts, points runners at, watches and wipes." />
      <Message tone="problem" action={<Button onPress={noop}>Start again</Button>}>The apt proxy stopped 12 minutes ago. Apt installs go straight to the internet until it runs again.</Message>
      <Table
        caption="Cache programs"
        title="Cache programs"
        rows={rows}
        rowKey={(r) => r.id}
        columns={[
          { id: 'n', header: 'Cache', cell: (r) => r.name },
          { id: 't', header: 'Program', cell: (r) => r.tool, grow: 1 },
          { id: 'pin', header: 'Checked version', cell: (r) => r.pin, secondary: true },
          { id: 's', header: 'Status', cell: (r) => <Status kind={r.state} word={r.word} /> },
          { id: 'z', header: 'Size', cell: (r) => r.size, secondary: true },
        ]}
      />
      <CardGrid>
        <Card title="Actions cache and mirrors" footer={<Button onPress={noop}>Wipe</Button>}>
          <Text tone="muted">These stay built into crsp. No outside program fits them.</Text>
          <Toggle label="Actions cache" start />
          <Toggle label="Repository mirrors" start />
        </Card>
        <Card title="Program versions">
          <Facts items={[{ name: 'Checked on', value: 'each start' }, { name: 'Newest known', value: 'Verdaccio 6.1.3' }]} />
          <Button onPress={noop}>Update the checked versions</Button>
        </Card>
      </CardGrid>
    </Page>
  );
}

// ---------- CRSP343 ----------
function Installers1() {
  const rows = [
    { id: '1', os: 'Windows', file: 'Crsp-0.1.28-win-x64.msi', sign: 'Not signed', k: 'off' as const, note: 'Needs a certificate' },
    { id: '2', os: 'macOS', file: 'Crsp-0.1.28-macos-arm64.pkg', sign: 'Signed, not notarized', k: 'waiting' as const, note: 'Mac warns on first open' },
    { id: '3', os: 'Debian and Ubuntu', file: 'Crsp-0.1.28-linux-x64.deb', sign: 'Checksum and attestation', k: 'info' as const, note: 'Not signed with a key' },
    { id: '4', os: 'Fedora and Red Hat', file: 'Crsp-0.1.28-linux-x64.rpm', sign: 'Checksum and attestation', k: 'info' as const, note: 'Not signed with a key' },
  ];
  return (
    <Page>
      <PageHeader title="Updates" lead="How far each installer can be trusted." />
      <Message tone="needs-you">Windows will show a warning when someone runs the MSI by hand. Updates through crsp are checked and do not warn.</Message>
      <Table
        caption="Installers"
        title="Trust per installer"
        rows={rows}
        rowKey={(r) => r.id}
        columns={[
          { id: 'o', header: 'System', cell: (r) => r.os },
          { id: 'f', header: 'File', cell: (r) => r.file, grow: 2, secondary: true },
          { id: 's', header: 'Trust', cell: (r) => <Status kind={r.k} word={r.sign} /> },
          { id: 'n', header: 'What the person sees', cell: (r) => r.note, grow: 2 },
        ]}
      />
      <CardGrid>
        <Card title="Certificate">
          <Facts items={[{ name: 'Windows', value: 'none yet' }, { name: 'Apple', value: 'expires 12 Mar 2028' }]} />
        </Card>
        <Card title="Signing" footer={<Button kind="primary" onPress={noop}>Turn on signing for the next release</Button>}>
          <Text tone="muted">Signing is off. The next release stays unsigned until you turn it on.</Text>
        </Card>
      </CardGrid>
    </Page>
  );
}

function Installers2() {
  const rows = [
    { id: '1', c: 'apt', os: 'Debian, Ubuntu', cmd: 'sudo apt install crsp', v: '0.1.28', s: 'ok' as const, w: 'Published' },
    { id: '2', c: 'yum and dnf', os: 'Fedora, Red Hat', cmd: 'sudo dnf install crsp', v: '0.1.28', s: 'ok' as const, w: 'Published' },
    { id: '3', c: 'Homebrew', os: 'macOS', cmd: 'brew install crsp', v: '0.1.27', s: 'waiting' as const, w: 'Waiting for review' },
    { id: '4', c: 'winget', os: 'Windows', cmd: 'winget install crsp', v: '0.1.26', s: 'failed' as const, w: 'Rejected' },
  ];
  return (
    <Page>
      <PageHeader title="Updates" lead="Install and update crsp with the tools each system already has." />
      <Table
        caption="Package channels"
        title="Package channels"
        rows={rows}
        rowKey={(r) => r.id}
        columns={[
          { id: 'c', header: 'Channel', cell: (r) => r.c },
          { id: 'o', header: 'System', cell: (r) => r.os, secondary: true },
          { id: 'cmd', header: 'Command', cell: (r) => r.cmd, grow: 2 },
          { id: 'v', header: 'Version', cell: (r) => r.v },
          { id: 's', header: 'Status', cell: (r) => <Status kind={r.s} word={r.w} /> },
        ]}
      />
      <Card title="Who updates this machine">
        <ChoiceField label="Updates on cache-nas" value="crsp" choices={[{ value: 'crsp', label: 'crsp updates itself, one machine at a time' }, { value: 'os', label: 'The system package manager updates it' }]} onChange={noop} help="Pick one. Two updaters on one machine would fight." />
        <Text tone="muted">With the system package manager, the Rollout page shows the version but cannot move it.</Text>
      </Card>
    </Page>
  );
}

function Installers3() {
  const releases = [
    { id: '1', v: '0.1.28', d: '27 Nov', t: 'Caches can be wiped by repository. The update window shows what is new.' },
    { id: '2', v: '0.1.27', d: '25 Nov', t: 'Rollout steps for a repository. Fixes the update that stalled on Windows.' },
    { id: '3', v: '0.1.26', d: '22 Nov', t: 'Remove a machine from the pool.' },
  ];
  return (
    <Page>
      <PageHeader title="Updates" lead="What changed, in words you can act on." />
      <Message tone="done" action={<Button onPress={noop}>Show what is new</Button>}>crsp is now version 0.1.28 on all 3 machines.</Message>
      <Section title="What is new in 0.1.28">
        <Stack>
          <Text>You can wipe a cache for one repository without touching the others.</Text>
          <Text>The update window now shows these notes before you press Update.</Text>
          <Text tone="muted">Needs your attention: a restart of the tray on Windows.</Text>
        </Stack>
      </Section>
      <Table
        caption="Earlier versions"
        title="Earlier versions"
        rows={releases}
        rowKey={(r) => r.id}
        columns={[
          { id: 'v', header: 'Version', cell: (r) => r.v },
          { id: 'd', header: 'Released', cell: (r) => r.d, secondary: true },
          { id: 't', header: 'What changed', cell: (r) => r.t, grow: 3 },
        ]}
      />
    </Page>
  );
}

const SCREENS: Record<string, ReactNode> = {
  '342-1': <Caches1 />,
  '342-2': <Caches2 />,
  '342-3': <Caches3 />,
  '343-1': <Installers1 />,
  '343-2': <Installers2 />,
  '343-3': <Installers3 />,
};

export function DesignOptions({ task, option }: { task: string; option: string }) {
  return (
    <MemoryRouter>
      {SCREENS[`${task}-${option}`] ?? (
        <Page>
          <Text>Unknown option</Text>
        </Page>
      )}
    </MemoryRouter>
  );
}
