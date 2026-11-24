// Component 14: the form field family. The name sits above the box, help between them, and a mistake above
// the box after the action is pressed (UX-20, UX-21). Rows for up to 6 answers, a drop-down for 7 or more
// (UX-22). Switches save at once; check boxes wait for Save.
import { useId, type ReactNode } from 'react';
import { CaretInput } from './CaretInput';
import { ThemePreview } from './ThemePreview';

export { FileField } from './FileField';

export interface FieldShellProps {
  label: string;
  help?: string | undefined;
  error?: string | undefined;
  optional?: boolean | undefined;
  width?: 'short' | 'medium' | 'full' | undefined;
  /** The value differs from what is saved: the field is marked "Changed" (LOOK21, UX-71). */
  changed?: boolean | undefined;
}

/** The mark of a field whose value differs from what is saved. Drawn by the shared parts only. */
export function ChangedMark() {
  return (
    <span className="ds-field__changed" aria-hidden="true">
      Changed
    </span>
  );
}

function Shell({
  label,
  help,
  error,
  optional,
  changed,
  width = 'medium',
  htmlFor,
  helpId,
  errorId,
  children,
}: FieldShellProps & {
  htmlFor: string;
  helpId: string;
  errorId: string;
  children: ReactNode;
}) {
  return (
    <div
      className="ds-field"
      data-width={width}
      data-invalid={Boolean(error) || undefined}
      data-changed={changed || undefined}
    >
      <div className="ds-field__head">
        <label className="ds-field__label" htmlFor={htmlFor} title={label}>
          {label}
          {optional ? ' (optional)' : ''}
        </label>
        {changed ? <ChangedMark /> : null}
      </div>
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
      {children}
    </div>
  );
}

export function TextField(
  props: FieldShellProps & {
    value: string;
    onChange: (value: string) => void;
    autoComplete?: string | undefined;
    mono?: boolean | undefined;
    readOnly?: boolean | undefined;
    /** A write-only secret (a password): hidden while typed, never filled in again (components 14). */
    secret?: boolean | undefined;
  },
) {
  const id = useId();
  const helpId = `${id}-h`;
  const errorId = `${id}-e`;
  return (
    <Shell {...props} htmlFor={id} helpId={helpId} errorId={errorId}>
      <CaretInput
        id={id}
        className="ds-input"
        type={props.secret ? 'password' : 'text'}
        value={props.value}
        readOnly={props.readOnly}
        autoComplete={props.autoComplete ?? (props.secret ? 'new-password' : 'off')}
        data-mono={props.mono || undefined}
        aria-invalid={props.error ? true : undefined}
        aria-describedby={
          [props.help ? helpId : '', props.error ? errorId : ''].join(' ').trim() ||
          undefined
        }
        onChange={(e) => props.onChange(e.target.value)}
      />
    </Shell>
  );
}

/** A text box of several lines, one item per line (components 14, UX-20, UX-21, UX-23). */
export function TextAreaField(
  props: FieldShellProps & {
    value: string;
    onChange: (value: string) => void;
    rows?: number | undefined;
    mono?: boolean | undefined;
    readOnly?: boolean | undefined;
  },
) {
  const id = useId();
  const helpId = `${id}-h`;
  const errorId = `${id}-e`;
  return (
    <Shell {...props} htmlFor={id} helpId={helpId} errorId={errorId}>
      <textarea
        id={id}
        className="ds-input ds-input--area"
        rows={props.rows ?? 6}
        value={props.value}
        readOnly={props.readOnly}
        spellCheck={false}
        autoComplete="off"
        data-mono={props.mono || undefined}
        aria-invalid={props.error ? true : undefined}
        aria-describedby={
          [props.help ? helpId : '', props.error ? errorId : ''].join(' ').trim() ||
          undefined
        }
        onChange={(e) => props.onChange(e.target.value)}
      />
    </Shell>
  );
}

