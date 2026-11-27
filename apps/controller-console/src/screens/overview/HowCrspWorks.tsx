// The first-run picture of how a job gets run (D33, LOOK31, LOOK44): the Help picture and an Open Help button.
// It shows on first run only, until the pool has a machine, a runner and a repository, and the user can dismiss it.
import { Button, LinkButton, Section } from '@crsp/ui';
import { useState } from 'react';
import { unwrap, useClient } from '../../api/api';
import { useLoad } from '../../api/hooks';
import { useMachines } from '../../api/machines';
import { useVersion } from '../../live/LiveProvider';
import { allRepositories } from '../repositories/plans';
import { JobPathPicture } from '../help/JobPathPicture';

export const STRIP_STORE_NAME = 'crsp.howItWorks.dismissed';

/** The strip is for a pool that is not set up: it needs a machine, a runner and a repository to go away. */
export function showStrip(f: {
  machines: number;
  runners: number;
  repositories: number;
  dismissed: boolean;
}): boolean {
  if (f.dismissed) return false;
  return f.machines === 0 || f.runners === 0 || f.repositories === 0;
}

function wasDismissed(): boolean {
  try {
    return window.localStorage.getItem(STRIP_STORE_NAME) === '1';
  } catch {
    return false;
  }
}

export function HowCrspWorks() {
  const client = useClient();
  const machines = useMachines();
  const rv = useVersion('runners');
  const runners = useLoad(() => unwrap(client.GET('/api/v1/runners')), [rv]);
  const repos = useLoad(() => allRepositories(client, { pool: 'on' }), []);
  const [dismissed, setDismissed] = useState(wasDismissed);
  // Nothing shows while the three counts are still loading: a set-up pool never sees it flash.
  if (
    machines.result.state !== 'ready' ||
    runners.result.state !== 'ready' ||
    repos.result.state !== 'ready'
  )
    return null;
  if (
    !showStrip({
      machines: machines.result.data.items.length,
      runners: runners.result.data.items.length,
      repositories: repos.result.data.items.length,
      dismissed,
    })
  )
    return null;
  return (
    <Section
      title="How a job gets run"
      actions={
        <>
          <LinkButton to="/help">Open Help</LinkButton>
          <Button
            onPress={() => {
              setDismissed(true);
              try {
                window.localStorage.setItem(STRIP_STORE_NAME, '1');
              } catch {
                // The picture stays hidden for this visit only.
              }
            }}
          >
            Hide this
          </Button>
        </>
      }
    >
      <JobPathPicture />
    </Section>
  );
}
