// The lifecycle API (install, update, remove): the contract shared with the controller. The generated client does
// not know these paths yet, so they go through one narrow, typed wrapper. Old paths stay as aliases for one release.
import { ApiError, unwrap, useClient } from './api';
import type {
  AuditEvent,
  UpdatePolicy,
  UpdateState,
  FleetState,
} from '@crsp/ui/lifecycle';
import { useMemo } from 'react';

export interface UpdateStatusV2 {
  state: UpdateState;
  current: string;
  available: string | null;
  required: boolean;
  policy: UpdatePolicy;
  window: { start: string; end: string } | null;
  next_check_at: string | null;
  failures: { version: string; count: number; retry_at: string | null } | null;
  waiting_for_jobs: boolean;
  /** 0 to 100 while a step runs. */
  percent?: number | undefined;
  /** Where crsp keeps its data and how big it is, when the controller says (for the Remove question). */
  data_path?: string | undefined;
  data_size_bytes?: number | undefined;
}

export interface HistoryItem {
  at: string;
  actor: string;
  action: AuditEvent['action'];
  version: string;
  result: AuditEvent['result'];
}

export interface FleetItem {
  machine_id: string;
  name: string;
  version: string;
  state: FleetState;
}

type Answer<T> = { data?: T; error?: unknown; response: Response };
interface Raw {
  GET: (path: string) => Promise<Answer<unknown>>;
  POST: (path: string, init?: { body?: unknown }) => Promise<Answer<unknown>>;
}

export interface LifecycleApi {
  status: () => Promise<UpdateStatusV2>;
  history: () => Promise<HistoryItem[]>;
  fleet: () => Promise<FleetItem[]>;
  setPolicy: (
    policy: UpdatePolicy,
    window: { start: string; end: string } | null,
  ) => Promise<void>;
  check: () => Promise<void>;
  install: (version: string, machineId?: string) => Promise<void>;
  remove: (deleteData: boolean, machineId?: string) => Promise<void>;
}

export function makeLifecycleApi(raw: Raw): LifecycleApi {
  const get = <T>(p: string) => unwrap<T>(raw.GET(p) as Promise<Answer<T>>);
  const post = async (p: string, body?: unknown) => {
    await unwrap<unknown>(raw.POST(p, body === undefined ? {} : { body }));
  };
  return {
    status: () => get<UpdateStatusV2>('/api/update/status'),
    history: () => get<HistoryItem[]>('/api/update/history'),
    fleet: () => get<FleetItem[]>('/api/update/fleet'),
    setPolicy: (policy, window) => post('/api/update/policy', { policy, window }),
    check: () => post('/api/update/check'),
    install: (version, machineId) =>
      post(
        '/api/update/install',
        machineId === undefined ? { version } : { version, machine_id: machineId },
      ),
    // delete_data is required: the caller always says which, never a default.
    remove: (deleteData, machineId) =>
      post(
        '/api/remove',
        machineId === undefined
          ? { delete_data: deleteData }
          : { machine_id: machineId, delete_data: deleteData },
      ),
  };
}

export function useLifecycle(): LifecycleApi {
  const client = useClient();
  return useMemo(() => makeLifecycleApi(client as unknown as Raw), [client]);
}

export { ApiError };
