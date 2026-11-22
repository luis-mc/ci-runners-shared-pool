// Component 75: numbered steps. The path forward of a suggestion: what the user does, in order, one step to a line.
export function Steps({ label, steps }: { label: string; steps: readonly string[] }) {
  return (
    <ol className="ds-steps" aria-label={label}>
      {steps.map((step, i) => (
        <li key={step} className="ds-steps__item">
          <span className="ds-steps__number" aria-hidden="true">
            {i + 1}
          </span>
          <span className="ds-steps__text">{step}</span>
        </li>
      ))}
    </ol>
  );
}
