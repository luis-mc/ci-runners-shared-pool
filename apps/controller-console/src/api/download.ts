// Save a file the controller built (an export, UX-39). The controller names it in Content-Disposition; the
// browser saves it through a short-lived object address.
import { ApiError } from './api';

/** The file name of a Content-Disposition value, or the fallback. */
export function fileNameOf(header: string | null, fallback: string): string {
  const m = /filename="?([^";]+)"?/.exec(header ?? '');
  return m?.[1] ?? fallback;
}

export function saveBlob(blob: Blob, name: string): void {
  const url = URL.createObjectURL(blob);
  const a = document.createElement('a');
  a.href = url;
  a.download = name;
  a.rel = 'noopener';
  document.body.append(a);
  a.click();
  a.remove();
  setTimeout(() => URL.revokeObjectURL(url), 0);
}

interface FileAnswer {
  data?: unknown;
  error?: unknown;
  response: Response;
}

/** Read a file answer (called with parseAs "blob") and save it, or throw an ApiError with the contract's code. */
export async function saveExport(
  call: Promise<FileAnswer>,
  fallback: string,
): Promise<void> {
  const { data, error, response } = await call;
  if (!response.ok) {
    const e = (error as { error?: { code?: string; message?: string } } | undefined)
      ?.error;
    throw new ApiError(
      e?.code ?? 'internal',
      e?.message ?? `The controller answered ${response.status}.`,
      response.status,
    );
  }
  saveBlob(
    data instanceof Blob ? data : new Blob([typeof data === 'string' ? data : '']),
    fileNameOf(response.headers.get('Content-Disposition'), fallback),
  );
}
