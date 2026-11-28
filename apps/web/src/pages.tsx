import {
  ActionBar,
  BulletList,
  Breadcrumbs,
  Card,
  CardGrid,
  Code,
  DocSearch,
  ExternalButton,
  Facts,
  FeatureBand,
  Hero,
  LinkButton,
  Message,
  PageHeader,
  Prose,
  Section,
  Shot,
  Stack,
  Steps,
  Strong,
  Text,
  TextLink,
  formatSize,
} from '@crsp/ui';
import { DOCS_URL, REPO_URL, Site } from './Site.tsx';
import { osCardId } from './lib/detect.ts';
import { GROUP_TITLE, PUBLIC_FOLDERS, type DocPage } from './lib/docs.ts';
import type { ShotData, SiteData } from './lib/pages.ts';
import {
  ARCHS,
  FORMATS,
  OSES,
  REPO,
  type Asset,
  type Os,
  type ReleaseData,
} from './lib/release.ts';

const INSTALL_GUIDE = '/docs/guides/install';

function ShotOf({
  shot,
  priority,
}: {
  shot: ShotData | undefined;
  priority?: boolean | undefined;
}) {
  if (!shot) return null;
  return (
    <Shot
      src={shot.src}
      alt={shot.alt}
      width={shot.width}
      height={shot.height}
      priority={priority}
    />
  );
}

export function Home({ data }: { data: SiteData }) {
  const { study, shots } = data;
  return (
    <Site>
      <Hero
        title="CI Runners Shared Pool"
        lead="Run your GitHub Actions jobs on your own computers. One pool of machines serves all your repositories, on Windows, macOS and Linux."
        media={<ShotOf shot={shots[0]} priority />}
      />
      <ActionBar
        label="Main action"
        primary={
          <LinkButton to="/download" kind="primary">
            Download
          </LinkButton>
        }
      />
      <FeatureBand title="In short">
        <CardGrid>
          <Card title="Who it is for">
            <Text>
              A developer or a small team that pays for hosted CI minutes and has
              computers that sit idle. The pool puts those computers to work.
            </Text>
          </Card>
          <Card title="What you get">
            <BulletList
              items={[
                'One pool for every repository you turn on',
                'A console on your own network to add machines and watch jobs',
                'Machines that join with one command',
              ]}
            />
          </Card>
          <Card title="What it does not do">
            <Text>
              Public repositories run free on hosted runners, so the pool saves nothing
              there. Savings last only while your machines are on.
            </Text>
          </Card>
        </CardGrid>
      </FeatureBand>
      <FeatureBand title="How it works" media={<ShotOf shot={shots[1]} />}>
        <Steps
          label="How it works"
          steps={[
            'Install the Controller on one computer.',
            'Open its console and connect GitHub.',
            'Add machines with the command the console shows, then turn on your repositories.',
          ]}
        />
        <Text>
          <TextLink to="/product">See what the pool does</TextLink>.
        </Text>
      </FeatureBand>
      <FeatureBand
        title="Example: what it could save"
        lead={`Example, not a measurement: ${study.workload}. Rates read ${study.ratesRead}.`}
      >
        <Facts
          items={study.totals.map((t) => ({
            name: `${t.name}, a month`,
            value: t.total,
          }))}
        />
        <Text tone="muted">
          {study.reading} Vendor prices are vendor claims, read from their pages on the
          date above.{' '}
          <TextLink to="/docs/studies/runs-on-vs-crsp/study">Read the study</TextLink>.
        </Text>
      </FeatureBand>
      <FeatureBand title="Learn more">
        <Text>
          The full docs are on this site: <TextLink to={DOCS_URL}>read the docs</TextLink>
          . The code is at <TextLink to={REPO_URL}>GitHub</TextLink>.
        </Text>
      </FeatureBand>
    </Site>
  );
}

/** The features the Product page shows, in this order. */
export const PRODUCT_FEATURES = [
  'machines',
  'runners',
  'scheduling',
  'caches',
  'registry',
  'ci-run-insights',
  'observability',
  'doctor',
  'notifications',
  'github-integration',
  'tray',
  'web-console',
  'operations',
] as const;

/** The first sentence or two of a summary, whole sentences, at most `max` characters. */
export function brief(text: string, max = 260): string {
  const sentences = text.match(/[^.!?]+[.!?]+(?:\s|$)/g) ?? [text];
  let out = '';
  for (const s of sentences) {
    if (out !== '' && (out + s).length > max) break;
    out += s;
  }
  return (out || text).trim();
}