export interface Choice {
  value: string;
  label: string;
  /** A theme id: the row shows a small row of that theme's colours, drawn from its tokens (UX-60). */
  swatch?: string | undefined;
  /** A theme id: the row shows a live ThemePreview of that theme in dark and in light (UX-60, component 42). */
  preview?: string | undefined;
  /** Why the choice cannot be taken (UX-38). */
  disabledReason?: string | undefined;
  /** Line 2, in the normal colour: the value of the option, such as an address (LOOK14). */
  detail?: string | undefined;
  /** Line 3, muted: what the option means (LOOK14). With `detail` or `description` the label is the bold name. */
  description?: string | undefined;
  /** Shown under the description only while this option is chosen, such as a text field (LOOK14). */
  extra?: ReactNode | undefined;
}

/** A row of a theme's colours. The theme id scopes the tokens, so the colours are the theme's own. */
function Swatches({ theme }: { theme: string }) {
  return (
    <span className="ds-swatches" data-theme-name={theme} aria-hidden="true">
      {['surface', 'primary', 'done', 'note', 'needs-you', 'problem'].map((t) => (
        <span className="ds-swatch" data-swatch={t} key={t} />
      ))}
    </span>
  );
}

/** A one-choice list (component 13): rows for up to 6 answers, a drop-down for 7 or more (UX-22). */
export function ChoiceField(
  props: FieldShellProps & {
    value: string;
    choices: readonly Choice[];
    onChange: (value: string) => void;
  },
) {
  const id = useId();
  const helpId = `${id}-h`;
  const errorId = `${id}-e`;
  if (props.choices.length >= 7) {
    return (
      <Shell {...props} htmlFor={id} helpId={helpId} errorId={errorId}>
        <select
          id={id}
          className="ds-input"
          value={props.value}
          onChange={(e) => props.onChange(e.target.value)}
        >
          {props.choices.map((c) => (
            <option key={c.value} value={c.value} disabled={Boolean(c.disabledReason)}>
              {c.label}
            </option>
          ))}
        </select>
      </Shell>
    );
  }
  return (
    <div
      className="ds-field"
      data-width="full"
      data-invalid={Boolean(props.error) || undefined}
      data-changed={props.changed || undefined}
    >
      <fieldset
        className="ds-choice"
        data-cards={props.choices.some((c) => c.preview) || undefined}
        aria-describedby={props.help ? helpId : undefined}
      >
        <legend className="ds-field__label">
          {props.label}
          {props.optional ? ' (optional)' : ''}
          {props.changed ? <ChangedMark /> : null}
        </legend>
        {props.help ? (
          <p className="ds-field__help" id={helpId}>
            {props.help}
          </p>
        ) : null}
        {props.error ? <p className="ds-field__error">{props.error}</p> : null}
        {props.choices.map((c) => {
          const structured = Boolean(c.detail || c.description);
          const row = (
            <label
              className="ds-choice__row"
              key={structured ? undefined : c.value}
              data-card={c.preview ? true : undefined}
              data-structured={structured || undefined}
              data-selected={props.value === c.value || undefined}
              data-disabled={Boolean(c.disabledReason) || undefined}
            >
              <input
                type="radio"
                className="ds-radio"
                name={id}
                value={c.value}
                checked={props.value === c.value}
                disabled={Boolean(c.disabledReason)}
                onChange={() => props.onChange(c.value)}
              />
              <span className="ds-choice__text">
                {structured ? (
                  <span className="ds-choice__name">{c.label}</span>
                ) : (
                  c.label
                )}
                {c.detail ? <span className="ds-choice__detail">{c.detail}</span> : null}
                {c.description ? (
                  <span className="ds-choice__description">{c.description}</span>
                ) : null}
                {c.swatch ? <Swatches theme={c.swatch} /> : null}
                {c.preview ? (
                  <span className="ds-choice__previews">
                    <ThemePreview theme={c.preview} mode="dark" />
                    <ThemePreview theme={c.preview} mode="light" />
                  </span>
                ) : null}
                {c.disabledReason ? (
                  <span className="ds-choice__reason"> {c.disabledReason}</span>
                ) : null}
              </span>
            </label>
          );
          if (!structured) return row;
          return (
            <div className="ds-choice__option" key={c.value}>
              {row}
              {c.extra && props.value === c.value ? (
                <div className="ds-choice__extra">{c.extra}</div>
              ) : null}
            </div>
          );
        })}
      </fieldset>
    </div>
  );
}

