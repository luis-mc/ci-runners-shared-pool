// The one API wrapper (R5.8..R5.11, R6.2). It adds the CSRF token to changes, turns an error answer into
// an ApiError with the contract's code, and tells the session when a call finds the session ended.
import { createApiClient, type ApiClient, type components } from '@crsp/contract';
import { createContext, useContext, useMemo, useRef, type ReactNode } from 'react';

export type Schemas = components['schemas'];

export class ApiError extends Error {
  readonly code: string;
  readonly status: number;
  readonly details: readonly { kind?: string; id?: string; text?: string }[];
  constructor(
    code: string,
    message: string,
    status: number,
    details: readonly { kind?: string; id?: string; text?: string }[] = [],
  ) {
    super(message);
    this.code = code;
    this.status = status;
    this.details = details;
  }
}

interface ApiContextValue {
  client: ApiClient;
  /** Set by the session: the CSRF token to send on changes. */
  setCsrf: (token: string | undefined) => void;
  /** Set by the session: called when a call finds the session ended (R5.11). */
  onSessionEnded: { current: (() => void) | undefined };
}

const ApiContext = createContext<ApiContextValue | null>(null);

export function ApiProvider({
  children,
  client: given,
}: {
  children: ReactNode;
  client?: ApiClient;
}) {
  const csrf = useRef<string | undefined>(undefined);
  const ended = useRef<(() => void) | undefined>(undefined);
  const value = useMemo<ApiContextValue>(() => {
    const client = given ?? createApiClient();
    client.use({
      onRequest({ request }) {
        if (csrf.current && request.method !== 'GET' && request.method !== 'HEAD') {
          request.headers.set('X-CSRF-Token', csrf.current);
        }
        return request;
      },
      async onResponse({ response }) {
        if (response.status === 401) {
          const body = (await response
            .clone()
            .json()
            .catch(() => null)) as { error?: { code?: string } } | null;
          if (body?.error?.code === 'session_expired') ended.current?.();
        }
        return response;
      },
    });
    return {
      client,
      setCsrf: (t) => {
        csrf.current = t;
      },
      onSessionEnded: ended,
    };
  }, [given]);
  return <ApiContext.Provider value={value}>{children}</ApiContext.Provider>;
}

export function useApi(): ApiContextValue {
  const v = useContext(ApiContext);
  if (!v) throw new Error('useApi needs an ApiProvider');
  return v;
}

export function useClient(): ApiClient {
  return useApi().client;
}

interface Answer<T> {
  data?: T;
  error?: unknown;
  response: Response;
}

/** Read one answer: the data, or an ApiError carrying the contract's code. */
export async function unwrap<T>(call: Promise<Answer<T>>): Promise<T> {
  let answer: Answer<T>;
  try {
    answer = await call;
  } catch (e) {
    throw new ApiError('unavailable', e instanceof Error ? e.message : 'No answer.', 0);
  }
  if (answer.response.ok) return answer.data as T;
  const env = answer.error as
    { error?: { code?: string; message?: string; details?: never } } | undefined;
  const e = env?.error;
  throw new ApiError(
    e?.code ?? 'internal',
    e?.message ?? `The controller answered ${answer.response.status}.`,
    answer.response.status,
    (e as { details?: ApiError['details'] } | undefined)?.details ?? [],
  );
}

export function errorText(e: unknown): string {
  return e instanceof Error ? e.message : 'Something went wrong.';
}
