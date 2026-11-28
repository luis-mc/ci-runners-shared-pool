// Settings, Updates (lifecycle standard): the Updates card (policy, window, Check, state with retry and failure),
// the update history, the pool's machines with a remote update, and Remove for this machine. The page passes data;
// the shared lifecycle parts draw it and say the words (LC-22 to LC-24).
import { Button, Message, Stack, Text, formatSize } from '@crsp/ui';
import {
  FleetRows,
  RemoteUpdate,
  RemoveDialog,
  UpdatesCard,
  lw,
  type FleetRow,
  type UpdatePolicy,
} from '@crsp/ui/lifecycle';
import { useState } from 'react';
import { useNavigate } from 'react-router-dom';
import { ApiError, errorText } from '../../api/api';
import { useCommand, useLoad } from '../../api/hooks';
import { useRefreshTick } from '../../api/machines';
import { useLifecycle, type FleetItem, type UpdateStatusV2 } from '../../api/lifecycle';
import { useSession } from '../../auth/session';
import { Load } from '../common';
import { cardFailure, fmtTime } from './lifecycleView';

const REFRESH_MS = 5_000;
export const PRODUCT = 'crsp';

const fleetRow = (f: FleetItem): FleetRow => ({
  id: f.machine_id,
  machine: f.name,
  version: f.version,
  state: f.state,
});

export function UpdatesSettings() {
  const api = useLifecycle();
  const navigate = useNavigate();
  const session = useSession();
  const tick = useRefreshTick(REFRESH_MS);
  const status = useLoad(() => api.status(), [tick]);
  const fleet = useLoad(() => api.fleet(), [tick]);
  // The service is down for the install: the pane says it in one line, no Note (CRSP364).
  const restarting =
    status.result.state === 'failed' &&
    status.result.error instanceof ApiError &&
    status.result.error.code === 'unavailable';
  const [removing, setRemoving] = useState(false);
  const [removeStarted, setRemoveStarted] = useState(false);
  const [remote, setRemote] = useState<string | undefined>(undefined);

  const policy = useCommand(async (p: UpdatePolicy, w: UpdateStatusV2['window']) => {
    await api.setPolicy(p, w);
    status.reload();
  });
  const check = useCommand(async () => {
    await api.check();
    status.reload();
  });
  const install = useCommand(async (version: string, machineId?: string) => {
    await api.install(version, machineId);
    status.reload();
    fleet.reload();
  });
  const remove = useCommand(async (deleteData: boolean) => {
    await api.remove(deleteData);
    setRemoving(false);
    setRemoveStarted(true);
  });

  return (
    <Stack gap="section">
      {[policy, check, install, remove].map((c, i) =>
        c.error ? (
          <Message tone="problem" key={i}>
            {c.error}
          </Message>
        ) : null,
      )}
      <Load
        result={status.result}
        what="the update status"
        reload={status.reload}
        quietRestart
      >
        {(s) => (
          <UpdatesCard
            policy={s.policy}
            window={s.window}
            state={s.state}
            current={s.current}
            available={s.available ?? undefined}
            required={s.required}
            notes={s.notes}
            failure={cardFailure(s)}
            nextCheck={fmtTime(s.next_check_at)}
            checking={check.busy}
            percent={s.percent}
            lastCheck={
              s.last_check
                ? {
                    time: fmtTime(s.last_check.at),
                    by: s.last_check.by,
                    kind: s.last_check.kind,
                  }
                : undefined
            }
            historyTo="/settings/audit"
            restarting={restarting}
            onPolicy={(p) => void policy.run(p, s.window)}
            onWindow={(w) => void policy.run(s.policy, w)}
            onCheck={() => void check.run()}
            onInstall={
              s.available ? () => void install.run(s.available ?? '') : undefined
            }
          />
        )}
      </Load>
      <Load result={fleet.result} what="the machines" reload={fleet.reload} quietRestart>
        {(rows) => {
          const sel = rows.find((r) => r.machine_id === remote);
          const to =
            status.result.state === 'ready'
              ? (status.result.data.available ?? status.result.data.current)
              : '';
          return (
            <Stack>
              <FleetRows
                rows={rows.map(fleetRow)}
                action={(r) =>
                  r.state === 'up_to_date' ? null : (
                    <Button onPress={() => setRemote(r.id)}>
                      {lw('remote.start', { machine: r.machine })}
                    </Button>
                  )
                }
              />
              {sel ? (
                <RemoteUpdate
                  machine={sel.name}
                  from={sel.version}
                  to={to}
                  state={
                    install.busy
                      ? 'running'
                      : sel.state === 'updating'
                        ? 'running'
                        : sel.state === 'failed'
                          ? 'failed'
                          : sel.state === 'up_to_date'
                            ? 'done'
                            : 'ready'
                  }
                  onStart={() => void install.run(to, sel.machine_id)}
                />
              ) : null}
            </Stack>
          );
        }}
      </Load>
      <Stack gap="tight">
        <Text>
          Stop and start the service after the running jobs end. No running job is
          stopped.
        </Text>
        <Button
          onPress={() => void navigate('/settings/updates/review?kind=restart')}
          disabled={!session.can('admin')}
          reason={session.can('admin') ? undefined : session.whyNot('admin')}
        >
          Restart the service
        </Button>
      </Stack>
      <Stack gap="tight">
        <Text>
          This controller cannot reach the release? Carry the agent installers over by
          hand and import them here.
        </Text>
        <Button
          onPress={() => void navigate('/settings/updates/agent-installers/import')}
          disabled={!session.can('admin')}
          reason={session.can('admin') ? undefined : session.whyNot('admin')}
        >
          Import agent installers
        </Button>
      </Stack>
      {removeStarted ? (
        <Message tone="note" dismissible={false}>
          {lw('remove.started', { product: PRODUCT })}
        </Message>
      ) : (
        <Stack gap="tight">
          <Text>{lw('remove.choose')}</Text>
          <Button kind="danger" onPress={() => setRemoving(true)}>
            {lw('remove.confirm', { product: PRODUCT })}
          </Button>
        </Stack>
      )}
      {removing ? (
        <RemoveDialog
          product={PRODUCT}
          {...removeFacts(
            status.result.state === 'ready' ? status.result.data : undefined,
          )}
          busy={remove.busy}
          onCancel={() => setRemoving(false)}
          onRemove={(c) => void remove.run(c === 'purge')}
        />
      ) : null}
    </Stack>
  );
}

/** The size and path of the data, when the controller said both. */
export function removeFacts(s: UpdateStatusV2 | undefined): {
  size?: string;
  path?: string;
} {
  return s?.data_path !== undefined && s.data_size_bytes !== undefined
    ? { size: formatSize(s.data_size_bytes), path: s.data_path }
    : {};
}

export { errorText };
