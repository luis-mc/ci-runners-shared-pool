import { ApiError } from '../../api/api';
import { useCommand, useQuestion } from '../../api/hooks';

/** What a 409 `needs_confirmation` names: what goes, and the jobs the drain waits for (UX-62). */
function split(e: ApiError): { goes: string[]; waits: string[] } {
  const text = (d: { text?: string; id?: string }) => d.text ?? d.id ?? '';
  return {
    goes: e.details
      .filter((d) => d.kind !== 'job')
      .map(text)
      .filter(Boolean),
    waits: e.details
      .filter((d) => d.kind === 'job')
      .map(text)
      .filter(Boolean),
  };
}

/** Ask the server what a step touches (it answers 409 without `confirm`), then open the question card. */
export function useAskFirst(opts: {
  probe: () => Promise<unknown>;
  commit: () => Promise<unknown>;
  fallback: () => string[];
  onDone: () => void;
}) {
  const ask = useQuestion(opts.commit, opts.onDone);
  const probe = useCommand(async () => {
    try {
      await opts.probe();
      opts.onDone();
    } catch (e) {
      if (e instanceof ApiError && e.code === 'needs_confirmation') {
        const { goes, waits } = split(e);
        ask.ask(goes.length > 0 ? goes : opts.fallback(), waits);
        return;
      }
      throw e;
    }
  });
  return { ask, probe };
}
