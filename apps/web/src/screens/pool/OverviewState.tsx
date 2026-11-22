// What a Pool screen shows until the overview is there, or when it could not be read.
import { Loading, Message } from '@crsp/ui';
import type { ReactNode } from 'react';
import type { Schemas } from '../../api/api';
import { useOverview } from './useOverview';

export function WithOverview({
  children,
}: {
  children: (o: Schemas['Overview'], readAt: number) => ReactNode;
}) {
  const { overview, failed, readAt } = useOverview();
  if (overview) return <>{children(overview, readAt)}</>;
  if (failed)
    return (
      <Message tone="problem">
        Could not load the pool. {failed} The console keeps trying.
      </Message>
    );
  return <Loading what="Loading the pool" />;
}
