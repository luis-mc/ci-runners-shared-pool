// Shared screen parts: the loading, could-not-load and fault states of every screen (R5.31, UX-28, UX-6).
import { Button, Failed, Loading, Message, formatAgo, useNow } from '@crsp/ui';
import { Component, useEffect, type ReactNode } from 'react';
import { useRouteError } from 'react-router-dom';
import { ApiError } from '../api/api';
import type { Loaded } from '../api/hooks';

/** How soon a screen asks again while the service restarts. */
export const RESTART_RETRY_MS = 3000;
export const RESTARTING = 'The service is restarting, back in a moment.';

/** The service did not answer at all (a lost connection, as during an install restart), not a refusal. */
const isUnreachable = (e: Error): boolean =>
  e instanceof ApiError && e.code === 'unavailable';

export function Load<T>({
  result,
  what,
  reload,
  children,
}: {
  result: Loaded<T>;
  what: string;
  reload: () => void;
  children: (data: T) => ReactNode;
}) {
  const now = useNow();
  const restarting = result.state === 'failed' && isUnreachable(result.error);
  // While the service restarts the screen asks again by itself, quietly (UX-28).
  useEffect(() => {
    if (!restarting) return undefined;
    const t = setTimeout(reload, RESTART_RETRY_MS);
    return () => clearTimeout(t);
  }, [restarting, reload, result]);
  if (result.state === 'loading') return <Loading what={`Loading ${what}`} />;
  if (restarting && result.state === 'failed' && result.data === undefined) {
    return <Loading what={RESTARTING} />;
  }
  if (result.state === 'failed' && result.data === undefined) {
    const why =
      result.error instanceof ApiError && result.error.code === 'github_unavailable'
        ? 'GitHub is not answering.'
        : result.error.message;
    return (
      <Failed
        what={`Could not load ${what}.`}
        why={`${why} Try again.`}
        action={<Button onPress={reload}>Try again</Button>}
      />
    );
  }
  const data = result.state === 'ready' ? result.data : (result as { data: T }).data;
  return (
    <>
      {restarting ? (
        <Message tone="note" dismissible={false}>
          {RESTARTING}
        </Message>
      ) : result.state === 'failed' ? (
        <Message tone="problem" action={<Button onPress={reload}>Try again</Button>}>
          Could not refresh {what}. {result.error.message} The list shows the last copy,
          from {formatAgo(result.loadedAt ?? now, now)}.
        </Message>
      ) : null}
      {children(data)}
    </>
  );
}

/** A fault in the page's own code is not a lost connection (UX-28). */
export class FaultBoundary extends Component<
  { children: ReactNode },
  { error: Error | undefined }
> {
  override state: { error: Error | undefined } = { error: undefined };
  static getDerivedStateFromError(error: Error) {
    return { error };
  }
  override render() {
    if (this.state.error) {
      return (
        <Failed
          what=""
          why=""
          fault
          details={this.state.error.stack ?? this.state.error.message}
        />
      );
    }
    return this.props.children;
  }
}

import { QuestionCard } from '@crsp/ui';
import type { Question } from '../api/hooks';

/** The question card of a destructive step, with its one-line result under it (UX-27). */
export function Ask({
  title,
  intro,
  action,
  busyLabel,
  q,
  busy,
  error,
  onCancel,
  onConfirm,
}: {
  title: string;
  intro?: string;
  action: string;
  busyLabel: string;
  q: Question | undefined;
  busy: boolean;
  error: string | undefined;
  onCancel: () => void;
  onConfirm: () => void;
}) {
  if (!q) return null;
  return (
    <QuestionCard
      title={title}
      intro={intro}
      items={error ? [...q.items, `Problem: ${error}`] : q.items}
      waitsFor={q.waitsFor}
      action={action}
      busyLabel={busyLabel}
      busy={busy}
      onCancel={onCancel}
      onConfirm={onConfirm}
    />
  );
}

const RELOAD_KEY = 'crsp-stale-reload';
const RELOAD_GAP_MS = 60_000;

/** A page opened on an older build asks for a hashed file the new build no longer has. */
export function isStaleBuildError(e: unknown): boolean {
  const m = e instanceof Error ? e.message : String(e ?? '');
  return /dynamically imported module|importing a module script failed|ChunkLoadError/i.test(m);
}

/** Reloads once for a stale build. False when it already did a moment ago (no reload loop). */
export function reloadOnceForNewBuild(now: number = Date.now()): boolean {
  try {
    const last = Number(window.sessionStorage.getItem(RELOAD_KEY) ?? 0);
    if (now - last < RELOAD_GAP_MS) return false;
    window.sessionStorage.setItem(RELOAD_KEY, String(now));
  } catch {
    return false;
  }
  window.location.reload();
  return true;
}

/** The route's error page, in plain words, in place of the developer error screen (UX-28). */
export function RouteError() {
  const error = useRouteError();
  const stale = isStaleBuildError(error);
  const reloading = stale && reloadOnceForNewBuild();
  if (reloading) return <Loading what="The service has a new version. Reloading" />;
  return (
    <Failed
      pageTitle="Something went wrong"
      what={stale ? 'The service has a new version.' : 'This page could not open.'}
      why={
        stale
          ? 'Reload to get the new version.'
          : 'Reload the page. If it keeps happening, the details are in the service log.'
      }
      action={<Button onPress={() => window.location.reload()}>Reload</Button>}
    />
  );
}
