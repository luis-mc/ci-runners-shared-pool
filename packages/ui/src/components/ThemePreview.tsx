// Component 42 (Theme choice): a small live sample of a colour theme (UX-55, UX-60): a pane with its title cut into the border,
// a selected row in reverse video and a primary button, drawn from that theme's own tokens in the dark or the light
// mode. It is a picture, not a control: it takes no focus and is described by one label. The Settings screen puts
// one beside each theme choice and passes data only.
import {
  isTerminalChoice,
  isThemeId,
  TERMINAL_THEME_ID,
  terminalChoicePalette,
  THEME_CHOICES,
} from '../themes';

export function ThemePreview({
  theme,
  mode,
  label,
}: {
  /** A theme id from `THEME_CHOICES`: a colour theme, or `terminal-green`, `terminal-amber`, `terminal-ice`. */
  theme: string;
  mode: 'dark' | 'light';
  /** What the sample says to a screen reader; defaults to "<theme name>, <mode>". */
  label?: string | undefined;
}) {
  const info = THEME_CHOICES.find((t) => t.id === theme);
  const name = info?.name ?? theme;
  const terminal = isTerminalChoice(theme);
  const palette = terminalChoicePalette(theme);
  return (
    <span
      className="ds-theme-preview"
      role="img"
      aria-label={label ?? `${name}, ${mode}`}
      data-theme-name={terminal ? TERMINAL_THEME_ID : isThemeId(theme) ? theme : undefined}
      data-pal={terminal && palette !== 'green' ? palette : undefined}
      data-theme={mode}
    >
      <span className="ds-theme-preview__pane" aria-hidden="true">
        <span className="ds-theme-preview__title">┤ Machines 3 ├</span>
        <span className="ds-theme-preview__row" data-selected="true">
          <span>home-pc</span>
          <span>✓ ok</span>
        </span>
        <span className="ds-theme-preview__row">
          <span>build-box</span>
          <span>◷ waiting</span>
        </span>
      </span>
      <span className="ds-theme-preview__button" aria-hidden="true">
        Add machine
      </span>
    </span>
  );
}
