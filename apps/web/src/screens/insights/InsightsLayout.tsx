// Analytics > Suggestions (LOOK56): a segment row (component 12) holds Do next, Tests and Guardrails; a filter by repository is kept across the segments (UX-31).
import { Chips, Segmented, Stack } from '@crsp/ui';
import { Outlet, useLocation, useNavigate, useSearchParams } from 'react-router-dom';
import { useFindings } from './api';

export const SEGMENTS = [
  { value: 'do-next', label: 'Do next', to: '/analytics' },
  { value: 'tests', label: 'Tests', to: '/analytics/tests' },
  { value: 'guardrails', label: 'Guardrails', to: '/analytics/guardrails' },
] as const;

/** The repository the chips chose, '' for every project. */
export function useRepositoryFilter(): string {
  return useSearchParams()[0].get('repository') ?? '';
}

export function InsightsLayout() {
  const navigate = useNavigate();
  const { pathname } = useLocation();
  const [params, setParams] = useSearchParams();
  const repository = params.get('repository') ?? '';
  const part = pathname.split('/')[2] || 'do-next';
  const current = SEGMENTS.find((s) => s.value === part)?.value ?? 'do-next';
  const { result } = useFindings(undefined);
  const names =
    result.state === 'ready'
      ? [
          ...new Set(
            result.data.items.flatMap((f) => (f.repository ? [f.repository] : [])),
          ),
        ]
      : [];
  return (
    <Stack gap="section">
      <Segmented
        label="Run insights views"
        options={SEGMENTS.map(({ value, label }) => ({ value, label }))}
        value={current}
        allLabel={null}
        onChange={(v) => {
          const to = SEGMENTS.find((s) => s.value === v)?.to ?? '/analytics';
          void navigate({ pathname: to, search: params.toString() });
        }}
      />
      {names.length > 0 ? (
        <Chips
          label="Project"
          options={names.map((n) => ({ value: n, label: n }))}
          value={repository}
          onChange={(v) => {
            const next = new URLSearchParams(params);
            if (v) next.set('repository', v);
            else next.delete('repository');
            setParams(next, { replace: true });
          }}
        />
      ) : null}
      <Outlet />
    </Stack>
  );
}
