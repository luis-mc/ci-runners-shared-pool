import createClient from 'openapi-fetch';
import type { paths } from './generated/schema';

export type { components, paths } from './generated/schema';

export type ApiClient = ReturnType<typeof createApiClient>;

/** Typed client for the controller API, generated from openapi.yaml. */
export function createApiClient(baseUrl = '', fetchImpl?: typeof fetch) {
  return createClient<paths>({ baseUrl, ...(fetchImpl ? { fetch: fetchImpl } : {}) });
}
export * from './progress';
