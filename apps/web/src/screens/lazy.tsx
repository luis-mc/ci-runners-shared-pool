// A screen that loads with its route, so the first JavaScript stays within its budget (R6.12).
import { Loading } from '@crsp/ui';
import { lazy, Suspense, type ComponentType } from 'react';

export function later<P extends object>(load: () => Promise<ComponentType<P>>) {
  const Screen = lazy(() => load().then((component) => ({ default: component })));
  return function Later(props: P) {
    return (
      <Suspense fallback={<Loading what="Loading" />}>
        <Screen {...props} />
      </Suspense>
    );
  };
}
