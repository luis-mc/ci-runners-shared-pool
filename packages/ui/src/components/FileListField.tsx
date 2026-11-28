// Component 14c: the file list field. Like the file field (14b) but for many files: a dashed drop zone
// that takes several files at once, then one row per file with its name, size and Remove. The screen
// reads the files and never shows their content (UX-10, UX-20, UX-21, UX-23).
import { useId, useRef, useState, type DragEvent } from 'react';
import type { FieldShellProps } from './Fields';
import { formatSize } from './format';

export function FileListField({
  label,
  help,
  error,
  optional,
  width = 'full',
  onChange,
  disabled,
  initial,
}: FieldShellProps & {
  /** Called with every file now chosen (a file with a name already chosen replaces it). */
  onChange: (files: File[]) => void;
  disabled?: boolean | undefined;
  /** The files chosen before (a wizard that comes back to this step). */
  initial?: readonly File[] | undefined;
}) {
  const id = useId();
  const helpId = `${id}-h`;
  const errorId = `${id}-e`;
  const input = useRef<HTMLInputElement>(null);
  const [files, setFiles] = useState<File[]>(() => [...(initial ?? [])]);
  const [over, setOver] = useState(false);

  const set = (next: File[]) => {
    setFiles(next);
    onChange(next);
  };
  const take = (picked: ArrayLike<File> | null | undefined) => {
    if (!picked || picked.length === 0) return;
    const added = Array.from(picked);
    const names = new Set(added.map((f) => f.name));
    set([...files.filter((f) => !names.has(f.name)), ...added]);
    if (input.current) input.current.value = '';
  };
  const remove = (name: string) => set(files.filter((f) => f.name !== name));
  const choose = () => input.current?.click();
  const drop = (e: DragEvent) => {
    e.preventDefault();
    setOver(false);
    if (!disabled) take(e.dataTransfer.files);
  };

  return (
    <div
      className="ds-field"
      data-width={width}
      data-invalid={Boolean(error) || undefined}
    >
      <label className="ds-field__label" htmlFor={id} title={label}>
        {label}
        {optional ? ' (optional)' : ''}
      </label>
      {help ? (
        <p className="ds-field__help" id={helpId}>
          {help}
        </p>
      ) : null}
      {error ? (
        <p className="ds-field__error" id={errorId}>
          {error}
        </p>
      ) : null}
      <input
        ref={input}
        id={id}
        className="ds-file__input"
        type="file"
        multiple
        tabIndex={-1}
        disabled={disabled}
        aria-invalid={error ? true : undefined}
        aria-describedby={
          [help ? helpId : '', error ? errorId : ''].join(' ').trim() || undefined
        }
        onChange={(e) => take(e.target.files)}
      />
      <div
        className="ds-file"
        data-state={files.length > 0 ? 'chosen' : error ? 'refused' : 'empty'}
        data-over={over || undefined}
        data-disabled={disabled || undefined}
        onDragOver={(e) => {
          e.preventDefault();
          if (!disabled) setOver(true);
        }}
        onDragLeave={() => setOver(false)}
        onDrop={drop}
      >
        <span className="ds-file__prompt">Drop the files here, or</span>
        <button type="button" className="ds-button" disabled={disabled} onClick={choose}>
          {files.length > 0 ? 'Add more files' : 'Choose files'}
        </button>
      </div>
      {files.length > 0 ? (
        <ul className="ds-file__list">
          {files.map((f) => (
            <li key={f.name} className="ds-file__row">
              <span className="ds-file__meta">
                <span className="ds-file__name" title={f.name}>
                  {f.name}
                </span>
                <span className="ds-file__size">{formatSize(f.size)}</span>
              </span>
              <button
                type="button"
                className="ds-button"
                disabled={disabled}
                aria-label={`Remove ${f.name}`}
                onClick={() => remove(f.name)}
              >
                Remove
              </button>
            </li>
          ))}
        </ul>
      ) : null}
    </div>
  );
}
