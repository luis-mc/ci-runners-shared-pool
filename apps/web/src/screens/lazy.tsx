// A screen that loads with its route, so the first JavaScript stays within its budget (R6.12).
// While its chunk loads the page still has its one heading (the words the loaded screen shows) and the shared loading part.
import { Loading, Page, PageHeader } from '@crsp/ui';
import { lazy, Suspense, type ComponentType } from 'react';
import { useSearchParams } from 'react-router-dom';

export interface LaterOptions {
  /** The h1 of the screen once loaded; a function when the query decides it. */
  heading: string | ((params: URLSearchParams) => string);
  /** The small window with no shell around it (TRAY4). */
  compact?: boolean;
}

function Waiting({ heading, compact = false }: LaterOptions) {
  const [params] = useSearchParams();
  const title = typeof heading === 'function' ? heading(params) : heading;
  const page = (
    <Page {...(compact ? { frame: 'compact' as const } : {})}>
      <PageHeader title={title} />
      <Loading what="Loading" />
    </Page>
  );
  return compact ? <main>{page}</main> : page;
}

export function later<P extends object>(
  load: () => Promise<ComponentType<P>>,
  options?: LaterOptions,
) {
  const Screen = lazy(() => load().then((component) => ({ default: component })));
  return function Later(props: P) {
    return (
      <Suspense fallback={options ? <Waiting {...options} /> : <Loading what="Loading" />}>
        <Screen {...props} />
      </Suspense>
    );
  };
}
