// The update banner part with its words, loaded on demand by the banner host (not in the first JavaScript).
import {
  UpdateBanner,
  updateStateLine,
  type BannerState,
  type UpdateStep,
} from '@crsp/ui/lifecycle';
import type { UpdateStatusV2 } from '../api/lifecycle';
import { cardFailure } from '../screens/settings/lifecycleView';

export default function UpdateBannerView(p: {
  state: BannerState;
  data: UpdateStatusV2;
  version: string;
  step: UpdateStep | undefined;
  error: string | undefined;
  onUpdate: () => void;
  onHide: () => void;
}) {
  const d = p.data;
  return (
    <UpdateBanner
      state={p.state}
      version={p.version}
      step={p.step}
      to={p.state === 'running' ? '/settings/updates' : undefined}
      reason={
        p.state === 'failed'
          ? updateStateLine({
              state: d.state,
              current: d.current,
              available: d.available ?? undefined,
              failure: cardFailure(d),
            })
          : p.error
      }
      onUpdate={p.onUpdate}
      onNotNow={p.onHide}
      onRetry={p.onUpdate}
      onClose={p.onHide}
    />
  );
}
