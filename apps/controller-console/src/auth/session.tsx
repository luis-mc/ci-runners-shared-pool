// Session state (R5.8..R5.13). No valid session sends every path but /sign-in and /setup to /sign-in and keeps
// the path it came from. A viewer sees what the role allows; the server decides (R6.5).
import type { ReactNode } from 'react';
import {
  createContext,
  useCallback,
  useContext,
  useEffect,
  useMemo,
  useState,
} from 'react';
import { ApiError, unwrap, useApi, type Schemas } from '../api/api';

export type Role = Schemas['Role'];
const RANK: Record<Role, number> = { viewer: 0, admin: 1, owner: 2 };

export type SessionState =
  | { status: 'loading' }
  | { status: 'signed-out' }
  | { status: 'no-access'; login: string }
  | { status: 'failed'; message: string }
  | { status: 'ready'; session: Schemas['Session'] };

export interface SessionApi {
  state: SessionState;
  /** True when a call found the session ended (R5.11): the dialog shows over the current page. */
  ended: boolean;
  refresh: () => void;
  signOut: () => Promise<void>;
  /** Does the signed-in role reach this level? */
  can: (level: Role) => boolean;
  /** Why a command is disabled for this role (UX-38), or undefined when it is allowed. */
  whyNot: (level: Role) => string | undefined;
}

const SessionContext = createContext<SessionApi | null>(null);

/** Only a path on the same origin is accepted (R5.9); anything else goes to the overview. */
export function safeReturnPath(raw: string | null | undefined): string {
  if (!raw || !raw.startsWith('/') || raw.startsWith('//') || raw.includes('\\'))
    return '/';
  return raw;
}

export function SessionProvider({ children }: { children: ReactNode }) {
  const api = useApi();
  const [state, setState] = useState<SessionState>({ status: 'loading' });
  const [ended, setEnded] = useState(false);
  const [tick, setTick] = useState(0);

  useEffect(() => {
    api.onSessionEnded.current = () => setEnded(true);
    return () => {
      api.onSessionEnded.current = undefined;
    };
  }, [api]);

  useEffect(() => {
    let live = true;
    unwrap(api.client.GET('/api/v1/me'))
      .then((session) => {
        if (!live) return;
        api.setCsrf(session.csrf);
        setEnded(false);
        setState({ status: 'ready', session });
      })
      .catch((e: unknown) => {
        if (!live) return;
        api.setCsrf(undefined);
        if (
          e instanceof ApiError &&
          (e.code === 'unauthenticated' || e.code === 'session_expired')
        ) {
          setState({ status: 'signed-out' });
        } else if (e instanceof ApiError && e.code === 'forbidden') {
          setState({ status: 'no-access', login: e.details[0]?.id ?? '' });
        } else {
          setState({
            status: 'failed',
            message: e instanceof Error ? e.message : 'The controller did not answer.',
          });
        }
      });
    return () => {
      live = false;
    };
  }, [api, tick]);

  const signOut = useCallback(async () => {
    try {
      await unwrap(api.client.POST('/api/v1/auth/logout'));
    } finally {
      api.setCsrf(undefined);
      setState({ status: 'signed-out' });
    }
  }, [api]);

  const value = useMemo<SessionApi>(() => {
    const role = state.status === 'ready' ? state.session.user.role : undefined;
    const can = (level: Role) => role !== undefined && RANK[role] >= RANK[level];
    return {
      state,
      ended,
      refresh: () => setTick((t) => t + 1),
      signOut,
      can,
      whyNot: (level) =>
        can(level)
          ? undefined
          : level === 'owner'
            ? 'Only the owner can do this.'
            : 'Only an admin can do this.',
    };
  }, [state, ended, signOut]);

  return <SessionContext.Provider value={value}>{children}</SessionContext.Provider>;
}

export function useSession(): SessionApi {
  const v = useContext(SessionContext);
  if (!v) throw new Error('useSession needs a SessionProvider');
  return v;
}

export function useUser(): Schemas['User'] {
  const s = useSession();
  if (s.state.status !== 'ready') throw new Error('useUser needs a signed-in session');
  return s.state.session.user;
}

/** Where sign-in goes: the controller starts GitHub's flow and comes back to `returnTo` (R5.9). */
export function signInUrl(returnTo: string): string {
  return `/auth/github/login?return_to=${encodeURIComponent(safeReturnPath(returnTo))}`;
}
