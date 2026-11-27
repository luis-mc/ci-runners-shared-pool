// The update banner, app-wide (UX-27, one banner slot): it reads the update status and shows the UpdateBanner part
// when an update is ready to start, running, done or failed. Not now hides it until the status changes.
import { lazy, Suspense, useEffect, useState, type ReactNode } from 'react';
import { useCommand, useLoad } from '../api/hooks';
import { useLifecycle } from '../api/lifecycle';
import { useRefreshTick } from '../api/machines';
import { bannerFor, stepOf } from '../screens/settings/lifecycleView';

const UpdateBannerView = lazy(() => import('./UpdateBannerView'));

const REFRESH_MS = 10_000;

/** The banner element, or null when nothing is worth showing (the banner slot then stays empty). */
export function useUpdateBanner(): ReactNode {
  const api = useLifecycle();
  const tick = useRefreshTick(REFRESH_MS);
  const { result, reload } = useLoad(() => api.status(), [tick]);
  // The state the person hid; a different state or version brings the banner back.
  const [hidden, setHidden] = useState<string | undefined>(undefined);
  const install = useCommand(async (version: string) => {
    await api.install(version);
    reload();
  });
  const data =
    result.state === 'ready' || result.state === 'failed' ? result.data : undefined;
  const key = data ? `${data.state}|${data.available ?? data.current}` : '';
  useEffect(() => {
    if (hidden !== undefined && hidden !== key) setHidden(undefined);
  }, [hidden, key]);
  if (!data) return null;
  const state = bannerFor(data, false);
  if (!state || hidden === key) return null;
  const version = state === 'done' ? data.current : (data.available ?? data.current);
  return (
    <Suspense key="update" fallback={null}>
      <UpdateBannerView
        state={state}
        data={data}
        version={version}
        step={stepOf(data)}
        error={install.error}
        onUpdate={() => void install.run(version)}
        onHide={() => setHidden(key)}
      />
    </Suspense>
  );
}
