import {
  BulletList,
  ActionBar,
  Card,
  CardGrid,
  Code,
  ExternalButton,
  Facts,
  LinkButton,
  Message,
  PageHeader,
  Section,
  Steps,
  Strong,
  Text,
  TextLink,
  formatSize,
} from '@crsp/ui';
import { DOCS_URL, REPO_URL, Site } from './Site.tsx';
import { osCardId } from './lib/detect.ts';
import {
  ARCHS,
  FORMATS,
  OSES,
  REPO,
  type Asset,
  type Os,
  type ReleaseData,
} from './lib/release.ts';

export function Home() {
  return (
    <Site>
      <PageHeader
        title="CI Runners Shared Pool"
        lead="Run your GitHub Actions jobs on your own computers. One pool of machines serves all your repositories, on Windows, macOS and Linux."
      />
      <ActionBar
        label="Main action"
        primary={
          <LinkButton to="/download" kind="primary">
            Download
          </LinkButton>
        }
      />
      <CardGrid>
        <Card title="Who it is for">
          <Text>
            A developer or a small team that pays for hosted CI minutes and has computers
            that sit idle. The pool puts those computers to work.
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
      <Section title="How it works">
        <Steps
          label="How it works"
          steps={[
            'Install the Controller on one computer.',
            'Open its console and connect GitHub.',
            'Add machines with the command the console shows, then turn on your repositories.',
          ]}
        />
      </Section>
      <Section title="Learn more">
        <Text>
          The full docs are in the repository:{' '}
          <TextLink to={DOCS_URL}>read the docs</TextLink>. The code is at{' '}
          <TextLink to={REPO_URL}>GitHub</TextLink>.
        </Text>
      </Section>
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
  const installGuide = `https://github.com/${REPO}/blob/main/docs/guides/install.md`;
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
          <TextLink to={installGuide}>install guide</TextLink> shows how.
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
          Older versions are on the{' '}
          <TextLink to={`https://github.com/${REPO}/releases`}>releases page</TextLink>.
          Install steps are in the <TextLink to={installGuide}>install guide</TextLink>.
          File names look like{' '}
          <Code>{`Crsp-${release.version}-<system>-<cpu>.<ext>`}</Code>.
        </Text>
      </Section>
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
