// How the lifecycle parts read the contract's answers: times in words, states into the parts' props.
import type { BannerState, UpdateStep, UpdatesCardProps } from '@crsp/ui/lifecycle';
import { formatDateTime } from '@crsp/ui';
import type { UpdateStatusV2 } from '../../api/lifecycle';

export const fmtTime = (iso: string | null | undefined): string =>
  iso ? formatDateTime(iso) : '';

/** The Updates card's props from the status the controller sent. */
export function cardFailure(s: UpdateStatusV2): UpdatesCardProps['failure'] {
  return s.failures
    ? {
        version: s.failures.version,
        count: s.failures.count,
        retryAt: fmtTime(s.failures.retry_at),
      }
    : null;
}

/** The banner state for a status, or undefined when nothing is worth a banner. */
export function bannerFor(s: UpdateStatusV2, doneSeen: boolean): BannerState | undefined {
  switch (s.state) {
    case 'available':
    case 'waiting_for_jobs':
      return s.policy === 'ask_first' || s.required ? 'ready' : undefined;
    case 'downloading':
    case 'verifying':
    case 'installing':
    case 'testing':
      return 'running';
    case 'updated':
      return doneSeen ? undefined : 'done';
    case 'rolled_back':
    case 'refused':
    case 'failed':
      return 'failed';
    default:
      return undefined;
  }
}

export const stepOf = (s: UpdateStatusV2): UpdateStep | undefined =>
  s.state === 'downloading' ||
  s.state === 'verifying' ||
  s.state === 'installing' ||
  s.state === 'testing'
    ? s.state
    : undefined;