export function Product({ data }: { data: SiteData }) {
  const features = PRODUCT_FEATURES.flatMap((slug) => {
    const page = data.docs.find((d) => d.path === `/docs/features/${slug}`);
    return page ? [page] : [];
  });
  return (
    <Site>
      <PageHeader
        title="What the pool does"
        lead="One pool of your own machines runs the GitHub Actions jobs of every repository you turn on. A console on your network shows and controls it."
      />
      <ActionBar
        label="Main action"
        primary={
          <LinkButton to="/download" kind="primary">
            Download
          </LinkButton>
        }
      />
      <FeatureBand
        title="The console"
        lead="The console runs on your own network. These are real screens of a demo Controller."
        media={<ShotOf shot={data.shots[0]} priority />}
      >
        <Text>
          Add machines, turn on repositories, watch jobs run, and see what the pool costs
          and saves.
        </Text>
      </FeatureBand>
      <FeatureBand title="What it does">
        <CardGrid>
          {features.map((f) => (
            <Card key={f.path} title={f.title} to={f.path}>
              <Text>{brief(f.summary)}</Text>
            </Card>
          ))}
        </CardGrid>
      </FeatureBand>
      <FeatureBand title="More screens" media={<ShotOf shot={data.shots[2]} />}>
        <Text>
          The Pool pages list every machine with its load, and every runner with what it
          is doing.
        </Text>
      </FeatureBand>
      <FeatureBand title="What it does not do">
        <BulletList
          items={[
            'Public repositories run free on hosted runners, so the pool saves nothing there.',
            'Savings last only while your machines are on.',
            'It is not a hosted service: your jobs run on your computers.',
          ]}
        />
      </FeatureBand>
    </Site>
  );
}

const OS_NAME: Record<Os, string> = {
  windows: 'Windows',
  macos: 'macOS',
  linux: 'Linux',
};
const FORMAT_NAME: Record<string, string> = {
  msi: 'Installer (.msi)',
  pkg: 'Installer (.pkg)',
  deb: 'Package (.deb)',
  rpm: 'Package (.rpm)',
  zip: 'Archive (.zip)',
  'tar.gz': 'Archive (.tar.gz)',
};

function AssetRow({ a }: { a: Asset }) {
  return (
    <>
      <ExternalButton href={a.url}>Download</ExternalButton>
      <Text mono>{a.name}</Text>
      <Text tone="muted">{formatSize(a.size)}</Text>
      <Text mono>SHA-256 {a.sha256}</Text>
    </>
  );
}

export function Download({ release }: { release: ReleaseData }) {
  return (
    <Site>
      <PageHeader
        title="Download the Controller"
        lead={`Version ${release.version}, published ${release.date}. The Controller runs on the one computer that manages the pool. Other machines join with a command the console shows.`}
      />
      {release.signed ? (
        <Message tone="done" dismissible={false}>
          The installers are signed. Check the SHA-256 shown with each file as well.
        </Message>
      ) : (
        <Message tone="note" dismissible={false}>
          The installers are not signed. Windows SmartScreen and macOS Gatekeeper may warn
          you. Check the SHA-256 shown with each file instead; the{' '}
          <TextLink to={INSTALL_GUIDE}>install guide</TextLink> shows how.
        </Message>
      )}
      <CardGrid>
        {OSES.map((os) => (
          <Card key={os} id={osCardId(os)} title={OS_NAME[os]}>
            {ARCHS.map((arch) => (
              <Facts
                key={arch}
                items={FORMATS[os].flatMap((format) => {
                  const a = release.assets.find(
                    (x) => x.os === os && x.arch === arch && x.format === format,
                  );
                  return a
                    ? [
                        {
                          name: `${arch}: ${FORMAT_NAME[format]}`,
                          value: <AssetRow a={a} />,
                        },
                      ]
                    : [];
                })}
              />
            ))}
          </Card>
        ))}
      </CardGrid>
      <Section title="Check what you downloaded">
        <BulletList
          items={[
            'SHA256SUMS holds the SHA-256 of every file.',
            'Two bills of materials list the parts inside: Go and web.',
          ]}
        />
        <Text>
          <ExternalButton href={release.checksumsUrl}>SHA256SUMS</ExternalButton>{' '}
          {release.sboms.map((s) => (
            <ExternalButton key={s.name} href={s.url}>
              {s.name}
            </ExternalButton>
          ))}{' '}
          <ExternalButton
            href={release.notesUrl}
          >{`Release notes for ${release.version}`}</ExternalButton>
        </Text>
        <Text tone="muted">
          Install steps are in the <TextLink to={INSTALL_GUIDE}>install guide</TextLink>.
          File names look like{' '}
          <Code>{`Crsp-${release.version}-<system>-<cpu>.<ext>`}</Code>.
        </Text>
      </Section>
      <Section
        title="Older versions"
        count={release.older.length}
        hint="Newest first. Each links to the release page on GitHub, where its files are."
      >
        {release.older.length === 0 ? (
          <Text>There is no older version yet.</Text>
        ) : (
          <Facts
            items={release.older.map((o) => ({
              name: o.version,
              value: (
                <>
                  <Text tone="muted">Published {o.date}</Text>
                  <ExternalButton href={o.notesUrl}>
                    {`Release page for ${o.version}`}
                  </ExternalButton>
                </>
              ),
            }))}
          />
        )}
      </Section>
    </Site>
  );
}

