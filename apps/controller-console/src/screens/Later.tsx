// The areas whose screens come in a later milestone (web-console 4.6..4.9). They keep their place in the menu
// and say so; no dead end (UX-6).
import { Empty, Page, PageHeader, Section, TextLink } from '@crsp/ui';

export function Later({
  title,
  what,
  milestone,
  nested = false,
}: {
  title: string;
  what: string;
  milestone: string;
  /** Inside a page that has its own headline (Settings): the title is a section, so the page keeps one h1 (R6.6). */
  nested?: boolean;
}) {
  const empty = (
    <Empty
      title={`${what} arrives in ${milestone}. Until then the overview shows the queue and the running jobs.`}
      action={<TextLink to="/">Open the overview</TextLink>}
    />
  );
  if (nested) return <Section title={title}>{empty}</Section>;
  return (
    <Page>
      <PageHeader title={title} />
      {empty}
    </Page>
  );
}

export function NotFound() {
  return (
    <Page>
      <PageHeader title="Page not found" />
      <Empty
        title="This page does not exist."
        action={<TextLink to="/">Open the overview</TextLink>}
      />
    </Page>
  );
}