/** A drop-down at any count of choices: the filters of a list, where a row of radios would fill the pane (LOOK62). */
export function SelectField(
  props: FieldShellProps & {
    value: string;
    choices: readonly Choice[];
    onChange: (value: string) => void;
  },
) {
  const id = useId();
  return (
    <Shell {...props} htmlFor={id} helpId={`${id}-h`} errorId={`${id}-e`}>
      <select
        id={id}
        className="ds-input"
        value={props.value}
        onChange={(e) => props.onChange(e.target.value)}
      >
        {props.choices.map((c) => (
          <option key={c.value} value={c.value} disabled={Boolean(c.disabledReason)}>
            {c.label}
          </option>
        ))}
      </select>
    </Shell>
  );
}

/** Component 22: a check box row. It waits for Save (UX-22). */
export function CheckRow({
  label,
  checked,
  onChange,
  help,
  disabledReason,
  aside,
  asideTone,
  stacked = false,
  changed = false,
}: {
  label: string;
  checked: boolean;
  onChange: (checked: boolean) => void;
  help?: string | undefined;
  disabledReason?: string | undefined;
  /** A quiet note at the end of the row; `needs-you` draws it amber. */
  aside?: string | undefined;
  asideTone?: 'needs-you' | undefined;
  /** The label in bold with the help on its own line below. */
  stacked?: boolean | undefined;
  /** The box differs from what is saved: the row is marked "Changed" (LOOK21). */
  changed?: boolean | undefined;
}) {
  return (
    <label
      className="ds-choice__row"
      data-changed={changed || undefined}
      data-disabled={Boolean(disabledReason) || undefined}
      data-aside={aside ? true : undefined}
      data-stack={stacked || undefined}
    >
      <input
        type="checkbox"
        className="ds-checkbox"
        checked={checked}
        disabled={Boolean(disabledReason)}
        onChange={(e) => onChange(e.target.checked)}
      />
      <span className="ds-choice__text">
        {stacked ? <span className="ds-choice__name">{label}</span> : label}
        {help && help !== disabledReason ? (
          <span className="ds-choice__reason"> {help}</span>
        ) : null}
        {disabledReason ? (
          <span className="ds-choice__reason"> {disabledReason}</span>
        ) : null}
      </span>
      {changed ? <ChangedMark /> : null}
      {aside ? (
        <span className="ds-choice__aside" data-tone={asideTone}>
          {aside}
        </span>
      ) : null}
    </label>
  );
}

/** A switch: a setting that is saved at once (UX-22). */
export function Switch({
  label,
  on,
  onChange,
  busy = false,
  disabledReason,
}: {
  label: string;
  on: boolean;
  onChange: (on: boolean) => void;
  busy?: boolean | undefined;
  disabledReason?: string | undefined;
}) {
  const reasonId = useId();
  const nameId = useId();
  const buttonId = useId();
  return (
    <span className="ds-switch-wrap">
      <button
        type="button"
        id={buttonId}
        role="switch"
        aria-checked={on}
        aria-labelledby={nameId}
        aria-describedby={disabledReason ? reasonId : undefined}
        className="ds-switch"
        data-on={on || undefined}
        disabled={busy || Boolean(disabledReason)}
        aria-busy={busy || undefined}
        onClick={() => onChange(!on)}
      >
        <span className="ds-switch__word">{on ? 'On' : 'Off'}</span>
      </button>
      {/* The name is always drawn next to the switch; pressing it works the switch. */}
      <label id={nameId} htmlFor={buttonId} className="ds-switch__name">
        {label}
      </label>
      {disabledReason ? (
        <span id={reasonId} className="ds-button-reason">
          {disabledReason}
        </span>
      ) : null}
    </span>
  );
}

/** The form's end: Cancel on the left, the one primary on the right (UX-24). */
export function FormActions({ children }: { children: ReactNode }) {
  return <div className="ds-form-actions">{children}</div>;
}

/** A list of all the mistakes at the top of a form (UX-21). */
export function ErrorSummary({ errors }: { errors: readonly string[] }) {
  if (errors.length === 0) return null;
  return (
    <div className="ds-message" data-tone="problem" role="alert">
      <span className="ds-message__label">
        <span aria-hidden="true">✕</span> Problem
      </span>
      <ul className="ds-list">
        {errors.map((e) => (
          <li key={e}>{e}</li>
        ))}
      </ul>
    </div>
  );
}
