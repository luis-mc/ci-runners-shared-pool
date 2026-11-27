// The lifecycle API (install, update, remove): the contract shared with the controller. The generated client does
// not know these paths yet, so they go through one narrow, typed wrapper. Old paths stay as aliases for one release.
import { ApiError, unwrap, useClient } from './api';
import type {
  AuditEvent,
  UpdatePolicy,
  UpdateState,
  FleetState,
} from '@crsp/ui/lifecycle';
import type { components } from '@crsp/contract';
import { useMemo } from 'react';

type Schemas = components['schemas'];
/** What the controller sends (openapi LifecycleStatus.state, UpdateHistoryRow, UpdateFleetMachine). */
export type WireState = Schemas['LifecycleStatus']['state'];
export type WireStatus = Schemas['LifecycleStatus'];
export type WireHistoryRow = Schemas['UpdateHistoryRow'];
export type WireFleetMachine = Schemas['UpdateFleetMachine'];

/**
 * Every state the contract allows, as the lifecycle words know it. A Record over the generated union: a new
 * server state without an entry fails typecheck (CRSP225).
 */
export const STATE_FROM_WIRE: Record<WireState, UpdateState> = {
  unpaired: 'idle',
  up_to_date: 'idle',
  available: 'available',
  review: 'available',
  waiting: 'waiting_for_jobs',
  downloading: 'downloading',
  verifying: 'verifying',
  refused: 'refused',
  installing: 'installing',
  testing: 'testing',
  updated: 'updated',
  rolling_back: 'rolled_back',
  rolled_back: 'rolled_back',
  skipped: 'rolled_back',
};

export const POLICY_FROM_WIRE: Record<
  Schemas['LifecycleStatus']['policy'],
  UpdatePolicy
> = {
  automatic_when_idle: 'automatic_when_idle',
  ask_first: 'ask_first',
};

export const ACTION_FROM_WIRE: Record<WireHistoryRow['action'], AuditEvent['action']> = {
  check: 'check',
  install: 'install',
  rollback: 'rollback',
  remove: 'remove',
};

/** The history result is a free string in the contract; these are the values the controller writes. */
export const RESULT_FROM_WIRE: Record<string, AuditEvent['result']> = {
  ok: 'ok',
  done: 'ok',
  failed: 'failed',
  refused: 'failed',
  skipped: 'skipped',
};

/** The fleet state is a free string in the contract. Lifecycle names, plus the status names a machine may report. */
export const FLEET_FROM_WIRE: Record<string, FleetState> = {
  up_to_date: 'up_to_date',
  idle: 'up_to_date',
  waiting: 'waiting',
  waiting_for_jobs: 'waiting',
  needs_you: 'needs_you',
  review: 'needs_you',
  available: 'needs_you',
  updating: 'updating',
  downloading: 'updating',
  verifying: 'updating',
  installing: 'updating',
  testing: 'updating',
  failed: 'failed',
  refused: 'failed',
  rolled_back: 'failed',
};

const warned = new Set<string>();
function pick<V>(table: Record<string, V>, key: string, fallback: V, what: string): V {
  const v = Object.hasOwn(table, key) ? table[key] : undefined;
  if (v !== undefined) return v;
  if (!warned.has(`${what}.${key}`)) {
    warned.add(`${what}.${key}`);
    console.warn(`lifecycle: unknown ${what} "${key}" from the controller`);
  }
  return fallback;
}

export function statusFromWire(w: WireStatus): UpdateStatusV2 {
  return {
    state: pick(STATE_FROM_WIRE, w.state, 'idle', 'state'),
    current: w.current,
    available: w.available,
    required: w.required,
    policy: pick(POLICY_FROM_WIRE, w.policy, 'automatic_when_idle', 'policy'),
    window: w.window,
    next_check_at: w.next_check_at,
    failures: w.failures,
    waiting_for_jobs: w.waiting_for_jobs,
  };
}

export const historyFromWire = (w: WireHistoryRow): HistoryItem => ({
  at: w.at,
  actor: w.actor,
  action: pick(ACTION_FROM_WIRE, w.action, 'check', 'action'),
  version: w.version,
  result: pick(RESULT_FROM_WIRE, w.result, 'skipped', 'result'),
});

export const fleetFromWire = (w: WireFleetMachine): FleetItem => ({
  machine_id: w.machine_id,
  name: w.name,
  version: w.version,
  state: pick(FLEET_FROM_WIRE, w.state, 'waiting', 'fleet state'),
});

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
  /** The last time crsp looked for an update, when the controller says (CRSP368). */
  last_check?: { at: string; by: string; kind: 'manual' | 'automatic' } | undefined;
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
    status: async () => {
      const w = await get<
        WireStatus & { percent?: number; data_path?: string; data_size_bytes?: number }
      >('/api/v1/update/status');
      return {
        ...statusFromWire(w),
        ...(w.last_check ? { last_check: w.last_check } : {}),
        ...(w.percent === undefined ? {} : { percent: w.percent }),
        ...(w.data_path === undefined ? {} : { data_path: w.data_path }),
        ...(w.data_size_bytes === undefined
          ? {}
          : { data_size_bytes: w.data_size_bytes }),
      };
    },
    history: async () =>
      (await get<WireHistoryRow[]>('/api/v1/update/history')).map(historyFromWire),
    fleet: async () =>
      (await get<WireFleetMachine[]>('/api/v1/update/fleet')).map(fleetFromWire),
    setPolicy: (policy, window) => post('/api/v1/update/policy', { policy, window }),
    check: () => post('/api/v1/update/check'),
    install: (version, machineId) =>
      post(
        '/api/v1/update/install',
        machineId === undefined ? { version } : { version, machine_id: machineId },
      ),
    // delete_data is required: the caller always says which, never a default.
    remove: (deleteData, machineId) =>
      post(
        '/api/v1/remove',
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