export function Security({ release }: { release: ReleaseData }) {
  return (
    <Site>
      <PageHeader
        title="Security"
        lead="How to report a problem privately, what is signed, and how to check a download yourself."
        hint="Opens GitHub's private reporting form."
      />
      <ActionBar
        label="Main action"
        primary={
          <ExternalButton
            href={`https://github.com/${REPO}/security/advisories/new`}
            kind="primary"
          >
            Report privately
          </ExternalButton>
        }
      />
      <FeatureBand title="Report a vulnerability">
        <Text>
          Use GitHub private vulnerability reporting. Do not open a public issue for a
          security problem. The full policy, what to expect and what a job can reach are
          in{' '}
          <TextLink to={`https://github.com/${REPO}/blob/main/SECURITY.md`}>
            SECURITY.md
          </TextLink>
          .
        </Text>
      </FeatureBand>
      <FeatureBand title="What is signed">
        {release.signed ? (
          <Text>
            The installers are signed. Check the SHA-256 and the attestation as well.
          </Text>
        ) : (
          <Text>
            <Strong>Today the installers are not signed.</Strong> Windows SmartScreen and
            macOS Gatekeeper may warn you. Every release gives you three checks in place
            of a signature: a checksum, a build attestation and a bill of materials.
          </Text>
        )}
      </FeatureBand>
      <FeatureBand title="Check a download">
        <Steps
          label="Check a download"
          steps={[
            'Compare the SHA-256 of the file with the line in SHA256SUMS.',
            'Verify the build attestation with the GitHub CLI: it names the workflow and the commit.',
            'Read the bill of materials, or feed it to a scanner.',
          ]}
        />
        <Text>
          The commands for each system are in the{' '}
          <TextLink to={INSTALL_GUIDE}>install guide</TextLink>. Release {release.version}{' '}
          has <ExternalButton href={release.checksumsUrl}>SHA256SUMS</ExternalButton>{' '}
          {release.sboms.map((s) => (
            <ExternalButton key={s.name} href={s.url}>
              {s.name}
            </ExternalButton>
          ))}
        </Text>
      </FeatureBand>
    </Site>
  );
}

const groupOf = (d: DocPage): string => d.folder.split('/')[0] ?? '';

export function Docs({ docs }: { docs: readonly DocPage[] }) {
  const groups = PUBLIC_FOLDERS.flatMap((folder) => {
    const items = docs
      .filter((d) => groupOf(d) === folder)
      .map((d) => ({
        to: d.path,
        title: d.title,
        hint: d.folder === folder ? undefined : d.folder.slice(folder.length + 1),
      }));
    return items.length > 0 ? [{ title: GROUP_TITLE[folder] ?? folder, items }] : [];
  });
  return (
    <Site>
      <PageHeader
        title="Docs"
        lead="The documentation of the pool, read from the repository. Search needs JavaScript; reading does not."
      />
      <DocSearch label="Search the docs" hint="Search the docs" groups={groups} />
      <Text tone="muted">
        For tools: <TextLink to="/llms.txt">llms.txt</TextLink> lists every page.
      </Text>
    </Site>
  );
}

export function Doc({ page }: { page: DocPage }) {
  const group = GROUP_TITLE[groupOf(page)] ?? groupOf(page);
  const meta = [
    page.version ? `Version ${page.version}` : '',
    page.lastVerified ? `last verified ${page.lastVerified}` : '',
  ]
    .filter(Boolean)
    .join(', ');
  return (
    <Site>
      <Breadcrumbs
        trail={[{ to: '/docs', label: 'Docs' }, { label: group }, { label: page.title }]}
      />
      <PageHeader
        title={page.title}
        lead={meta === '' ? undefined : meta}
        action={<ExternalButton href={page.editUrl}>Edit on GitHub</ExternalButton>}
      />
      <Stack>
        <Prose html={page.html} />
        <Text tone="muted">
          <TextLink to="/docs">All docs</TextLink>
        </Text>
      </Stack>
    </Site>
  );
}

export function NotFound() {
  return (
    <Site>
      <PageHeader title="This page is gone" lead="The page you opened does not exist." />
      <ActionBar
        label="Main action"
        primary={
          <LinkButton to="/" kind="primary">
            Open Home
          </LinkButton>
        }
      />
      <Text>
        <Strong>Looking for the docs?</Strong>{' '}
        <TextLink to={DOCS_URL}>Read the docs</TextLink>.
      </Text>
    </Site>
  );
}
