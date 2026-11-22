// A screen that loads with its route, so the first JavaScript stays within its budget (R6.12).
import { Loading, PageHeader } from '@crsp/ui';
import { lazy, Suspense, type ComponentType } from 'react';

// `page` is for a screen that owns the whole page: while it loads, the page still has its one heading.
export function later<P extends object>(
  load: () => Promise<ComponentType<P>>,
  page = false,
) {
  const Screen = lazy(() => load().then((component) => ({ default: component })));
  return function Later(props: P) {
    return (
      <Suspense
        fallback={
          page ? (
            <>
              <PageHeader title="Loading" />
              <Loading what="Loading" />
            </>
          ) : (
            <Loading what="Loading" />
          )
        }
      >
        <Screen {...props} />
      </Suspense>
    );
  };
}
